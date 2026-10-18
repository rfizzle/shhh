package chatgpt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// File names inside the account directory.
const (
	credentialsFile = "credentials.json"
	hostIDFile      = "host-id"
	lockFileName    = "credentials.lock"
)

// FileStore keeps the credential record, the host ID and the lock file in one
// directory the app names; the package knows no other place. It is a struct
// and not an interface because nothing here needs a second kind of store: the
// record's whole-file replace and the cross-process lock are the contract, and
// both are file operations. The directory is created 0700 and every file in
// it is 0600.
type FileStore struct {
	// Dir is the account directory.
	Dir string
}

// CredentialsPath is the record's path.
func (s FileStore) CredentialsPath() string { return filepath.Join(s.Dir, credentialsFile) }

func (s FileStore) hostIDPath() string { return filepath.Join(s.Dir, hostIDFile) }

// ensure creates the directory owner-only. A directory that was already there
// keeps nobody else's read on it: the record inside is a credential.
func (s FileStore) ensure() error {
	if s.Dir == "" {
		return errors.New("chatgpt: the store has no directory")
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(s.Dir, 0o700)
}

// has reports whether the directory holds a credential record, creating
// nothing.
func (s FileStore) has() bool {
	if s.Dir == "" {
		return false
	}
	_, err := os.Stat(s.CredentialsPath())
	return err == nil
}

// readCredential is the record's bytes; a record not there is fs.ErrNotExist.
func (s FileStore) readCredential() ([]byte, error) {
	if s.Dir == "" {
		return nil, errors.New("chatgpt: the store has no directory")
	}
	return os.ReadFile(s.CredentialsPath())
}

// writeCredential replaces the record whole and owner-only. replaceFile keeps
// the mode of a file already there, so a record copied in with a looser mode
// is narrowed first rather than rewritten as readable as it arrived.
func (s FileStore) writeCredential(data []byte) error {
	path := s.CredentialsPath()
	if info, err := os.Stat(path); err == nil && info.Mode().Perm() != 0o600 {
		if err := os.Chmod(path, 0o600); err != nil {
			return err
		}
	}
	return replaceFile(path, string(data), 0o600)
}

// readHostID is the host-id file's text, trimmed; one not there is
// fs.ErrNotExist.
func (s FileStore) readHostID() (string, error) {
	data, err := os.ReadFile(s.hostIDPath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s FileStore) writeHostID(id string) error {
	return replaceFile(s.hostIDPath(), id+"\n", 0o600)
}

// openLock opens credentials.lock, a file of its own because the record is
// replaced by a rename: a lock held on the record would be on the old file the
// moment the first writer renamed over it.
func (s FileStore) openLock() (*os.File, error) {
	return os.OpenFile(filepath.Join(s.Dir, lockFileName), os.O_RDWR|os.O_CREATE, 0o600)
}

// replaceFile writes text to path so a reader sees the old file whole or the
// new one whole, never a prefix of either: the text goes to a temporary file
// in the same directory, which is synced before it is renamed over the path,
// and the directory is synced after the rename. A file already there keeps its
// own mode; mode is for a new one.
func replaceFile(path, text string, mode os.FileMode) error {
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.WriteString(text)
	if werr == nil {
		werr = tmp.Sync()
	}
	if err := tmp.Close(); werr == nil {
		werr = err
	}
	if werr == nil {
		werr = os.Chmod(name, mode)
	}
	if werr == nil {
		werr = os.Rename(name, path)
	}
	if werr != nil {
		_ = os.Remove(name) // the write is the failure to report
		return werr
	}
	// The file is in place by now; a directory the platform will not sync —
	// Windows opens none for writing — is not a failed write.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// errHostID names the file a bad host ID is in.
func (s FileStore) errHostID() error {
	return fmt.Errorf("%s does not hold a host ID; delete it to have a new one made", s.hostIDPath())
}
