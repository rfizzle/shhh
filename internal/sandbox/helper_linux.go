package sandbox

import "golang.org/x/sys/unix"

// pollHangupEvents adds a peer's half-close to what wakes the hang-up watch:
// an engine that carries the stream over a socket closes its write side
// rather than the whole of it, and that is a hang-up too.
const pollHangupEvents = unix.POLLRDHUP
