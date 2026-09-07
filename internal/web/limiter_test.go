package web

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

// fakeWaits drives a limiter's clock so a test can assert the schedule
// without spending it: every wait is granted at once, recorded, and the clock
// moves on by what was waited.
type fakeWaits struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
}

func newFakeWaits(l *limiter) *fakeWaits {
	f := &fakeWaits{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	l.now = f.clock
	l.after = f.wait
	return f
}

func (f *fakeWaits) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeWaits) wait(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	f.waits = append(f.waits, d)
	f.now = f.now.Add(d)
	now := f.now
	f.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- now
	return ch
}

func (f *fakeWaits) recorded() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.waits...)
}

// One host is asked one request at a time; another host is not made to wait
// for it, because pacing one site says nothing about another.
func TestLimiter_OneHostAtATimeAndAnotherHostFree(t *testing.T) {
	l := newLimiter(0)
	ctx := context.Background()

	release, err := l.take(ctx, "docs.rs")
	if err != nil {
		t.Fatalf("take: %v", err)
	}

	// A second host goes straight through while the first is held.
	otherDone := make(chan struct{})
	go func() {
		r, err := l.take(ctx, "pkg.go.dev")
		if err != nil {
			t.Errorf("take on a second host: %v", err)
		} else {
			r()
		}
		close(otherDone)
	}()
	select {
	case <-otherDone:
	case <-time.After(2 * time.Second):
		t.Fatal("a second host waited for the first")
	}

	// The same host does not, until the turn is given back.
	sameDone := make(chan struct{})
	go func() {
		r, err := l.take(ctx, "docs.rs")
		if err == nil {
			r()
		}
		close(sameDone)
	}()
	select {
	case <-sameDone:
		t.Fatal("two requests to one host ran at once")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-sameDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the host's turn was never handed on")
	}
}

// The turn is handed on with a gap, not immediately: the point is a site
// that is asked at a pace, not one asked as fast as the answers arrive.
func TestLimiter_GapBetweenRequestsToOneHost(t *testing.T) {
	l := newLimiter(hostGap)
	waits := newFakeWaits(l)

	release, err := l.take(context.Background(), "docs.rs")
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	release()
	release, err = l.take(context.Background(), "docs.rs")
	if err != nil {
		t.Fatalf("take again: %v", err)
	}
	release()

	got := waits.recorded()
	if len(got) != 1 || got[0] != hostGap {
		t.Fatalf("waits = %v, want one gap of %s", got, hostGap)
	}
}

// A host is paced under one spelling: case and the trailing dot of an
// absolute name are two spellings of one site, and pacing one of them while
// leaving the other free would pace neither.
func TestLimiter_HostSpellingsShareOneQueue(t *testing.T) {
	l := newLimiter(0)
	release, err := l.take(context.Background(), "docs.rs")
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	defer release()

	done := make(chan struct{})
	go func() {
		if r, err := l.take(context.Background(), "DOCS.rs."); err == nil {
			r()
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("a second spelling of one host got its own turn")
	case <-time.After(50 * time.Millisecond):
	}
}

// The wait a refusal costs is readable while it is being served — that is
// what the row counts down — and cancelling the turn gives it up.
func TestLimiter_WaitIsVisibleAndAbandoned(t *testing.T) {
	l := newLimiter(0)
	// A wait nothing ever fires, so the test drives its end itself.
	l.after = func(time.Duration) <-chan time.Time { return make(chan time.Time) }

	errs := make(chan error, 1)
	go func() { errs <- l.waitOut(context.Background(), "docs.rs", 8*time.Second) }()

	var left time.Duration
	var ok bool
	for range 200 {
		if left, ok = l.waiting("docs.rs"); ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !ok {
		t.Fatal("a wait in flight reported nothing")
	}
	if left <= 7*time.Second || left > 8*time.Second {
		t.Errorf("remaining = %s, want about 8s", left)
	}
	if _, ok := l.waiting("pkg.go.dev"); ok {
		t.Error("a host nobody is waiting on reported a wait")
	}

	l.abandonWaits()
	select {
	case err := <-errs:
		if !errors.Is(err, errWaitAbandoned) {
			t.Fatalf("err = %v, want the wait abandoned", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wait outlived the cancel")
	}
	if _, ok := l.waiting("docs.rs"); ok {
		t.Error("an abandoned wait is still reported")
	}

	// And the limiter can wait again: the abandoned channel was replaced.
	l.after = func(d time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	if err := l.waitOut(context.Background(), "docs.rs", time.Second); err != nil {
		t.Fatalf("the limiter could not wait after a cancel: %v", err)
	}
}

func TestRefusalWait(t *testing.T) {
	cases := []struct {
		named time.Duration
		want  time.Duration
	}{
		{0, defaultRefusalWait},                        // the host named nothing
		{100 * time.Millisecond, minRefusalWait},       // implausibly short
		{5 * time.Second, 5 * time.Second},             // believed
		{time.Hour, maxRefusalWait},                    // capped
		{-time.Second, defaultRefusalWait},             // nonsense reads as absent
		{maxRefusalWait, maxRefusalWait},               // the cap itself
		{maxRefusalWait + time.Second, maxRefusalWait}, // just over it
	}
	for _, tc := range cases {
		if got := refusalWait(tc.named); got != tc.want {
			t.Errorf("refusalWait(%s) = %s, want %s", tc.named, got, tc.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		header string
		want   time.Duration
	}{
		{"", 0},
		{"2", 2 * time.Second},
		{"  30  ", 30 * time.Second},
		{"soon", 0},
		{"-5", 0},
		{now.Add(9 * time.Second).Format(http.TimeFormat), 9 * time.Second},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0}, // a window that already turned
	}
	for _, tc := range cases {
		if got := parseRetryAfter(tc.header, now); got != tc.want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", tc.header, got, tc.want)
		}
	}
}

func TestRefused(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		if !refused(status) {
			t.Errorf("%d is a host asking for the request again later", status)
		}
	}
	for _, status := range []int{200, 404, 403, 500, 502, 504} {
		if refused(status) {
			t.Errorf("%d was read as a request to come back later", status)
		}
	}
}
