package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var testFixture = fixture{roots: []string{"/ws"}, files: map[string]bool{"a.go": true, "dir/b.go": true}}

// finding decides one assertion over a stream.
func finding(t *testing.T, a Assertion, tr trace) Finding {
	t.Helper()
	got := tr.assert([]Assertion{a}, testFixture)
	if len(got) != 1 {
		t.Fatalf("findings = %+v", got)
	}
	return got[0]
}

func TestBudgetCountsOneToolOrEveryCall(t *testing.T) {
	tr := streamOf(t,
		callLine(1, "1", "read_file", `{"path":"a"}`), callLine(1, "2", "read_file", `{"path":"b"}`),
		callLine(2, "3", "search", `{}`),
	)
	for _, tc := range []struct {
		a    Assertion
		held bool
		n    int
	}{
		{Assertion{Word: wordBudget, Tool: "read_file", Limit: 2}, true, 2},
		{Assertion{Word: wordBudget, Tool: "read_file", Limit: 1}, false, 2},
		{Assertion{Word: wordBudget, Tool: "calls", Limit: 3}, true, 3},
		{Assertion{Word: wordBudget, Tool: "calls", Limit: 2}, false, 3},
		{Assertion{Word: wordBudget, Tool: "glob", Limit: 0}, true, 0},
	} {
		f := finding(t, tc.a, tr)
		if f.Held != tc.held || f.Count != tc.n {
			t.Errorf("%s: held %v count %d, want %v %d", tc.a.Label(), f.Held, f.Count, tc.held, tc.n)
		}
	}
	if got := finding(t, Assertion{Word: wordBudget, Tool: "calls", Limit: 2}, tr).Detail; got != "3 calls" {
		t.Errorf("detail = %q", got)
	}
}

// The key is the loop's own: the same call written with its arguments in a
// different order is a repeat, and the same call that came back with different
// bytes is not.
func TestNoRepeatUsesTheLoopsKey(t *testing.T) {
	a := Assertion{Word: wordNoRepeat}
	breaks := streamOf(t,
		callLine(1, "1", "search", `{"pattern":"x","path":"."}`), resultLine(1, "1", "search", "hit"),
		callLine(2, "2", "search", `{"path":".","pattern":"x"}`), resultLine(2, "2", "search", "hit"),
	)
	f := finding(t, a, breaks)
	if f.Held || f.Count != 1 || f.Detail != "search: 1 repeated call" {
		t.Errorf("a repeated call should break it: %+v", f)
	}
	holds := streamOf(t,
		callLine(1, "1", "execute_command", `{"command":"go test"}`), resultLine(1, "1", "execute_command", "FAIL"),
		callLine(2, "2", "execute_command", `{"command":"go test"}`), resultLine(2, "2", "execute_command", "ok"),
	)
	if f := finding(t, a, holds); !f.Held {
		t.Errorf("the same command with a new result is a different interaction: %+v", f)
	}
}

// A tool name on a call line is the model's own text, so a repeated call to a
// tool the session never had is drawn without it.
func TestNoRepeatDoesNotDrawAToolNameTheModelMadeUp(t *testing.T) {
	tr := streamOf(t,
		callLine(1, "1", "cat_/etc/passwd", `{}`), unknownResultLine(1, "1", "cat_/etc/passwd"),
		callLine(2, "2", "cat_/etc/passwd", `{}`), unknownResultLine(2, "2", "cat_/etc/passwd"),
	)
	f := finding(t, Assertion{Word: wordNoRepeat}, tr)
	if f.Held || strings.Contains(f.Detail, "passwd") {
		t.Errorf("finding = %+v", f)
	}
}

func TestReadBeforeEditExemptsNewFilesAndAppends(t *testing.T) {
	a := Assertion{Word: wordReadBefore}
	blind := streamOf(t, callLine(1, "1", "edit_file", `{"path":"a.go","old_text":"x","new_text":"y"}`))
	f := finding(t, a, blind)
	if f.Held || f.Count != 1 || f.Detail != "edit_file: 1 call on a path never read" {
		t.Errorf("an edit of an unread fixture file should break it: %+v", f)
	}
	if strings.Contains(f.Detail, "a.go") {
		t.Errorf("a detail never carries a path: %q", f.Detail)
	}
	for name, tr := range map[string]trace{
		"read first": streamOf(t,
			callLine(1, "1", "read_file", `{"path":"/ws/a.go"}`),
			callLine(2, "2", "edit_file", `{"path":"a.go","old_text":"x","new_text":"y"}`)),
		"new file": streamOf(t, callLine(1, "1", "write_file", `{"path":"fresh.go","content":"x"}`)),
		"append":   streamOf(t, callLine(1, "1", "edit_file", `{"path":"a.go","append":"more"}`)),
		"a file the session wrote": streamOf(t,
			callLine(1, "1", "write_file", `{"path":"fresh.go","content":"x"}`),
			callLine(2, "2", "edit_file", `{"path":"fresh.go","old_text":"x","new_text":"y"}`)),
	} {
		if f := finding(t, a, tr); !f.Held {
			t.Errorf("%s should hold: %+v", name, f)
		}
	}
	overwrite := streamOf(t, callLine(1, "1", "write_file", `{"path":"dir/b.go","content":"x"}`))
	if f := finding(t, a, overwrite); f.Held {
		t.Errorf("overwriting an unread fixture file should break it: %+v", f)
	}
}

// A read that came back as an error taught the session nothing of the file, so
// an edit that follows it is still blind; a write that failed likewise.
func TestReadBeforeEditCountsOnlyAReadThatSucceeded(t *testing.T) {
	a := Assertion{Word: wordReadBefore}
	failedRead := func(id, tool string) map[string]any {
		l := resultLine(1, id, tool, "error: permission denied")
		l["class"] = "permission"
		return l
	}
	edit := callLine(2, "2", "edit_file", `{"path":"a.go","old_text":"x","new_text":"y"}`)
	broken := map[string]trace{
		"failed read": streamOf(t, callLine(1, "1", "read_file", `{"path":"a.go"}`), failedRead("1", "read_file"), edit),
		"failed write": streamOf(t,
			callLine(1, "1", "write_file", `{"path":"a.go","content":"x"}`), failedRead("1", "write_file"), edit),
	}
	for name, tr := range broken {
		if f := finding(t, a, tr); f.Held {
			t.Errorf("%s should break it: %+v", name, f)
		}
	}
	ok := streamOf(t, callLine(1, "1", "read_file", `{"path":"a.go"}`), resultLine(1, "1", "read_file", "package a"), edit)
	if f := finding(t, a, ok); !f.Held {
		t.Errorf("a read that succeeded should hold: %+v", f)
	}
}

func TestLoadRefusesABudgetOnAToolNoSessionOffers(t *testing.T) {
	err := writeAssertCase(t, `budget = ["read_fiel 3"]`)
	for _, want := range []string{"budget", CaseFile, "read_fiel"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
	for _, tool := range []string{"read_file", "execute_command", "ask", "process", "spawn_agent", "git_write", "web_fetch", "hover", "calls"} {
		if _, err := parseAssertions([]string{tool + " 1"}, nil, "case.toml", false); err != nil {
			t.Errorf("%s is a tool a session offers: %v", tool, err)
		}
	}
}

func TestNoMutationCountsAnUnknownTool(t *testing.T) {
	a := Assertion{Word: wordNoMutation}
	reads := streamOf(t, callLine(1, "1", "read_file", `{}`), resultLine(1, "1", "read_file", "x"))
	if f := finding(t, a, reads); !f.Held {
		t.Errorf("reads should hold: %+v", f)
	}
	for _, tool := range []string{"write_file", "edit_file", "execute_command", "process", "git_write"} {
		if f := finding(t, a, streamOf(t, callLine(1, "1", tool, `{}`))); f.Held || f.Count != 1 {
			t.Errorf("%s should break it: %+v", tool, f)
		}
	}
	asked := streamOf(t,
		callLine(1, "1", "my_secret_edit", `{"path":"/home/me/private"}`), unknownResultLine(1, "1", "my_secret_edit"))
	f := finding(t, a, asked)
	if f.Held || f.Count != 1 {
		t.Fatalf("a tool the session never offered should break it: %+v", f)
	}
	if strings.Contains(f.Detail, "my_secret_edit") || strings.Contains(f.Detail, "private") {
		t.Errorf("the model's own tool name and arguments stay out of the detail: %q", f.Detail)
	}
}

func TestNoShell(t *testing.T) {
	a := Assertion{Word: wordNoShell}
	if f := finding(t, a, streamOf(t, callLine(1, "1", "read_file", `{}`))); !f.Held {
		t.Errorf("no shell call should hold: %+v", f)
	}
	f := finding(t, a, streamOf(t, callLine(1, "1", "execute_command", `{"command":"cat x"}`), callLine(2, "2", "execute_command", `{"command":"ls"}`)))
	if f.Held || f.Count != 2 || f.Detail != "execute_command: 2 calls" {
		t.Errorf("shell calls should break it: %+v", f)
	}
}

func TestNoDenialNamesTheReason(t *testing.T) {
	a := Assertion{Word: wordNoDenial}
	if f := finding(t, a, streamOf(t, decisionLine(1, "allow", "mode"), decisionLine(2, "ask", "safety"))); !f.Held {
		t.Errorf("allows and asks should hold: %+v", f)
	}
	f := finding(t, a, streamOf(t, decisionLine(1, "deny", "out-of-scope"), decisionLine(2, "deny", "denylist"), decisionLine(3, "deny", "out-of-scope")))
	if f.Held || f.Count != 3 || f.Detail != "denied: denylist 1, out-of-scope 2" {
		t.Errorf("denials should break it, by reason code: %+v", f)
	}
}

func TestAsksNeedsAQuestion(t *testing.T) {
	a := Assertion{Word: wordAsks}
	if f := finding(t, a, streamOf(t, callLine(1, "1", "read_file", `{}`))); f.Held {
		t.Errorf("no question should break it: %+v", f)
	}
	if f := finding(t, a, streamOf(t, callLine(1, "1", "ask", `{"question":"which?"}`))); !f.Held {
		t.Errorf("a question should hold: %+v", f)
	}
}

// writeAssertCase lays a workspace case down with extra top-level keys.
func writeAssertCase(t *testing.T, extra string) error {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "sample")
	if err := os.MkdirAll(filepath.Join(dir, workspaceDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, workspaceDir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "prompt = \"do it\"\ncheck = [\"true\"]\n" + extra + "\n"
	if err := os.WriteFile(filepath.Join(dir, CaseFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(root)
	return err
}

func TestLoadTakesBudgetAndAssert(t *testing.T) {
	if err := writeAssertCase(t, "budget = [\"read_file 6\", \"calls 15\"]\nassert = [\"no-repeat\", \"read-before-edit\", \"no-mutation\", \"no-shell\", \"no-denial\"]"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRefusesAWordOutsideTheSet(t *testing.T) {
	for extra, want := range map[string]string{
		`assert = ["no-typos"]`:                   "assert",
		`assert = ["no-repeat", "no-repeat"]`:     "assert",
		`budget = ["read_file"]`:                  "budget",
		`budget = ["read_file many"]`:             "budget",
		`budget = ["read_file -1"]`:               "budget",
		`budget = ["read_file 1 2"]`:              "budget",
		`budget = ["read_file 1", "read_file 2"]`: "budget",
	} {
		err := writeAssertCase(t, extra)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), CaseFile) {
			t.Errorf("%s: err = %v, want a sentence naming the %s key and the file", extra, err, want)
		}
	}
}

func TestLoadRefusesAsksWithoutAPersona(t *testing.T) {
	err := writeAssertCase(t, `assert = ["asks"]`)
	if err == nil || !strings.Contains(err.Error(), "asks") || !strings.Contains(err.Error(), "persona") {
		t.Fatalf("err = %v", err)
	}
	if _, err := parseAssertions(nil, []string{"asks"}, "case.toml", true); err != nil {
		t.Errorf("a case with a persona may name asks: %v", err)
	}
}

func TestLoadRefusesAssertionsOnACaseThatRunsNoSession(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "table")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, CaseFile), []byte("kind = \"classifier\"\nassert = [\"no-shell\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, TableFile), []byte("[[row]]\nname = \"a\"\nexpect = [\"allow\"]\ntool = \"execute_command\"\narguments = \"{}\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "assert") {
		t.Fatalf("err = %v", err)
	}
}
