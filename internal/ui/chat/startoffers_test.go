package chat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
)

// twoWritten is a reading that writes two offers, neither about anything a
// deterministic row names.
const twoWritten = `{"offers":[{"title":"trace how a request reaches the cache","prompt":"Trace how a request reaches the cache and say where it is read."},{"title":"list what the gate does not check","prompt":"Read the quality gate and say what it leaves unchecked."}]}`

const (
	writtenTrace = "trace how a request reaches the cache"
	writtenGate  = "list what the gate does not check"
)

// startReadingGather is what the CLI's background read would hand the reading:
// names and subjects only.
func startReadingGather(context.Context) agent.StartOffersRequest {
	return agent.StartOffersRequest{
		Dirty:   []string{"cache.go", "cache_test.go"},
		Commits: []string{"name the lifetime", "start the cache package"},
		Ready:   []string{"cache-ttl: Give the cache a lifetime"},
	}
}

// startReadingModel is a sized session on its start screen with the reading wired
// on p, the switch at on, recording every start-offer signal it files.
func startReadingModel(t *testing.T, info StartInfo, p provider.Provider, on bool) (Model, *[]string) {
	t.Helper()
	var signals []string
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, multiTokenStream("hi there"), Wiring{
		Observer: observe.Observer{Signal: func(_ observe.Pos, code, reason string) {
			if code == observe.SignalStartOffer {
				signals = append(signals, reason)
			}
		}},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = updated.(Model)
	m.start = new(info)
	m.suggest.writer, m.suggest.on = nil, on
	m.startOffers.writer, m.startOffers.gather = agent.NewStartOfferer(p, agent.StartOffersConfig{Model: "fast"}), startReadingGather
	return m, &signals
}

// startReadingInfo is a clean checkout with no session to resume: two read-only
// rows, both tours, and the gate's row.
func startReadingInfo() StartInfo {
	info := startFixture()
	info.Recent = StartRecent{}
	info.Project.Dirty = 0
	info.Project.Instruction = project.InstructionCheck{File: "AGENTS.md", Modified: startNow,
		Block: "# Agents\n\nRun make test before you finish."}
	return info
}

// askStartReading asks for the reading and runs it, returning the message it lands
// with.
func askStartReading(t *testing.T, m *Model) startOffersDoneMsg {
	t.Helper()
	cmd := m.startOffersCmd()
	if cmd == nil {
		t.Fatal("an open start screen should ask for its reading")
	}
	msg, ok := cmd().(startOffersDoneMsg)
	if !ok {
		t.Fatal("the reading did not come back as its own message")
	}
	return msg
}

func landStartReading(t *testing.T, m Model, msg startOffersDoneMsg) Model {
	t.Helper()
	updated, _ := m.Update(msg)
	return updated.(Model)
}

// The reading is asked once, at open, over the instruction block as the
// prompt got it, the screen's fact and gate lines, and the names the CLI's
// background read gathered — and nothing is asked with the switch off.
func TestStart_AReadingIsAskedOnceAtOpen(t *testing.T) {
	p := &suggestProvider{line: twoWritten}
	m, _ := startReadingModel(t, startReadingInfo(), p, true)
	msg := askStartReading(t, &m)
	if p.calls != 1 {
		t.Fatalf("calls = %d, want one", p.calls)
	}
	if m.startOffersCmd() != nil {
		t.Fatal("a session is read once, at its open")
	}
	if p.opts.Model != "fast" || p.opts.ToolChoice != provider.ToolChoiceNone || len(p.opts.Tools) != 0 {
		t.Errorf("opts = %+v", p.opts)
	}
	evidence := p.msgs[1].Content
	for _, want := range []string{
		`"checkout_facts":"go 1.24 · clean tree · 41 packages"`,
		`"gate":"default — vet, test · runs without asking"`,
		`Run make test before you finish.`,
		`"changed_files":["cache.go","cache_test.go"]`,
		`"recent_commits":["name the lifetime","start the cache package"]`,
		`"ready_items":["cache-ttl: Give the cache a lifetime"]`,
	} {
		if !strings.Contains(evidence, want) {
			t.Errorf("the evidence lacks %s:\n%s", want, evidence)
		}
	}
	if len(msg.verdict.Offers) != 2 || msg.verdict.Offers[0].Title != writtenTrace {
		t.Fatalf("offers = %+v", msg.verdict.Offers)
	}

	// Off is no request at all.
	off := &suggestProvider{line: twoWritten}
	m, _ = startReadingModel(t, startReadingInfo(), off, false)
	if m.startOffersCmd() != nil || off.calls != 0 {
		t.Fatal("with suggestions off the start screen asked for a reading")
	}
	// Nor does a session with no screen, or no model to ask.
	m, _ = startReadingModel(t, startReadingInfo(), nil, true)
	if m.startOffersCmd() != nil {
		t.Fatal("a reading with no provider was asked for")
	}
}

// A landed reading takes the read-only rows only, at the slot's price: the
// resume and the approval rows stay where they were, and a deterministic row
// stays unless a written offer names the same thing.
func TestStart_AReadingTakesOnlyTheReadOnlyRows(t *testing.T) {
	// Two tours: both are written over, and the gate's row stays last.
	m, _ := startReadingModel(t, startReadingInfo(), &suggestProvider{line: twoWritten}, true)
	m = landStartReading(t, m, askStartReading(t, &m))
	titles, actions := startRows(m)
	if len(titles) != 3 || titles[0] != writtenTrace || titles[1] != writtenGate ||
		titles[2] != "run the default quality gate and triage what fails" {
		t.Fatalf("offers = %q", titles)
	}
	view := startText(m)
	if !strings.Contains(view, writtenTrace+" — reads only, no writes") {
		t.Fatalf("a written row should carry the slot's price:\n%s", view)
	}
	if !strings.Contains(actions[0], "Trace how a request reaches the cache") || !strings.Contains(actions[0], "Change nothing") {
		t.Fatalf("action = %q, want the written prompt and the slot's price", actions[0])
	}

	// A session to resume keeps the first row, and the one read-only row
	// left is the only one a reading takes.
	info := startReadingInfo()
	info.Recent = startFixture().Recent
	m, _ = startReadingModel(t, info, &suggestProvider{line: twoWritten}, true)
	m = landStartReading(t, m, askStartReading(t, &m))
	titles, _ = startRows(m)
	if len(titles) != 3 || !strings.HasPrefix(titles[0], "pick up ") || titles[1] != writtenTrace ||
		titles[2] != "run the default quality gate and triage what fails" {
		t.Fatalf("resume offers = %q", titles)
	}

	// The ready item is a fact the checkout states: a written offer about
	// something else leaves it and takes the tour beside it.
	info = startReadingInfo()
	info.Ready = StartReady{Present: true, Slug: "cache-ttl", Title: "Give the cache a lifetime", Noun: "story"}
	m, _ = startReadingModel(t, info, &suggestProvider{line: twoWritten}, true)
	m = landStartReading(t, m, askStartReading(t, &m))
	titles, _ = startRows(m)
	if titles[0] != "read cache-ttl and say what it would take" || titles[1] != writtenTrace {
		t.Fatalf("ready offers = %q, want the item kept and the tour written over", titles)
	}

	// One that names the item takes the item's row.
	named := `{"offers":[{"title":"check what cache-ttl still needs","prompt":"Read cache-ttl and the cache package and say what is left."}]}`
	m, _ = startReadingModel(t, info, &suggestProvider{line: named}, true)
	m = landStartReading(t, m, askStartReading(t, &m))
	titles, _ = startRows(m)
	if titles[0] != "check what cache-ttl still needs" || titles[1] != "walk me through what this project does" {
		t.Fatalf("named offers = %q, want the item's row written over and the tour kept", titles)
	}

	// A name is a whole word: a branch called dev is not named by
	// "development", and is by "the dev branch".
	if writtenNames(agent.StartOffer{Title: "read the development notes", Prompt: "Read them."}, "dev") ||
		!writtenNames(agent.StartOffer{Title: "review the dev branch", Prompt: "Read it."}, "dev") ||
		!writtenNames(agent.StartOffer{Title: "assess it", Prompt: "Read AGENTS.md."}, "AGENTS.md") {
		t.Fatal("a written offer should name a row's thing by the whole word only")
	}

	// A dirty tree and a branch with no tour beside them keep their rows.
	info = startReadingInfo()
	info.Project.Dirty = 2
	info.Branch = StartBranch{Ahead: 1}
	m, _ = startReadingModel(t, info, &suggestProvider{line: twoWritten}, true)
	m = landStartReading(t, m, askStartReading(t, &m))
	titles, _ = startRows(m)
	if titles[0] != "explain what changed in the working tree" ||
		titles[1] != "review what this branch changes before it goes up" {
		t.Fatalf("dirty offers = %q, want the facts kept", titles)
	}
	if n := countOffers(startText(m)); n != 3 {
		t.Fatalf("offers = %d, want 3", n)
	}

	// /ui suggest off takes the written rows back off with the switch.
	m, _ = startReadingModel(t, startReadingInfo(), &suggestProvider{line: twoWritten}, true)
	m = landStartReading(t, m, askStartReading(t, &m))
	m.suggestCommand([]string{"/ui", "suggest", "off"})
	if titles, _ = startRows(m); titles[0] != "walk me through what this project does" {
		t.Fatalf("offers with the switch off = %q", titles)
	}
}

// The suggester's drop rules: a reading that lands after a key, a chosen row
// or a turn is dropped and filed; a failed one changes nothing and files
// nothing.
func TestStart_ALateReadingChangesNothing(t *testing.T) {
	const tour = "walk me through what this project does"

	// A key pressed while it was out, even one whose draft was cleared again.
	m, signals := startReadingModel(t, startReadingInfo(), &suggestProvider{line: twoWritten}, true)
	msg := askStartReading(t, &m)
	for _, k := range []tea.KeyPressMsg{{Code: 'x', Text: "x"}, {Code: tea.KeyBackspace}} {
		updated, _ := m.Update(k)
		m = updated.(Model)
	}
	m = landStartReading(t, m, msg)
	if titles, _ := startRows(m); titles[0] != tour {
		t.Fatalf("a reading landed over a key: %q", titles)
	}
	if len(*signals) != 1 || (*signals)[0] != observe.StartOfferDropped {
		t.Fatalf("signals = %q, want one drop filed", *signals)
	}

	// A row chosen, which starts a turn: the screen is gone and stays gone.
	m, signals = startReadingModel(t, startReadingInfo(), &suggestProvider{line: twoWritten}, true)
	msg = askStartReading(t, &m)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = landStartReading(t, updated.(Model), msg)
	if m.startScreenShowing() || len(m.startOffers.written) != 0 {
		t.Fatal("a reading landed after a row was chosen")
	}
	if len(*signals) != 1 || (*signals)[0] != observe.StartOfferDropped {
		t.Fatalf("signals = %q, want one drop filed", *signals)
	}
	// And nothing the model reads carries the reading's words: the turn is
	// the row's line and nothing else.
	for _, mm := range m.agent.Messages() {
		if strings.Contains(mm.Content, "Trace how a request") {
			t.Fatalf("the reading reached the conversation: %+v", mm)
		}
	}

	// A failed reading — an answer that is not the JSON asked for — draws
	// nothing, says nothing and files nothing.
	m, signals = startReadingModel(t, startReadingInfo(), &suggestProvider{line: "I would look at the cache."}, true)
	before := startText(m)
	m = landStartReading(t, m, askStartReading(t, &m))
	if after := startText(m); after != before {
		t.Fatalf("a failed reading changed the screen:\n%s", after)
	}
	if len(*signals) != 0 {
		t.Fatalf("signals = %q, want none for a failed reading", *signals)
	}
}

// A written row taken is the person's sent line and nothing else: the line
// is what reaches the conversation, and the rows it did not choose do not.
func TestStart_ATakenWrittenOfferIsTheSentLine(t *testing.T) {
	m, _ := startReadingModel(t, startReadingInfo(), &suggestProvider{line: twoWritten}, true)
	m = landStartReading(t, m, askStartReading(t, &m))
	_, actions := startRows(m)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	var users []string
	for _, mm := range m.agent.Messages() {
		if strings.Contains(mm.Content, writtenGate) || strings.Contains(mm.Content, "leaves unchecked") {
			t.Fatalf("an offer not chosen reached the conversation: %+v", mm)
		}
		if mm.Role == provider.RoleUser {
			users = append(users, mm.Content)
		}
	}
	if len(users) != 1 || !strings.Contains(users[0], actions[0]) {
		t.Fatalf("user messages = %q, want the chosen row's line %q", users, actions[0])
	}
}
