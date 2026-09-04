package quality

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Fingerprint pins a gate result to the tree it ran against: the git HEAD
// commit plus a digest of the porcelain status and of the content of every
// path that status names. A result whose fingerprint no longer matches the
// tree reports stale instead of silently passing; a workspace that is not a
// git repository reports unverifiable, which is also never a silent pass.
type Fingerprint struct {
	Repo       bool
	Head       string
	StatusHash string
	DirtyPaths int
	// Unhashed marks a tree too large to digest within the bounds below.
	// Content changes are invisible in that state, so a result carrying it
	// is stale by definition rather than merely unequal.
	Unhashed bool
}

// The content digest is bounded so taking a fingerprint stays far cheaper
// than the checks it guards — it runs before and after every gate run and at
// every rewind checkpoint. Measured on this machine: 512 files of 8 KiB cost
// about 10 ms end to end (the per-file open dominates, at roughly 20 µs), and
// 32 MiB of content hashes in about 12 ms. Both bounds together keep the
// worst case near a twentieth of a second, well under the cheapest real
// check. A tree past either bound is one nobody can review in a turn anyway,
// so refusing to vouch for it costs nothing.
const (
	maxHashedPaths = 512
	maxHashedBytes = 32 << 20
)

// ContentBound names those bounds in the words a surface uses when it has to
// say why a reading could not be taken. It is a function over one phrase
// rather than two exported numbers because every caller handed the numbers
// has to word them itself, and a second wording of one bound goes stale the
// moment the bound moves — the reader then has two answers to "how large is
// too large" and no way to tell which is current.
func ContentBound() string {
	return fmt.Sprintf("%d-path / %d MiB", maxHashedPaths, maxHashedBytes>>20)
}

// TakeFingerprint captures the workspace's current fingerprint. Every gate
// run takes it — attended, backgrounded or unattended — so there is one
// definition of "the tree this verdict covers" and no path that skips it.
//
// Hashing the content of the paths porcelain names, and not just the names,
// is what makes staleness mean anything: porcelain lists dirty paths but not
// what is in them, so an edit to a file that was already dirty — nearly
// always the file being worked on — leaves the path list identical and would
// otherwise let a stale pass read as current. Over the bounds above the
// fingerprint is marked unhashed and the verdict is reported stale by
// definition; failing that way round keeps a pass from vouching for content
// nobody hashed.
// See docs/capabilities/approvals-and-safety.md#quality-gates-run-what-you-wrote.
//
// Any git failure (no repo, no HEAD yet, no git binary) yields the zero
// fingerprint, which Describe reports honestly.
func TakeFingerprint(workspace string) Fingerprint {
	// Porcelain names every path from the repository's top level however
	// deep the workspace sits, so the root has to be asked for rather than
	// assumed: joining those paths onto a subdirectory workspace would miss
	// every file, hash them all as absent, and quietly restore the blindness
	// this function exists to remove.
	out, err := gitOutput(workspace, "rev-parse", "HEAD", "--show-toplevel")
	if err != nil {
		return Fingerprint{}
	}
	head, root, ok := strings.Cut(strings.TrimSpace(out), "\n")
	if !ok {
		return Fingerprint{}
	}
	// -z gives raw paths, so a path with a space or a quote in it needs no
	// unescaping; -uall lists the files inside an untracked directory
	// instead of collapsing it to one entry, which would hide every edit
	// made inside it.
	status, err := gitOutput(workspace, "status", "--porcelain", "-z", "-uall")
	if err != nil {
		return Fingerprint{}
	}
	paths := porcelainPaths(status)
	fp := Fingerprint{Repo: true, Head: head, DirtyPaths: len(paths)}
	// A hash never fails a write, so these go unchecked, as the tree does
	// elsewhere for a sink that cannot refuse.
	h := sha256.New()
	fmt.Fprint(h, status)
	if len(paths) > maxHashedPaths {
		fp.Unhashed = true
	} else {
		budget := int64(maxHashedBytes)
		for _, p := range paths {
			if !hashPath(h, root, p, &budget) {
				fp.Unhashed = true
				break
			}
		}
	}
	fp.StatusHash = hex.EncodeToString(h.Sum(nil))
	return fp
}

// porcelainPaths reads the paths out of `git status --porcelain -z`. Each
// record is two status letters, a space and the path; a rename or a copy
// carries its origin path as the next record, which is the same file under
// its old name and so is skipped rather than hashed twice.
func porcelainPaths(status string) []string {
	records := strings.Split(status, "\x00")
	paths := make([]string, 0, len(records))
	for i := 0; i < len(records); i++ {
		r := records[i]
		if len(r) < 4 {
			continue // the empty tail after the final separator
		}
		paths = append(paths, r[3:])
		if r[0] == 'R' || r[0] == 'C' || r[1] == 'R' || r[1] == 'C' {
			i++
		}
	}
	return paths
}

// hashPath folds one dirty path into h as a fixed-width digest of whatever
// the filesystem holds at it, reporting whether that stayed inside the byte
// budget. Fixed width is the point: content written straight into the outer
// hash could be shifted across the boundary between one path and the next,
// so a file whose bytes imitate the separator would let two different trees
// digest identically — a silent pass on exactly the kind of content nobody
// reads closely. The path itself is already in the status blob and is not
// repeated here.
func hashPath(h hash.Hash, root, path string, budget *int64) bool {
	sub := sha256.New()
	ok := digestPath(sub, filepath.Join(root, filepath.FromSlash(path)), budget)
	fmt.Fprintf(h, "%x", sub.Sum(nil))
	return ok
}

// digestPath writes what lives at full into sub. A path porcelain lists but
// the filesystem does not have was deleted, which is a state and not a
// failure: it digests as absent, so deleting a file and restoring it read as
// different trees. A symlink digests as its target text, because that is
// what git stores for it and so a relink is a content change — and reading
// the link beats following it, which may lead outside the tree or nowhere.
// Anything else non-regular is a submodule, whose content is not this tree's
// to read; its own status entry moves when its HEAD does. A read that fails
// for any other reason reports false and the caller marks the whole
// fingerprint unhashed, which is the fail-closed direction: unreadable means
// unvouched, not equal.
func digestPath(sub hash.Hash, full string, budget *int64) bool {
	info, err := os.Lstat(full)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprint(sub, "absent")
		return true
	case err != nil:
		return false
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(full)
		if err != nil {
			return false
		}
		fmt.Fprint(sub, "link:", target)
		return true
	case !info.Mode().IsRegular():
		fmt.Fprint(sub, "unread")
		return true
	}
	if *budget -= info.Size(); *budget < 0 {
		return false
	}
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprint(sub, "absent") // raced with a delete, not a failure
			return true
		}
		return false
	}
	defer f.Close()
	_, err = io.Copy(sub, f)
	return err == nil
}

// Describe renders the fingerprint for result output.
func (f Fingerprint) Describe() string {
	if !f.Repo {
		return "not a git repository — staleness cannot be verified, treat the verdict as unverified"
	}
	head := f.Head
	if len(head) > 12 {
		head = head[:12]
	}
	if f.DirtyPaths == 0 {
		return fmt.Sprintf("HEAD %s, clean tree", head)
	}
	if f.Unhashed {
		return fmt.Sprintf("HEAD %s, dirty tree (%d changed/untracked paths, unhashed: past the %s bound on content)",
			head, f.DirtyPaths, ContentBound())
	}
	return fmt.Sprintf("HEAD %s, dirty tree (%d changed/untracked paths)", head, f.DirtyPaths)
}

func gitOutput(workspace string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
