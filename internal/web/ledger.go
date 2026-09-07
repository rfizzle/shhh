package web

import (
	"strings"
	"sync"
	"time"
)

// The ledger is what a session read: one row per fetch and per search, made
// by the fetch itself rather than by the model afterwards. A sources list a
// model writes is a claim like any other; one built from what the fetcher
// returned is a fact, which is what makes a citation checkable.
// See docs/capabilities/chat.md#what-was-read.

// The two kinds of read a row records.
const (
	KindFetch  = "fetch"
	KindSearch = "search"
)

// Orchestrator is the agent a session's own reads are recorded under. A
// child records under its own name, so the ledger says which of a fan-out's
// researchers read a page rather than only that the session did.
const Orchestrator = "orchestrator"

// Source is one read. It carries what identifies a page and what a read
// cost, and no page text: the ledger lives with the session record, which a
// resume reads back and a person reads on a screen, so the page itself stays
// in the evidence store and this row names the entry that holds it.
//
// Title is the exception that proves that rule — it is the extraction's
// title, not the page's prose, and it is here because it is half of how a
// source is cited. A write-up that lists bare URLs makes a reader open every
// one of them to find out which is which.
type Source struct {
	ID    int64
	Turn  int64
	Agent string
	Kind  string

	// Query is the search's words; it is empty on a fetch.
	Query string
	// Requested is the URL the model asked for and FinalURL the one that
	// answered. They differ whenever a redirect was followed, and the
	// difference is the point: a citation names the URL that answered.
	Requested string
	FinalURL  string
	Title     string

	Status int
	Bytes  int
	// Results is how many hits a search came back with. It is its own field
	// rather than a count borrowed from Status or Bytes, which are the HTTP
	// status and the page's size: a number filed under the wrong name is
	// wrong on every screen that reads it.
	Results int
	Cached  bool
	// Evidence is the store entry holding the whole page, where the fetch
	// was long enough to leave one, and "" where the page fitted inline.
	Evidence string

	At time.Time
}

// LedgerBackend is where the rows persist. The ledger keeps its own copy in
// memory and writes through, so a backend that fails costs the resume and
// not the session.
type LedgerBackend interface {
	// SaveSource persists one row under the session key and returns its id.
	SaveSource(session string, s Source) (int64, error)
	// LoadSources returns a session's rows, oldest first.
	LoadSources(session string) ([]Source, error)
}

// Ledger is one session's record of what it read, shared with its children:
// the toolset a session builds is the object its children fetch through, so
// every row in a fan-out lands here under the name of the agent that made
// it.
type Ledger struct {
	mu      sync.Mutex
	backend LedgerBackend
	session string
	rows    []Source
	nextID  int64
	turn    int64
	scrub   func(string) string
	now     func() time.Time
}

// NewLedger opens a ledger over backend; a nil backend is one that lives as
// long as the process.
//
// There is no cap on the rows. Every row costs a request that has already
// been paced, spent a network round trip and put a slice of a page in front of
// the model — a session cannot fill this the way it can fill a notebook, and a
// ledger that dropped its oldest rows would be missing exactly the early
// sources a long write-up cites.
func NewLedger(backend LedgerBackend) *Ledger {
	return &Ledger{backend: backend, now: time.Now, nextID: 1}
}

// SetScrub installs the rewrite a row goes through before it is kept. A URL
// carries whatever the model put in its query string, and a row outlives the
// turn that made it as a line in the state directory, so a vaulted value
// reaching one has leaked in the way that lasts longest.
// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
func (l *Ledger) SetScrub(scrub func(string) string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.scrub = scrub
	l.mu.Unlock()
}

// SetTurn tells the ledger which turn is open, so a row a child makes
// carries the turn its parent spawned it in. A ledger nobody tells stamps
// zero, which reads as "no turn said so" rather than as turn one.
func (l *Ledger) SetTurn(turn int64) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.turn = turn
	l.mu.Unlock()
}

// Bind names the session the ledger belongs to and loads what that session
// read. It is called wherever the session's slot becomes known or changes —
// at start, at resume, and again when the session rebinds. Rows recorded
// before the first Bind are written through under the new key.
func (l *Ledger) Bind(session string) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if session == l.session {
		return nil
	}
	l.session = session
	if l.backend == nil || session == "" {
		return nil
	}
	loaded, err := l.backend.LoadSources(session)
	if err != nil {
		return err
	}
	pending := l.rows
	l.rows = loaded
	for _, s := range loaded {
		if s.ID >= l.nextID {
			l.nextID = s.ID + 1
		}
	}
	// Anything already recorded was recorded before the bind; it goes to the
	// backend now, after what the slot already had. A backend that will not
	// place a row answers with a zero id rather than an error — a headless
	// run has no slot to hang one on — and the row keeps the id it was given
	// here.
	for _, s := range pending {
		id, err := l.backend.SaveSource(session, s)
		if err != nil {
			return err
		}
		if id > 0 {
			s.ID = id
		}
		l.rows = append(l.rows, s)
		if s.ID >= l.nextID {
			l.nextID = s.ID + 1
		}
	}
	return nil
}

// Record adds one read, signed by the agent that made it, and returns the
// row as stored. A ledger that cannot persist still records: the row is the
// session's own working state either way, and only the resume loses it.
func (l *Ledger) Record(agent string, s Source) Source {
	if l == nil {
		return s
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.scrub != nil {
		s.Query = l.scrub(s.Query)
		s.Requested = l.scrub(s.Requested)
		s.FinalURL = l.scrub(s.FinalURL)
		s.Title = l.scrub(s.Title)
	}
	s.Agent = agent
	s.Turn = l.turn
	s.ID = l.nextID
	if s.At.IsZero() {
		s.At = l.now()
	}
	if l.backend != nil && l.session != "" {
		if id, err := l.backend.SaveSource(l.session, s); err == nil && id > 0 {
			s.ID = id
		}
	}
	if s.ID >= l.nextID {
		l.nextID = s.ID + 1
	}
	l.rows = append(l.rows, s)
	return s
}

// List returns every row, oldest first.
func (l *Ledger) List() []Source {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Source, len(l.rows))
	copy(out, l.rows)
	return out
}

// Len is the number of rows.
func (l *Ledger) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.rows)
}

// Host is the row's host — the one the final URL names, falling back to the
// requested one for a fetch that never got an answer. It is what the sources
// screen groups by, and "" for a search, which left for the one endpoint the
// person configured rather than for a site.
func (s Source) Host() string {
	for _, raw := range []string{s.FinalURL, s.Requested} {
		if h := hostOf(raw); h != "" {
			return h
		}
	}
	return ""
}

// Pages returns the pages a ledger says were actually read, newest last, one
// row per final URL.
//
// Two rows are dropped and both for the same reason — a source is a page
// somebody could have read something in. A fetch that did not answer with a
// 2xx returned an error page or nothing at all, and a page fetched twice is
// one source: the row kept is the first read of it, because that is the one
// a write-up's claims were formed from.
func Pages(rows []Source) []Source {
	seen := map[string]bool{}
	var out []Source
	for _, s := range rows {
		if s.Kind != KindFetch || s.FinalURL == "" || s.Status < 200 || s.Status > 299 {
			continue
		}
		key := CanonicalURL(s.FinalURL)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

// CanonicalURL is the form two citations of one page are compared in: the
// scheme and host case-folded, a default port and a trailing slash dropped,
// and a fragment removed. It is deliberately not a normalisation of the path
// — %-encoding and a trailing "index.html" are the publisher's business, and
// a comparison that guessed at them would call two pages one.
func CanonicalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	scheme := ""
	rest := raw
	if i := strings.Index(raw, "://"); i >= 0 {
		scheme, rest = strings.ToLower(raw[:i]), raw[i+3:]
	}
	authority, path := rest, ""
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		authority, path = rest[:i], rest[i:]
	}
	authority = strings.ToLower(authority)
	switch {
	case scheme == "http" && strings.HasSuffix(authority, ":80"):
		authority = strings.TrimSuffix(authority, ":80")
	case scheme == "https" && strings.HasSuffix(authority, ":443"):
		authority = strings.TrimSuffix(authority, ":443")
	}
	path = strings.TrimSuffix(path, "/")
	if scheme == "" {
		return authority + path
	}
	return scheme + "://" + authority + path
}

// citedTrailers are the characters a URL in prose picks up from the sentence
// around it. They come off the end rather than being refused inside it: a
// path may legitimately hold any of them, and it is only the last ones that
// belong to the sentence rather than to the address.
const citedTrailers = ".,;:!?')]}"

// CitedURLs is every http or https URL a piece of prose names, in the order
// it names them and once each. It is how a write-up's citations are read
// back out of it, so a URL that is in the text and not in the ledger can be
// listed as cited and not read.
//
// It is a scan and not a parser: a citation may be bare, in a markdown link,
// in angle brackets or inside a sentence, and the one thing every shape has
// in common is that the address starts at the scheme and ends at whitespace
// or at a delimiter that can sit against one.
func CitedURLs(text string) []string {
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(text); {
		j := indexScheme(text[i:])
		if j < 0 {
			break
		}
		start := i + j
		end := start
		for end < len(text) && !isURLBreak(text[end]) {
			end++
		}
		i = end
		raw := trimCited(text[start:end])
		if len(raw) <= len("https://") {
			continue
		}
		key := CanonicalURL(raw)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, raw)
	}
	return out
}

// trimCited takes the sentence's punctuation off the end of an address. A
// closing bracket the address itself opened stays: a path with a parenthesis
// in it is a real shape — every disambiguated Wikipedia article has one — and
// cutting it names a page that does not exist. An unmatched one came from the
// sentence, which is the `(see https://x)` case, and goes.
func trimCited(raw string) string {
	for len(raw) > 0 {
		last := raw[len(raw)-1]
		if !strings.ContainsRune(citedTrailers, rune(last)) {
			break
		}
		if last == ')' && strings.Count(raw, "(") >= strings.Count(raw, ")") {
			break
		}
		raw = raw[:len(raw)-1]
	}
	return raw
}

// indexScheme is where the next http:// or https:// starts in s, or -1.
func indexScheme(s string) int {
	for at := 0; ; {
		i := strings.Index(s[at:], "http")
		if i < 0 {
			return -1
		}
		at += i
		rest := s[at:]
		if strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://") {
			return at
		}
		at += len("http")
	}
}

// isURLBreak reports the characters a URL in prose ends at: whitespace, and
// the two markdown delimiters that can sit directly against one.
func isURLBreak(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '<', '>', '"', '`':
		return true
	}
	return false
}

// hostOf is the host of a URL, without parsing it as one: the ledger's rows
// hold what the fetcher returned, which is already a URL it parsed.
func hostOf(raw string) string {
	rest := strings.TrimSpace(raw)
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndexByte(rest, '@'); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.LastIndexByte(rest, ':'); i >= 0 && !strings.Contains(rest[i:], "]") {
		rest = rest[:i]
	}
	return strings.ToLower(strings.Trim(rest, "[]"))
}
