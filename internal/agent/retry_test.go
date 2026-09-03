package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

func overloaded(after time.Duration) *provider.Failure {
	return &provider.Failure{Class: provider.ClassOverloaded, Status: 529, RetryAfter: after}
}

func TestBackoff_BelievesTheProviderThenDoubles(t *testing.T) {
	var b Backoff
	n, ok := b.Next(overloaded(45 * time.Second))
	if !ok || n.Wait != 45*time.Second {
		t.Errorf("a named wait should be believed, got %v (ok=%v)", n.Wait, ok)
	}
	b.Reset()
	if n, _ := b.Next(overloaded(2 * time.Hour)); n.Wait != maxRetryWait {
		t.Errorf("an implausible wait is capped, got %v", n.Wait)
	}
	b.Reset()
	if n, _ := b.Next(overloaded(time.Millisecond)); n.Wait != minRetryWait {
		t.Errorf("an implausibly short wait is floored, got %v", n.Wait)
	}

	// A provider that named nothing gets doubling off a one-second base.
	var silent Backoff
	for _, want := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
		n, ok := silent.Next(overloaded(0))
		if !ok || n.Wait != want {
			t.Errorf("attempt %d backs off to %v, got %v (ok=%v)", n.Attempt, want, n.Wait, ok)
		}
	}
}

func TestBackoff_BoundIsAcrossTheStall(t *testing.T) {
	var b Backoff
	for i := 1; i <= MaxRetryAttempts; i++ {
		n, ok := b.Next(overloaded(0))
		if !ok {
			t.Fatalf("attempt %d should still be offered", i)
		}
		if n.Attempt != i || n.Max != MaxRetryAttempts {
			t.Fatalf("attempt %d of %d, want %d of %d", n.Attempt, n.Max, i, MaxRetryAttempts)
		}
	}
	if _, ok := b.Next(overloaded(0)); ok {
		t.Error("past the bound there is no further attempt")
	}
	// A request the provider answered ends the stall, and the next one gets
	// its own bound rather than what is left of this one.
	b.Reset()
	if n, ok := b.Next(overloaded(0)); !ok || n.Attempt != 1 {
		t.Errorf("after a reset the count starts again, got attempt %d (ok=%v)", n.Attempt, ok)
	}
}

func TestBackoff_OnlyWhatWaitingCanFix(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
	}{
		{"a rejected key", &provider.Failure{Class: provider.ClassAuth}},
		{"a request that did not fit", &provider.Failure{Class: provider.ClassContextLength}},
		{"an unclassified error", errors.New("connection reset")},
	} {
		var b Backoff
		if _, ok := b.Next(c.err); ok {
			t.Errorf("%s should not be waited out", c.name)
		}
		if b.Attempt() != 0 {
			t.Errorf("%s should not spend an attempt", c.name)
		}
	}
}

// The reason a surface records is the failure class, from the closed set the
// signal's comment gives — never the provider's own words, which are its own.
func TestRetryNotice_SignalIsClosed(t *testing.T) {
	for _, c := range []struct {
		class provider.Class
		want  string
	}{
		{provider.ClassRateLimit, "rate-limit"},
		{provider.ClassOverloaded, "overloaded"},
		{provider.ClassNetwork, "network"},
		{provider.ClassUnclassified, "other"},
	} {
		n := RetryNotice{Failure: &provider.Failure{Class: c.class}}
		if got := n.Signal(); got != c.want {
			t.Errorf("%s signals %q, want %q", c.class, got, c.want)
		}
	}
	if got := (RetryNotice{}).Signal(); got != "other" {
		t.Errorf("a notice with no failure signals %q, want other", got)
	}
}

// The line every surface says is the same line, and it names the three things
// a reader waiting on a silent run needs: what failed, how long, and how many
// more of these there can be.
func TestRetryNotice_NoticeSaysHowLongAndHowMany(t *testing.T) {
	var b Backoff
	n, ok := b.Next(overloaded(30 * time.Second))
	if !ok {
		t.Fatal("an overloaded provider earns a wait")
	}
	want := "529 overloaded — asking again in 30s (attempt 1 of 3)"
	if n.Notice != want {
		t.Errorf("notice = %q, want %q", n.Notice, want)
	}
}
