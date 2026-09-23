package project

// What a checkout is allowed to make a session load. A clone arrives with
// files that name skills to activate, personas to spawn, check commands to
// run, servers to start and settings that say which commands run without
// asking, and every one of them is somebody else's writing executing as the
// person who cloned it. So none of them load until that person has said so,
// once, for the whole checkout — and the answer is about the repository, so
// it holds while they edit its files and until they withdraw it. What an
// edit gets instead is a notice, once: a pull that rewrote a suite's command
// line is still worth saying, and saying it is what the person acts on. See
// docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs.
//
// Instruction files are deliberately not in this set. AGENTS.md and its
// siblings are prose: the worst a checkout can do with them is ask, and a
// session that would not read the file it was pointed at is a session with
// no project context at all. A wording under .shhh/prompts is in the set for
// the other half of that same line: it is not a file the model chooses to
// read, it is what shhh itself says at a stage that changes the tree without
// asking, and a checkout that could rewrite that could take the standards
// sentence out of every run in every clone. A backlog profile is that same
// text plus the shape of the run that sends it — which steps there are, what
// each may do to the tree, where the person is asked — so it is in the set
// on both counts.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	KindProfile  Kind = "backlog profile"
)

// resource is one kind and the paths, relative to the root, it is read from.
// A path that is not there is still part of the fingerprint: writing the
// file is itself a change worth telling.
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
	{KindProfile, []string{TodoProfileDir}},
}

// Store is where the answer is kept: outside the checkout, because a file in
// the checkout is the thing being decided about. A nil store trusts nothing,
// which is the safe reading of "cannot tell".
type Store interface {
	// ProjectTrusted returns the digest of each kind the checkout under
	// root was last read at, keyed by the kind's name, if it was ever
	// trusted. An answer recorded before digests were kept per kind comes
	// back with none, and reads as trusted and unchanged.
	ProjectTrusted(root string) (digests map[string]string, ok bool)
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
	// Fingerprint is the checkout's resource set as it stands now, and
	// Digests is the same reading one kind at a time — what a re-stamp
	// records, so the next session can say which kinds moved.
	Fingerprint string
	Digests     map[Kind]string
	// Granted is the answer: the person trusted this checkout and has not
	// withdrawn it. It is keyed on the root and nothing else, so an edit to
	// the checkout's own files does not take it away.
	Granted bool
	// Changed are the kinds whose files moved since the answer was last
	// read, in listing order. It withholds nothing — it is the notice a
	// trusted checkout gets, once, because the session that reads it
	// re-stamps the row.
	Changed []Kind
	// Outdated says the record is not at the digests read now: something
	// changed, or the answer was recorded before digests were kept per kind.
	// Either way the session start writes the current ones.
	Outdated bool
	// Present are the kinds this checkout actually holds, in listing order.
	// A clone with no skills and no suites has nothing to withhold, and
	// saying otherwise would put a warning on every empty repository.
	Present []Kind
}

// Allows reports whether the checkout's own resources may load.
func (t Trust) Allows() bool { return t.Granted }

// RunsOwnPrograms reports whether a program the checkout itself carries may
// be run by something shhh starts. It is the same answer Allows gives, under
// the name of the question a caller outside the resource list is asking.
//
// The caller is the commit: git runs whatever core.hooksPath names, and a
// checkout can point that at a directory inside itself — which is how every
// hook manager in the field works. So a commit made on an untrusted checkout
// runs no hooks, and the receipt says so. It is the line trust already draws,
// asked about a program that is not in the resource set because it is not a
// file shhh reads; nothing here changes what is fingerprinted.
func (t Trust) RunsOwnPrograms() bool { return t.Granted }

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
func (t Trust) WithheldNames() []string { return kindNames(t.Withheld()) }

// ChangedNames is Changed as a surface prints it.
func (t Trust) ChangedNames() []string { return kindNames(t.Changed) }

// DigestNames is Digests keyed by the kinds' names, as the store keeps them.
func (t Trust) DigestNames() map[string]string {
	out := make(map[string]string, len(t.Digests))
	for k, d := range t.Digests {
		out[string(k)] = d
	}
	return out
}

func kindNames(kinds []Kind) []string {
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
// holds, whether the person has answered for it, and which kinds moved since
// the answer was last read.
func ReadTrust(root string, store Store) Trust {
	t := Trust{Root: root}
	if root == "" {
		return t
	}
	t.Fingerprint, t.Digests, t.Present = fingerprint(root)
	if store == nil {
		return t
	}
	recorded, ok := store.ProjectTrusted(root)
	if !ok {
		return t
	}
	t.Granted = true
	if len(recorded) == 0 {
		// Recorded before digests were kept per kind: there is nothing to
		// compare against, so it is carried as unchanged and stamped now.
		t.Outdated = true
		return t
	}
	for _, res := range resources {
		was, known := recorded[string(res.Kind)]
		// A kind the record never named is one the code learned about
		// since; it is news only if the checkout holds some of it.
		if !known && !slices.Contains(t.Present, res.Kind) {
			continue
		}
		if was != t.Digests[res.Kind] {
			t.Changed = append(t.Changed, res.Kind)
		}
	}
	t.Outdated = len(t.Changed) > 0
	return t
}

// Fingerprint identifies the checkout's resource set by its contents, and
// reports which kinds it holds. Two checkouts whose declared resources are
// byte for byte the same fingerprint the same, and any edit to any of them
// — including writing one of the files for the first time — changes it.
//
// Contents rather than paths, because what a notice says is that what those
// files say has moved. A fingerprint over the names alone would keep reading
// as current across an edit that replaced the command a suite runs, which is
// the one change worth telling.
func Fingerprint(root string) (string, []Kind) {
	whole, _, present := fingerprint(root)
	return whole, present
}

// fingerprint is Fingerprint with the digest of each kind beside the whole,
// read in the one walk: the whole is what the row has always carried, and
// the kinds are what lets a notice name what changed.
func fingerprint(root string) (string, map[Kind]string, []Kind) {
	h := sha256.New()
	digests := make(map[Kind]string, len(resources))
	var present []Kind
	for _, res := range resources {
		kh := sha256.New()
		w := io.MultiWriter(h, kh)
		found := false
		for _, rel := range res.Paths {
			fmt.Fprintf(w, "\x00%s\x00", rel)
			if hashPath(w, filepath.Join(root, filepath.FromSlash(rel))) {
				found = true
			}
		}
		digests[res.Kind] = hex.EncodeToString(kh.Sum(nil)[:16])
		if found {
			present = append(present, res.Kind)
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:16]), digests, present
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
// of what was read, so creating the file is a change like any other.
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
