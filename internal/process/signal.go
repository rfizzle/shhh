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

// signalable reports whether pid names a process tree this supervisor may
// signal.
//
// The guard exists because of how a group is addressed on unix: the pid is
// negated (signal_unix.go), and two small values stop meaning "one tree" when
// they are. kill(-1) is POSIX's broadcast — every process the caller has
// permission to signal, which under a CI runner or a developer's shell is the
// whole login session, supervisor included. kill(-0) is the caller's own
// process group, which is the session doing the stopping. Either one ends far
// more than the process that was asked for, and neither can ever be a process
// this supervisor holds: a started one leads a group of its own (attr_*.go),
// and an adopted one is refused at the boundary (adopt.go). So a pid that
// would become one of them is dropped rather than sent.
//
// Windows addresses a tree by pid rather than by negation, so the broadcast
// is not reachable there; the same floor is applied anyway so that the
// supervisor's contract about what it will signal reads the same on both.
func signalable(pid int) bool {
	return pid > 1
}
