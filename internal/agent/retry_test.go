package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/provider"
)

func overloaded(after time.Duration) *provider.Failure {
	return &provider.Failure{Class: provider.ClassOverloaded, Status: 529, RetryAfter: after}
}

// steady is a Backoff whose spread is pinned to none, so a test can assert
// the schedule itself. Every test here that is not about the spread uses
// one, because a wait that is partly random has no exact number to check.
func steady() Backoff { return Backoff{jitter: func() float64 { return 0 }} }

func ptr(n int) *int { return &n }

func TestBackoff_BelievesTheProviderThenDoubles(t *testing.T) {
	b := steady()
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
	silent := steady()
	for _, want := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
		n, ok := silent.Next(overloaded(0))
		if !ok || n.Wait != want {
			t.Errorf("attempt %d backs off to %v, got %v (ok=%v)", n.Attempt, want, n.Wait, ok)
		}
	}
}

func TestBackoff_BoundIsAcrossTheStall(t *testing.T) {
	b := steady()
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
	b := steady()
	n, ok := b.Next(overloaded(30 * time.Second))
	if !ok {
		t.Fatal("an overloaded provider earns a wait")
	}
	want := "529 overloaded — asking again in 30s (attempt 1 of 3)"
	if n.Notice != want {
		t.Errorf("notice = %q, want %q", n.Notice, want)
	}
}

// A generous bound is still a backoff. The doubling is a shift, and a bound
// somebody set high enough would walk it off the top of the integer and hand
// back a wait of nothing at all — which is a retry storm wearing a
// backoff's name.
func TestBackoff_ALargeBoundStillWaits(t *testing.T) {
	b := steady()
	many := 80
	b.SetLimit(&many)
	for i := 1; i <= many; i++ {
		n, ok := b.Next(overloaded(0))
		if !ok {
			t.Fatalf("attempt %d should still be offered", i)
		}
		if n.Wait < minRetryWait || n.Wait > maxRetryWait {
			t.Fatalf("attempt %d waits %v, want between %v and %v", i, n.Wait, minRetryWait, maxRetryWait)
		}
	}
}

// The spread is what keeps a fan-out from coming back in step. It is added
// and never taken off, because a wait the provider named is the earliest the
// window it named has rolled over.
func TestBackoff_SpreadOnlyEverAdds(t *testing.T) {
	for _, share := range []float64{0, 0.5, 0.999} {
		b := Backoff{jitter: func() float64 { return share }}
		n, ok := b.Next(overloaded(20 * time.Second))
		if !ok {
			t.Fatal("an overloaded provider earns a wait")
		}
		if n.Wait < 20*time.Second || n.Wait > 25*time.Second {
			t.Errorf("a share of %v waits %v, want between 20s and 25s", share, n.Wait)
		}
	}
	// The cap is on the wait that happens: the spread cannot carry one past
	// the number the countdown said.
	atTheCap := Backoff{jitter: func() float64 { return 0.999 }}
	if n, _ := atTheCap.Next(overloaded(maxRetryWait)); n.Wait != maxRetryWait {
		t.Errorf("a wait at the cap came out as %v, want %v", n.Wait, maxRetryWait)
	}
	// Two backoffs handed the same failure in the same second come away with
	// different waits, which is the whole point of the spread.
	early, late := Backoff{jitter: func() float64 { return 0 }}, Backoff{jitter: func() float64 { return 0.9 }}
	a, _ := early.Next(overloaded(0))
	z, _ := late.Next(overloaded(0))
	if a.Wait == z.Wait {
		t.Errorf("two children got the same wait, %v", a.Wait)
	}
}

// A configured bound replaces the built-in one, and zero is the answer that
// takes the waiting away rather than an unset key.
func TestBackoff_ConfiguredBound(t *testing.T) {
	for _, c := range []struct {
		name  string
		limit *int
		want  int
	}{
		{"a file that names none", nil, MaxRetryAttempts},
		{"a file that names one", ptr(1), 1},
		{"a file that names more", ptr(5), 5},
		{"a file that says none", ptr(0), 0},
		{"a file that says less than none", ptr(-2), 0},
	} {
		b := steady()
		b.SetLimit(c.limit)
		got := 0
		for {
			n, ok := b.Next(overloaded(0))
			if !ok {
				break
			}
			if n.Max != c.want {
				t.Errorf("%s: notice states a bound of %d, want %d", c.name, n.Max, c.want)
			}
			got++
			if got > c.want+1 {
				t.Fatalf("%s: %d attempts and still going, want %d", c.name, got, c.want)
			}
		}
		if got != c.want {
			t.Errorf("%s: %d attempts, want %d", c.name, got, c.want)
		}
		if c.want == 0 && b.Attempt() != 0 {
			t.Errorf("%s: a bound of none spends no attempt, got %d", c.name, b.Attempt())
		}
	}
}

// Every attempt lands in the diagnostic log, and from here rather than from
// each driver: stderr, a lane and a countdown are all gone by the time
// somebody asks why a run took twenty minutes, and the file is not.
func TestBackoff_WritesEveryAttemptToTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	logs.To(path)
	t.Cleanup(func() { logs.To("") })

	b := steady()
	f := &provider.Failure{Class: provider.ClassRateLimit, Status: 429, Provider: "openai", RetryAfter: 30 * time.Second}
	if _, ok := b.Next(f); !ok {
		t.Fatal("a rate limit earns a wait")
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("nothing was written to the log: %v", err)
	}
	written := string(body)
	for _, want := range []string{"provider request retried", "provider=openai", `class="rate limited"`, "wait=30s", "attempt=1"} {
		if !strings.Contains(written, want) {
			t.Errorf("the retry line does not say %s:\n%s", want, written)
		}
	}
}
