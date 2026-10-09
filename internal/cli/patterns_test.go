package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// patternFixtureInputs is one pattern of each kind the readings count, and
// one of each that calls for no proposal: the tree as a path, and a command
// the person did not allow every time.
func patternFixtureInputs() patternInputs {
	return patternInputs{
		Patterns: observePatterns{
			Files: []storage.AgentFilePattern{{Path: "internal/cli/session.go", Sessions: 4, Calls: 6}, {Path: ".", Sessions: 3, Calls: 3}},
			Commands: []storage.AgentCommandPattern{
				{Command: "go test", Sessions: 3, Asked: 3, Allowed: 3},
				{Command: "make lint", Sessions: 3, Asked: 3, Allowed: 2},
			},
			Suites: []storage.AgentSuitePattern{{Suite: "default", Sessions: 3, Ran: 4}},
		},
		Sequences: []commandSequence{{Keys: []string{"go vet", "go build", "go test"},
			Lines: []string{"go vet ./...", "go build ./...", "go test ./..."}, Sessions: 3}},
		Reads:   map[string][]string{"internal/cli/session.go": {`read_file {"path":"internal/cli/session.go"}`}},
		Trusted: true,
		Memory:  true,
	}
}

func proposalKinds(ps []chat.Proposal) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Kind+":"+p.Pattern)
	}
	return out
}

// Each pattern maps to one proposal kind, decided here: a file read session
// after session is a convention memory naming the path, a command asked about
// and allowed every time is an allowlist line, a run of three commands is a
// skill whose steps are the commands as run, and a suite failing first is a
// lesson. An untrusted checkout gets no allowlist line and no skill; a
// session without memory gets no memory; what was declined, is already on the
// list or is already remembered is not proposed again.
func TestPatterns_EachKindMapsToOneProposal(t *testing.T) {
	ps := patternProposals(patternFixtureInputs())
	want := []string{"memory:internal/cli/session.go", "allowlist:go test", "skill:go vet → go build → go test", "memory:default"}
	if got := proposalKinds(ps); !slices.Equal(got, want) {
		t.Fatalf("proposals = %v, want %v", got, want)
	}
	if ps[0].MemoryKind != "convention" || !strings.Contains(ps[0].Text, "internal/cli/session.go") ||
		!strings.Contains(ps[0].Facts, "4 of this checkout's sessions") || len(ps[0].Lines) != 1 {
		t.Errorf("file memory = %+v", ps[0])
	}
	if ps[1].Entry != "go test" || ps[1].Key != "go test" {
		t.Errorf("allowlist = %+v", ps[1])
	}
	if ps[2].Skill.Name != "go-vet-go-build-go-test" || !slices.Equal(ps[2].Skill.Steps, []string{"go vet ./...", "go build ./...", "go test ./..."}) ||
		ps[2].Skill.Check() != nil {
		t.Errorf("skill = %+v (%v)", ps[2].Skill, ps[2].Skill.Check())
	}
	if ps[3].MemoryKind != "lesson" || len(ps[3].Lines) != 0 {
		t.Errorf("suite memory = %+v", ps[3])
	}

	in := patternFixtureInputs()
	in.Trusted = false
	if got := proposalKinds(patternProposals(in)); !slices.Equal(got, []string{"memory:internal/cli/session.go", "memory:default"}) {
		t.Errorf("untrusted = %v", got)
	}
	in = patternFixtureInputs()
	in.Memory = false
	if got := proposalKinds(patternProposals(in)); !slices.Equal(got, []string{"allowlist:go test", "skill:go vet → go build → go test"}) {
		t.Errorf("no memory = %v", got)
	}
	in = patternFixtureInputs()
	in.Allowlisted = []string{"go test"}
	in.SkillExists = func(name string) bool { return name == "go-vet-go-build-go-test" }
	in.Remembered = func(s string) bool { return s == "default" }
	in.Declined = func(kind, key string) bool {
		return kind == storage.ProposalMemory && key == "read in session after session: internal/cli/session.go"
	}
	if got := patternProposals(in); len(got) != 0 {
		t.Errorf("answered already = %v", proposalKinds(got))
	}
}

// A proposal that could not be written is not made, since it could never
// reach the card it would be declined on: an entry the list would split, and
// a step over two lines.
func TestPatterns_AnUnwritableProposalIsNotMade(t *testing.T) {
	in := patternFixtureInputs()
	in.Patterns.Commands = []storage.AgentCommandPattern{{Command: "echo a,b", Sessions: 3, Asked: 3, Allowed: 3}}
	in.Sequences[0].Lines[1] = "go build \\\n  ./..."
	for _, p := range patternProposals(in) {
		if p.Kind != storage.ProposalMemory {
			t.Errorf("proposed %s %q", p.Kind, p.Pattern)
		}
	}
}

// A subject is named as a whole: a longer path or word holding it is not it,
// and a sentence's closing full stop is not part of it.
func TestNamesSubject(t *testing.T) {
	for _, c := range []struct {
		text, subject string
		want          bool
	}{
		{"main.go builds the binary.", "main.go", true},
		{"The binary starts in main.go.", "main.go", true},
		{"data.go holds the rows", "a.go", false},
		{"internal/a.go holds them", "a.go", false},
		{"Run linting before you push", "lint", false},
		{"The lint suite fails first", "lint", true},
		{"", "x", false},
	} {
		if got := namesSubject(c.text, c.subject); got != c.want {
			t.Errorf("namesSubject(%q, %q) = %v", c.text, c.subject, got)
		}
	}
}

// Three commands in the same order in enough sessions are one sequence, keyed
// on the commands' first two words; a retried command is one step, and a run
// one step along from a kept one is the same habit.
func TestCommandSequences_ThreeInTheSameOrder(t *testing.T) {
	sessions := [][]string{
		{"go vet ./...", "go build ./...", "go test ./...", "go test ./...", "git status"},
		{"go vet ./internal", "go build .", "go test -run X", "git status"},
		{"ls", "go vet ./...", "go build ./...", "go test ./..."},
		{"go build ./...", "go vet ./...", "go test ./..."},
	}
	got := commandSequences(sessions, 3)
	if len(got) != 1 || !slices.Equal(got[0].Keys, []string{"go vet", "go build", "go test"}) || got[0].Sessions != 3 ||
		!slices.Equal(got[0].Lines, []string{"go vet ./...", "go build ./...", "go test ./..."}) {
		t.Fatalf("sequences = %+v", got)
	}
	if got := commandSequences(sessions, 4); len(got) != 0 {
		t.Fatalf("at four sessions = %+v", got)
	}
}

// The host reads the commands each session ran from its conversation, and
// writes only what a card's yes asks for: the allowlist line through the
// settings writer, shown first as the change to the checkout's file; the
// skill through the skill writer; and a never as the declined row for that
// proposal's own kind and key.
func TestPatternsHost_PreviewsWritesAndDeclinesTheProposalItIsGiven(t *testing.T) {
	db := fixtureStore(t)
	for i := range 3 {
		seedPatternRound(t, db, fmt.Sprintf("s%d", i), []provider.ToolCall{
			{ID: "a", Name: "execute_command", Arguments: `{"command":"go vet ./..."}`},
			{ID: "b", Name: "execute_command", Arguments: `{"command":"go build ./..."}`},
			{ID: "c", Name: "execute_command", Arguments: `{"command":"go test ./..."}`},
		})
	}
	tr, err := readPatternTranscripts(db, time.Now().AddDate(0, 0, -30), "p")
	if err != nil || len(commandSequences(tr.Commands, 3)) != 1 {
		t.Fatalf("transcripts = %+v (%v)", tr, err)
	}

	root := t.TempDir()
	settings := filepath.Join(root, ".shhh", "config.toml")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte("# why\n[behavior]\ndefault_mode = \"manual\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := patternsHost{db: db, root: root, declineRoot: root, project: "p", trusted: true}.chat()

	allow := chat.Proposal{Kind: storage.ProposalAllowlist, Entry: "go test", Key: "go test"}
	path, before, after, err := h.Preview(allow)
	if err != nil || path != filepath.Join(".shhh", "config.toml") {
		t.Fatalf("preview: %q (%v)", path, err)
	}
	if after != before+"command_allowlist = [\"go test\"]\n" {
		t.Fatalf("the preview is not one added line:\n%s", after)
	}
	if raw, _ := os.ReadFile(settings); string(raw) != before {
		t.Fatal("a preview wrote the file")
	}
	if _, err := h.Write(allow); err != nil {
		t.Fatalf("write: %v", err)
	}
	if raw, _ := os.ReadFile(settings); string(raw) != after {
		t.Fatalf("written = %s", raw)
	}

	sk := chat.Proposal{Kind: storage.ProposalSkill, Key: "go vet → go build → go test",
		Skill: skill.Draft{Name: "check-it", Description: "Use when the tree changed.", Steps: []string{"go vet ./..."}}}
	if _, _, after, err := h.Preview(sk); err != nil || !strings.Contains(after, "name: check-it") {
		t.Fatalf("skill preview: %q (%v)", after, err)
	}
	if _, err := os.Stat(skill.DraftPath(root, "check-it")); err == nil {
		t.Fatal("a preview wrote the skill")
	}
	if _, err := h.Write(sk); err != nil {
		t.Fatalf("skill write: %v", err)
	}
	if s, err := skill.LoadFile(skill.DraftPath(root, "check-it")); err != nil || len(s.Warnings) != 0 {
		t.Fatalf("written skill: %+v (%v)", s, err)
	}

	if err := h.Decline(allow); err != nil {
		t.Fatal(err)
	}
	if !db.ProposalDeclined(root, storage.ProposalAllowlist, "go test") || db.ProposalDeclined(root, storage.ProposalMemory, "go test") {
		t.Fatal("the never was not written under the allowlist kind alone")
	}
}
