package eval

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/web"
)

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
