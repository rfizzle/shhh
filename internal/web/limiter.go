package web

// Pacing, and the one wait a refusal costs.
//
// A fan-out is three researchers reading one documentation site, which is
// one session as far as that site is concerned. The limiter is what makes it
// behave like one: requests to a host go out one at a time with a gap
// between them, and a host that answers "slow down" is waited out once with
// its turn still held, so the other two do not walk into the same refusal.
// See docs/capabilities/evidence.md#a-site-is-read-at-the-pace-it-answers.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// hostGap is the quiet one host gets between two of this session's requests.
// A quarter of a second is four requests a second at worst, which is under
// every published limit shhh has any business approaching, and it is short
// enough that reading ten pages off one site spends under three seconds on
// the courtesy — a session reads a site, it does not crawl one.
const hostGap = 250 * time.Millisecond

const (
	// minRefusalWait floors a host that names an implausibly short wait: a
	// window needs time to actually roll over, or the second request is the
	// first one again.
	minRefusalWait = time.Second
	// maxRefusalWait caps a host that names an hour. A wait longer than this
	// is a decision for the person at the keyboard — read something else —
	// rather than a countdown, and it is the number the row shows, so it has
	// to be a number the fetch keeps.
	maxRefusalWait = 20 * time.Second
	// defaultRefusalWait is what a host that named nothing gets: the first
	// wait of the schedule a stalled provider is waited out on, because one
	// session should not have two answers to "how long is a short wait".
	defaultRefusalWait = 2 * time.Second
)

// errWaitAbandoned is a wait that was given up on rather than served — the
// turn behind it was cancelled. It reads as the cancellation it is rather
// than as a fetch that failed.
var errWaitAbandoned = errors.New("the wait for this host was abandoned")

// limiter paces requests per host and owns the waits a refusal costs.
//
// One object per session, held by the fetcher, because the fetcher is one
// object per session and a child fetches through its parent's: three
// limiters pacing one host are not pacing it at all.
type limiter struct {
	// gap is the quiet between two requests to one host.
	gap time.Duration
	// now and after are the clock, injectable so a test can assert the
	// schedule without spending it. Both are set at construction and never
	// replaced, which is why neither is behind the lock.
	now   func() time.Time
	after func(time.Duration) <-chan time.Time

	mu sync.Mutex
	// hosts is one queue per host this session has reached. It grows with
	// the hosts visited and is never pruned: an entry is two words, and a
	// session that has read a thousand sites has bigger costs than this map.
	hosts map[string]*hostQueue
	// waits is the deadline of the wait each host is currently being given,
	// for the row that says so.
	waits map[string]time.Time
	// abandon is closed to end every wait in flight, then replaced. A
	// channel rather than a flag because a waiter is parked in a select and
	// a flag it cannot see is a flag it will read twenty seconds late.
	abandon chan struct{}
}

// hostQueue is one host's turn to be asked.
type hostQueue struct {
	// slot holds the one request allowed to be in flight to this host;
	// taking it is taking the host's turn.
	slot chan struct{}
	// next is the earliest the following request may leave, which is what
	// makes the gap a gap rather than a burst of back-to-back requests.
	next time.Time
}

func newLimiter(gap time.Duration) *limiter {
	return &limiter{
		gap:     gap,
		now:     time.Now,
		after:   time.After,
		hosts:   map[string]*hostQueue{},
		waits:   map[string]time.Time{},
		abandon: make(chan struct{}),
	}
}

// take waits for this host's turn and returns the release that ends it.
// Requests to different hosts never wait for each other: pacing one site
// says nothing about another.
//
// The caller holds the turn for the whole fetch — every redirect hop, the
// body read, and any wait a refusal costs — so the release is deferred, not
// called at the first return.
func (l *limiter) take(ctx context.Context, host string) (func(), error) {
	q := l.queue(host)
	select {
	case q.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Once, because giving the turn back twice takes it from whoever holds
	// it next: two requests to one host would then be in flight together,
	// which is the one thing the queue exists to prevent and would show up
	// as a rate limit rather than as a bug here.
	var done sync.Once
	release := func() {
		done.Do(func() {
			l.mu.Lock()
			q.next = l.now().Add(l.gap)
			l.mu.Unlock()
			<-q.slot
		})
	}
	l.mu.Lock()
	gap := q.next.Sub(l.now())
	l.mu.Unlock()
	if gap > 0 {
		select {
		case <-l.after(gap):
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
	}
	return release, nil
}

// queue is this host's queue, created on first sight.
func (l *limiter) queue(host string) *hostQueue {
	key := pacedHost(host)
	l.mu.Lock()
	defer l.mu.Unlock()
	q := l.hosts[key]
	if q == nil {
		q = &hostQueue{slot: make(chan struct{}, 1)}
		l.hosts[key] = q
	}
	return q
}

// waitOut sits out the wait a host asked for. The caller still holds the
// host's turn, which is the point: the next request to it queues behind the
// wait instead of collecting the same refusal a second time.
func (l *limiter) waitOut(ctx context.Context, host string, d time.Duration) error {
	key := pacedHost(host)
	l.mu.Lock()
	l.waits[key] = l.now().Add(d)
	abandon := l.abandon
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.waits, key)
		l.mu.Unlock()
	}()
	select {
	case <-l.after(d):
		return nil
	case <-abandon:
		return errWaitAbandoned
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waiting is how much of a host's wait is left, and false where no wait is
// being served. It is read on whichever goroutine draws the screen while the
// wait is being served on another, which is what the lock is for.
func (l *limiter) waiting(host string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	deadline, ok := l.waits[pacedHost(host)]
	if !ok {
		return 0, false
	}
	return max(deadline.Sub(l.now()), 0), true
}

// abandonWaits ends every wait in flight. The waiters see the closed channel
// and give up; the replacement is what the next wait parks on, so one
// cancelled turn does not leave the session unable to wait ever again.
func (l *limiter) abandonWaits() {
	l.mu.Lock()
	close(l.abandon)
	l.abandon = make(chan struct{})
	l.mu.Unlock()
}

// pacedHost is the one spelling a host is paced under. It folds case and
// drops the trailing dot of an absolute name for the reason the host lists
// do — those are two spellings of one host, and pacing one of them while
// leaving the other free would pace neither.
func pacedHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

// refused reports whether a status is the host asking for the request again
// later rather than answering it. Two statuses say that: 429 outright, and
// the 503 a host under load sends instead. Every other 5xx is a server that
// broke, which a second identical request does not fix.
func refused(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
}

// refusalWait is how long a refusal is waited out: what the host named,
// floored and capped, and the default where it named nothing. A host is
// believed over the default — it knows when its own window turns.
func refusalWait(named time.Duration) time.Duration {
	if named <= 0 {
		return defaultRefusalWait
	}
	return min(max(named, minRefusalWait), maxRefusalWait)
}

// parseRetryAfter reads the wait out of a Retry-After header, in both forms
// the header has: a count of seconds, or the date the window turns. Zero is
// a header that was absent, unparseable, or already in the past — all three
// mean the same thing to the caller, which is that the host named no wait.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return max(time.Duration(secs)*time.Second, 0)
	}
	if when, err := http.ParseTime(v); err == nil {
		return max(when.Sub(now), 0)
	}
	return 0
}
