package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// idiomRoot is the checkout each idiom below is run in. The files under it
// are the fixtures, and output/ holds what the shell idiom prints over them,
// written down once and never run here: the gate cannot depend on jq, yq,
// sqlite3 or unzip being installed, and a golden that re-ran the idiom would
// be measuring this machine's copy of it.
const idiomRoot = "testdata/idiom"

// Each shell idiom the data readers replace, answered by one call of the
// built-in tool, with a result no larger than the idiom's output as the model
// would have read it — the command's output under the status lines
// execute_command puts above it, since that is what the call would have
// cost. A reader that answers in more calls or more bytes than the idiom is
// one the model walks past, so a change that makes one chattier fails here.
// See docs/capabilities/coding-agent.md#structured-files-are-read-in-one-call.
func TestDataReaders_AnswerEachIdiomInOneCallNoLargerThanIt(t *testing.T) {
	cases := []struct {
		name   string // output/<name>.txt is the idiom's output
		tool   string
		args   map[string]any
		answer []string // what both the idiom and the tool must say
		// sized is true for a listing whose rows carry a kind label and the
		// file's size, which tar tzf's bare names do not: the sizes are
		// what tar tzvf adds. The tool keeps them, since a model sizing up
		// an archive reads them next, so each row's label and size are
		// allowed on top of the idiom's bytes.
		sized bool
	}{
		{"lockfile-field", QueryName,
			map[string]any{"paths": []string{idiomRoot + "/package-lock.json"}, "expression": `.packages["node_modules/left-pad"].version`},
			[]string{"1.3.0"}, false},
		{"workflow-jobs", QueryName,
			map[string]any{"paths": []string{idiomRoot + "/workflows/ci.yml"}, "expression": ".jobs | keys[]"},
			[]string{"build", "lint", "test"}, false},
		{"toml-value", QueryName,
			map[string]any{"paths": []string{idiomRoot + "/Cargo.toml"}, "expression": ".package.version"},
			[]string{"0.4.1"}, false},
		{"csv-distinct", QueryName,
			map[string]any{"paths": []string{idiomRoot + "/sales.csv"}, "expression": "map(.region) | unique[]", "slurp": true},
			[]string{"east", "north", "west"}, false},
		{"table-count", SqliteName,
			map[string]any{"path": idiomRoot + "/app.db", "sql": "SELECT count(*) FROM orders"},
			[]string{"42"}, false},
		{"zip-entries", ListDirectoryName,
			map[string]any{"path": idiomRoot + "/release.zip", "depth": 3},
			[]string{"README.md", "LICENSE", "ledger", "default.toml", "49", "47", "384", "39"}, false},
		{"tgz-entries", ListDirectoryName,
			map[string]any{"path": idiomRoot + "/release.tar.gz", "depth": 3},
			[]string{"README.md", "LICENSE", "ledger", "default.toml", "bin/ledger"}, true},
		{"gz-line", SearchName,
			map[string]any{"pattern": "ERROR", "path": idiomRoot + "/logs/app.log.gz", "context_lines": 0},
			[]string{"ERROR payments: card processor timed out after 30s (order 1187)"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			golden, err := os.ReadFile(filepath.Join(idiomRoot, "output", tc.name+".txt"))
			if err != nil {
				t.Fatal(err)
			}
			first, output, ok := strings.Cut(string(golden), "\n")
			if !ok || !strings.HasPrefix(first, "# output of `") {
				t.Fatalf("output/%s.txt must open with the command it is the output of, got %q", tc.name, first)
			}
			idiom := FormatExecResult(ExecResult{Output: output, Outcome: ExecSucceeded})

			raw, err := json.Marshal(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			got, err := NewRecorder().Execute(tc.tool, raw)
			if err != nil {
				t.Fatalf("%s: %v", tc.tool, err)
			}
			// The fixtures sit under this package, not at a session's root,
			// so a path in the answer is measured as the session would have
			// named it: from the checkout the idiom ran in.
			got = strings.ReplaceAll(got, idiomRoot+"/", "")

			for _, want := range tc.answer {
				if !strings.Contains(output, want) {
					t.Fatalf("the idiom's output does not say %q; the golden is not about this question:\n%s", want, output)
				}
				if !strings.Contains(got, want) {
					t.Errorf("one %s call does not answer %q:\n%s", tc.tool, want, got)
				}
			}
			t.Logf("%s: %d bytes, the idiom %d", tc.tool, len(got), len(idiom))
			budget := len(idiom)
			if tc.sized {
				budget += rowFurniture(got)
			}
			if len(got) > budget {
				t.Errorf("%s answered in %d bytes where the idiom costs %d:\n%s\n--- the idiom, as the model reads it:\n%s",
					tc.tool, len(got), budget, got, idiom)
			}
		})
	}
}

// rowFurniture is the bytes a listing spends on each row beyond the name the
// idiom prints: the `file: ` or `dir: ` label that says what the entry is,
// and on a file the tab and size after it.
func rowFurniture(listing string) int {
	n := 0
	for _, row := range strings.Split(listing, "\n") {
		for _, label := range []string{"file: ", "dir: "} {
			if strings.HasPrefix(row, label) {
				n += len(label)
			}
		}
		if _, size, ok := strings.Cut(row, "\t"); ok {
			n += 1 + len(size)
		}
	}
	return n
}
