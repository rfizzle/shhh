package process

// termSignal is the two ways the supervisor asks a process tree to end: the
// one that lets it clean up, and the one that does not.
//
// It is this package's own type rather than syscall.Signal because the values
// are meaningless on Windows, where ending a tree is a command rather than a
// signal — and a platform-specific type in a shared function signature is how
// a package stops compiling somewhere its author never built it.
type termSignal int

const (
	signalTerm termSignal = iota
	signalKill
)
