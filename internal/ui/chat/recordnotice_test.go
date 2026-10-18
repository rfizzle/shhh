package chat

import (
	"errors"
	"strings"
	"testing"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/observe"
)

// The record's failure is a line in the history the first time the screen
// turns over after it, and not again.
func TestRecordNotice_IsDrawnOnce(t *testing.T) {
	failure := errors.New("disk full")
	asked := 0
	m := New([]llm.Message{{Role: llm.RoleSystem, Content: "sys"}}, mockStream, Wiring{
		Observer: observe.Observer{WriteFailed: func() error {
			asked++
			err := failure
			failure = nil
			return err
		}},
	})
	for range 3 {
		next, _ := m.Update(nil)
		m = next.(Model)
	}
	n := 0
	for _, e := range *m.entries() {
		if strings.Contains(e.text, "the session record stopped writing: disk full") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the notice was drawn %d times, want once", n)
	}
	if asked < 3 {
		t.Fatalf("asked %d times, want each pass to ask", asked)
	}
}
