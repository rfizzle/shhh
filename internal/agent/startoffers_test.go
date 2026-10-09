package agent

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A reading is bounded rather than trusted: at most two offers, each title
// one printable line under sixty cells, and no line that would be a command
// or a shell escape rather than a message.
func TestStartOffers_AnAnswerIsBoundedToTwoRowsThatRead(t *testing.T) {
	long := strings.Repeat("trace the request path through the cache ", 4)
	answer := "```json\n" + `{"offers":[` +
		`{"title":"` + long + `","prompt":"Trace it."},` +
		`{"title":"/clear the screen","prompt":"say hi"},` +
		`{"title":"run it","prompt":"!rm -rf ."},` +
		`{"title":"read\u001b[31m the gate","prompt":"Read the\ngate."},` +
		`{"title":"a third","prompt":"Read more."}` +
		`]}` + "\n```"
	offers := ParseStartOffers(answer)
	if len(offers) != MaxStartOffers {
		t.Fatalf("offers = %+v, want %d", offers, MaxStartOffers)
	}
	if w := ansi.StringWidth(offers[0].Title); w >= 60 || !strings.HasSuffix(offers[0].Title, "…") {
		t.Fatalf("title %q is %d cells, want under sixty and cut", offers[0].Title, w)
	}
	if offers[1].Title != "read[31m the gate" || offers[1].Prompt != "Read the gate." {
		t.Fatalf("second offer = %+v, want the control byte dropped and one line", offers[1])
	}
	if ParseStartOffers("I would read the cache.") != nil || ParseStartOffers(`{"offers":[]}`) != nil {
		t.Fatal("an answer with no usable offer should offer nothing")
	}
}
