package web

// What the world says about a host, read before a fetch is judged
// (docs/capabilities/approvals-and-safety.md#a-host-is-read-against-the-world-before-it-is-judged).
//
// The reading advises and never widens. It is handed to the approval policy
// and to the auto-mode classifier beside the host, and the policy is where
// what it may change is decided: a known host is let through where auto mode
// would have asked a classifier, and a young, disposable or listed one is put
// to the person where a classifier would have let it through. Nothing here
// decides anything.

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Standing is what the lists say about a host: a closed set, so a policy and
// a record can each spell every answer.
type Standing string

const (
	// StandingKnown is a host on shhh's own short list, or among the most
	// visited sites the Tranco ranking names.
	StandingKnown Standing = "known"
	// StandingUnknown is every host no list names, and every host a list that
	// could not be read would have named. It changes nothing.
	StandingUnknown Standing = "unknown"
	// StandingYoung is a domain registered in the last few days.
	StandingYoung Standing = "young"
	// StandingDisposable is a throwaway domain: the kind made to be used once
	// and dropped.
	StandingDisposable Standing = "disposable"
	// StandingListed is a host a malware or blocking list names.
	StandingListed Standing = "listed"
)

// Reading is one host's standing and the list that gave it. The zero value
// reads as unknown, which is what a surface that was given no lists has.
type Reading struct {
	Standing Standing
	// Source is the list that said so, by the name the configuration turns
	// it off with, and empty for an unknown host.
	Source string
}

// Known reports whether the reading is one that may let a fetch through
// where auto mode would have asked.
func (r Reading) Known() bool { return r.Standing == StandingKnown }

// Warns reports whether the reading is one that puts a fetch to the person
// where a classifier would have let it through.
func (r Reading) Warns() bool {
	switch r.Standing {
	case StandingYoung, StandingDisposable, StandingListed:
		return true
	}
	return false
}

// Reason is the reading in the words the card and the row print after a
// dash, and "" for a reading that says nothing. It names the list, because a
// person who disagrees with a verdict needs to know whose it was to turn it
// off.
func (r Reading) Reason() string {
	if r.Standing == StandingKnown && r.Source == BuiltinHosts {
		return builtinReason
	}
	for _, src := range hostSources {
		if src.name == r.Source && src.standing == r.Standing {
			return src.reason
		}
	}
	return ""
}

// StandingOf is the standing a reason string was written for, and "" for
// any other text. It is the inverse of Reason and sits beside it so the two
// cannot drift: the record files a decision by its reason, and a reason this
// could not read back would reach the metrics as "other".
func StandingOf(reason string) Standing {
	if reason == builtinReason {
		return StandingKnown
	}
	for _, src := range hostSources {
		if reason == src.reason {
			return src.standing
		}
	}
	return ""
}

// BuiltinHosts is the name of shhh's own list, as the configuration turns it
// off.
const BuiltinHosts = "builtin"

const builtinReason = "known host (built-in list)"

// builtinKnown is the short list shhh ships of the sites a coding session
// reads every day. It is short on purpose and it is shhh's: every entry is an
// organisation that runs every host under its name itself, so the entry
// covers its subdomains, which is exactly what the Tranco ranking cannot say
// about a platform where anybody can publish.
// See docs/capabilities/approvals-and-safety.md#a-host-is-read-against-the-world-before-it-is-judged.
var builtinKnown = []string{
	"anthropic.com", "claude.com", "openai.com",
	"github.com", "gitlab.com",
	"stackoverflow.com", "stackexchange.com", "serverfault.com", "superuser.com",
	"go.dev", "golang.org",
	"python.org", "pypi.org",
	"rust-lang.org", "docs.rs", "crates.io",
	"nodejs.org", "npmjs.com", "typescriptlang.org", "developer.mozilla.org",
	"ruby-lang.org", "rubygems.org",
	"kotlinlang.org", "swift.org", "dev.java", "docs.oracle.com",
	"learn.microsoft.com", "cppreference.com", "php.net",
	"postgresql.org", "sqlite.org", "git-scm.com", "kubernetes.io", "docs.docker.com",
}

// trancoTop is how much of the Tranco ranking counts as known. Past the first
// ten thousand, a site being visited says little about whether it is safe to
// read from, and the list is a million rows.
const trancoTop = 10000

// hostSource is one list: what it answers, how often it is asked again, how
// old it may be and still answer, and how it is downloaded.
type hostSource struct {
	// name is the list as the configuration names it and as its file is
	// named in the cache.
	name     string
	standing Standing
	// reason is what a decision the list changed says, in the card's words.
	reason string
	// refresh is when a copy on disk is old enough to ask for again, and
	// window is when it is too old to answer at all. The two are different
	// because a refresh that has not landed yet should not silence a list
	// that was right yesterday, and a list nobody has been able to fetch for
	// weeks should not go on answering for today.
	refresh, window time.Duration
	// snapshot is the list as shhh ships it, in the on-disk form, or nil for
	// a list whose truth decays in days — a snapshot of this week's new
	// domains is wrong by the time the binary is installed.
	snapshot []byte
	// url is where the list is downloaded from, and parse reads its body
	// into hosts. download, where it is set, replaces the one request —
	// Tranco names each day's list by an identifier asked for first.
	url      string
	parse    func([]byte) []string
	download func(ctx context.Context, c *http.Client) ([]byte, error)
}

//go:embed hosts/tranco.hosts
var trancoSnapshot []byte

//go:embed hosts/disposable.hosts
var disposableSnapshot []byte

const day = 24 * time.Hour

// hostSources is every list in the order a reading consults them: the ones
// that warn first, so a host two lists disagree about is read the way that
// never widens, and the popularity ranking last.
var hostSources = []hostSource{
	{
		name: "urlhaus", standing: StandingListed, reason: "listed host (urlhaus)",
		refresh: day, window: 7 * day,
		url: "https://urlhaus.abuse.ch/downloads/hostfile/", parse: parseHostsFile,
	},
	{
		name: "stevenblack", standing: StandingListed, reason: "listed host (stevenblack)",
		refresh: 3 * day, window: 30 * day,
		url: "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts", parse: parseHostsFile,
	},
	{
		name: "disposable", standing: StandingDisposable, reason: "disposable domain (disposable list)",
		refresh: 7 * day, window: 180 * day, snapshot: disposableSnapshot,
		url:   "https://raw.githubusercontent.com/disposable-email-domains/disposable-email-domains/main/disposable_email_blocklist.conf",
		parse: parseDomainLines,
	},
	{
		name: "nrd", standing: StandingYoung, reason: "registered in the last 10 days (nrd)",
		refresh: day, window: 3 * day,
		url: "https://dl.cenk.app/nrd/nrd-last-10-days.txt", parse: parseDomainLines,
	},
	{
		name: "tranco", standing: StandingKnown, reason: "known host (tranco top 10k)",
		refresh: 7 * day, window: 180 * day, snapshot: trancoSnapshot,
		url: "https://tranco-list.eu/api/lists/date/latest", parse: parseTranco, download: downloadTranco,
	},
}

// HostListNames is every list the configuration can turn off, shhh's own
// first.
func HostListNames() []string {
	names := []string{BuiltinHosts}
	for _, src := range hostSources {
		names = append(names, src.name)
	}
	return names
}

// Reputation reads hosts against the lists. One is opened per process and
// installed with UseReputation, so a session, its children and an
// unattended run all read a host through the same function.
type Reputation struct {
	dir     string
	builtin bool
	lists   []*hostList
	// now is the clock ages are judged by; a test replaces it.
	now func() time.Time

	mu   sync.Mutex
	memo map[string]Reading
}

// hostList is one source as this process holds it: the source, and the one
// background refresh it may start.
type hostList struct {
	src     hostSource
	refresh sync.Once
}

// OpenReputation is the reading over the lists cached under dir, with the
// ones off names turned off. An empty dir is a machine with nowhere to cache
// a list: the shipped snapshots answer and nothing is downloaded. It reads nothing and downloads nothing: a list
// is opened the first time a host is read against it, and that is also the
// first time a stale one is asked for again.
func OpenReputation(dir string, off []string) *Reputation {
	skip := make(map[string]bool, len(off))
	for _, name := range off {
		skip[strings.ToLower(strings.TrimSpace(name))] = true
	}
	r := &Reputation{dir: dir, builtin: !skip[BuiltinHosts], now: time.Now, memo: map[string]Reading{}}
	for _, src := range hostSources {
		if !skip[src.name] {
			r.lists = append(r.lists, &hostList{src: src})
		}
	}
	return r
}

// current is the reputation this process reads with. It is package state on
// purpose, like the runner's adopter: a fetch is decided on half a dozen
// paths — the session's card, a child's policy, an unattended run, a served
// one — and a reading that reached some of them and not the others would be
// a host a parent lets through and its child asks about.
var current atomic.Pointer[Reputation]

// UseReputation installs the reading every fetch decision in this process
// asks. Nil takes it away, and every host reads as unknown again.
func UseReputation(r *Reputation) { current.Store(r) }

// ReadFetch is the reading of the host a fetch call leaves for — the one
// function every surface asks. A URL that carries a query string never reads
// as known from the popularity ranking: a query string is how a GET carries
// data out, and a site being visited says nothing about who reads what is
// sent to it. It still reads as known from shhh's own list, whose sites were
// chosen, and it still reads as young, disposable or listed.
func ReadFetch(args json.RawMessage) Reading {
	var a fetchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Reading{Standing: StandingUnknown}
	}
	u, err := url.Parse(strings.TrimSpace(a.URL))
	if err != nil {
		return Reading{Standing: StandingUnknown}
	}
	reading := ReadHost(u.Hostname())
	if reading.Known() && reading.Source != BuiltinHosts && (u.RawQuery != "" || u.User != nil) {
		return Reading{Standing: StandingUnknown}
	}
	return reading
}

// ReadHost is the reading of one host with the installed reputation, and
// unknown where none is installed.
func ReadHost(host string) Reading {
	r := current.Load()
	if r == nil {
		return Reading{Standing: StandingUnknown}
	}
	return r.Read(host)
}

// Read is what the lists say about host. It is remembered for the process,
// because the card and the policy ask about the same host more than once.
func (r *Reputation) Read(host string) Reading {
	host = normalizeHost(host)
	if host == "" {
		return Reading{Standing: StandingUnknown}
	}
	r.mu.Lock()
	if reading, ok := r.memo[host]; ok {
		r.mu.Unlock()
		return reading
	}
	r.mu.Unlock()

	reading := r.read(host)
	r.mu.Lock()
	r.memo[host] = reading
	r.mu.Unlock()
	return reading
}

func (r *Reputation) read(host string) Reading {
	// The lists name domains. An address or a single-label name — loopback,
	// a machine on the local network — is nothing any of them could vouch
	// for or warn about, so it is not read against them at all, and a fetch
	// to one never starts a download.
	if isIP(host) || !strings.Contains(host, ".") {
		return Reading{Standing: StandingUnknown}
	}
	names := candidates(host)
	for _, l := range r.lists {
		if l.src.standing == StandingKnown {
			continue
		}
		if r.has(l, names) {
			return Reading{Standing: l.src.standing, Source: l.src.name}
		}
	}
	if r.builtin {
		for _, name := range names {
			for _, known := range builtinKnown {
				if name == known {
					return Reading{Standing: StandingKnown, Source: BuiltinHosts}
				}
			}
		}
	}
	for _, l := range r.lists {
		if l.src.standing == StandingKnown && r.has(l, names) {
			return Reading{Standing: StandingKnown, Source: l.src.name}
		}
	}
	return Reading{Standing: StandingUnknown}
}

// has reports whether any of names is on the list. A list that cannot be read
// — missing, malformed, or older than its window — names nothing, which is
// what makes a failure read as unknown and never as known.
func (r *Reputation) has(l *hostList, names []string) bool {
	r.maybeRefresh(l)
	search, closeFn, ok := r.open(l)
	if !ok {
		return false
	}
	defer closeFn()
	for _, name := range names {
		if search.has(name) {
			return true
		}
	}
	return false
}

// open finds the copy of a list that may answer: the download when it is
// readable and within its window, the shipped snapshot when that is.
func (r *Reputation) open(l *hostList) (sortedHosts, func(), bool) {
	now := r.now()
	if f, err := os.Open(r.path(l.src.name)); r.dir != "" && err == nil {
		if info, err := f.Stat(); err == nil {
			if s, at, ok := readHeader(f, info.Size(), l.src.name); ok && now.Sub(at) <= l.src.window {
				return s, func() { f.Close() }, true
			}
		}
		f.Close()
	}
	if len(l.src.snapshot) > 0 {
		b := bytes.NewReader(l.src.snapshot)
		if s, at, ok := readHeader(b, int64(len(l.src.snapshot)), l.src.name); ok && now.Sub(at) <= l.src.window {
			return s, func() {}, true
		}
	}
	return sortedHosts{}, nil, false
}

// ListState is one list as the doctor reports it.
type ListState struct {
	Name string
	// Off is a list the configuration turned off.
	Off bool
	// From is where the copy that answers came from: "download",
	// "snapshot", or "" when nothing can answer.
	From string
	// Age is how old that copy is.
	Age time.Duration
	// Stale is a copy on disk older than its window, which answers nothing.
	Stale bool
	// Failed is a download that did not land within the last hour.
	Failed bool
}

// Lists is every list's state, shhh's own first, for the doctor.
func (r *Reputation) Lists() []ListState {
	now := r.now()
	out := []ListState{{Name: BuiltinHosts, Off: !r.builtin, From: "binary"}}
	on := map[string]*hostList{}
	for _, l := range r.lists {
		on[l.src.name] = l
	}
	for _, src := range hostSources {
		l, ok := on[src.name]
		if !ok {
			out = append(out, ListState{Name: src.name, Off: true})
			continue
		}
		st := ListState{Name: src.name}
		if info, err := os.Stat(r.failMarker(src.name)); err == nil && now.Sub(info.ModTime()) < hostFailTTL {
			st.Failed = true
		}
		if f, err := os.Open(r.path(src.name)); r.dir != "" && err == nil {
			if info, err := f.Stat(); err == nil {
				if _, at, ok := readHeader(f, info.Size(), src.name); ok {
					st.Age = now.Sub(at)
					if st.Age <= src.window {
						st.From = "download"
					} else {
						st.Stale = true
					}
				}
			}
			f.Close()
		}
		if st.From == "" && len(l.src.snapshot) > 0 {
			if _, at, ok := readHeader(bytes.NewReader(l.src.snapshot), int64(len(l.src.snapshot)), src.name); ok &&
				now.Sub(at) <= src.window {
				st.From, st.Age, st.Stale = "snapshot", now.Sub(at), false
			}
		}
		out = append(out, st)
	}
	return out
}

func (r *Reputation) path(name string) string {
	return filepath.Join(r.dir, name+".hosts")
}

func (r *Reputation) failMarker(name string) string {
	return filepath.Join(r.dir, name+".fail")
}

// hostFailTTL is how long a failed download is remembered, for the reason
// the model-data table remembers one: without it every process asks again
// and pays the timeout again.
const hostFailTTL = time.Hour

// hostClient is the downloads' HTTP client. It is a package variable so a
// test can put a transport in front of it that stands in for a network that
// is not there; nothing else replaces it.
var hostClient = &http.Client{Timeout: 2 * time.Minute}

// maxListBytes bounds one download. The largest list is tens of megabytes;
// a body past this is not a list.
const maxListBytes = 128 << 20

var (
	// hostRefreshing holds every background download so a test can wait for
	// one, and hostRefreshStarted is whether any started at all.
	hostRefreshing     sync.WaitGroup
	hostRefreshStarted atomic.Bool
)

// HostListsRefreshed reports whether this process has started a list
// download. It is how a suite in another package asserts it never reached a
// real list: a download that was never started is the proof nothing left the
// machine for one.
func HostListsRefreshed() bool { return hostRefreshStarted.Load() }

// maybeRefresh starts the list's download behind the caller when the copy on
// disk is missing or older than its refresh, at most once per process, and
// not within an hour of a download that failed. The reading goes on with
// what is there; the next process reads what lands.
func (r *Reputation) maybeRefresh(l *hostList) {
	if r.dir == "" {
		return
	}
	now := r.now()
	if info, err := os.Stat(r.failMarker(l.src.name)); err == nil && now.Sub(info.ModTime()) < hostFailTTL {
		return
	}
	if f, err := os.Open(r.path(l.src.name)); err == nil {
		info, statErr := f.Stat()
		fresh := false
		if statErr == nil {
			if _, at, ok := readHeader(f, info.Size(), l.src.name); ok && now.Sub(at) <= l.src.refresh {
				fresh = true
			}
		}
		f.Close()
		if fresh {
			return
		}
	}
	l.refresh.Do(func() {
		hostRefreshStarted.Store(true)
		hostRefreshing.Add(1)
		go func() {
			defer hostRefreshing.Done()
			_ = r.download(context.Background(), l.src)
		}()
	})
}

// download fetches one list over its cache and records the outcome: a
// failure leaves the copy on disk alone and writes the marker, a success
// clears it.
func (r *Reputation) download(ctx context.Context, src hostSource) error {
	if err := r.fetchList(ctx, src); err != nil {
		if os.MkdirAll(r.dir, 0o700) == nil {
			_ = os.WriteFile(r.failMarker(src.name), []byte(r.now().UTC().Format(time.RFC3339)+"\n"), 0o600)
		}
		return err
	}
	_ = os.Remove(r.failMarker(src.name))
	return nil
}

func (r *Reputation) fetchList(ctx context.Context, src hostSource) error {
	var body []byte
	var err error
	if src.download != nil {
		body, err = src.download(ctx, hostClient)
	} else {
		body, err = getBody(ctx, hostClient, src.url)
	}
	if err != nil {
		return err
	}
	hosts := normalizeList(src.parse(body))
	// An empty list is a body that was not the list — an error page, a
	// format that changed — and is not written: it would read as a list
	// that names nobody, which is a failure dressed as an answer.
	if len(hosts) == 0 {
		return fmt.Errorf("%s: no hosts in the download", src.name)
	}
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return err
	}
	// Written beside the copy and renamed over it, because the writer is a
	// goroutine in a process that may exit at any moment.
	tmp, err := os.CreateTemp(r.dir, src.name+".hosts.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := writeHostList(tmp, src.name, r.now(), hosts); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), r.path(src.name))
}

func getBody(ctx context.Context, c *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxListBytes))
}

// downloadTranco asks for the latest list's identifier and then for its top
// rows alone, so the million-row list is never downloaded.
func downloadTranco(ctx context.Context, c *http.Client) ([]byte, error) {
	meta, err := getBody(ctx, c, "https://tranco-list.eu/api/lists/date/latest")
	if err != nil {
		return nil, err
	}
	var latest struct {
		ListID string `json:"list_id"`
	}
	if err := json.Unmarshal(meta, &latest); err != nil || latest.ListID == "" ||
		strings.ContainsAny(latest.ListID, "/?#") {
		return nil, errors.New("tranco: no list identifier")
	}
	return getBody(ctx, c, fmt.Sprintf("https://tranco-list.eu/download/%s/%d", url.PathEscape(latest.ListID), trancoTop))
}

// parseTranco reads `rank,domain` rows, keeping the top trancoTop.
func parseTranco(body []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() && len(out) < trancoTop {
		_, domain, ok := strings.Cut(strings.TrimSpace(sc.Text()), ",")
		if ok {
			out = append(out, domain)
		}
	}
	return out
}

// parseDomainLines reads one domain per line, with # comments.
func parseDomainLines(body []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Fields(line)[0])
	}
	return out
}

// parseHostsFile reads a hosts file: an address and a name per line. Only the
// names pointed at a null address are the list's; the lines naming localhost
// and the broadcast address are the file being a hosts file.
func parseHostsFile(body []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] != "0.0.0.0" && fields[0] != "127.0.0.1" {
			continue
		}
		for _, name := range fields[1:] {
			if name == "0.0.0.0" || name == "localhost" || name == "localhost.localdomain" || name == "local" {
				continue
			}
			out = append(out, name)
		}
	}
	return out
}

// hostHeader opens every list file: the format, the list it is, and when it
// was taken. '#' sorts ahead of every character a host can hold, so the
// header keeps the file sorted.
const hostHeader = "#shhh-hosts 1 "

// writeHostList writes the on-disk form: the header, then one host per line,
// sorted and unique, which is what a lookup searches without loading it.
func writeHostList(w io.Writer, name string, at time.Time, hosts []string) error {
	bw := bufio.NewWriter(w)
	if _, err := fmt.Fprintf(bw, "%s%s %s\n", hostHeader, name, at.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	for _, h := range hosts {
		if _, err := bw.WriteString(h + "\n"); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// readHeader checks a list's first line and returns the search over the rest
// with when the list was taken. A file that is not a list of this name, in
// this format, is not read at all.
func readHeader(r io.ReaderAt, size int64, name string) (sortedHosts, time.Time, bool) {
	s := sortedHosts{r: r, size: size}
	line, ok := s.line(0)
	if !ok {
		return sortedHosts{}, time.Time{}, false
	}
	rest, found := strings.CutPrefix(line, hostHeader)
	if !found {
		return sortedHosts{}, time.Time{}, false
	}
	gotName, stamp, _ := strings.Cut(rest, " ")
	if gotName != name {
		return sortedHosts{}, time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return sortedHosts{}, time.Time{}, false
	}
	s.start = int64(len(line)) + 1
	return s, at, true
}

// sortedHosts is a binary search over a sorted file of lines, read where it
// lies: a lookup reads a few hundred bytes a probe and never the whole list.
type sortedHosts struct {
	r           io.ReaderAt
	start, size int64
}

// has reports whether key is a line of the file.
func (s sortedHosts) has(key string) bool {
	lo, hi := s.start, s.size
	for lo < hi {
		mid := lo + (hi-lo)/2
		start := mid
		if mid > lo {
			nl, ok := s.newline(mid - 1)
			if !ok {
				hi = mid
				continue
			}
			start = nl + 1
		}
		if start >= hi {
			hi = mid
			continue
		}
		line, ok := s.line(start)
		if !ok {
			return false
		}
		switch c := strings.Compare(line, key); {
		case c == 0:
			return true
		case c < 0:
			lo = start + int64(len(line)) + 1
		default:
			hi = mid
		}
	}
	return false
}

// newline is the offset of the first newline at or after off.
func (s sortedHosts) newline(off int64) (int64, bool) {
	var buf [256]byte
	for off < s.size {
		n, err := s.r.ReadAt(buf[:], off)
		if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
			return off + int64(i), true
		}
		if n == 0 || (err != nil && err != io.EOF) {
			return 0, false
		}
		off += int64(n)
	}
	return 0, false
}

// line is the line starting at off, without its newline.
func (s sortedHosts) line(off int64) (string, bool) {
	var out []byte
	var buf [256]byte
	for off < s.size {
		n, err := s.r.ReadAt(buf[:], off)
		if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
			return string(append(out, buf[:i]...)), true
		}
		out = append(out, buf[:n]...)
		if n == 0 || (err != nil && err != io.EOF) {
			return "", false
		}
		off += int64(n)
	}
	return string(out), len(out) > 0
}

// normalizeList lower-cases, validates, sorts and de-duplicates a list's
// hosts into the on-disk order.
func normalizeList(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h = normalizeHost(h); h != "" {
			out = append(out, h)
		}
	}
	sort.Strings(out)
	j := 0
	for i, h := range out {
		if i == 0 || h != out[j-1] {
			out[j] = h
			j++
		}
	}
	return out[:j]
}

// normalizeHost is a host in the one spelling the lists are written in, and
// "" for text that is not a host.
func normalizeHost(h string) string {
	h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	if h == "" || len(h) > 253 {
		return ""
	}
	for _, c := range h {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '-', c == '_', c == ':':
		default:
			return ""
		}
	}
	return h
}

func isIP(host string) bool { return net.ParseIP(host) != nil }

// candidates is the host and every name above it down to its registrable
// domain, which is where a list's entry stops covering it. The registrable
// domain is the public suffix list's, private section included, so
// `anything.github.io` and `anything.workers.dev` are domains of their own
// and are not covered by the platform's rank.
func candidates(host string) []string {
	root, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return []string{host}
	}
	out := []string{host}
	for name := host; name != root; {
		_, parent, ok := strings.Cut(name, ".")
		if !ok {
			break
		}
		out = append(out, parent)
		name = parent
	}
	return out
}
