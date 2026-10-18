//go:build !windows

package sandbox

import (
	"os"
	"testing"
	"time"
)

// The hang-up watch reports a pipe whose write end has closed, and leaves
// a pipe that holds unread bytes alone, bytes intact.
func TestWatchHangup(t *testing.T) {
	t.Run("closed write end", func(t *testing.T) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		got := make(chan bool, 1)
		go func() { got <- watchHangup(int(r.Fd())) }()
		time.Sleep(50 * time.Millisecond)
		w.Close()
		select {
		case hung := <-got:
			if !hung {
				t.Error("watchHangup = false for a closed pipe")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the closed pipe was not noticed")
		}
	})
	t.Run("unread bytes", func(t *testing.T) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		defer w.Close()
		if _, err := w.WriteString("x"); err != nil {
			t.Fatal(err)
		}
		got := make(chan bool, 1)
		go func() { got <- watchHangup(int(r.Fd())) }()
		select {
		case <-got:
			t.Fatal("pending bytes were taken for a hang-up")
		case <-time.After(300 * time.Millisecond):
		}
		buf := make([]byte, 1)
		if n, err := r.Read(buf); n != 1 || err != nil || buf[0] != 'x' {
			t.Errorf("the bytes were not left in the pipe: %d %v %q", n, err, buf)
		}
		w.Close()
		select {
		case hung := <-got:
			if !hung {
				t.Error("watchHangup = false after the pipe closed")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the close after the bytes was not noticed")
		}
	})
}
