package chat

import (
	"strings"
	"testing"
)

// waitLineModel is a session in the given wait, with nothing else under the
// transcript to draw.
func waitLineModel(t *testing.T, st state, mut func(*Model)) Model {
	t.Helper()
	m := frameModel(t, 80, 40)
	m.turnOpen = true
	m.state = st
	if mut != nil {
		mut(&m)
	}
	return m
}

// TestNotice_TheWaitLinesDoNotSpin: what the session draws under the
// transcript while it waits on something is a still notice line, or nothing
// where the frame's status or the transcript already says it. The frame's
// status is the one thing on screen that animates, so no frame of the tick
// moves any of these.
func TestNotice_TheWaitLinesDoNotSpin(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   state
		mut  func(*Model)
		// want is the line, its spaces folded; empty is no line at all.
		want string
	}{
		{"applying changes", stateRunningCmd, func(m *Model) {
			m.pendingApproval = &approvalRequest{kind: approvalDiff}
		}, "· Applying changes…"},
		{"listing models", stateModelList, nil, "· Listing models…"},
		{"running the quality gate", stateCloseGate, nil, "· Running the quality gate…"},
		// The frame's status says `deciding…`, which is the same wait.
		{"checking permission", stateClassifying, nil, ""},
		// The transcript's last row is `· Compacting conversation…`.
		{"compacting", stateStreaming, func(m *Model) {
			m.compacting = true
			m.appendEntry(entry{kind: entrySystem, text: compactingNotice})
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := waitLineModel(t, tc.st, tc.mut)
			for frame := range 8 {
				m.spinFrame = frame
				tail := stripANSI(m.liveTail(80))
				if strings.ContainsAny(tail, brailleFrames) {
					t.Fatalf("frame %d: the wait line spins: %q", frame, tail)
				}
				if got := strings.Join(strings.Fields(tail), " "); got != tc.want {
					t.Fatalf("frame %d: the wait line is %q, want %q", frame, got, tc.want)
				}
			}
		})
	}
}
