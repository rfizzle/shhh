package chat

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// The two commits a resumed session compares. Full ids, because the message
// is what abbreviates them and a test that passed short ones would be
// asserting on its own fixture.
const (
	headWas = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
	headNow = "e4f5a6b7c8d9e0f1a2b3c4d5e6f708192a3b4c5d"
)

func repoSurvey() project.Info {
	return project.Info{Dir: "/w", Repo: true, Branch: "master", Dirty: 3, Head: headNow}
}

func TestResumeNotice_MovedHeadNamesBothCommits(t *testing.T) {
	n := resumeNotice(repoSurvey(), storage.ChatResume{Head: headWas})
	if !strings.Contains(n.Text, shortCommit(headWas)) || !strings.Contains(n.Text, shortCommit(headNow)) {
		t.Fatalf("a moved head names the commit that was and the one that is:\n%s", n.Text)
	}
	if !strings.Contains(n.Text, "moved") {
		t.Fatalf("a moved head says so:\n%s", n.Text)
	}
	// The whole account of the move is one line, so a model reading past the
	// bracketed facts cannot miss it between paragraphs.
	moved := ""
	for _, line := range strings.Split(n.Text, "\n") {
		if strings.Contains(line, "moved") {
			moved = line
		}
	}
	if strings.Count(moved, shortCommit(headWas)) != 2 || !strings.Contains(moved, shortCommit(headNow)) {
		t.Fatalf("the move belongs on one line, got %q", moved)
	}
}

func TestResumeNotice_UnchangedHeadClaimsNoMove(t *testing.T) {
	n := resumeNotice(repoSurvey(), storage.ChatResume{Head: headNow})
	if strings.Contains(n.Text, "moved") || strings.Contains(n.Text, "→") {
		t.Fatalf("a tree that did not move is not reported as one:\n%s", n.Text)
	}
	if !strings.Contains(n.Text, shortCommit(headNow)) {
		t.Fatalf("the commit in front of the reader is still stated:\n%s", n.Text)
	}
}

// A slot written before the commit was recorded has nothing to compare
// against, and a comparison invented from that would be the one lie this
// message can tell.
func TestResumeNotice_NoStoredHeadClaimsNoMove(t *testing.T) {
	n := resumeNotice(repoSurvey(), storage.ChatResume{})
	if strings.Contains(n.Text, "moved") {
		t.Fatalf("nothing to compare against is not a move:\n%s", n.Text)
	}
}

func TestResumeNotice_SummaryFollowsTheSurvey(t *testing.T) {
	n := resumeNotice(repoSurvey(), storage.ChatResume{Head: headNow, Summary: "the cache work is half done"})
	if len(n.Messages) != 2 {
		t.Fatalf("expected the survey and the summary, got %d messages", len(n.Messages))
	}
	if !strings.HasPrefix(n.Messages[0].Content, resumeMessagePrefix) {
		t.Fatalf("the survey goes first, got %q", n.Messages[0].Content)
	}
	if !strings.HasPrefix(n.Messages[1].Content, resumeSummaryPrefix) ||
		!strings.Contains(n.Messages[1].Content, "the cache work is half done") {
		t.Fatalf("the summary follows it, got %q", n.Messages[1].Content)
	}
	for _, msg := range n.Messages {
		if msg.Role != provider.RoleUser {
			t.Fatalf("the context joins the conversation as the user's, got %q", msg.Role)
		}
	}
	if n.Summary != "the cache work is half done" {
		t.Fatalf("the summary is handed back so the session goes on saving it, got %q", n.Summary)
	}
}

func TestResumeNotice_NoSummaryLeavesNoPlaceholder(t *testing.T) {
	n := resumeNotice(repoSurvey(), storage.ChatResume{Head: headNow})
	if len(n.Messages) != 1 {
		t.Fatalf("a conversation that never compacted carries one message, got %d", len(n.Messages))
	}
	if strings.Contains(n.Text, resumeSummaryPrefix) || strings.Contains(strings.ToLower(n.Text), "no summary") {
		t.Fatalf("there is no placeholder for a summary nobody wrote:\n%s", n.Text)
	}
}

func TestResumeRow_NamesTheBranchAndWhatIsChanged(t *testing.T) {
	if got := resumeRow(repoSurvey()); got != "resumed · master · 3 changed" {
		t.Fatalf("row = %q", got)
	}
	detached := project.Info{Dir: "/w", Repo: true, Detached: true, Head: headNow}
	if got := resumeRow(detached); got != "resumed · (detached) · 0 changed" {
		t.Fatalf("detached row = %q", got)
	}
	if got := resumeRow(project.Info{Dir: "/w"}); got != "resumed · no git here" {
		t.Fatalf("non-repo row = %q", got)
	}
}

func TestResumeNotice_OutsideARepositorySaysSo(t *testing.T) {
	n := resumeNotice(project.Info{Dir: "/w"}, storage.ChatResume{})
	if !strings.Contains(n.Text, "not a git repository") {
		t.Fatalf("a workspace with no repository says so:\n%s", n.Text)
	}
	if strings.Contains(n.Text, "HEAD") {
		t.Fatalf("there is no head to name outside a repository:\n%s", n.Text)
	}
}

// The reading is rebuilt every time a conversation is opened, so the one an
// earlier opening left behind is replaced rather than stacked in front of it.
func TestStripResumeContext_ReplacesTheEarlierReading(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: resumeMessagePrefix + "branch master · 0 changed]\nold"},
		{Role: provider.RoleUser, Content: resumeSummaryPrefix + "\n\nolder still"},
		{Role: provider.RoleUser, Content: "the real first turn"},
	}
	kept := stripResumeContext(msgs)
	if len(kept) != 2 || kept[0].Role != provider.RoleSystem || kept[1].Content != "the real first turn" {
		t.Fatalf("the reading should come off the head of the conversation, got %+v", kept)
	}
	// A conversation that never carried one is handed back untouched.
	plain := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hello"},
	}
	if got := stripResumeContext(plain); len(got) != 2 {
		t.Fatalf("nothing to strip should change nothing, got %d", len(got))
	}
	// A turn that happens to open the way the summary half does is somebody's
	// message, not half a reading: without a survey in front of it there is
	// no block to be the second half of.
	typed := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: resumeSummaryPrefix + "\n\nI wrote this myself"},
	}
	if got := stripResumeContext(typed); len(got) != 2 {
		t.Fatalf("a message is not a reading just because it opens like one, got %d", len(got))
	}
	// Nor is a first turn that happens to start with the same characters: the
	// shape is a whole bracketed opening line, not its first nine.
	opener := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: resumeMessagePrefix + "why does the row say this?"},
	}
	if got := stripResumeContext(opener); len(got) != 2 {
		t.Fatalf("a turn that opens with those characters is still a turn, got %d", len(got))
	}
}

// A branch is a conversation of its own, and its handoff is the one stored
// with it. Without that the next save would stamp the summary of the branch
// just left onto the branch just opened, over the one the fork carried there.
func TestSwitchToBranch_TakesTheBranchesOwnSummary(t *testing.T) {
	db := rewindTestDB(t)
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "the branch's own turn"},
	}
	if err := db.SaveChat("root", msgs); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatBranch("root", "root (branch 1)", msgs); err != nil {
		t.Fatal(err)
	}
	if err := db.SetChatResume("root (branch 1)", storage.ChatResume{Summary: "the branch's own handoff"}); err != nil {
		t.Fatal(err)
	}

	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).WithDB(db)
	m.compactSummary = "a summary of somewhere else entirely"
	if note := m.switchToBranch("root (branch 1)"); !strings.Contains(note, "Switched to branch") {
		t.Fatalf("switch failed: %q", note)
	}
	if m.compactSummary != "the branch's own handoff" {
		t.Fatalf("compactSummary = %q after the switch", m.compactSummary)
	}

	// And a branch with none of its own opens with none rather than with the
	// one the session was carrying.
	m.compactSummary = "a summary of somewhere else entirely"
	if note := m.switchToBranch("root"); !strings.Contains(note, "Switched to branch") {
		t.Fatalf("switch back failed: %q", note)
	}
	if m.compactSummary != "" {
		t.Fatalf("compactSummary = %q, want none carried across", m.compactSummary)
	}
}

func TestResumeConversation_InjectsAheadOfTheTranscriptAndDrawsOneRow(t *testing.T) {
	db := rewindTestDB(t)
	saved := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "old question"},
		{Role: provider.RoleAssistant, Content: "old answer"},
	}
	if err := db.SaveChat("yesterday", saved); err != nil {
		t.Fatal(err)
	}
	if err := db.SetChatResume("yesterday", storage.ChatResume{
		Summary: "the cache work is half done", Head: headWas}); err != nil {
		t.Fatal(err)
	}

	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithDB(db).
		WithResumedMessages("yesterday", saved)

	msgs := m.Messages()
	if len(msgs) != 5 {
		t.Fatalf("expected the survey and the summary in front of three saved messages, got %d", len(msgs))
	}
	if !strings.HasPrefix(msgs[1].Content, resumeMessagePrefix) ||
		!strings.HasPrefix(msgs[2].Content, resumeSummaryPrefix) {
		t.Fatalf("the reading belongs between the prompt and the transcript, got %q / %q", msgs[1].Content, msgs[2].Content)
	}
	if msgs[3].Content != "old question" {
		t.Fatalf("the restored transcript follows it, got %q", msgs[3].Content)
	}
	if !strings.Contains(msgs[1].Content, shortCommit(headWas)) {
		t.Fatalf("the commit the slot was written on is what the move is measured from, got %q", msgs[1].Content)
	}
	// The summary comes back with the conversation, so this sitting's saves
	// keep it rather than writing an empty one over the slot.
	if m.compactSummary != "the cache work is half done" {
		t.Fatalf("compactSummary = %q", m.compactSummary)
	}

	row := m.transcript[len(m.transcript)-1]
	if row.kind != entrySystem || !strings.HasPrefix(row.text, resumeVerb+" ·") {
		t.Fatalf("the last row should be the resumed row, got %+v", row)
	}
	if row.toolResult == "" || !strings.HasPrefix(row.toolResult, resumeMessagePrefix) {
		t.Fatal("the row's body is the text the conversation was given")
	}
	if !expandable(row) {
		t.Fatal("a row with a body is one reading mode can open")
	}
}

// Opening the same conversation twice tells it about the tree once. The
// reading is not part of the conversation, so it is neither saved with it nor
// stacked in front of the one an earlier opening left.
func TestResumeConversation_SecondOpeningReplacesTheFirstsReading(t *testing.T) {
	db := rewindTestDB(t)
	saved := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "old question"},
		{Role: provider.RoleAssistant, Content: "old answer"},
	}
	if err := db.SaveChat("yesterday", saved); err != nil {
		t.Fatal(err)
	}
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithDB(db).
		WithResumedMessages("yesterday", saved)

	// What the session would save is the conversation without the reading.
	stored := stripResumeContext(m.Messages())
	if len(stored) != len(saved) {
		t.Fatalf("the slot keeps the conversation and not the reading, got %d messages", len(stored))
	}

	again := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithDB(db).
		WithResumedMessages("yesterday", m.Messages())
	blocks := 0
	for _, msg := range again.Messages() {
		if strings.HasPrefix(msg.Content, resumeMessagePrefix) {
			blocks++
		}
	}
	if blocks != 1 {
		t.Fatalf("a conversation opened twice carries one reading, got %d", blocks)
	}
}
