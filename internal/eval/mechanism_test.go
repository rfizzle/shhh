package eval

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// The label is what the mechanism did, so the test of a scripted kind is that
// each of its words is reachable and that none of them is reached by the
// wrong evidence. What the suite's own rows assert about the mechanisms is in
// evals/; what these assert is that the shape can tell them apart.

func TestSteerRowsAnswerWithWhatTheTurnWasTold(t *testing.T) {
	task := "fix the failing test in internal/calc"
	for _, c := range []struct {
		name string
		row  Row
		want string
	}{
		{"a departure earns a steer that quotes the task", Row{
			State: LabelOffTarget, Reason: "editing the exporter", Instruction: task, Round: 8, Rounds: 9},
			LabelSteered},
		{"a wording that does not carry what the row required", Row{
			State: LabelOffTarget, Instruction: task, Round: 8, Rounds: 9,
			Needs: []string{"a sentence no steer contains"}},
			LabelUnquoted},
		{"a sufficiency reading pulls the check-in forward", Row{
			State: LabelSufficient, Instruction: task, Round: 8, Rounds: 9},
			LabelEnough},
		{"a reading older than the interval", Row{
			State: LabelOffTarget, Instruction: task, Round: 2, Rounds: 14},
			LabelStale},
		{"a reading that lands after the turn stopped", Row{
			State: LabelOffTarget, Instruction: task, Round: 8, Rounds: 9, Finished: true},
			LabelNone},
		{"work that is on target", Row{
			State: LabelOnTarget, Instruction: task, Round: 8, Rounds: 9},
			LabelNone},
	} {
		if got := askSteer(c.row).Label; got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// The wording is the half a unit test of the policy cannot see: the policy
// answers "steer" either way, and what the model is handed is a message that
// either names the instruction or does not.
func TestSteerRowReadsTheWordingAndNotTheDecision(t *testing.T) {
	row := Row{State: LabelOffTarget, Instruction: "rewrite the CSV exporter", Round: 8, Rounds: 9}
	a := askSteer(row)
	if a.Label != LabelSteered {
		t.Fatalf("label = %s", a.Label)
	}
	row.Needs = []string{"rewrite the CSV exporter", "and something else entirely"}
	if a := askSteer(row); a.Label != LabelUnquoted || !strings.Contains(a.Reason, "and something else") {
		t.Fatalf("a steer missing one of two required strings: %s (%s)", a.Label, a.Reason)
	}
}

// A conversation over the line is recovered and one under it is left alone,
// and what the row asserts is what the next round still has.
func TestCompactionRowsReadTheRebuiltConversation(t *testing.T) {
	long := strings.Repeat("the importer reads rows in batches and flushes on close. ", 60)
	conversation := []provider.Message{
		{Role: provider.RoleSystem, Content: "you are shhh"},
		{Role: provider.RoleUser, Content: "why does the importer drop the last row"},
		{Role: provider.RoleAssistant, Content: "reading the importer"},
		{Role: provider.RoleTool, Content: "OLDEST: " + long},
		{Role: provider.RoleUser, Content: "and the exporter"},
		{Role: provider.RoleAssistant, Content: "reading the exporter"},
		{Role: provider.RoleTool, Content: "NEWEST: " + long},
	}
	row := Row{Conversation: conversation, Window: 1200, Summary: "read both writers",
		Needs: []string{"NEWEST"}}
	if a := askCompaction(row); a.Label != LabelKept {
		t.Errorf("the current turn's result: %s (%s)", a.Label, a.Reason)
	}

	row.Needs = []string{"OLDEST"}
	if a := askCompaction(row); a.Label != LabelLost {
		t.Errorf("a result the trim spent: %s (%s)", a.Label, a.Reason)
	}

	row.Window, row.Needs = 200000, []string{"OLDEST", "NEWEST"}
	if a := askCompaction(row); a.Label != LabelUntouched {
		t.Errorf("a conversation under the line: %s (%s)", a.Label, a.Reason)
	}
}

// A conversation the trim cannot shrink is replaced by the summary, and the
// row is what says the turn being answered came through it.
func TestCompactionRowSeesTheRebuild(t *testing.T) {
	prose := strings.Repeat("the session read the loader, the parser and both writers. ", 120)
	row := Row{
		Conversation: []provider.Message{
			{Role: provider.RoleSystem, Content: "you are shhh"},
			{Role: provider.RoleUser, Content: "work out why the importer drops the last row"},
			{Role: provider.RoleAssistant, Content: prose},
			{Role: provider.RoleUser, Content: "now say what you would change"},
			{Role: provider.RoleTool, Content: "NEWEST: the flush happens before the last append"},
		},
		Window: 1600, Summary: "read the importer and the exporter",
		Needs: []string{agent.CompactSummaryPrefix, "now say what you would change", "NEWEST"},
	}
	if a := askCompaction(row); a.Label != LabelKept {
		t.Fatalf("%s (%s)", a.Label, a.Reason)
	}
}

func TestGateRowsAnswerWithTheGatesOwnVerdict(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("the checks a gate row runs are commands, and this machine has no sh")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("a gate result is fingerprinted against a repository")
	}
	suite := func(argv string) string {
		return `{"suites":{"default":{"checks":[{"name":"unit","exe":"sh","args":["-c","` + argv + `"]}]}}}`
	}
	for _, c := range []struct {
		name, config, want string
	}{
		{"every check passed", suite("exit 0"), LabelPass},
		{"a check failed", suite("exit 1"), LabelFail},
		{"a check could not be started",
			`{"suites":{"default":{"checks":[{"name":"unit","exe":"shhh-eval-no-such-checker"}]}}}`,
			LabelGateBlock},
	} {
		if got := askGate(context.Background(), Row{Config: c.config}).Label; got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// The loop, end to end: a writer child that changed a file, a patch computed
// by git and applied to the checkout, and a report that says all of it.
func TestSpawnRowRunsTheLoopAndReadsWhatTheParentWasTold(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("a writer child works in a worktree")
	}
	row := Row{
		Role: "writer", Task: "add the exporter", Paths: []string{"exporter.go"},
		WritePath: "exporter.go", WriteBody: "package export\n", Reply: "added the exporter",
		Needs: []string{"done", "added the exporter", "patch applied to the workspace", "exporter.go"},
	}
	if a := askSpawn(context.Background(), row); a.Label != LabelReported {
		t.Fatalf("%s: %s %s", a.Label, a.Reason, a.Err)
	}

	// The same loop with the patch refused, which the parent has to be able
	// to tell from the one above.
	row.Decline = true
	row.Needs = []string{"declined the patch", "no files were changed"}
	if a := askSpawn(context.Background(), row); a.Label != LabelReported {
		t.Fatalf("declined: %s: %s %s", a.Label, a.Reason, a.Err)
	}

	// And a row asking for something the loop never says is a miss rather
	// than a pass, which is the whole difference between this and a smoke
	// test.
	row.Needs = []string{"a sentence no report contains"}
	if a := askSpawn(context.Background(), row); a.Label != LabelIncomplete {
		t.Fatalf("a report missing what the row required: %s (%s)", a.Label, a.Reason)
	}
}

// A row that cannot measure anything is refused where it is written rather
// than reported as a rate.
func TestScriptedRowsAreRefusedWhereTheyMeasureNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		kind Kind
		row  Row
		want string
	}{
		{"a steer with no reading", KindSteer, Row{Name: "r", Instruction: "x"}, "state is required"},
		{"a steer with a word that is not a reading", KindSteer,
			Row{Name: "r", Instruction: "x", State: "sideways"}, "is not a reading"},
		{"a steer with nothing to quote", KindSteer, Row{Name: "r", State: LabelOffTarget}, "instruction is required"},
		{"a compaction with no window", KindCompaction,
			Row{Name: "r", Conversation: []provider.Message{{}}, Needs: []string{"x"}}, "window is required"},
		{"a compaction that requires nothing", KindCompaction,
			Row{Name: "r", Conversation: []provider.Message{{}}, Window: 10}, "needs is required"},
		{"a spawn with no call", KindSpawn, Row{Name: "r", Needs: []string{"x"}}, "role and task are required"},
		{"a gate with no workspace", KindGate, Row{Name: "r"}, "config is required"},
	} {
		err := checkScriptedRow("table.toml", c.kind, c.row)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want a refusal naming %q", c.name, err, c.want)
		}
	}
}

// And a row that can is not refused for the shape of another kind.
func TestScriptedRowsThatMeasureSomethingLoad(t *testing.T) {
	for _, c := range []struct {
		kind Kind
		row  Row
	}{
		{KindSteer, Row{Name: "r", State: LabelOffTarget, Instruction: "x"}},
		{KindCompaction, Row{Name: "r", Conversation: []provider.Message{{}}, Window: 10, Needs: []string{"x"}}},
		{KindSpawn, Row{Name: "r", Role: "writer", Task: "t", Needs: []string{"x"}}},
		{KindGate, Row{Name: "r", Config: "{}"}},
	} {
		if err := checkScriptedRow("table.toml", c.kind, c.row); err != nil {
			t.Errorf("%s: %v", c.kind, err)
		}
	}
}

// A scripted case asks nobody, which is what lets a machine with no account
// measure it — and what a run must not then price at nothing.
func TestScriptedKindsNeedNoProviderAndAreNotPriced(t *testing.T) {
	c := Case{Name: "gate", Kind: KindGate, Rows: []Row{{Name: "r", Config: `{"suites":{}}`}}}
	a := tableAttempt(context.Background(), c, Options{
		Price: func(string, int, int) (float64, bool) { return 1, true },
	})
	if a.Err != nil {
		t.Fatalf("a scripted attempt refused to run without a provider: %v", a.Err)
	}
	if a.Priced || a.Cost != 0 {
		t.Errorf("a case that made no request was priced: %v %v", a.Cost, a.Priced)
	}
	if needsBinary([]Case{c}) {
		t.Error("a scripted case does not start a session")
	}
}
