package eval

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/web"
)

func requireLoopbackContract(t *testing.T) {
	t.Helper()
	if os.Getenv("SHHH_TEST_CONTRACT") != "1" {
		t.Skip("research fixture contract; run make test-contract on a listener-capable host")
	}
}

// writeSite lays out a research case's site the way an author would, and
// returns the case directory.
func writeSite(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, SiteDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		path := filepath.Join(dir, SiteDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const onePage = `<!doctype html><html><head><title>Grebe 1.4</title></head><body>
<p>Grebe 1.4 returns rows oldest first when a query names no order.</p>
</body></html>`

// A fixture site answers the fetcher it was opened for, through the same
// tool a session fetches with — the header, the final URL and the body the
// model would see.
func TestAFixtureSiteAnswersTheFetchItWasOpenedFor(t *testing.T) {
	requireLoopbackContract(t)
	dir := writeSite(t, "grebe", map[string]string{"index.html": onePage})
	site, err := openSite(filepath.Join(dir, SiteDir))
	if err != nil {
		t.Fatal(err)
	}
	defer site.close()

	f := web.NewFetcher(web.FixturePolicy(site.authority))
	res, err := f.Fetch(context.Background(), site.base+"/index.html", nil)
	if err != nil {
		t.Fatalf("the site the run was opened for must be reachable: %v", err)
	}
	if res.Status != http.StatusOK || !strings.Contains(string(res.Body), "oldest first") {
		t.Errorf("status %d, body %q", res.Status, res.Body)
	}
	if got := site.pages[site.base+"/index.html"]; !strings.Contains(got, "oldest first") {
		t.Errorf("the page text a quotation is checked against = %q", got)
	}
}

// The exception is one server's, not loopback's. A second listener on the
// same address is a different origin and stays refused, which is the whole
// difference between this and setting AllowPrivate for the run.
func TestAFixtureSiteIsTheOnlyThingTheRunCanReach(t *testing.T) {
	requireLoopbackContract(t)
	dir := writeSite(t, "grebe", map[string]string{"index.html": onePage})
	site, err := openSite(filepath.Join(dir, SiteDir))
	if err != nil {
		t.Fatal(err)
	}
	defer site.close()
	other, err := openSite(filepath.Join(dir, SiteDir))
	if err != nil {
		t.Fatal(err)
	}
	defer other.close()

	f := web.NewFetcher(web.FixturePolicy(site.authority))
	if _, err := f.Fetch(context.Background(), other.base+"/index.html", nil); err == nil {
		t.Error("a second server on the same loopback address must still be refused")
	}
	if _, err := f.Fetch(context.Background(), "http://example.com/", nil); err == nil {
		t.Error("a run that could also read the public web is not reproducible")
	}
	// And the guard is not weakened for anybody else: the ordinary policy
	// still refuses the very site this one reaches.
	plain := web.NewFetcher(web.Policy{})
	if _, err := plain.Fetch(context.Background(), site.base+"/index.html", nil); err == nil {
		t.Error("the loopback block must still stand for a policy that named no fixture")
	}
}

// The search fixture answers on the same listener, in the shape the search
// tool's provider answers in, and ranks rather than filters — a query nobody
// anticipated must come back with pages rather than with nothing.
func TestTheSearchFixtureAnswersEveryQueryWithPages(t *testing.T) {
	requireLoopbackContract(t)
	dir := writeSite(t, "grebe", map[string]string{
		"index.html": onePage,
		"other.html": `<!doctype html><html><head><title>Heron</title></head><body><p>Heron is unrelated.</p></body></html>`,
	})
	site, err := openSite(filepath.Join(dir, SiteDir))
	if err != nil {
		t.Fatal(err)
	}
	defer site.close()

	s := &web.Searcher{APIKey: "fixture", Endpoint: site.base + searchPath}
	hits, err := s.Search(context.Background(), web.SearchQuery{Query: "grebe default order", Count: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("every page should be a hit, got %d", len(hits))
	}
	if !strings.HasSuffix(hits[0].URL, "/index.html") {
		t.Errorf("the page carrying the query's words should rank first, got %s", hits[0].URL)
	}
	none, err := s.Search(context.Background(), web.SearchQuery{Query: "nothing on this site at all", Count: 5})
	if err != nil || len(none) == 0 {
		t.Errorf("a query that matches nothing must still answer: %d hits, %v", len(none), err)
	}
}

// gradingCase is the case a fixed write-up is put to below.
func gradingCase(t *testing.T, facts ...string) Case {
	t.Helper()
	c := Case{Kind: KindResearch}
	for _, f := range facts {
		c.Facts = append(c.Facts, regexp.MustCompile(f))
	}
	return c
}

const (
	readURL   = "http://127.0.0.1:9/grebe-1.4.html"
	unreadURL = "http://127.0.0.1:9/grebe-2.0.html"
)

func readLedger() []web.Source {
	return []web.Source{{Kind: web.KindFetch, Requested: readURL, FinalURL: readURL, Status: 200}}
}

func readPages() map[string]string {
	return map[string]string{readURL: "Grebe 1.4 returns rows oldest first when a query names no order."}
}

// The three rates are three readings and never one. A write-up that carries
// the fact, quotes it correctly and cites a page the run never opened is the
// failure this shape exists for, and it has to show up as a citation miss
// rather than as a rounding error in a single score.
func TestTheThreeRatesAreReadSeparately(t *testing.T) {
	c := gradingCase(t, `(?i)oldest first`)
	writeup := "Grebe returns rows oldest first (" + readURL + "): " +
		"\"Grebe 1.4 returns rows oldest first when a query names no order.\"\n\n" +
		"The 2.0 line reversed it (" + unreadURL + ")."

	score := GradeResearch(c, writeup, readLedger(), readPages())
	if score.Cited != (Rate{Got: 1, Of: 2}) {
		t.Errorf("cited = %v, want the unread page counted against it", score.Cited)
	}
	if score.Quoted != (Rate{Got: 1, Of: 1}) {
		t.Errorf("quoted = %v", score.Quoted)
	}
	if score.Facts != (Rate{Got: 1, Of: 1}) {
		t.Errorf("facts = %v", score.Facts)
	}
	if score.OK() {
		t.Error("a write-up citing a page it never read must not pass")
	}
	if len(score.Misses) != 1 || !strings.Contains(score.Misses[0], unreadURL) {
		t.Errorf("misses = %v, want the unread citation named", score.Misses)
	}
}

// A sentence in quotation marks that is on no page is the second failure:
// the URL beside it was really fetched, so the citation rate says nothing
// about it.
func TestAQuotationThatIsNotOnThePageItNamesIsAMiss(t *testing.T) {
	c := gradingCase(t, `(?i)oldest first`)
	writeup := "Grebe returns rows oldest first. From " + readURL + ": " +
		"\"Grebe 1.4 has always returned rows oldest first, in every release since 1.0.\""

	score := GradeResearch(c, writeup, readLedger(), readPages())
	if !score.Cited.OK() {
		t.Errorf("the URL was fetched, so the citation is sound: %v", score.Cited)
	}
	if score.Quoted != (Rate{Got: 0, Of: 1}) {
		t.Errorf("quoted = %v, want the invented sentence counted", score.Quoted)
	}
	if len(score.Misses) != 1 || !strings.Contains(score.Misses[0], "quoted but not on") {
		t.Errorf("misses = %v", score.Misses)
	}
}

// A block quote is attributed to the paragraph that introduces it, which is
// where a model puts the URL. Line wrapping and typographic marks are not
// what is being measured, so they are folded before the comparison.
func TestABlockQuoteIsAttributedToTheParagraphAboveIt(t *testing.T) {
	c := gradingCase(t)
	writeup := "The 1.4 reference (" + readURL + ") says:\n\n" +
		"> Grebe 1.4 returns rows oldest\n> first when a query names no order.\n"

	score := GradeResearch(c, writeup, readLedger(), readPages())
	if score.Quoted != (Rate{Got: 1, Of: 1}) {
		t.Errorf("quoted = %v, misses %v", score.Quoted, score.Misses)
	}
}

// A short quoted run is typography, not a quotation: a write-up that puts a
// value in marks must not be asked to find it verbatim on a page.
func TestAQuotedValueIsNotAQuotation(t *testing.T) {
	c := gradingCase(t)
	writeup := "Pass \"order_by\" to ask for anything else (" + readURL + ")."

	score := GradeResearch(c, writeup, readLedger(), readPages())
	if score.Quoted.Of != 0 {
		t.Errorf("quoted = %v, want nothing taken as a quotation", score.Quoted)
	}
}

// The negative case: the only right answer is that the sources do not settle
// it. The grader has to accept that answer and refuse a confident one, or the
// case measures nothing.
func TestTheUnanswerableCaseAcceptsAnAdmissionAndRefusesAnInvention(t *testing.T) {
	c := shippedCase(t, "research-unanswerable")
	rows := []web.Source{{Kind: web.KindFetch, Requested: readURL, FinalURL: readURL, Status: 200}}
	pages := map[string]string{readURL: "Sparrow retries a delivery four times."}

	admitted := "I could not confirm the default port from these sources. " +
		"The pages on the site cover retries and durability and neither names a port."
	if score := GradeResearch(c, admitted, rows, pages); !score.OK() {
		t.Errorf("an honest admission must pass: %v", score.Misses)
	}
	invented := "The Sparrow broker listens on TCP port 5672 by default."
	if score := GradeResearch(c, invented, rows, pages); score.OK() {
		t.Error("an invented answer must fail the case it was invented for")
	}
}

// shippedCase loads one of the cases in this repository, so a test that
// depends on a case's own wording fails when that wording changes.
func shippedCase(t *testing.T, name string) Case {
	t.Helper()
	c, err := LoadCase(filepath.Join("..", "..", "evals", name))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A research attempt with no provider is a machine to fix and never a task
// that failed, the way a session that never started is.
func TestAResearchAttemptWithNoProviderNeverRan(t *testing.T) {
	c := shippedCase(t, "research-version")
	a := researchAttempt(context.Background(), c, Options{})
	if a.Err == nil {
		t.Fatal("a case with nothing to ask must error rather than fail")
	}
	if a.Passed || a.Research != nil {
		t.Errorf("nothing was graded, so nothing may be reported: %+v", a)
	}
	res := Result{Case: c, Attempts: []Attempt{a}}
	if res.Verdict() != Errored {
		t.Errorf("verdict = %v", res.Verdict())
	}
}

// The site a shipped case brings has to serve, and its prompt has to be able
// to name it: without the placeholder every attempt depends on the search
// fixture finding the right page first.
func TestEveryShippedResearchCaseServesItsSiteAndNamesIt(t *testing.T) {
	requireLoopbackContract(t)
	cases, err := Load(filepath.Join("..", "..", "evals"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, c := range cases {
		if c.Kind != KindResearch {
			continue
		}
		found++
		if !strings.Contains(c.Prompt, SitePlaceholder) {
			t.Errorf("%s: the question never names the site", c.Name)
		}
		if len(c.Facts) == 0 {
			t.Errorf("%s: no facts, so nothing decides the answer", c.Name)
		}
		site, err := openSite(c.Site)
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		resp, err := http.Get(site.base + "/")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s: the site's root answered %d — a case starts at index.html", c.Name, resp.StatusCode)
			}
		} else {
			t.Errorf("%s: %v", c.Name, err)
		}
		site.close()
	}
	if found == 0 {
		t.Error("the suite measures no research run")
	}
}

// A model quoting a clause closes it with a full stop where the page has a
// comma. The words are the page's and the punctuation is the write-up's, so a
// literal comparison would score an accurate quotation as an invented one.
func TestAQuotationKeepsItsOwnTerminalPunctuation(t *testing.T) {
	c := gradingCase(t)
	pages := map[string]string{
		readURL: "Grebe 1.4 returns rows oldest first when a query names no order, and it has done so in every 1.x release.",
	}
	writeup := "From " + readURL + ": \"Grebe 1.4 returns rows oldest first when a query names no order.\""

	score := GradeResearch(c, writeup, readLedger(), pages)
	if score.Quoted != (Rate{Got: 1, Of: 1}) {
		t.Errorf("quoted = %v, misses %v", score.Quoted, score.Misses)
	}
	// The relaxation is punctuation and nothing else: a word the page does
	// not have is still an invented quotation.
	invented := "From " + readURL + ": \"Grebe 1.4 returns rows newest first when a query names no order.\""
	if score := GradeResearch(c, invented, readLedger(), pages); score.Quoted.Got != 0 {
		t.Errorf("a changed word must still miss: %v", score.Quoted)
	}
}
