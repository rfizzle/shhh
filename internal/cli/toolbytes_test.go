package cli

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/rfizzle/shhh/internal/tools"
)

// maxToolBytes is the most one tool's definition may weigh as a provider
// sends it: name, description and input schema as JSON. Every request pays
// for every registered definition, so a description that grows past this
// carries its reasoning into the prompt. The reasoning belongs in the
// capability document the description cites. See
// docs/capabilities/coding-agent.md#the-agent-knows-what-this-machine-has.
const maxToolBytes = 4900

// toolBytes is each distinct tool of the full coding toolset plus every
// optional registration, serialised the way a provider sends it.
var descBytes map[string]int

func toolBytes(t *testing.T) (map[string]int, int) {
	t.Helper()
	seen := map[string]int{}
	descBytes = map[string]int{}
	total := 0
	for _, d := range append(tools.DefinitionsFull(), registrable(t)...) {
		if _, dup := seen[d.Name]; dup {
			continue
		}
		b, err := json.Marshal(struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"input_schema"`
		}{d.Name, d.Description, d.Parameters})
		if err != nil {
			t.Fatalf("%s: %v", d.Name, err)
		}
		seen[d.Name] = len(b)
		descBytes[d.Name] = len(d.Description)
		total += len(b)
	}
	return seen, total
}

func TestToolDescriptions_AreBounded(t *testing.T) {
	sizes, total := toolBytes(t)
	names := make([]string, 0, len(sizes))
	for n := range sizes {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return sizes[names[i]] > sizes[names[j]] })
	for _, n := range names {
		t.Logf("%6d  %6d  %s", sizes[n], descBytes[n], n)
		if sizes[n] > maxToolBytes {
			t.Errorf("%s weighs %d bytes as sent, over the %d ceiling: move its reasoning to the capability doc it cites", n, sizes[n], maxToolBytes)
		}
	}
	t.Logf("%6d  total (%d tools)", total, len(names))
}
