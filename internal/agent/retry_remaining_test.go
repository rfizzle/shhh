package agent

import "testing"

// Remaining is the bound less the attempts used: the unconfigured bound, a
// configured one, and none at all.
func TestBackoff_RemainingIsTheBoundLessTheAttemptsUsed(t *testing.T) {
	b := steady()
	if got := b.Remaining(); got != MaxRetryAttempts {
		t.Errorf("a fresh stall has %d retries, want %d", got, MaxRetryAttempts)
	}
	b.Next(overloaded(0))
	if got := b.Remaining(); got != MaxRetryAttempts-1 {
		t.Errorf("after one attempt %d remain, want %d", got, MaxRetryAttempts-1)
	}
	b.SetLimit(ptr(0))
	if got := b.Remaining(); got != 0 {
		t.Errorf("with no retries allowed %d remain, want 0", got)
	}
	b.SetLimit(ptr(1))
	b.Reset()
	b.Next(overloaded(0))
	b.Next(overloaded(0))
	if got := b.Remaining(); got != 0 {
		t.Errorf("past the bound %d remain, want 0 and never a negative", got)
	}
}
