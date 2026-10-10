package sandbox

import "github.com/rfizzle/shhh/internal/scope/mask"

// scopeMask answers the working scope's questions about containment from the
// package's own lists, so the scope reads the same mask the sandbox applies
// and not a second copy of it. It is registered when the package is linked:
// the scope declares what it needs (internal/scope/mask) and does not import
// the sandbox to get it.
type scopeMask struct{}

func (scopeMask) DenyPaths() []string       { return DenyPaths() }
func (scopeMask) ShhhPaths() []string       { return ShhhPaths() }
func (scopeMask) CredentialPaths() []string { return CredentialPaths() }
func (scopeMask) GitStoreOf(dir string) (string, bool) {
	return GitStoreOf(dir)
}

func init() { mask.Use(scopeMask{}) }
