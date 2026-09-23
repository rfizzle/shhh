package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// A review stopped at its cap still ends on the line the reviewer's prompt
// asks for, since the lane reads the verdict off that label.
func TestTheReportDirectiveNamesTheVerdictLine(t *testing.T) {
	if !strings.Contains(reviewReportDirective, "`Verdict: <word>`") {
		t.Fatalf("the report directive does not name the verdict line:\n%s", reviewReportDirective)
	}
}

// count is how many times a message with exactly this text was put in front
// of the child. The whole conversation is re-sent every round, so the answer
// is the most any single request carried and never the sum across them.
func (s *scriptedEnv) count(text string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	most := 0
	for _, msgs := range s.requests {
		n := 0
		for _, m := range msgs {
			if m.Content == text {
				n++
			}
		}
		most = max(most, n)
	}
	return most
}

// firstUserTurn is what the child's opening request put in front of it — the
// message a review's declared evidence has to reach it in.
func firstUserTurn(t *testing.T, env *scriptedEnv) string {
	t.Helper()
	env.mu.Lock()
	defer env.mu.Unlock()
	if len(env.requests) == 0 {
		t.Fatal("the child never made a request")
	}
	for _, m := range env.requests[0] {
		if m.Role == provider.RoleUser {
			return m.Content
		}
	}
	t.Fatal("the child's first request carried no user turn")
	return ""
}

// TestReviewOpensOnItsDeclaredEvidence is the bounded-review contract at its
// narrowest: the paths are in front of the reviewer before its task, so the
// pass starts at the change instead of at a survey to find it.
func TestReviewOpensOnItsDeclaredEvidence(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{text: "no findings"}}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName,
		`{"role":"reviewer","task":"judge the frame change","paths":["internal/ui/chat/frame.go"]}`)
	execTool(t, sup, ReportToolName, `{"name":"reviewer-1"}`)

	turn := firstUserTurn(t, env)
	evidence := strings.Index(turn, "internal/ui/chat/frame.go")
	task := strings.Index(turn, "judge the frame change")
	switch {
	case evidence < 0:
		t.Fatalf("the review never received its declared paths:\n%s", turn)
	case task < 0:
		t.Fatalf("the review lost its task:\n%s", turn)
	case evidence > task:
		t.Fatalf("the evidence must arrive ahead of the task:\n%s", turn)
	}
	if !strings.Contains(turn, "Report when that pass is done") {
		t.Fatalf("the review was not directed to report on the pass:\n%s", turn)
	}
}

// TestReviewWithoutDeclaredPathsIsUnchanged: a task that carries the change
// itself gets no manufactured evidence block.
func TestReviewWithoutDeclaredPathsIsUnchanged(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{text: "no findings"}}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"reviewer","task":"judge this diff: ..."}`)
	execTool(t, sup, ReportToolName, `{"name":"reviewer-1"}`)

	if turn := firstUserTurn(t, env); turn != "judge this diff: ..." {
		t.Fatalf("an undeclared review opens on its task alone, got:\n%s", turn)
	}
}

// TestReviewEvidenceCarriesTheWorkspaceDiff: the reviewer is handed the change
// under its paths and nothing from outside them — the difference between
// evidence it was given and a survey it had to conduct.
func TestReviewEvidenceCarriesTheWorkspaceDiff(t *testing.T) {
	repo := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "main.go"),
		[]byte("package main\n\nfunc reviewed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "other.go"),
		[]byte("package main\n\nfunc unrelated() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", "other.go")

	env := &scriptedEnv{steps: []streamStep{{text: "no findings"}}}
	sup := New(context.Background(), Options{Root: repo, NewEnv: env.factory()})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"reviewer","task":"judge it","paths":["main.go"]}`)
	execTool(t, sup, ReportToolName, `{"name":"reviewer-1"}`)

	turn := firstUserTurn(t, env)
	if !strings.Contains(turn, "func reviewed() {}") {
		t.Fatalf("the review did not receive the change under its paths:\n%s", turn)
	}
	if strings.Contains(turn, "unrelated") {
		t.Fatalf("the review received a change outside its declared paths:\n%s", turn)
	}
}

// TestReviewPathsAreNotAWriteClaim: two reviews of the same change are the
// ordinary case, and nothing may be reserved on a reader's behalf.
func TestReviewPathsAreNotAWriteClaim(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{text: "a"}, {text: "b"}}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"reviewer","task":"correctness","paths":["internal/ui/**"]}`)
	execTool(t, sup, SpawnToolName, `{"role":"reviewer","task":"clarity","paths":["internal/ui/**"]}`)

	if _, err := parseSpawnArgs(nil, json.RawMessage(
		`{"role":"researcher","task":"x","paths":["internal/ui/**"]}`)); err == nil ||
		!strings.Contains(err.Error(), "does neither") {
		t.Fatalf("a role that neither writes nor reviews has no use for paths: %v", err)
	}
	// And the approval card must not read a review's evidence as a scope:
	// the card answers what the child can change, and the answer is nothing.
	plan, err := SpawnPlan(nil, json.RawMessage(
		`{"role":"reviewer","task":"x","paths":["internal/ui/**"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Writer || !strings.HasPrefix(plan.Scope, "nothing") {
		t.Fatalf("a review's card must state it changes nothing: %+v", plan)
	}
	if !strings.Contains(plan.Scope, "internal/ui/**") {
		t.Fatalf("the card must still name the evidence: %+v", plan)
	}
}

// TestReviewCapEndsInAReport: the round cap is where a review's inspection
// stops. Every other child is asked to take stock and given more room; a
// review is told to report on what it has, and its cap does not double.
func TestReviewCapEndsInAReport(t *testing.T) {
	env := &scriptedEnv{
		steps: []streamStep{
			{calls: []provider.ToolCall{{ID: "r1", Name: "read_file", Arguments: `{"path":"a"}`}}},
			{text: "no findings"},
		},
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName,
		`{"role":"reviewer","task":"judge it","paths":["a"],"max_rounds":1}`)
	report := execTool(t, sup, ReportToolName, `{"name":"reviewer-1"}`)

	if !strings.Contains(report, "no findings") {
		t.Fatalf("a review at its cap must still report: %s", report)
	}
	if !env.asked(reviewReportDirective) {
		t.Fatal("a review at its cap was not directed to report")
	}
	if env.asked("Taking stock") {
		t.Fatal("a review's cap must not become a check-in that widens the pass")
	}
	var st Status
	for _, s := range sup.Snapshot() {
		if s.Name == "reviewer-1" {
			st = s
		}
	}
	if st.CheckIns != 0 {
		t.Fatalf("a review takes no check-ins, got %d", st.CheckIns)
	}
	if st.State != StateDone {
		t.Fatalf("state = %v, want done", st.State)
	}
}

// TestReviewThatSpendsItsReportAllowanceStops: the directive is given once.
// A review that answers it by opening more files runs out for good rather
// than being told to report again, which is the loop a stop that renews
// itself would be.
func TestReviewThatSpendsItsReportAllowanceStops(t *testing.T) {
	call := func(id string) streamStep {
		return streamStep{calls: []provider.ToolCall{{ID: id, Name: "read_file", Arguments: `{"path":"a"}`}}}
	}
	// One inspection round, then more calls than the report allowance. A
	// turn's round counter starts at zero, so the allowance is the report
	// turn's whole cap: an increment on the rounds already spent would hand
	// the report a second pass the size of the inspection.
	steps := []streamStep{call("r0")}
	for i := 0; i <= reviewReportRounds; i++ {
		steps = append(steps, call(fmt.Sprintf("r%d", i+1)))
	}
	env := &scriptedEnv{steps: append(steps, streamStep{text: "too late"})}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName,
		`{"role":"reviewer","task":"judge it","paths":["a"],"max_rounds":1}`)
	report := execTool(t, sup, ReportToolName, `{"name":"reviewer-1"}`)

	if !strings.Contains(report, "round limit") {
		t.Fatalf("a review that would not report must stop at its cap: %s", report)
	}
	var st Status
	for _, s := range sup.Snapshot() {
		if s.Name == "reviewer-1" {
			st = s
		}
	}
	if st.State != StateFailed {
		t.Fatalf("state = %v, want failed", st.State)
	}
	// The inspection round plus the allowance, and not one call more.
	if want := 1 + reviewReportRounds; st.ToolCalls != want {
		t.Fatalf("the report turn ran %d calls in all, want %d", st.ToolCalls, want)
	}
	// Once, not once per cap: a second directive is the loop.
	if n := env.count(reviewReportDirective); n != 1 {
		t.Fatalf("the report directive was given %d times, want 1", n)
	}
}

// TestRoleDefaultBudgetsClearTheOrdinaryDefault: no role's default may leave a
// child unable to investigate, act and verify. The floor is what an explicit
// bounded spawn may name, never what a role falls back to.
func TestRoleDefaultBudgetsClearTheOrdinaryDefault(t *testing.T) {
	profiles := customProfiles()
	for _, role := range profiles.Names() {
		args, err := parseSpawnArgs(profiles, json.RawMessage(
			`{"role":"`+role+`","task":"x"}`))
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if args.maxTokens < DefaultMaxTokens {
			t.Fatalf("%s defaults to %d, below the ordinary default %d",
				role, args.maxTokens, DefaultMaxTokens)
		}
		if _, err := parseSpawnArgs(profiles, json.RawMessage(
			`{"role":"`+role+`","task":"x","max_tokens":60000}`)); err == nil ||
			!strings.Contains(err.Error(), "below the minimum") {
			t.Fatalf("%s admitted a doomed budget: %v", role, err)
		}
		args, err = parseSpawnArgs(profiles, json.RawMessage(
			`{"role":"`+role+`","task":"x","max_tokens":200000}`))
		if err != nil || args.maxTokens != MinChildMaxTokens {
			t.Fatalf("%s must allow the declared floor: %d %v", role, args.maxTokens, err)
		}
	}
}

// TestAdmissionRefusalLeavesNoWorktree: the refusal happens before a writer's
// slot, so the repository it would have copied is untouched.
func TestAdmissionRefusalLeavesNoWorktree(t *testing.T) {
	repo := initTestRepo(t)
	sup := New(context.Background(), Options{
		Root: repo,
		NewEnv: func(context.Context, Spec) (Env, error) {
			return Env{SystemPrompt: strings.Repeat("p", 8_000)}, nil
		},
		Record: func(Spec, string) Recorder { t.Fatal("a refused spawn opened a record"); return Recorder{} },
	})
	t.Cleanup(sup.Close)

	before := worktreeCount(t, repo)
	if _, err := sup.Spawn(json.RawMessage(
		`{"role":"writer","task":"x","max_tokens":200000}`)); err == nil {
		t.Fatal("an undersized budget must be refused")
	}
	if after := worktreeCount(t, repo); after != before {
		t.Fatalf("a refused spawn opened a worktree: %d, was %d", after, before)
	}
	if children := sup.Snapshot(); len(children) != 0 {
		t.Fatalf("a refused spawn claimed a child slot: %+v", children)
	}
}

// TestAdmissionFloorGrowsWithTheEvidence: a review's declared evidence is part
// of what it must be admitted for, so a budget that could not carry it is
// refused rather than spent reading it.
func TestAdmissionFloorGrowsWithTheEvidence(t *testing.T) {
	repo := initTestRepo(t)
	body := "package main\n" + strings.Repeat("// a line of the change under review\n", 20_000)
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &scriptedEnv{steps: []streamStep{{text: "no findings"}}}
	sup := New(context.Background(), Options{Root: repo, NewEnv: env.factory()})
	t.Cleanup(sup.Close)

	_, err := sup.Spawn(json.RawMessage(
		`{"role":"reviewer","task":"judge it","paths":["main.go"],"max_tokens":200000}`))
	if err == nil || !strings.Contains(err.Error(), "cannot admit") {
		t.Fatalf("the evidence must raise the admission floor: %v", err)
	}
	if children := sup.Snapshot(); len(children) != 0 {
		t.Fatalf("a refused review claimed a child slot: %+v", children)
	}
}

func TestWorkspaceDiffTruncatesAtALineBoundary(t *testing.T) {
	repo := initTestRepo(t)
	body := "package main\n" + strings.Repeat("// a long line of a very large change\n", 4_000)
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	d := workspaceDiff(repo, []string{"main.go"})
	if !strings.Contains(d, "[diff truncated:") {
		t.Fatalf("an oversized diff must say it was cut:\n%s", d[max(0, len(d)-200):])
	}
	if len(d) > reviewEvidenceBytes+200 {
		t.Fatalf("truncated diff is %d bytes, over the %d cap", len(d), reviewEvidenceBytes)
	}
	for _, line := range strings.Split(d, "\n") {
		if strings.HasPrefix(line, "// a long line") && line != "// a long line of a very large change" {
			t.Fatalf("the cut landed mid-line: %q", line)
		}
	}
	if workspaceDiff(t.TempDir(), []string{"main.go"}) != "" {
		t.Fatal("a directory git cannot answer for is no evidence, not an error")
	}
}

// run executes one git command in dir, failing the test on error.
func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func worktreeCount(t *testing.T, repo string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v\n%s", err, out)
	}
	return strings.Count(string(out), "worktree ")
}
