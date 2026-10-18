package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rfizzle/shhh/internal/storage/datadir"
)

// secretGuard answers whether a path the model names is one of shhh's own
// secret files: everything under the data directory, which holds the sign-in
// record (<datadir>/chatgpt/credentials.json), the session store and the
// sandbox records. The read tools consult it whatever the containment
// setting, because containment only masks what a contained command can
// reach and these tools run in shhh's own process.
//
// A path is judged by where it resolves to, never by how it is spelled: its
// symlinks and ".." are resolved, and each ancestor is compared with the
// root by identity (os.SameFile) as well as by name, so a case variant on a
// case-insensitive disk or a link in the workspace is the same file it
// names. The guard fails closed: a path that cannot be resolved for any
// reason other than being absent is refused.
// See docs/capabilities/coding-agent.md#shhhs-own-files-are-not-the-models-to-read.
type secretGuard []secretRoot

type secretRoot struct {
	path string
	info os.FileInfo
}

// newSecretGuard resolves the roots now, so the answer follows the data
// directory the process is using (XDG_DATA_HOME can differ between tests and
// between sessions).
func newSecretGuard() secretGuard {
	dir, err := datadir.Dir()
	if err != nil {
		return nil
	}
	var g secretGuard
	add := func(p string) {
		r := secretRoot{path: p}
		r.info, _ = os.Stat(p)
		g = append(g, r)
	}
	add(dir)
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		add(real)
	}
	// The files themselves, by identity: a hard link to one is the same
	// file under a name outside the directory, and no path comparison sees
	// it. The directory's own files and the sign-in directory's are the
	// whole of what shhh keeps; the config file holds keys and server
	// environments, and is named by file so that the skills and profiles
	// beside it stay readable.
	for _, d := range []string{dir, filepath.Join(dir, "chatgpt")} {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Type().IsRegular() {
				add(filepath.Join(d, e.Name()))
			}
		}
	}
	for _, p := range configFilePaths() {
		add(p)
		if real, err := filepath.EvalSymlinks(p); err == nil && real != p {
			add(real)
		}
	}
	return g
}

// RefuseSecret is the check a tool outside this package makes on a path the
// model named, before it reads the file.
func RefuseSecret(path string) error { return refuseSecret(path) }

// refuseOpened is the check made on the file actually opened, which is the
// one that counts when the name was changed between the check and the open.
func refuseOpened(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return secretPathRefusal(f.Name())
	}
	if newSecretGuard().matches(info) {
		return secretPathRefusal(f.Name())
	}
	return nil
}

// matches reports whether info is one of the guarded files or directories.
func (g secretGuard) matches(info os.FileInfo) bool {
	for _, r := range g {
		if r.info != nil && os.SameFile(info, r.info) {
			return true
		}
	}
	return false
}

// resolveLoose is EvalSymlinks for a path that may not exist: the deepest
// part that does is resolved and the rest is appended, so a file not yet
// there under a linked directory is judged by the directory it lands in.
func resolveLoose(p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err == nil {
		return real, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return abs, nil
	}
	rp, err := resolveLoose(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(rp, filepath.Base(abs)), nil
}

// secretPathRefusal is the sentence the model reads.
func secretPathRefusal(path string) error {
	return fmt.Errorf("%s is under shhh's own data directory, which holds sign-in tokens and records and is never shown to the model, so nothing under it can be read. If the task needs a file from there, ask the person", path)
}

// refuseSecret is the check a tool makes on a path it was named, before it
// opens it.
func refuseSecret(path string) error {
	if newSecretGuard().holds(path) {
		return secretPathRefusal(path)
	}
	return nil
}

// holds reports whether p, once resolved, is a secret root or inside one.
func (g secretGuard) holds(p string) bool {
	if len(g) == 0 {
		return false
	}
	abs, err := resolveLoose(p)
	if err != nil {
		return true
	}
	for a := abs; ; a = filepath.Dir(a) {
		if g.isRoot(a) {
			return true
		}
		if up := filepath.Dir(a); up == a {
			return false
		}
	}
}

// isRoot reports whether the directory a is a secret root, by name or by
// identity.
func (g secretGuard) isRoot(a string) bool {
	var info os.FileInfo
	statted := false
	for _, r := range g {
		if a == r.path {
			return true
		}
		if r.info == nil {
			continue
		}
		if !statted {
			info, _ = os.Stat(a)
			statted = true
		}
		if info != nil && os.SameFile(info, r.info) {
			return true
		}
	}
	return false
}

// skips reports whether a walk leaves entry p out: a directory that is a
// secret root, or a symlink that leads into one. A walk does not descend
// symlinked directories, so a link is the only way a name inside the
// searched tree reaches a root without passing through it. Nothing is said
// of an entry skipped, so a search over a parent answers as if the secret
// files were not there.
func (g secretGuard) skips(p string, d fs.DirEntry) bool {
	if len(g) == 0 {
		return false
	}
	if d.IsDir() {
		abs, err := filepath.Abs(p)
		return err != nil || g.isRoot(abs)
	}
	if d.Type()&fs.ModeSymlink != 0 {
		return g.holds(p)
	}
	if d.Type().IsRegular() {
		info, err := d.Info()
		return err != nil || g.matches(info)
	}
	return false
}

// configFilePaths are the config files, in config.Paths' search order. The
// list is kept here and not imported: the config package reaches the hook
// package, which this package's dependants test, and an import cycle is a
// build that fails in a test binary. secretpaths_test.go holds the two lists
// equal.
func configFilePaths() []string {
	var out []string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		out = append(out, filepath.Join(xdg, "shhh", "config.toml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".config", "shhh", "config.toml"))
	}
	return out
}
