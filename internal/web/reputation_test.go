package web

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fixedNow is the clock every reputation test reads by.
var fixedNow = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

type hostRoundTrip func(*http.Request) (*http.Response, error)

func (f hostRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubHostClient puts fn in front of the list downloads for one test and
// counts the requests it answers. Every reputation test installs one, so a
// test that reached a real list would be answered by the stub rather than the
// network.
func stubHostClient(t *testing.T, fn func(*http.Request) (*http.Response, error)) *atomic.Int64 {
	t.Helper()
	var calls atomic.Int64
	original := hostClient.Transport
	hostClient.Transport = hostRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fn(r)
	})
	t.Cleanup(func() { hostClient.Transport = original })
	return &calls
}

// refuseDownloads is the stub a test that must not download installs.
func refuseDownloads(t *testing.T) *atomic.Int64 {
	return stubHostClient(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("a list was downloaded: %s", r.URL)
		return nil, errors.New("no network in this test")
	})
}

func writeFixture(t *testing.T, dir, name string, at time.Time, hosts ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".hosts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeHostList(f, name, at, normalizeList(hosts)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// fixtureReputation is a reputation over fresh fixture copies of every list:
// each list holds what lists names and nothing else, so no list is stale and
// nothing is downloaded.
func fixtureReputation(t *testing.T, lists map[string][]string, off ...string) *Reputation {
	t.Helper()
	refuseDownloads(t)
	dir := t.TempDir()
	for _, src := range hostSources {
		writeFixture(t, dir, src.name, fixedNow.Add(-time.Hour), lists[src.name]...)
	}
	r := OpenReputation(dir, off)
	r.now = func() time.Time { return fixedNow }
	return r
}

func TestReputationReadsEachStanding(t *testing.T) {
	r := fixtureReputation(t, map[string][]string{
		"tranco":      {"example.com", "github.io", "doubleclick.net"},
		"nrd":         {"brand-new.test"},
		"disposable":  {"throwaway.test"},
		"urlhaus":     {"malware.test"},
		"stevenblack": {"doubleclick.net"},
	})
	for _, c := range []struct {
		host string
		want Reading
	}{
		{"example.com", Reading{StandingKnown, "tranco"}},
		{"docs.example.com", Reading{StandingKnown, "tranco"}},
		{"docs.python.org", Reading{StandingKnown, BuiltinHosts}},
		{"GitHub.com.", Reading{StandingKnown, BuiltinHosts}},
		{"nowhere.test", Reading{StandingUnknown, ""}},
		{"brand-new.test", Reading{StandingYoung, "nrd"}},
		{"mail.throwaway.test", Reading{StandingDisposable, "disposable"}},
		{"malware.test", Reading{StandingListed, "urlhaus"}},
		// A host two lists disagree about is read the way that never widens.
		{"ad.doubleclick.net", Reading{StandingListed, "stevenblack"}},
		// A platform's rank does not cover a site somebody published on it:
		// the registrable domain is the public suffix list's.
		{"attacker.github.io", Reading{StandingUnknown, ""}},
		{"93.184.216.34", Reading{StandingUnknown, ""}},
		{"127.0.0.1", Reading{StandingUnknown, ""}},
		{"localhost", Reading{StandingUnknown, ""}},
		{"", Reading{StandingUnknown, ""}},
	} {
		if got := r.Read(c.host); got != c.want {
			t.Errorf("Read(%q) = %+v, want %+v", c.host, got, c.want)
		}
	}
}

func TestReputationReasonsReadBack(t *testing.T) {
	readings := []Reading{{StandingKnown, BuiltinHosts}}
	for _, src := range hostSources {
		readings = append(readings, Reading{src.standing, src.name})
	}
	for _, reading := range readings {
		reason := reading.Reason()
		if reason == "" {
			t.Fatalf("%+v has no reason", reading)
		}
		if got := StandingOf(reason); got != reading.Standing {
			t.Errorf("StandingOf(%q) = %q, want %q", reason, got, reading.Standing)
		}
	}
	if (Reading{Standing: StandingUnknown}).Reason() != "" || StandingOf("session grant") != "" {
		t.Error("an unknown reading has a reason, or an unrelated reason reads as a standing")
	}
}

// A list that cannot be read names nobody. That is the whole of how a failure
// stays on the side of asking: the known lists fall silent and the reading
// is unknown, never known.
func TestReputationAListThatCannotBeReadSaysNothing(t *testing.T) {
	refuseDownloads(t)
	later := fixedNow.Add(365 * day)
	for _, c := range []struct {
		name  string
		write func(dir string)
	}{
		{"missing", func(string) {}},
		{"stale", func(dir string) { writeFixture(t, dir, "tranco", later.Add(-181*day), "example.com") }},
		{"malformed", func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, "tranco.hosts"), []byte("example.com\n"), 0o600)
		}},
		{"another list's file", func(dir string) {
			var buf bytes.Buffer
			_ = writeHostList(&buf, "nrd", later, []string{"example.com"})
			_ = os.WriteFile(filepath.Join(dir, "tranco.hosts"), buf.Bytes(), 0o600)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, src := range hostSources {
				if src.name != "tranco" {
					writeFixture(t, dir, src.name, later)
				}
			}
			c.write(dir)
			// The failure marker keeps the missing and broken copies from
			// being asked for again, which this test is not about.
			_ = os.WriteFile(filepath.Join(dir, "tranco.fail"), nil, 0o600)
			r := OpenReputation(dir, nil)
			// Past the shipped snapshot's window too, so the only copy left to
			// answer is the one this case wrote.
			r.now = func() time.Time { return later }
			_ = os.Chtimes(filepath.Join(dir, "tranco.fail"), r.now(), r.now())
			if got := r.Read("example.com"); got.Standing != StandingUnknown {
				t.Fatalf("Read = %+v, want unknown", got)
			}
		})
	}
}

func TestReputationListsCanBeTurnedOff(t *testing.T) {
	lists := map[string][]string{"tranco": {"example.com"}, "urlhaus": {"malware.test"}}
	r := fixtureReputation(t, lists, "tranco", "urlhaus", BuiltinHosts)
	for _, host := range []string{"example.com", "malware.test", "github.com"} {
		if got := r.Read(host); got.Standing != StandingUnknown {
			t.Errorf("Read(%q) = %+v with its list off", host, got)
		}
	}
}

// The ranking says a site is visited, not who reads what is sent to it, and
// a query string is how a GET sends something.
func TestReadFetchAQueryStringIsNotVouchedForByTheRanking(t *testing.T) {
	r := fixtureReputation(t, map[string][]string{"tranco": {"telegram.org"}, "urlhaus": {"malware.test"}})
	UseReputation(r)
	t.Cleanup(func() { UseReputation(nil) })
	for _, c := range []struct {
		url  string
		want Standing
	}{
		{"https://api.telegram.org/bot1/getMe", StandingKnown},
		{"https://api.telegram.org/bot1/sendMessage?chat_id=1&text=secret", StandingUnknown},
		{"https://user@telegram.org/", StandingUnknown},
		{"https://pkg.go.dev/search?q=context", StandingKnown},
		{"https://malware.test/?x=1", StandingListed},
	} {
		args, _ := json.Marshal(map[string]string{"url": c.url})
		if got := ReadFetch(args); got.Standing != c.want {
			t.Errorf("ReadFetch(%s) = %+v, want %s", c.url, got, c.want)
		}
	}
	if got := ReadFetch(json.RawMessage(`{`)); got.Standing != StandingUnknown {
		t.Errorf("unreadable arguments read as %+v", got)
	}
}

func TestReadHostWithNothingInstalledIsUnknown(t *testing.T) {
	UseReputation(nil)
	if got := ReadHost("github.com"); got.Standing != StandingUnknown {
		t.Fatalf("ReadHost = %+v with no reputation installed", got)
	}
}

// The shipped copies answer before anything was downloaded.
func TestReputationTheSnapshotAnswersBeforeADownload(t *testing.T) {
	refuseDownloads(t)
	dir := t.TempDir()
	for _, src := range hostSources {
		// A marker for every list, so the missing copies are not asked for.
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, src.name+".fail"), nil, 0o600)
	}
	r := OpenReputation(dir, nil)
	if got := r.Read("google.com"); got != (Reading{StandingKnown, "tranco"}) {
		t.Fatalf("Read(google.com) = %+v from the snapshot", got)
	}
	if got := r.Read("0-mail.com"); got != (Reading{StandingDisposable, "disposable"}) {
		t.Fatalf("Read(0-mail.com) = %+v from the snapshot", got)
	}
}

// The snapshots are searched where they lie, so they have to be in the
// on-disk form exactly: the header, then sorted unique hosts in the one
// spelling. A snapshot regenerated by hand that broke the order would make
// the search miss hosts it holds.
func TestHostSnapshotsAreInTheOnDiskForm(t *testing.T) {
	for name, data := range map[string][]byte{"tranco": trancoSnapshot, "disposable": disposableSnapshot} {
		_, at, ok := readHeader(bytes.NewReader(data), int64(len(data)), name)
		if !ok || at.IsZero() {
			t.Fatalf("%s: the snapshot's header does not read", name)
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Scan()
		prev, n := "", 0
		for sc.Scan() {
			h := sc.Text()
			if normalizeHost(h) != h || h <= prev {
				t.Fatalf("%s: line %q after %q is not normalised, sorted and unique", name, h, prev)
			}
			prev = h
			n++
		}
		if n == 0 {
			t.Fatalf("%s: the snapshot holds no hosts", name)
		}
	}
}

func TestSortedHostsFindsEveryLineAndNothingElse(t *testing.T) {
	hosts := []string{"a.test", "b.test", "bb.test", "c.test", strings.Repeat("x", 300) + ".test", "z.test"}
	var buf bytes.Buffer
	if err := writeHostList(&buf, "t", fixedNow, hosts); err != nil {
		t.Fatal(err)
	}
	s, _, ok := readHeader(bytes.NewReader(buf.Bytes()), int64(buf.Len()), "t")
	if !ok {
		t.Fatal("header did not read")
	}
	for _, h := range hosts {
		if !s.has(h) {
			t.Errorf("has(%q) = false", h)
		}
	}
	for _, h := range []string{"", "a", "a.tes", "b.testt", "d.test", "zz.test", "#shhh-hosts 1 t"} {
		if s.has(h) {
			t.Errorf("has(%q) = true", h)
		}
	}
}

// fakeLists answers each source's URL with a body in that source's own
// format.
func fakeLists(t *testing.T) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		body := ""
		switch u := r.URL.String(); {
		case strings.HasSuffix(u, "/api/lists/date/latest"):
			body = `{"list_id": "ABCDE", "download": "https://tranco-list.eu/download/ABCDE/1000000"}`
		case strings.HasSuffix(u, "/download/ABCDE/10000"):
			body = "1,Example.com\n2,other.test\n"
		case strings.Contains(u, "urlhaus"):
			body = "# URLhaus Host file\n127.0.0.1\tmalware.test\n"
		case strings.Contains(u, "StevenBlack"):
			body = "127.0.0.1 localhost\n0.0.0.0 0.0.0.0\n0.0.0.0 ads.test # tracker\n"
		case strings.Contains(u, "disposable"):
			body = "throwaway.test\n"
		case strings.Contains(u, "nrd"):
			body = "# NRD\nbrand-new.test\n"
		default:
			t.Errorf("unexpected request %s", u)
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
}

func TestReputationDownloadsAMissingListBehindTheCaller(t *testing.T) {
	calls := stubHostClient(t, fakeLists(t))
	dir := t.TempDir()
	r := OpenReputation(dir, nil)
	r.now = func() time.Time { return fixedNow }
	// The first reading answers with what is there, which for nrd is nothing.
	if got := r.Read("brand-new.test"); got.Standing != StandingUnknown {
		t.Fatalf("the first reading waited for the download: %+v", got)
	}
	hostRefreshing.Wait()
	if calls.Load() == 0 {
		t.Fatal("nothing was downloaded")
	}
	// The next process reads what landed.
	next := OpenReputation(dir, nil)
	next.now = func() time.Time { return fixedNow }
	for host, want := range map[string]Reading{
		"brand-new.test": {StandingYoung, "nrd"},
		"malware.test":   {StandingListed, "urlhaus"},
		"ads.test":       {StandingListed, "stevenblack"},
		"throwaway.test": {StandingDisposable, "disposable"},
		"example.com":    {StandingKnown, "tranco"},
	} {
		if got := next.Read(host); got != want {
			t.Errorf("after the download, Read(%q) = %+v, want %+v", host, got, want)
		}
	}
	if next.Read("localhost").Standing != StandingUnknown {
		t.Error("a hosts file's own localhost line was read as a listed host")
	}
}

func TestReputationAFailedDownloadIsRememberedAndKeepsTheCopy(t *testing.T) {
	calls := stubHostClient(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	dir := t.TempDir()
	writeFixture(t, dir, "urlhaus", fixedNow.Add(-2*day), "malware.test")
	r := OpenReputation(dir, []string{"stevenblack", "disposable", "nrd", "tranco"})
	r.now = func() time.Time { return fixedNow }
	if err := r.download(t.Context(), hostSources[0]); err == nil {
		t.Fatal("a 500 was taken as a list")
	}
	if _, err := os.Stat(filepath.Join(dir, "urlhaus.fail")); err != nil {
		t.Fatal("the failure was not remembered")
	}
	if got := r.Read("malware.test"); got.Standing != StandingListed {
		t.Fatalf("the copy on disk stopped answering: %+v", got)
	}
	before := calls.Load()
	// Within the hour the marker holds; nothing is asked for again.
	r.maybeRefresh(r.lists[0])
	hostRefreshing.Wait()
	if calls.Load() != before {
		t.Fatal("a list that failed within the hour was asked for again")
	}
}

func TestReputationAFreshListIsNotAskedForAgain(t *testing.T) {
	r := fixtureReputation(t, nil)
	r.Read("example.com")
	hostRefreshing.Wait()
	// refuseDownloads fails the test on any request.
}

// An address is not read against the lists, so a fetch to one — loopback, a
// machine on the local network — never starts a download.
func TestReputationAnAddressStartsNoDownload(t *testing.T) {
	refuseDownloads(t)
	r := OpenReputation(t.TempDir(), nil)
	for _, host := range []string{"127.0.0.1", "::1", "localhost", "buildbox"} {
		if got := r.Read(host); got.Standing != StandingUnknown {
			t.Errorf("Read(%q) = %+v", host, got)
		}
	}
	hostRefreshing.Wait()
}

func TestListsReportsEachListsAge(t *testing.T) {
	refuseDownloads(t)
	dir := t.TempDir()
	writeFixture(t, dir, "urlhaus", fixedNow.Add(-5*time.Hour), "malware.test")
	writeFixture(t, dir, "nrd", fixedNow.Add(-4*day), "brand-new.test")
	_ = os.WriteFile(filepath.Join(dir, "nrd.fail"), nil, 0o600)
	r := OpenReputation(dir, []string{"stevenblack"})
	r.now = func() time.Time { return fixedNow }
	_ = os.Chtimes(filepath.Join(dir, "nrd.fail"), fixedNow, fixedNow)
	got := map[string]ListState{}
	for _, st := range r.Lists() {
		got[st.Name] = st
	}
	if st := got["urlhaus"]; st.From != "download" || st.Age != 5*time.Hour {
		t.Errorf("urlhaus = %+v", st)
	}
	if st := got["nrd"]; !st.Stale || !st.Failed || st.From != "" {
		t.Errorf("nrd = %+v, want stale and failed", st)
	}
	if st := got["stevenblack"]; !st.Off {
		t.Errorf("stevenblack = %+v, want off", st)
	}
	if st := got["tranco"]; st.From != "snapshot" {
		t.Errorf("tranco = %+v, want the snapshot", st)
	}
	if st := got[BuiltinHosts]; st.Off || st.From != "binary" {
		t.Errorf("builtin = %+v", st)
	}
}

// BenchmarkReputationLookup measures a reading against the largest list's
// on-disk form: seven hundred thousand hosts, searched where they lie, with
// the memo defeated so every iteration is a fresh search of every list.
func BenchmarkReputationLookup(b *testing.B) {
	dir := b.TempDir()
	hosts := make([]string, 700_000)
	for i := range hosts {
		hosts[i] = fmt.Sprintf("host%07d.test", i)
	}
	for _, src := range hostSources {
		f, err := os.Create(filepath.Join(dir, src.name+".hosts"))
		if err != nil {
			b.Fatal(err)
		}
		if err := writeHostList(f, src.name, fixedNow, normalizeList(hosts)); err != nil {
			b.Fatal(err)
		}
		f.Close()
	}
	r := OpenReputation(dir, nil)
	r.now = func() time.Time { return fixedNow }
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.memo = map[string]Reading{}
		r.Read(fmt.Sprintf("www.miss%07d.test", i))
	}
}

// The documentation lists shhh's own hosts, because a list auto mode reads
// without a second opinion has to be one a person can read too. The two are
// held together here, so an entry added in one place is added in both.
func TestBuiltinHostsAreListedInTheDoc(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "capabilities", "approvals-and-safety.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(doc), "## A host is read against the world before it is judged")
	if !ok {
		t.Fatal("the section is missing")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	flat := strings.Join(strings.Fields(section), " ")
	for _, host := range builtinKnown {
		if !strings.Contains(flat, " "+host+",") && !strings.Contains(flat, " "+host+" ") && !strings.Contains(flat, " "+host+".") {
			t.Errorf("%s is on the built-in list and not in the section", host)
		}
	}
}
