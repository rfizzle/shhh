//go:build !windows

package cli

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// A hang-up or a termination skips the deferred removal, so the signal path
// removes the container itself, once, after stopping the commands attached to
// it. The signal is a value on a channel: no test sends one to its process
// group.
func TestSandboxSessionRemovesItsContainerOnASignal(t *testing.T) {
	for _, tc := range []struct {
		sig      syscall.Signal
		reraised bool
	}{{syscall.SIGHUP, true}, {syscall.SIGTERM, false}} {
		t.Run(tc.sig.String(), func(t *testing.T) {
			log := fakeSandboxEngine(t, helperAnswers)
			cfg, sc, ws := sandboxTestSetup(t)
			_, cleanup, err := sandboxContainment(context.Background(), cfg, ws, sc, nil)
			if err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			closeContainer := func() { once.Do(cleanup) }

			sigs := make(chan os.Signal, 1)
			reraised := make(chan os.Signal, 1)
			var drained atomic.Bool
			stop := closeOnSignal(sigs, func() { drained.Store(true) }, closeContainer, func(s os.Signal) { reraised <- s })
			defer stop()
			sigs <- tc.sig

			var calls string
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				if calls = engineCalls(t, log); strings.Contains(calls, "rm --force shhh-sbx-") {
					break
				}
			}
			if n := strings.Count(calls, "rm --force shhh-sbx-"); n != 1 {
				t.Fatalf("%d removals after %v, want 1:\n%s", n, tc.sig, calls)
			}
			// The way out of the session comes to the same removal and must
			// not repeat it.
			closeContainer()
			if n := strings.Count(engineCalls(t, log), "rm --force shhh-sbx-"); n != 1 {
				t.Errorf("the quit removed the container again: %d removals", n)
			}
			select {
			case s := <-reraised:
				if !tc.reraised || s != tc.sig {
					t.Errorf("handed %v back, want reraised=%v", s, tc.reraised)
				}
			case <-time.After(time.Second):
				if tc.reraised {
					t.Errorf("%v was not handed back once the container was gone", tc.sig)
				}
			}
			if !drained.Load() {
				t.Error("the attached commands were not stopped before the container went")
			}
		})
	}
}
