package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeQueryFile writes a fixture under dir and returns its path.
func writeQueryFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// runQuery calls the tool as the executor would, through a record of its own
// so no test reaches another's evidence store.
func runQuery(t *testing.T, r *Recorder, args map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return r.Execute(QueryName, raw)
}

func mustQuery(t *testing.T, args map[string]any) string {
	t.Helper()
	out, err := runQuery(t, NewRecorder(), args)
	if err != nil {
		t.Fatalf("query %v: %v", args, err)
	}
	return out
}

// Each format answers the idiom it replaces in one call, with the output the
// idiom would have printed — strings raw, everything else compact.
func TestQuery_ReadsEachFormat(t *testing.T) {
	dir := t.TempDir()
	pkg := writeQueryFile(t, dir, "package.json", `{"name":"shhh","version":"1.2.3","dependencies":{"b":"^2","a":"^1"},"n":12345678901234567890}`)
	lines := writeQueryFile(t, dir, "events.jsonl", "{\"kind\":\"start\",\"n\":1}\n\n{\"kind\":\"stop\",\"n\":2}\n")
	// An anchor, an alias and a merge key, and a second document.
	ci := writeQueryFile(t, dir, "ci.yml", `defaults: &defaults
  runs-on: ubuntu-latest
  timeout: 10
jobs:
  build:
    <<: *defaults
    steps: [checkout, test]
  lint: *defaults
---
second: true
`)
	cargo := writeQueryFile(t, dir, "Cargo.toml", `[package]
name = "demo"
released = 1979-05-27T07:32:00Z
born = 1979-05-27
[[bin]]
name = "a"
[[bin]]
name = "b"
`)
	pom := writeQueryFile(t, dir, "pom.xml", `<?xml version="1.0"?>
<project id="p1">
  <name lang="en">Demo</name>
  <dep>a</dep>
  <dep>b</dep>
  <ns:extra>x</ns:extra>
</project>`)
	people := writeQueryFile(t, dir, "people.csv", "name,city\n\"Smith, Jo\",Oslo\nAli,\"Cairo\"\n")
	sheet := writeQueryFile(t, dir, "sheet.tsv", "a\tb\n1\tsay \"hi\"\n")

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"a JSON string comes back raw", map[string]any{"paths": []string{pkg}, "expression": ".version"}, "1.2.3"},
		{"anything else is compact JSON", map[string]any{"paths": []string{pkg}, "expression": ".dependencies | keys"}, `["a","b"]`},
		{"a large integer is not rounded", map[string]any{"paths": []string{pkg}, "expression": ".n"}, "12345678901234567890"},
		{"JSONL lines are inputs in turn", map[string]any{"paths": []string{lines}, "expression": ".kind"}, "start\nstop"},
		{"slurp folds them into one array", map[string]any{"paths": []string{lines}, "expression": "map(.n) | add", "slurp": true}, "3"},
		{"a YAML merge key resolves its anchor", map[string]any{"paths": []string{ci}, "expression": "select(.jobs) | .jobs.build[\"runs-on\"]"}, "ubuntu-latest"},
		{"a YAML alias is the value it names", map[string]any{"paths": []string{ci}, "expression": "select(.jobs) | .jobs.lint.timeout"}, "10"},
		{"YAML documents are inputs in turn", map[string]any{"paths": []string{ci}, "expression": "keys | length"}, "2\n1"},
		{"a TOML datetime keeps its offset", map[string]any{"paths": []string{cargo}, "expression": ".package.released"}, "1979-05-27T07:32:00Z"},
		{"a TOML local date stays a date", map[string]any{"paths": []string{cargo}, "expression": ".package.born"}, "1979-05-27"},
		{"a TOML array of tables is an array", map[string]any{"paths": []string{cargo}, "expression": "[.bin[].name]"}, `["a","b"]`},
		{"an XML attribute reads as +@name", map[string]any{"paths": []string{pom}, "expression": `.project["+@id"]`}, "p1"},
		{"text beside an attribute is +content", map[string]any{"paths": []string{pom}, "expression": `.project.name["+content"]`}, "Demo"},
		{"a repeated element is an array", map[string]any{"paths": []string{pom}, "expression": ".project.dep"}, `["a","b"]`},
		{"a namespace prefix stays on the name", map[string]any{"paths": []string{pom}, "expression": `.project["ns:extra"]`}, "x"},
		{"a quoted comma stays inside its cell", map[string]any{"paths": []string{people}, "expression": ".name"}, "Smith, Jo\nAli"},
		{"CSV rows are keyed by the header", map[string]any{"paths": []string{people}, "expression": "select(.city == \"Cairo\") | .name"}, "Ali"},
		{"a TSV quote is a character", map[string]any{"paths": []string{sheet}, "expression": ".b"}, `say "hi"`},
		{"format overrides the extension", map[string]any{"paths": []string{writeQueryFile(t, dir, "deps.lock", `{"x":1}`)}, "format": "json", "expression": ".x"}, "1"},
		{"nothing produced is said", map[string]any{"paths": []string{pkg}, "expression": "empty"}, NoQueryResults},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustQuery(t, tc.args); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// With no expression the answer is the file's shape: the first call made of
// an unfamiliar file, and a few lines rather than the file.
func TestQuery_NoExpressionAnswersTheShape(t *testing.T) {
	dir := t.TempDir()
	pkg := writeQueryFile(t, dir, "package.json", `{"name":"shhh","private":true,"files":["a","b","c"],"scripts":{"a":"x","b":"y"},"n":1,"z":null}`)
	got := mustQuery(t, map[string]any{"paths": []string{pkg}})
	want := "an object, 6 keys\n" +
		"  files: an array, 3 items\n" +
		"  n: number\n" +
		"  name: string\n" +
		"  private: boolean\n" +
		"  scripts: an object, 2 keys\n" +
		"  z: null"
	if got != want {
		t.Errorf("shape:\n%s\nwant:\n%s", got, want)
	}

	rows := writeQueryFile(t, dir, "rows.csv", "id,name\n1,a\n2,b\n3,c\n")
	got = mustQuery(t, map[string]any{"paths": []string{rows}})
	if !strings.HasPrefix(got, "3 rows; the first is an object, 2 keys\n  id: string\n  name: string") {
		t.Errorf("a CSV's shape should count its rows and name its columns:\n%s", got)
	}

	list := writeQueryFile(t, dir, "list.json", `[{"a":1},{"a":2}]`)
	got = mustQuery(t, map[string]any{"paths": []string{list}})
	if !strings.Contains(got, "an array, 2 items\nthe first item is an object, 1 key\n  a: number") {
		t.Errorf("an array's shape should describe its first item:\n%s", got)
	}

	var wide strings.Builder
	wide.WriteString("{")
	for i := range MaxQueryShapeKeys + 5 {
		if i > 0 {
			wide.WriteString(",")
		}
		fmt.Fprintf(&wide, `"k%03d":%d`, i, i)
	}
	wide.WriteString("}")
	got = mustQuery(t, map[string]any{"paths": []string{writeQueryFile(t, dir, "wide.json", wide.String())}})
	if !strings.HasSuffix(got, "… and 5 more keys") {
		t.Errorf("a shape with more keys than the bound should say how many it left out:\n%s", got[len(got)-80:])
	}
}

// Several files are one call, globs included, and each file's answer is under
// its own label.
func TestQuery_SeveralFilesAreOneCallEachLabelled(t *testing.T) {
	dir := t.TempDir()
	a := writeQueryFile(t, dir, "wf/a.yml", "name: build\n")
	b := writeQueryFile(t, dir, "wf/b.yml", "name: lint\n")
	writeQueryFile(t, dir, "wf/skip.txt", "nope")
	got := mustQuery(t, map[string]any{"paths": []string{filepath.Join(dir, "wf", "*.yml")}, "expression": ".name"})
	want := "==> " + a + " <==\nbuild\n==> " + b + " <==\nlint"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	// A file that will not parse is a line under its own label, and the
	// others still answer.
	bad := writeQueryFile(t, dir, "bad.json", `{"x":`)
	got = mustQuery(t, map[string]any{"paths": []string{bad, a}, "expression": ".name"})
	if !strings.Contains(got, "==> "+bad+" <==\nerror: ") || !strings.HasSuffix(got, "build") {
		t.Errorf("one broken file should not cost the others their answer:\n%s", got)
	}

	if _, err := runQuery(t, NewRecorder(), map[string]any{"paths": []string{filepath.Join(dir, "none", "*.json")}}); err == nil ||
		!strings.Contains(err.Error(), "no files matched") {
		t.Errorf("a glob matching nothing should say so: %v", err)
	}
}

// An expression error says where in the expression; a file error says the
// file, the line and the column.
func TestQuery_ErrorsSayWhere(t *testing.T) {
	dir := t.TempDir()
	good := writeQueryFile(t, dir, "good.json", `{"a":1}`)
	_, err := runQuery(t, NewRecorder(), map[string]any{"paths": []string{good}, "expression": ".a | ]"})
	if err == nil || !strings.Contains(err.Error(), "column 6") {
		t.Errorf("an expression error should name its column: %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "\n  .a | ]\n       ^") {
		t.Errorf("an expression error should point at the place: %v", err)
	}

	cases := []struct {
		name, file, content, want string
	}{
		{"json", "broken.json", "{\n  \"a\": 1,\n  \"b\" 2\n}", "broken.json:3:7:"},
		{"json cut off", "cut.json", "{\n  \"a\": [1,\n", "cut.json:3:1:"},
		{"jsonl", "broken.jsonl", "{\"a\":1}\n{\"a\" 2}\n", "broken.jsonl:2:6:"},
		{"yaml", "broken.yaml", "a: 1\nb: [1, 2\nc: 3\n", "broken.yaml:1: cannot parse: did not find expected ','"},
		{"toml", "broken.toml", "a = 1\nb = = 2\n", "broken.toml:2:5:"},
		{"xml", "broken.xml", "<a>\n  <b></c>\n</a>", "broken.xml:2:"},
		{"csv", "broken.csv", "a,b\n1,\"x\"y\n", "broken.csv:2:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeQueryFile(t, dir, tc.file, tc.content)
			_, err := runQuery(t, NewRecorder(), map[string]any{"paths": []string{p}, "expression": "."})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// An answer longer than the bound is cut with a count, and the rest goes to
// the session's evidence store under the id the notice names.
func TestQuery_ACutAnswerKeepsTheRestInEvidence(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("[")
	for i := range 500 {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":%d}`, i)
	}
	b.WriteString("]")
	p := writeQueryFile(t, dir, "many.json", b.String())

	var kept string
	r := NewRecorder()
	r.UseEvidence(func(tool, content string) (string, bool) {
		if tool != QueryName {
			t.Errorf("filed under %q", tool)
		}
		kept = content
		return "ev-42", true
	})
	out, err := runQuery(t, r, map[string]any{"paths": []string{p}, "expression": ".[].id"})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != MaxQueryResults+1 {
		t.Fatalf("want %d results and a notice, got %d lines", MaxQueryResults, len(lines))
	}
	notice := lines[len(lines)-1]
	if !TruncationNotice(notice) || !strings.Contains(notice, "200 of 500 results") || !strings.Contains(notice, "evidence ev-42") {
		t.Errorf("notice = %q", notice)
	}
	if strings.Count(kept, "\n") != 500 || !strings.HasSuffix(kept, "499\n") {
		t.Errorf("the whole answer was not kept: %d lines", strings.Count(kept, "\n"))
	}

	// With nowhere to keep it, the notice says what to do instead.
	out, err = runQuery(t, NewRecorder(), map[string]any{"paths": []string{p}, "expression": ".[].id"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "narrow the expression") || strings.Contains(out, "evidence") {
		t.Errorf("notice without a store: %q", out[strings.LastIndex(out, "\n"):])
	}

	// One result bigger than the byte bound is shown up to it, not dropped.
	big := writeQueryFile(t, dir, "big.json", `{"s":"`+strings.Repeat("x", 2*MaxQueryOutputBytes)+`"}`)
	out, err = runQuery(t, NewRecorder(), map[string]any{"paths": []string{big}, "expression": ".s"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > MaxQueryOutputBytes+200 || !strings.Contains(out, "truncated at 1 of 1 results") {
		t.Errorf("an oversized result: %d bytes, tail %q", len(out), out[len(out)-120:])
	}
}

// Answering part of a file too large to read whole is what the tool is for.
func TestQuery_AFileOverTheReadCeilingIsStillQueryable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	row := `{"id":%d,"pad":"` + strings.Repeat("p", 200) + "\"}\n"
	n := 0
	for written := 0; written <= MaxReadFileSize; n++ {
		c, err := fmt.Fprintf(f, row, n)
		if err != nil {
			t.Fatal(err)
		}
		written += c
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := NewRecorder().Execute(ReadFileName, json.RawMessage(fmt.Sprintf(`{"path":%q}`, p))); err != nil || !strings.Contains(got, "read_file returns no file over") {
		t.Fatalf("fixture should be past read_file's ceiling: %q, %v", got, err)
	}
	got := mustQuery(t, map[string]any{"paths": []string{p}, "expression": "select(.id == 7) | .id"})
	if got != "7" {
		t.Errorf("got %q", got)
	}
	got = mustQuery(t, map[string]any{"paths": []string{p}, "expression": "length", "slurp": true})
	if got != fmt.Sprint(n) {
		t.Errorf("slurped length = %q, want %d", got, n)
	}
}

// The engine reads the input it is handed and nothing else: no environment,
// no second file, no module.
func TestQuery_TheExpressionCannotReachOutside(t *testing.T) {
	t.Setenv("SHHH_QUERY_SECRET", "hunter2")
	dir := t.TempDir()
	p := writeQueryFile(t, dir, "a.json", `{"a":1}`)
	if got := mustQuery(t, map[string]any{"paths": []string{p}, "expression": "env"}); got != "{}" {
		t.Errorf("env = %q, want an empty object", got)
	}
	if got := mustQuery(t, map[string]any{"paths": []string{p}, "expression": "$ENV.SHHH_QUERY_SECRET"}); got != "null" {
		t.Errorf("$ENV reached the process environment: %q", got)
	}
	for _, expr := range []string{"input", `include "x"; .`, `import "x" as x; .`} {
		if _, err := runQuery(t, NewRecorder(), map[string]any{"paths": []string{p}, "expression": expr}); err == nil {
			t.Errorf("%s: expected a refusal", expr)
		}
	}
}

// An expression that never finishes comes back as an error, not a hang.
func TestQuery_ARunawayExpressionIsStopped(t *testing.T) {
	old := queryTimeout
	queryTimeout = 100 * time.Millisecond
	t.Cleanup(func() { queryTimeout = old })
	p := writeQueryFile(t, t.TempDir(), "a.json", `1`)
	_, err := runQuery(t, NewRecorder(), map[string]any{"paths": []string{p}, "expression": "def f: f; f"})
	if err == nil || !strings.Contains(err.Error(), "stopped after") {
		t.Errorf("err = %v", err)
	}
}

// lowerQueryBound sets one of the query bounds for a test and puts it back.
func lowerQueryBound[T any](t *testing.T, bound *T, to T) {
	t.Helper()
	old := *bound
	*bound = to
	t.Cleanup(func() { *bound = old })
}

// An expression that builds without end is stopped by what it holds, well
// before the timeout, and the refusal names the bound it reached.
func TestQuery_AnExpressionThatBuildsWithoutEndIsStoppedByItsMemory(t *testing.T) {
	lowerQueryBound(t, &queryExpressionMemory, 4<<20)
	p := writeQueryFile(t, t.TempDir(), "a.json", `1`)
	for _, expr := range []string{"[range(1e9)]", "[limit(1e9; repeat(1))]"} {
		start := time.Now()
		_, err := runQuery(t, NewRecorder(), map[string]any{"paths": []string{p}, "expression": expr})
		if err == nil || !strings.Contains(err.Error(), "built more than 4.0 MB in memory") ||
			!strings.Contains(err.Error(), "the most one expression may hold") {
			t.Errorf("%s: err = %v", expr, err)
		}
		if took := time.Since(start); took >= queryTimeout {
			t.Errorf("%s: stopped after %s, by the timeout rather than the memory bound", expr, took)
		}
	}
}

// A document that decodes to many times its size is refused by the value
// bound: an array of empty arrays and a deep nest before the decoder builds
// either, a YAML alias fan-out as its values are counted. A file of many
// inputs is counted one input at a time, unless slurp holds them together.
func TestQuery_ADocumentPastTheValueBoundIsRefused(t *testing.T) {
	lowerQueryBound(t, &queryValues, 1000)
	dir := t.TempDir()
	wide := writeQueryFile(t, dir, "wide.json", "["+strings.Repeat("[],", 5000)+"[]]")
	deep := writeQueryFile(t, dir, "deep.json", strings.Repeat("[", 2000)+strings.Repeat("]", 2000))
	var fan strings.Builder
	fan.WriteString("a: &a [x, x, x, x, x, x, x, x, x, x]\n")
	fan.WriteString("b: &b [*a, *a, *a, *a, *a, *a, *a, *a, *a, *a]\n")
	fan.WriteString("c: [*b, *b, *b, *b, *b, *b, *b, *b, *b, *b]\n")
	for i := range 40 {
		fmt.Fprintf(&fan, "k%d: v\n", i)
	}
	fanout := writeQueryFile(t, dir, "fan.yaml", fan.String())
	for _, p := range []string{wide, deep, fanout} {
		_, err := runQuery(t, NewRecorder(), map[string]any{"paths": []string{p}, "expression": "length"})
		if err == nil || !strings.Contains(err.Error(), "decodes to more than 1000 values in one document") {
			t.Errorf("%s: err = %v", filepath.Base(p), err)
		}
	}

	line := "[" + strings.Repeat("0,", 600) + "0]\n"
	lines := writeQueryFile(t, dir, "lines.jsonl", strings.Repeat(line, 3))
	if got := mustQuery(t, map[string]any{"paths": []string{lines}, "expression": "length"}); got != "601\n601\n601" {
		t.Errorf("each input is counted on its own: got %q", got)
	}
	_, err := runQuery(t, NewRecorder(), map[string]any{"paths": []string{lines}, "expression": "length", "slurp": true})
	if err == nil || !strings.Contains(err.Error(), "more than 1000 values") {
		t.Errorf("slurped inputs are one document: err = %v", err)
	}
}

// A document the value bound passes can still decode past the memory bound,
// and a decoder that builds as it reads is stopped part way through it.
func TestQuery_ADocumentPastTheMemoryBoundIsRefused(t *testing.T) {
	lowerQueryBound(t, &queryMemory, 16<<20)
	p := writeQueryFile(t, t.TempDir(), "list.yaml", strings.Repeat("- a\n", 1<<20))
	_, err := runQuery(t, NewRecorder(), map[string]any{"paths": []string{p}, "expression": "length"})
	if err == nil || !strings.Contains(err.Error(), "list.yaml decodes to more than 16.0 MB in memory") {
		t.Errorf("err = %v", err)
	}
}

// The bounds leave an ordinary large file alone: a 5 MB lockfile answers.
func TestQuery_AnOrdinaryLockfileIsUnderEveryBound(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"packages":{`)
	n := 0
	for b.Len() < 5<<20 {
		fmt.Fprintf(&b, `"node_modules/pkg%d":{"version":"1.2.%d","resolved":"https://registry.npmjs.org/pkg%d/-/pkg%d-1.2.%d.tgz","integrity":"sha512-0123456789abcdef","dependencies":{"a":"^1.0.0"}},`, n, n, n, n, n)
		n++
	}
	b.WriteString(`"":{}}}`)
	p := writeQueryFile(t, t.TempDir(), "package-lock.json", b.String())
	got := mustQuery(t, map[string]any{"paths": []string{p}, "expression": `[.packages[] | .version] | length`})
	if got != fmt.Sprint(n+1) {
		t.Errorf("got %q, want %d", got, n+1)
	}
}

func TestQuery_RefusesWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"no paths", map[string]any{"expression": "."}, "paths is required"},
		{"an unknown format", map[string]any{"paths": []string{"x.json"}, "format": "ini"}, "invalid format"},
		{"an extension that says nothing", map[string]any{"paths": []string{writeQueryFile(t, dir, "notes.txt", "x")}}, "pass format"},
		{"a directory", map[string]any{"paths": []string{dir}}, "is a directory"},
		{"a missing file", map[string]any{"paths": []string{filepath.Join(dir, "gone.json")}}, "cannot read file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := runQuery(t, NewRecorder(), tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// The tool bounds its own answer, so the reduction pipeline leaves it alone.
func TestQuery_IsSelfBounding(t *testing.T) {
	if !SelfBounding(QueryName) {
		t.Error("query bounds its own output and must be declared so")
	}
}
