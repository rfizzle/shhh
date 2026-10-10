// Package mask is the seam between the working scope and the sandbox: the
// facts about containment that the scope must know before it offers to widen
// anything, declared as an interface the scope reads and the sandbox
// provides.
//
// It is a leaf so that neither side imports the other. The scope used to
// import the sandbox for four answers and so sat above everything the
// sandbox reaches (the configuration, the shell, the runner and the
// provider); now the sandbox registers itself here when it is linked and the
// scope asks whoever registered. A binary that links the scope without the
// sandbox has no mask, and the scope answers that case closed rather than
// open (see scope.Classify).
package mask

// Mask is what containment masks and where it keeps its own stores.
type Mask interface {
	// DenyPaths is the deny mask that cannot be disabled.
	DenyPaths() []string
	// ShhhPaths are shhh's own configuration and state directories.
	ShhhPaths() []string
	// CredentialPaths are the credential stores masked until they are granted.
	CredentialPaths() []string
	// GitStoreOf reports the repository store dir would unmask if it were
	// granted.
	GitStoreOf(dir string) (store string, ok bool)
}

var current Mask

// Use registers the mask. It is called once, from the registering package's
// init, so it takes no lock.
func Use(m Mask) { current = m }

// Current is the registered mask, or nil when none has been.
func Current() Mask { return current }
