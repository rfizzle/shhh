package observe

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The event kinds are a closed set held against the documented one, so a kind
// is added by editing the list and the doc together.
// See docs/capabilities/headless.md#the-stream-is-the-record-as-it-happens.
func TestEventKinds_AreTheDocumentedSet(t *testing.T) {
	raw, err := os.ReadFile("../../docs/capabilities/headless.md")
	if err != nil {
		t.Fatal(err)
	}
	var para string
	for _, p := range strings.Split(string(raw), "\n\n") {
		if strings.HasPrefix(p, "**The kinds are a closed set") {
			para = p
		}
	}
	if para == "" {
		t.Fatal("headless.md no longer has the paragraph that lists the event kinds")
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile("`([a-z/-]+)`").FindAllStringSubmatch(para, -1) {
		documented[m[1]] = true
	}
	coded := map[string]bool{}
	for _, k := range EventKinds() {
		coded[k] = true
		if !documented[k] {
			t.Errorf("event kind %q is not in the documented set", k)
		}
	}
	for k := range documented {
		if !coded[k] {
			t.Errorf("documented event kind %q is not in observe.EventKinds", k)
		}
	}
}
