package quality

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
)

// Fingerprint pins a gate result to the tree it ran against: the git HEAD
// commit plus a digest of the porcelain status. A result whose fingerprint no
// longer matches the tree reports stale instead of silently passing; a
// workspace that is not a git repository reports unverifiable, which is also
// never a silent pass.
type Fingerprint struct {
	Repo       bool
	Head       string
	StatusHash string
	DirtyPaths int
}

// TakeFingerprint captures the workspace's current fingerprint. Any git
// failure (no repo, no HEAD yet, no git binary) yields the zero fingerprint,
// which Describe reports honestly.
func TakeFingerprint(workspace string) Fingerprint {
	head, err := gitOutput(workspace, "rev-parse", "HEAD")
	if err != nil {
		return Fingerprint{}
	}
	status, err := gitOutput(workspace, "status", "--porcelain")
	if err != nil {
		return Fingerprint{}
	}
	sum := sha256.Sum256([]byte(status))
	dirty := 0
	if s := strings.TrimRight(status, "\n"); s != "" {
		dirty = strings.Count(s, "\n") + 1
	}
	return Fingerprint{
		Repo:       true,
		Head:       strings.TrimSpace(head),
		StatusHash: hex.EncodeToString(sum[:]),
		DirtyPaths: dirty,
	}
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
