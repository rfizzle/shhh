package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chatSuite is a chat case over a one-file fixture.
func chatSuite(t *testing.T, facts string, extra string) []Case {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "ask")
	ws := filepath.Join(dir, workspaceDir)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "kind = \"chat\"\nprompt = \"why?\"\nfacts = " + facts + "\n" + extra
	if err := os.WriteFile(filepath.Join(dir, CaseFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

// chatBody is a fake chat session's stdout: the lines, then a close line
// stating final.
func chatBody(final string, lines ...string) string {
	return "cat <<'STREAM'\n" + strings.Join(append(lines,
		`{"kind":"close","turn":1,"round":2,"outcome":"completed","exit":0,"final":"`+final+`","usage":{"prompt_tokens":10,"completion_tokens":5}}`),
		"\n") + "\nSTREAM"
}

// An attempt runs chat in the copied fixture, passes on the facts and the
// held promise, and is not handed --yes.
func TestAChatAttemptPassesOnFactsAndNoMutation(t *testing.T) {
	requireGit(t)
	argv := filepath.Join(t.TempDir(), "argv")
	bin := fakeShhh(t, "echo \"$@\" > "+argv+"\n"+chatBody("normalize.Path trims the Trailing Slash",
		`{"kind":"tool-call","turn":1,"round":1,"id":"1","tool":"read_file","arguments":"{}"}`,
		`{"kind":"tool-result","turn":1,"round":1,"id":"1","tool":"read_file","result":"x","class":"read"}`))
	cases := chatSuite(t, `["normalize\\.Path", "(?i)trailing slash"]`, "")

	sum, err := Run(context.Background(), cases, Options{Binary: bin, Args: []string{"--model", "m"}})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.Err != nil || !a.Passed || !a.CheckPassed {
		t.Fatalf("attempt = %+v, want a pass", a)
	}
	got, _ := os.ReadFile(argv)
	if want := "chat --print --output jsonl --model m why?"; strings.TrimSpace(string(got)) != want {
		t.Errorf("argv = %q, want %q", strings.TrimSpace(string(got)), want)
	}
	if len(a.Findings) != 1 || a.Findings[0].Word != wordNoMutation || !a.Findings[0].Held {
		t.Errorf("findings = %+v, want the built-in no-mutation, held", a.Findings)
	}
}

// A fact missing from the final text fails the check, in counts alone.
func TestAChatAttemptThatMissesAFactFails(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, chatBody("it just does"))
	cases := chatSuite(t, `["normalize\\.Path", "(?i)trailing slash"]`, "")

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.Passed || a.CheckPassed || a.CheckOutput != "2 of 2 facts missing from the answer" {
		t.Fatalf("attempt = %+v, want a failed check naming counts", a)
	}
}

// A conversation cannot call an edit, so the one form of trying is asking for
// a tool it was never given; the facts can hold and the attempt still fails.
func TestAChatAttemptThatAskedForAnUnknownToolFails(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, chatBody("Trailing slash",
		`{"kind":"tool-call","turn":1,"round":1,"id":"1","tool":"edit_file","arguments":"{}"}`,
		`{"kind":"tool-result","turn":1,"round":1,"id":"1","tool":"edit_file","result":"unknown tool","class":"unknown"}`))
	cases := chatSuite(t, `["(?i)trailing slash"]`, "")

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.Err != nil || a.Passed || !a.CheckPassed {
		t.Fatalf("attempt = %+v, want the facts held and the attempt failed", a)
	}
	if f := a.Findings[0]; f.Held || f.Count != 1 {
		t.Errorf("finding = %+v, want no-mutation broken by one call", f)
	}
}

// Dirty paths after the run are recorded as unintended mutations.
func TestAChatAttemptRecordsADirtyTree(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, "echo changed > a.txt\n"+chatBody("trailing slash"))
	cases := chatSuite(t, `["trailing slash"]`, "")

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.Results[0].Attempts[0].Behaviour.UnintendedMutations; got != 1 {
		t.Errorf("unintended mutations = %d, want 1", got)
	}
}

func TestLoadRefusesAChatCaseWithACheckOrNoFacts(t *testing.T) {
	for name, tc := range map[string]struct{ facts, extra, want string }{
		"a check":  {`["x"]`, "check = [\"true\"]\n", "no check"},
		"no facts": {`[]`, "", "facts is required"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "ask")
			if err := os.MkdirAll(filepath.Join(dir, workspaceDir), 0o755); err != nil {
				t.Fatal(err)
			}
			body := "kind = \"chat\"\nprompt = \"why?\"\nfacts = " + tc.facts + "\n" + tc.extra
			if err := os.WriteFile(filepath.Join(dir, CaseFile), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// The built-in is not declared twice when the case declares it too, and a
// chat case takes the other words.
func TestAChatCaseKeepsOneNoMutationAndTakesBudgets(t *testing.T) {
	cases := chatSuite(t, `["x"]`, "assert = [\"no-mutation\", \"no-shell\"]\nbudget = [\"calls 5\"]\n")
	var words []string
	for _, a := range cases[0].Assertions {
		words = append(words, a.Label())
	}
	if got := strings.Join(words, ","); got != "budget calls 5,no-mutation,no-shell" {
		t.Errorf("assertions = %s", got)
	}
	if !cases[0].AnalysisOnly || !cases[0].Kind.RunsBinary() {
		t.Error("a chat case is analysis-only and runs the binary")
	}
}

// The shipped case loads, with its two facts.
func TestTheShippedChatCaseLoads(t *testing.T) {
	c, err := loadCase(filepath.Join("..", "..", "evals", "chat-explains-a-checkout"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != KindChat || len(c.Facts) != 2 || len(c.Check) != 0 {
		t.Errorf("case = %+v", c)
	}
}
