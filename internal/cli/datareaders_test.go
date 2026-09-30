package cli

import (
	"encoding/json"
	"testing"

	"github.com/rfizzle/shhh/internal/tools"
)

// The jaq and yq definitions as they last shipped, measured the way this
// test measures the readers that replaced them: the JSON of each definition
// as a request carries it. They are figures rather than code because the
// tools are gone, and the bound below is about what their going paid for.
const (
	retiredJaqBytes = 1488
	retiredYqBytes  = 1949
)

// query took the place of jaq and yq and sqlite joined it, so the tool
// definitions every request's prefix carries may grow by one tool's worth
// and no more: the two readers together cost no more than the two they
// retired plus the larger of those, and query alone no more than the pair
// it replaced. A reader whose description grows past that is paid for on
// every request of every session, which is the cost the one-call rule was
// written to keep down.
// See docs/capabilities/coding-agent.md#structured-files-are-read-in-one-call.
func TestDataReaders_GrowThePrefixByOneToolAtMost(t *testing.T) {
	size := map[string]int{}
	for _, d := range tools.Definitions() {
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		size[d.Name] = len(b)
	}
	query, sqlite := size[tools.QueryName], size[tools.SqliteName]
	if query == 0 || sqlite == 0 {
		t.Fatalf("the base toolset defines query (%d bytes) and sqlite (%d bytes)", query, sqlite)
	}
	retired := retiredJaqBytes + retiredYqBytes
	oneTool := max(retiredJaqBytes, retiredYqBytes)
	t.Logf("query %d + sqlite %d bytes against jaq %d + yq %d", query, sqlite, retiredJaqBytes, retiredYqBytes)
	if query > retired {
		t.Errorf("query is %d bytes, more than the %d of jaq and yq it replaced", query, retired)
	}
	if grew := query + sqlite - retired; grew > oneTool {
		t.Errorf("the data readers grew the prefix by %d bytes, more than one tool's %d", grew, oneTool)
	}
}
