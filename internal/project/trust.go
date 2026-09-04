package project

// What a checkout is allowed to make a session load. A clone arrives with
// files that name skills to activate, personas to spawn, check commands to
// run, servers to start and settings that say which commands run without
// asking, and every one of them is somebody else's writing executing as the
// person who cloned it. So none of them load until that
// person has said so, once, for the whole checkout — and what they said so
// about is the checkout as it stood, so an edit to any of those files asks
// again. See
// docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs.
//
// Instruction files are deliberately not in this set. AGENTS.md and its
// siblings are prose: the worst a checkout can do with them is ask, and a
// session that would not read the file it was pointed at is a session with
// no project context at all. A wording under .shhh/prompts is in the set for
// the other half of that same line: it is not a file the model chooses to
// read, it is what shhh itself says at a stage that changes the tree without
// asking, and a checkout that could rewrite that could take the standards
// sentence out of every run in every clone.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Kind is one class of thing a checkout can put into a session, in the words
// every surface says it with. A withheld list is read by someone deciding
// whether to trust the checkout, so the names are what they would look for
// in it rather than the identifiers behind them.
type Kind string

const (
	KindSkills   Kind = "skills"
	KindAgents   Kind = "agent profiles"
	KindGate     Kind = "quality suites"
	KindHooks    Kind = "hooks"
	KindServers  Kind = "MCP servers"
	KindSettings Kind = "settings"
	KindPrompts  Kind = "wordings"
)

// resource is one kind and the paths, relative to the root, it is read from.
// A path that is not there is still part of the fingerprint: writing the
// file is itself the change that has to ask.
type resource struct {
	Kind  Kind
	Paths []string
}

// resources is everything the trust answer covers, in the order a listing
// names it. Adding a way for a checkout to name something that runs means
// adding it here — the fingerprint and the withheld list are both this
// list, so a kind that is loaded but not listed cannot happen quietly.
var resources = []resource{
	{KindSkills, []string{".shhh/skills", ".agents/skills", ".claude/skills"}},
	{KindAgents, []string{".shhh/agents"}},
	{KindGate, []string{".shhh/quality.json"}},
	{KindHooks, []string{HooksFile}},
	{KindServers, []string{".shhh/mcp.json", ".mcp.json"}},
	{KindSettings, []string{ConfigFile}},
	{KindPrompts, []string{PromptsDir}},
}

// Store is where the answer is kept: outside the checkout, because a file in
// the checkout is the thing being decided about. A nil store trusts nothing,
// which is the safe reading of "cannot tell".
type Store interface {
	// ProjectTrusted returns the fingerprint the checkout under root was
	// trusted at, if it ever was.
	ProjectTrusted(root string) (fingerprint string, ok bool)
}

// Trust is one checkout's standing, as a session reads it at startup.
//
// The zero value withholds. Every field that could open something is a
// positive statement someone had to make, so a surface that forgets to ask,
// a store that will not open and a root nobody could name all land on the
// same answer.
type Trust struct {
	// Root is the directory the answer is keyed on.
	Root string
	// Fingerprint is the checkout's resource set as it stands now.
	Fingerprint string
	// Granted is trust recorded at exactly that fingerprint. Changed is
	// trust recorded at a different one — the checkout was trusted once and
	// has been edited since, which is a different sentence to say to the
	// reader than never having been asked.
	Granted bool
	Changed bool
	// Present are the kinds this checkout actually holds, in listing order.
	// A clone with no skills and no suites has nothing to withhold, and
	// saying otherwise would put a warning on every empty repository.
	Present []Kind
}

// Allows reports whether the checkout's own resources may load.
func (t Trust) Allows() bool { return t.Granted }

// Withheld is what this session did not load and would have, or nothing
// when the checkout is trusted or holds none of it. It is a diagnostic:
// nothing here stops a session starting.
func (t Trust) Withheld() []Kind {
	if t.Granted {
		return nil
	}
	return t.Present
}

// WithheldNames is Withheld as a surface prints it.
func (t Trust) WithheldNames() []string {
	kinds := t.Withheld()
	if len(kinds) == 0 {
		return nil
	}
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return out
}

// ReadTrust is a session's standing in the checkout rooted at root: what it
// holds, what state that is in, and whether the person has answered for it.
func ReadTrust(root string, store Store) Trust {
	t := Trust{Root: root}
	if root == "" {
		return t
	}
	t.Fingerprint, t.Present = Fingerprint(root)
	if store == nil {
		return t
	}
	fp, ok := store.ProjectTrusted(root)
	switch {
	case !ok:
		return t
	case fp != t.Fingerprint:
		t.Changed = true
		return t
	}
	t.Granted = true
	return t
}

// Fingerprint identifies the checkout's resource set by its contents, and
// reports which kinds it holds. Two checkouts whose declared resources are
// byte for byte the same fingerprint the same, and any edit to any of them
// — including writing one of the files for the first time — changes it.
//
// Contents rather than paths, because the decision being recorded is about
// what those files say. A fingerprint over the names alone would keep
// reading as current across an edit that replaced the command a suite runs,
// which is the one change trust exists to catch.
func Fingerprint(root string) (string, []Kind) {
	h := sha256.New()
	var present []Kind
	for _, res := range resources {
		found := false
		for _, rel := range res.Paths {
			fmt.Fprintf(h, "\x00%s\x00", rel)
			if hashPath(h, filepath.Join(root, filepath.FromSlash(rel))) {
				found = true
			}
		}
		if found {
			present = append(present, res.Kind)
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:16]), present
}

// maxFingerprintFiles bounds the walk of one resource directory. It is a
// ceiling on a pathological tree rather than a tuned number: a skills
// directory is tens of files, and a checkout that puts a hundred thousand
// under one would otherwise make every session start with a full walk of it.
// Past the bound the fingerprint stops being sensitive to further files,
// which is stated in the digest so a tree that crosses the line reads
// differently from one that does not.
const maxFingerprintFiles = 4096

// hashPath writes one resource path into the digest and reports whether
// anything was there. A missing path is written as such: the absence is part
// of what was trusted, so creating the file is a change like any other.
func hashPath(h io.Writer, path string) bool {
	info, err := os.Lstat(path)
	switch {
	case err != nil:
		fmt.Fprint(h, "absent")
		return false
	case info.Mode()&fs.ModeSymlink != 0:
		// A symlink is recorded as the link, not as what it points at:
		// following it would hash a directory outside the checkout and make
		// the answer depend on a tree the person was never shown.
		target, _ := os.Readlink(path)
		fmt.Fprintf(h, "link\x00%s", target)
		return true
	case !info.IsDir():
		fmt.Fprint(h, "file")
		hashFile(h, path)
		return true
	}
	fmt.Fprint(h, "dir")
	files := 0
	// WalkDir reads each directory in lexical order, so the digest does not
	// depend on how the filesystem happens to return entries.
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory that cannot be read is recorded as unreadable
			// rather than skipped, so a permission change is a change.
			fmt.Fprintf(h, "\x00%s\x00unreadable", relSlash(path, p))
			return fs.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if files >= maxFingerprintFiles {
			return fs.SkipAll
		}
		files++
		fmt.Fprintf(h, "\x00%s\x00", relSlash(path, p))
		if d.Type()&fs.ModeSymlink != 0 {
			target, _ := os.Readlink(p)
			fmt.Fprintf(h, "link\x00%s", target)
			return nil
		}
		hashFile(h, p)
		return nil
	})
	if files >= maxFingerprintFiles {
		fmt.Fprintf(h, "\x00over %d files", maxFingerprintFiles)
	}
	return true
}

// hashFile folds one file's bytes into the digest, with the count after
// them so two files cannot run together into the same stream a differently
// split pair would produce. A file that cannot be read contributes the
// reason instead: an unreadable file is a state of the checkout and not a
// reason to answer "unchanged".
func hashFile(h io.Writer, path string) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprint(h, "unreadable")
		return
	}
	defer f.Close()
	n, err := io.Copy(h, f)
	if err != nil {
		fmt.Fprint(h, "unreadable")
		return
	}
	fmt.Fprintf(h, "\x00%d", n)
}

// relSlash is p stated from base with forward slashes, so the same checkout
// fingerprints the same on either kind of filesystem.
func relSlash(base, p string) string {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		rel = p
	}
	return filepath.ToSlash(rel)
}

// ResourceNames lists every path the trust answer covers, root-relative and
// sorted, as the doctor and the docs name them. It is derived from the same
// list the fingerprint walks, so a surface cannot describe a set the code
// does not read.
func ResourceNames() []string {
	var out []string
	for _, res := range resources {
		out = append(out, res.Paths...)
	}
	sort.Strings(out)
	return out
}
