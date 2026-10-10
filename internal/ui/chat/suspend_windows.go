//go:build windows

package chat

// StopProcess is nil where there is no job control: Bubble Tea cannot suspend
// here, the chord keeps doing what it does, and no line claims otherwise.
var StopProcess func()
