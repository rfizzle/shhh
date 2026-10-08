package receipt

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/tools"
)

func readAct(p string) Act {
	return Act{Receipt: Build(Call{Name: tools.ReadFileName, Args: fmt.Sprintf(`{"path":%q}`, p), Result: "package a\n"})}
}

func searchAct(pattern string) Act {
	return Act{Receipt: Build(Call{Name: tools.SearchName, Args: fmt.Sprintf(`{"pattern":%q}`, pattern), Result: "a.go:1: x"})}
}

func lookupAct() Act {
	return Act{Receipt: Build(Call{Name: "hover", Args: `{"path":"a.go","line":1}`, Result: "func A()"})}
}

func commandAct(line, out string, outcome tools.ExecOutcome) Act {
	return Act{Receipt: Build(Call{Args: line, Result: out, Exec: &tools.ExecResult{Outcome: outcome, Output: out}})}
}

func editAct(p string, hunks ...diff.Hunk) Act {
	return Act{Receipt: Build(Call{Name: tools.EditFileName, Args: fmt.Sprintf(`{"path":%q}`, p), Result: "edited " + p, Hunks: hunks})}
}

func hunk(start int, lines ...diff.Line) diff.Hunk {
	return diff.Hunk{OldStart: start, NewStart: start, Lines: lines}
}

func reads(paths ...string) []Act {
	acts := make([]Act, 0, len(paths))
	for _, p := range paths {
		acts = append(acts, readAct(p))
	}
	return acts
}

// The header counts a step's calls by kind, each kind where it was first
// used, in the receipt's own verbs; a call that never ran is not counted.
func TestStepReceipt_CountsByVerbInOrderOfFirstUse(t *testing.T) {
	refused := readAct("c.go")
	refused.State = StateRefused
	cases := []struct {
		name string
		acts []Act
		want string
	}{
		{"reads, a search and a lookup",
			[]Act{readAct("a.go"), searchAct("x"), readAct("b/c.go"), lookupAct(), lookupAct()},
			"read 2 files · searched x · 2 lookups"},
		{"the order is of first use, not of kind",
			[]Act{lookupAct(), searchAct("x"), searchAct("y"), readAct("a.go"), readAct("b/c.go"), lookupAct()},
			"2 lookups · searched twice · read 2 files"},
		{"a kind with one call names what it was about, and counting starts at two",
			[]Act{commandAct("git status --short .plan/", "", tools.ExecSucceeded), readAct("a.go"), lookupAct()},
			"ran git status --short .plan/ · read a.go · looked up a.go"},
		{"a file read twice is one file",
			[]Act{readAct("a.go"), readAct("a.go")},
			"read 1 file"},
		{"commands and writes",
			[]Act{commandAct("go build", "", tools.ExecSucceeded), editAct("a.go"), editAct("b.go"), editAct("a.go"),
				commandAct("go test", "ok", tools.ExecSucceeded)},
			"ran 2 commands · wrote 2 files"},
		{"a read that names no file is counted as a call, never as a file",
			[]Act{readAct("a.go"), {Receipt: Build(Call{Name: "git", Args: `{"verb":"log"}`, Result: "abc x"})}},
			"read twice"},
		{"a refused call did nothing",
			[]Act{searchAct("x"), refused, searchAct("y"), searchAct("z")},
			"searched 3 times"},
		{"a kind with no word of its own is counted under its name",
			[]Act{{Receipt: Build(Call{Name: "spawn_agent", Args: `{"task":"t"}`, Result: "started"})},
				{Receipt: Build(Call{Name: "spawn_agent", Args: `{"task":"u"}`, Result: "started"})}},
			"2 spawns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildStep(tc.acts).Counts(); got != tc.want {
				t.Errorf("counts %q, want %q", got, tc.want)
			}
		})
	}
}

// The reads roll up by directory, the fullest directory first, and the
// header names no more of them than the ceiling.
func TestStepReceipt_ReadsRollUpByDirectoryUnderTheCeiling(t *testing.T) {
	var large []Act
	for i := range 14 {
		large = append(large, readAct(fmt.Sprintf("internal/ui/f%d.go", i)))
		if i%5 == 0 {
			large = append(large, readAct(fmt.Sprintf("docs/d%d.md", i)))
		}
	}
	large = append(large, commandAct("go build", "", tools.ExecSucceeded), commandAct("go vet", "", tools.ExecSucceeded),
		commandAct("go test", "", tools.ExecSucceeded), editAct("internal/ui/keys.go"), editAct("internal/ui/render.go"))

	// Every directory past the ceiling is one the header leaves to `…`, and
	// each of these holds two files so that the order is by count.
	var spread []Act
	for i := range ReadDirCeiling + 2 {
		spread = append(spread, reads(fmt.Sprintf("d%d/a.go", i), fmt.Sprintf("d%d/b.go", i))...)
	}
	var atCeiling []Act
	for i := range ReadDirCeiling {
		atCeiling = append(atCeiling, reads(fmt.Sprintf("d%d/a.go", i), fmt.Sprintf("d%d/b.go", i))...)
	}

	cases := []struct {
		name string
		acts []Act
		want string
	}{
		{"the artboard's large step", large,
			"read 14 files in internal/ui/, 3 in docs/ · ran 3 commands · wrote 2 files"},
		{"the fullest directory leads, whenever it was read",
			reads("a/x.go", "b/x.go", "b/y.go", "b/z.go", "a/y.go"),
			"read 3 files in b/, 2 in a/"},
		{"a rollup that groups nothing is not drawn",
			reads("a/x.go", "b/x.go", "c/x.go"),
			"read 3 files"},
		{"one directory", reads("a/x.go", "a/y.go"), "read 2 files in a/"},
		{"a file at the top is in ./", reads("x.go", "y.go", "a/z.go"), "read 2 files in ./, 1 in a/"},
		{"exactly at the ceiling there is nothing to elide", atCeiling,
			"read 2 files in d0/, 2 in d1/, 2 in d2/"},
		{"past the ceiling", spread,
			"read 2 files in d0/, 2 in d1/, 2 in d2/, …"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildStep(tc.acts).Counts(); got != tc.want {
				t.Errorf("counts %q, want %q", got, tc.want)
			}
		})
	}

	// The open card lists every directory, the ceiling being the header's.
	s := BuildStep(spread)
	if len(s.Reads) != ReadDirCeiling+2 {
		t.Errorf("the rollup holds %d directories, want every one of %d", len(s.Reads), ReadDirCeiling+2)
	}
	if root := BuildStep(reads("/a.go", "/b.go")).Reads; len(root) != 1 || root[0].Name != "/" {
		t.Errorf("files at the root of the disk roll up as %+v", root)
	}
	top := BuildStep(reads("x.go", "y.go")).Reads
	if len(top) != 1 || top[0].Name != "./" || !slices.Equal(top[0].Files, []string{"x.go", "y.go"}) {
		t.Errorf("files at the top roll up as %+v", top)
	}
}

// The step's glyph is one precedence — a failure over a write over a
// command over a read — whatever order the calls came in, and the rail is
// carried by any call that ran and carries it.
func TestStepReceipt_TheGlyphIsThePrecedence(t *testing.T) {
	failedRead := Act{Receipt: Build(Call{Name: tools.ReadFileName, Args: `{"path":"gone.go"}`, Result: "error: no such file"})}
	refusedEdit := editAct("a.go")
	refusedEdit.State = StateRefused
	stopped := commandAct("sleep 9", "", tools.ExecSucceeded)
	stopped.State = StateRefused
	ceiling := commandAct("go test", "", tools.ExecSucceeded)
	ceiling.State = StateFailed
	running := readAct("a.go")
	running.State = StateRunning

	cases := []struct {
		name    string
		acts    []Act
		want    Mark
		rail    bool
		running bool
	}{
		{"reads alone", []Act{readAct("a.go"), searchAct("x"), lookupAct()}, Mark{Kind: KindRead}, false, false},
		{"a command over a read", []Act{searchAct("x"), commandAct("ls", "a", tools.ExecSucceeded), readAct("a.go")},
			Mark{Kind: KindRun}, true, false},
		{"a write over a command, wherever it came",
			[]Act{commandAct("ls", "a", tools.ExecSucceeded), editAct("a.go"), commandAct("go test", "ok", tools.ExecSucceeded)},
			Mark{Kind: KindWrite}, true, false},
		{"a failure over a write", []Act{editAct("a.go"), commandAct("go test", "FAIL", tools.ExecExited)},
			Mark{Kind: KindRun, State: StateFailed}, true, false},
		{"a read that broke is a failure too", []Act{editAct("a.go"), failedRead},
			Mark{Kind: KindRead, State: StateFailed}, true, false},
		{"a break only the session saw", []Act{readAct("a.go"), ceiling},
			Mark{Kind: KindRun, State: StateFailed}, true, false},
		{"a refused write wrote nothing", []Act{readAct("a.go"), refusedEdit}, Mark{Kind: KindRead}, false, false},
		{"a stopped command is not a failure", []Act{readAct("a.go"), stopped}, Mark{Kind: KindRead}, false, false},
		{"a step that was only refused", []Act{refusedEdit, stopped}, Mark{Kind: KindWrite, State: StateRefused}, false, false},
		{"a handed-off command is working, not broken",
			[]Act{commandAct("serve", "", tools.ExecHandedOff)}, Mark{Kind: KindRun}, true, false},
		{"a call in flight leaves the precedence alone", []Act{editAct("a.go"), running},
			Mark{Kind: KindWrite}, true, true},
		{"no calls", nil, Mark{}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := BuildStep(tc.acts)
			if s.Lead != tc.want || s.Rail != tc.rail || s.Running != tc.running {
				t.Errorf("lead %+v rail %v running %v, want %+v rail %v running %v",
					s.Lead, s.Rail, s.Running, tc.want, tc.rail, tc.running)
			}
		})
	}
}

// The footer's line is the tool's own text, picked by the precedence that
// picked the glyph, so it is always about what the glyph says.
func TestStepReceipt_EvidenceIsPickedByPrecedence(t *testing.T) {
	keys := hunk(40,
		diff.Line{Kind: diff.Context, Text: "switch k {", OldNo: 40, NewNo: 40},
		diff.Line{Kind: diff.Add, Text: `case "c": m.copyBlock(m.focusedBlock())`, NewNo: 41})
	failing := commandAct("go test ./internal/ui/", "ok  \tother\n--- FAIL: TestReplyGolden\nFAIL\n\n", tools.ExecExited)
	toolFailed := Act{Receipt: Build(Call{Name: tools.ReadFileName, Args: `{"path":"gone.go"}`,
		Result: "error: open gone.go: no such file\nmore"})}
	formatted := Act{Receipt: Build(Call{Name: tools.ExecCommandName, Args: `{"command":"go vet"}`,
		Result: "error: command exited with status 1\noutput:\nvet: a.go:3: bad\n… (output truncated)\n"})}
	long := commandAct("cat min.js", "a"+strings.Repeat("é", evidenceMaxBytes), tools.ExecSucceeded)

	cases := []struct {
		name string
		acts []Act
		want Evidence
	}{
		{"a failed command's last line of output", []Act{editAct("internal/ui/keys.go", keys), failing},
			Evidence{Line: "FAIL", Call: 1}},
		{"the last failure is the one the step stands on",
			[]Act{toolFailed, commandAct("ls", "a", tools.ExecSucceeded), failing},
			Evidence{Line: "FAIL", Call: 2}},
		{"a tool's failure is its error line", []Act{readAct("a.go"), toolFailed},
			Evidence{Line: "error: open gone.go: no such file", Call: 1}},
		{"a command a tool ran is read under its status line, past the bound's notice", []Act{formatted},
			Evidence{Line: "vet: a.go:3: bad", Call: 0}},
		{"a write's first changed line, with its file",
			[]Act{commandAct("go build", "built", tools.ExecSucceeded), editAct("internal/ui/keys.go", keys),
				editAct("internal/ui/render.go", hunk(1, diff.Line{Kind: diff.Del, Text: "old", OldNo: 1}))},
			Evidence{Line: `+ case "c": m.copyBlock(m.focusedBlock())`, Path: "internal/ui/keys.go", Call: 1}},
		{"a write with no change to show shows nothing, not the command under it",
			[]Act{commandAct("go build", "built", tools.ExecSucceeded), editAct("a.go")},
			Evidence{}},
		{"a command's first line of output, the last command's",
			[]Act{commandAct("git status", "?? .plan/\n?? x", tools.ExecSucceeded), commandAct("ls", "\n\nREADME.md\nx", tools.ExecSucceeded), readAct("a.go")},
			Evidence{Line: "README.md", Call: 1}},
		{"a command that printed nothing gives way to one that did",
			[]Act{commandAct("git status", "?? .plan/", tools.ExecSucceeded), commandAct("true", "", tools.ExecSucceeded)},
			Evidence{Line: "?? .plan/", Call: 0}},
		{"a read's path, where the step read one file", []Act{lookupAct(), readAct("internal/ui/keys.go")},
			Evidence{Line: "internal/ui/keys.go", Call: 1}},
		{"a search's query, the last one asked, over a read's path",
			[]Act{searchAct("x"), readAct("internal/ui/keys.go"), searchAct("copyBlock")},
			Evidence{Line: "copyBlock", Call: 2}},
		{"one line, bounded on a rune", []Act{long},
			Evidence{Line: "a" + strings.Repeat("é", evidenceMaxBytes/2-1), Call: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildStep(tc.acts).Evidence; got != tc.want {
				t.Errorf("evidence %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A failed command's footer is the last line that says it failed, not the
// last line it printed: make closes a failed run on a directory trailer that
// names nothing that failed. Only where no line reads as a failure is the
// last line the one.
func TestStepReceipt_FailureEvidenceIsTheFailureLine(t *testing.T) {
	cases := []struct {
		name, out, want string
	}{
		{"a go test under make, its trailers either side",
			"make[1]: Entering directory '/src/shhh'\n=== RUN   TestReplyGolden\n" +
				"--- FAIL: TestReplyGolden: reply_copy.golden differs\n" +
				"ok  \tgithub.com/rfizzle/shhh/internal/ui/components\t0.41s\n" +
				"make[1]: Leaving directory '/src/shhh'\n",
			"--- FAIL: TestReplyGolden: reply_copy.golden differs"},
		{"the last failure line, not the first",
			"--- FAIL: TestReplyGolden (0.42s)\nFAIL\tgithub.com/rfizzle/shhh/internal/ui\t0.9s\nmake: Leaving directory '/src'",
			"FAIL\tgithub.com/rfizzle/shhh/internal/ui\t0.9s"},
		{"make's own error line is not the failure",
			"main.go:3:2: error: undefined: x\nmake: *** [build] Error 1\n", "main.go:3:2: error: undefined: x"},
		{"a panic", "goroutine 1 [running]:\npanic: runtime error\n\tmain.go:4", "panic: runtime error"},
		{"git's fatal", "fatal: not a git repository\nhint: run git init", "fatal: not a git repository"},
		{"an Error that opens a line", "Error: Cannot find module 'x'\n    at require (node:1)", "Error: Cannot find module 'x'"},
		{"a non-zero exit status", "building\nexit status 2\ncleanup done", "exit status 2"},
		{"a zero exit status is not a failure", "building\nexit status 0\ncleanup done", "cleanup done"},
		{"no failure line: the last line, past make's trailer",
			"compiling\nkilled\nmake[2]: Leaving directory '/src'\n", "killed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acts := []Act{commandAct("make test", tc.out, tools.ExecExited)}
			if got := BuildStep(acts).Evidence; got != (Evidence{Line: tc.want, Call: 0}) {
				t.Errorf("evidence %+v, want %q", got, tc.want)
			}
		})
	}
}

// A step that only read, and has nothing one line could add to its header,
// has no footer.
func TestStepReceipt_AQuietReadHasNoEvidence(t *testing.T) {
	running := commandAct("go test", "", tools.ExecSucceeded)
	running.State = StateRunning
	cases := []struct {
		name string
		acts []Act
	}{
		{"several files", reads("a/x.go", "b/y.go")},
		{"only lookups", []Act{lookupAct(), lookupAct()}},
		{"a read that named no file", []Act{{Receipt: Build(Call{Name: "git", Args: `{"verb":"log"}`, Result: "abc x"})}}},
		{"a command still running has said nothing yet", []Act{readAct("a/x.go"), readAct("b/y.go"), running}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildStep(tc.acts).Evidence; got != (Evidence{}) {
				t.Errorf("evidence %+v, want none", got)
			}
		})
	}
}

// The strip is every call in the order it was made, each with its own
// standing: refused calls are in it though nothing counts them.
func TestStepReceipt_TheStripIsEveryCallInOrder(t *testing.T) {
	refused := editAct("a.go")
	refused.State = StateRefused
	running := searchAct("y")
	running.State = StateRunning
	acts := []Act{readAct("a.go"), commandAct("go test", "FAIL", tools.ExecExited), refused,
		editAct("b.go"), running}
	for i := range acts {
		acts[i].Duration = time.Duration(i+1) * time.Second
	}
	s := BuildStep(acts)
	want := []Mark{
		{Kind: KindRead},
		{Kind: KindRun, State: StateFailed},
		{Kind: KindWrite, State: StateRefused},
		{Kind: KindWrite},
		{Kind: KindSearch, State: StateRunning},
	}
	if !slices.Equal(s.Strip, want) {
		t.Errorf("strip %+v, want %+v", s.Strip, want)
	}
	if s.Duration != 15*time.Second {
		t.Errorf("duration %v, want the calls' sum", s.Duration)
	}
}

// A step's change count is every edit's, and nothing a refused edit would
// have done.
func TestStepReceipt_AddsUpItsEdits(t *testing.T) {
	refused := editAct("c.go", hunk(1, diff.Line{Kind: diff.Add, Text: "x", NewNo: 1}))
	refused.State = StateRefused
	s := BuildStep([]Act{
		editAct("a.go", hunk(1, diff.Line{Kind: diff.Add, Text: "x", NewNo: 1}, diff.Line{Kind: diff.Add, Text: "y", NewNo: 2})),
		editAct("b.go", hunk(5, diff.Line{Kind: diff.Del, Text: "z", OldNo: 5})),
		refused,
	})
	if s.Added != 2 || s.Removed != 1 {
		t.Errorf("+%d −%d, want +2 −1", s.Added, s.Removed)
	}
}

// A card's header gives its receipt up in an order as the pane narrows, so
// the receipt is split where the order cuts it: the verb that leads, the one
// call's subject that is cut and never dropped, the directory clause that
// goes first and the rollup that goes after it.
func TestStepReceipt_TheHeaderSplitsForTheDropOrder(t *testing.T) {
	var large []Act
	for _, p := range []string{"internal/ui/a.go", "internal/ui/b.go", "internal/ui/c.go", "docs/x.md", "docs/y.md"} {
		large = append(large, readAct(p))
	}
	large = append(large, commandAct("go test", "ok", tools.ExecSucceeded), commandAct("go vet", "", tools.ExecSucceeded))
	cases := []struct {
		name string
		acts []Act
		want Header
	}{
		{"reads by directory, then the other kinds",
			large,
			Header{Verb: "read", Rollup: "3 files in internal/ui/, 2 in docs/ · ran 2 commands",
				Bare: "5 files · ran 2 commands"}},
		{"one call names its subject, and the rest is the rollup",
			[]Act{commandAct("git status --short .plan/", "", tools.ExecSucceeded), readAct("a.go"), readAct("b/c.go")},
			Header{Verb: "ran", Subject: "git status --short .plan/", Rollup: "read 2 files", Bare: "read 2 files"}},
		{"a call behind the lead is counted, not named, in the narrow rollup",
			[]Act{readAct("a.go"), readAct("b/c.go"), commandAct("go test ./internal/ui/...", "ok", tools.ExecSucceeded)},
			Header{Verb: "read", Rollup: "2 files · ran go test ./internal/ui/...", Bare: "2 files · ran 1 command"}},
		{"a clause that does not open with its verb counts behind it",
			[]Act{lookupAct(), lookupAct()},
			Header{Verb: "looked up", Rollup: "twice", Bare: "twice"}},
		{"a write behind the lead is named wide and counted narrow",
			[]Act{readAct("a.go"), editAct("b.go")},
			Header{Verb: "read", Subject: "a.go", Rollup: "wrote b.go", Bare: "wrote 1 file"}},
		{"nothing that ran is no header", nil, Header{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildStep(tc.acts).Header(); got != tc.want {
				t.Errorf("header %+v, want %+v", got, tc.want)
			}
		})
	}
}

// An open card lists the calls under the receipt's verbs, in the order the
// receipt counts kinds; a refused call is listed under its kind, and a kind
// nothing of which ran is a group of its own, last.
func TestStepReceipt_GroupsAreTheCallsUnderTheirVerbs(t *testing.T) {
	refused := editAct("c.go")
	refused.State = StateRefused
	slow := readAct("internal/ui/a.go")
	slow.Duration = 2 * time.Second
	edit := editAct("x.go", hunk(1, diff.Line{Kind: diff.Add, Text: "a"}, diff.Line{Kind: diff.Del, Text: "b"}))
	cases := []struct {
		name string
		acts []Act
		want []string
	}{
		{"by kind, in the order each was first used",
			[]Act{slow, commandAct("go build", "", tools.ExecSucceeded), readAct("internal/ui/b.go"), edit,
				commandAct("go test", "FAIL x", tools.ExecExited)},
			[]string{"read 2 files [0 2] 2s", "ran 2 commands [1 4] 0s", "wrote x.go [3] 0s +1 −1"}},
		{"the reads are counted without where",
			reads("internal/ui/a.go", "internal/ui/b.go", "docs/c.md"),
			[]string{"read 3 files [0 1 2] 0s"}},
		{"a refusal is listed under the kind that ran",
			[]Act{edit, refused},
			[]string{"wrote x.go [0 1] 0s +1 −1"}},
		{"a kind nothing of which ran is counted as refused, last",
			[]Act{refused, readAct("a.go")},
			[]string{"read a.go [1] 0s", "1 refused call [0] 0s"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, g := range BuildStep(c.acts).Groups {
				line := fmt.Sprintf("%s %v %s", g.Label, g.Calls, g.Duration)
				if g.Added+g.Removed > 0 {
					line += fmt.Sprintf(" +%d −%d", g.Added, g.Removed)
				}
				got = append(got, line)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("groups = %q, want %q", got, c.want)
			}
		})
	}
}

// A call's row in an open card says the one line the call stands on: the
// line a broken call said it with, an edit's first changed line, and
// nothing for a call that came back with nothing to add to its subject.
func TestStepReceipt_ACallsLineIsItsBreakOrItsHunk(t *testing.T) {
	cases := []struct {
		name string
		act  Act
		want string
	}{
		{"a failed command's last line", commandAct("go test", "ok a\n--- FAIL: TestX\nFAIL", tools.ExecExited), "FAIL"},
		{"an edit's first changed line", editAct("x.go", hunk(4, diff.Line{Kind: diff.Add, Text: "case \"c\":"})), "+ case \"c\":"},
		{"a command that passed", commandAct("go build", "built", tools.ExecSucceeded), ""},
		{"a read", readAct("a.go"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.act.Line(); got != c.want {
				t.Errorf("Line() = %q, want %q", got, c.want)
			}
		})
	}
}
