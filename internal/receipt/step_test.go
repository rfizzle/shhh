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
			[]Act{readAct("a.go"), searchAct("x"), readAct("b/c.go"), lookupAct()},
			"read 2 files · searched once · 1 lookup"},
		{"the order is of first use, not of kind",
			[]Act{lookupAct(), searchAct("x"), searchAct("y"), readAct("a.go"), lookupAct()},
			"2 lookups · searched twice · read 1 file"},
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
			[]Act{{Receipt: Build(Call{Name: "spawn_agent", Args: `{"task":"t"}`, Result: "started"})}},
			"1 spawn"},
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
		{"a read's path, where the step read one file", []Act{searchAct("x"), readAct("internal/ui/keys.go")},
			Evidence{Line: "internal/ui/keys.go", Call: 1}},
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
		{"only searches and lookups", []Act{searchAct("x"), lookupAct()}},
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
