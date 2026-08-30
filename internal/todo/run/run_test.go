package run

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

func item(size todo.Size) todo.Item {
	return todo.Item{Slug: "x", Title: "Do x", Size: size, Priority: todo.PriorityMedium, Path: "/r/.shhh/todo/x.md", Body: "## Acceptance criteria\n- [ ] works"}
}

const planText = "## Plan: do x\n\n1. Read the thing\n   files: a.go\n   action: read\n2. Change it\n   files: a.go\n   action: edit\n\nsize: S\nquestions: none\n"

func TestRun_HappyPathSmall(t *testing.T) {
	it := item(todo.SizeM)
	s := Start(it, "sess", "manual", 3)
	first := s.First(it, "")
	if first.Action != ActionPrompt || first.Mode != ModePlan || !strings.Contains(first.Prompt, "RESEARCH") || !strings.Contains(first.Prompt, "BACKLOG ITEM x") {
		t.Fatalf("first = %+v", first)
	}
	step := s.Observe(it, planText)
	if step.Action != ActionPrompt || step.Stage != StageImplement || step.Mode != ModeAuto || s.Size != todo.SizeS || len(s.Steps) != 2 {
		t.Fatalf("after research = %+v, state %+v", step, s)
	}
	if !strings.Contains(step.Prompt, "APPROVED PLAN") || !strings.Contains(step.Prompt, "tick its checkbox") {
		t.Fatalf("implement prompt = %q", step.Prompt)
	}
	step = s.Observe(it, "Changed a.go.")
	if step.Action != ActionVerify {
		t.Fatalf("after implement = %+v", step)
	}
	step = s.VerifyResult(it, true, "")
	if step.Action != ActionPrompt || step.Stage != StageReview || step.Mode != ModePlan || !s.Verified {
		t.Fatalf("after verify = %+v", step)
	}
	step = s.Observe(it, "Looked at everything.\nblocked: is what the item said\nverdict: clean\n")
	if step.Action != ActionPrompt || step.Stage != StageCommit || step.Mode != ModePlan {
		t.Fatalf("after review = %+v", step)
	}
	step = s.Observe(it, "COMMIT:\nDo the x thing\n\nBecause.\n\nREPORT:\n## Report\nSummary: did x\n")
	if step.Action != ActionCommit || s.Message != "Do the x thing\n\nBecause." || !strings.HasPrefix(s.Report, "## Report") {
		t.Fatalf("after commit turn = %+v, msg %q", step, s.Message)
	}
	step = s.Committed([]string{"a.go"})
	if step.Action != ActionDone || !s.Over() || s.Files[0] != "a.go" {
		t.Fatalf("done = %+v", step)
	}
}

func TestRun_RemediationRoundsBySize(t *testing.T) {
	for _, c := range []struct {
		size   todo.Size
		rounds int
	}{{todo.SizeS, 1}, {todo.SizeM, 2}, {todo.SizeL, 2}, {"", 2}} {
		it := item(c.size)
		s := Start(it, "", "", 0)
		s.First(it, "")
		s.Observe(it, strings.Replace(planText, "size: S", "size: "+string(c.size), 1))
		s.Observe(it, "done")
		var step Step
		for i := 0; i <= c.rounds; i++ {
			step = s.VerifyResult(it, false, "FAIL a_test.go")
			if i < c.rounds {
				if step.Action != ActionPrompt || step.Stage != StageRemediate || !strings.Contains(step.Prompt, "FAIL a_test.go") || s.Round != i+1 {
					t.Fatalf("%s round %d: %+v", c.size, i, step)
				}
				if back := s.Observe(it, "fixed"); back.Action != ActionVerify {
					t.Fatalf("remediate should go back to verify, got %+v", back)
				}
			}
		}
		if step.Action != ActionBlocked || !strings.Contains(s.Blocked, "remediation rounds spent") {
			t.Fatalf("%s: after rounds spent = %+v", c.size, step)
		}
	}
}

func TestRun_ReviewFindingsThenClean(t *testing.T) {
	it := item(todo.SizeM)
	s := Start(it, "", "", 0)
	s.First(it, "")
	s.Observe(it, strings.Replace(planText, "size: S", "size: M", 1))
	s.Observe(it, "done")
	s.VerifyResult(it, true, "")
	step := s.Observe(it, "verdict: findings\n1. a.go:3 off by one")
	if step.Stage != StageRemediate || !strings.Contains(step.Prompt, "off by one") {
		t.Fatalf("findings = %+v", step)
	}
	s.Observe(it, "fixed")
	s.VerifyResult(it, true, "")
	if step := s.Observe(it, "no verdict here"); step.Action != ActionBlocked {
		t.Fatalf("a review without a verdict should block, got %+v", step)
	}
}

func TestRun_ResearchGates(t *testing.T) {
	cases := map[string]string{
		"open question": "## Plan: x\n\n1. a\n\nsize: S\nquestions:\n- keep the old flag?\n",
		"no plan":       "I would change a.go.\nsize: S\nquestions: none",
		"blocked":       "blocked: the item asks for a file that was deleted",
		"empty":         "   ",
	}
	for name, text := range cases {
		it := item(todo.SizeS)
		s := Start(it, "", "", 0)
		s.First(it, "")
		if step := s.Observe(it, text); step.Action != ActionBlocked || s.Blocked == "" {
			t.Errorf("%s: %+v", name, step)
		}
	}
	it := item(todo.SizeS)
	s := Start(it, "", "", 0)
	s.First(it, "")
	s.Observe(it, "## Plan: x\n\n1. a\n\nsize: L\nquestions: none\n")
	if s.Size != todo.SizeL || s.SizeBefore != todo.SizeS || !strings.Contains(s.Summary(), "size L (was S)") {
		t.Errorf("regrade not recorded: %s", s.Summary())
	}
}

func TestParsers(t *testing.T) {
	if qs := questionLines("questions:\n- a?\n* b?\nnot a bullet\n- c?"); strings.Join(qs, "|") != "a?|b?" {
		t.Errorf("questions = %v", qs)
	}
	if qs := questionLines("Questions: Is it on?\n\n- later"); strings.Join(qs, "|") != "Is it on?" {
		t.Errorf("inline question = %v", qs)
	}
	if v, f := verdictLine("Verdict: FINDINGS\n1. x"); v != "findings" || f != "1. x" {
		t.Errorf("verdict = %q %q", v, f)
	}
	if _, _, ok := commitParts("REPORT: r\nCOMMIT: c"); ok {
		t.Error("markers out of order accepted")
	}
	if m, r, ok := commitParts("COMMIT:\n```text\nSubject\n\nBody\n```\nREPORT:\n## Report\nx"); !ok || m != "Subject\n\nBody" || r != "## Report\nx" {
		t.Errorf("fenced commit = %q %q %v", m, r, ok)
	}
}

func TestTestCommands_SnapshotAtStart(t *testing.T) {
	it := item(todo.SizeS)
	it.Body = "## Tests\n- `go test ./a`\n- go vet ./...\n-\n\n## Notes\n- not a test"
	s := Start(it, "", "", 0)
	if got := strings.Join(s.Tests, "|"); got != "go test ./a|go vet ./..." {
		t.Errorf("tests = %q", got)
	}
}

func TestCheckpoint_RoundTrips(t *testing.T) {
	root := t.TempDir()
	s := Start(item(todo.SizeS), "sess", "manual", 2)
	s.Stage = StageVerify
	s.Round = 1
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	back, err := Load(root, "x")
	if err != nil || back.Stage != StageVerify || back.Round != 1 || back.PrevMode != "manual" || back.Turn != 2 {
		t.Fatalf("loaded = %+v, %v", back, err)
	}
	Discard(root, "x")
	if _, err := Load(root, "x"); err == nil {
		t.Fatal("checkpoint survived Discard")
	}
}
