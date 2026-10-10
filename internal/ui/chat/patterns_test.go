package chat

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// TestProgram_TheStartScreenOffersWhatRepeatsAndNeverIsWrittenDown is the
// route of the patterns scene (scripts/tui/scenes/patterns): a clean
// checkout whose record holds proposals offers them in the read-only slot,
// choosing the row opens /patterns, enter on the allowlist row opens its card
// with the line it would add, and [N] writes the never for that proposal and
// nothing else.
func TestProgram_TheStartScreenOffersWhatRepeatsAndNeverIsWrittenDown(t *testing.T) {
	info := startFixture()
	info.Recent = StartRecent{}
	info.Project.Dirty = 0
	info.Patterns = 3
	var r proposalRecorder
	m, _ := scriptedSessionWith(Wiring{Start: &info, Patterns: r.patterns(t)}, programTurn{text: "Nothing is sent."})
	tm := runProgramAt(t, m, 110, 40)

	waitForAll(t, tm, "Some things worth doing first", "look at what repeats — 3 patterns", "reads only, then asks")
	programPress(t, tm, "enter")
	waitForAll(t, tm, "/patterns · 3 proposals", "internal/cli/session.go")
	programPress(t, tm, "down", "enter")
	waitForAll(t, tm, "Approve allowlist line", `command_allowlist = ["go test"]`, "[N] never")
	programPress(t, tm, "N")
	waitForText(t, tm, "will not be proposed again")

	finalFrame(t, tm)
	if !slices.Equal(r.declined, []string{"allowlist:go test"}) || len(r.written) != 0 {
		t.Fatalf("declined %v, written %v", r.declined, r.written)
	}
}

// TestGolden_PatternsStart captures the start screen's read-only slot with
// the record's proposals in it: the row above the tour, and what it costs.
func TestGolden_PatternsStart(t *testing.T) {
	captureGolden(t, "patterns-start", "the start screen offering what repeats", goldenWidths, func(width int) []golden.Panel {
		info := startFixture()
		info.Recent = StartRecent{}
		info.Project.Dirty = 0
		info.Patterns = 3
		m := frameModel(t, width, 40)
		m.start = new(info)
		return []golden.Panel{{Label: "a clean checkout with three proposals", View: m.renderHistory()}}
	})
}

// TestGolden_Patterns captures /patterns as it opens — one proposal of each
// kind, the pointer on the first — and the allowlist card its second row
// opens: the one line the write adds, shown as the change to the file, and
// the three answers with what each leaves behind.
func TestGolden_Patterns(t *testing.T) {
	captureGolden(t, "patterns", "the patterns screen and the allowlist card", goldenWidths, func(width int) []golden.Panel {
		var r proposalRecorder
		var decisions []string
		m := sendText(t, patternsModel(t, width, &r, &decisions), patternsCommandName)
		list := strings.Join(overlayFor(stateProposals).lines(m, m.contentWidth(), 24), "\n")
		card := openProposal(t, patternsModel(t, width, &r, &decisions), 1)
		return []golden.Panel{
			{Label: "the list as it opens", View: list},
			{Label: "the allowlist card · nothing written yet", View: card.panelView()},
		}
	})
}

// proposalRecorder is the host behind /patterns in a test: what it was asked
// to write, to decline and to word, and the memory it was asked to keep.
type proposalRecorder struct {
	written  []string
	declined []string
	worded   []string
	saved    []string
	// word is the reading's answer; nil is a failed reading.
	word func(Proposal) Proposal
}

// fixtureProposals is one proposal of each kind, in the order the host lists
// them.
func fixtureProposals() []Proposal {
	return []Proposal{
		{Kind: storage.ProposalMemory, Pattern: "internal/cli/session.go", What: "read", Sessions: 4,
			Key: "read in session after session: internal/cli/session.go", MemoryKind: "convention",
			Text: "internal/cli/session.go is read in most sessions here.", Facts: "internal/cli/session.go read in 4 sessions",
			Lines: []string{"read_file internal/cli/session.go"}},
		{Kind: storage.ProposalAllowlist, Pattern: "go test", What: "asked about and allowed every time", Sessions: 3,
			Key: "go test", Entry: "go test"},
		{Kind: storage.ProposalSkill, Pattern: "go vet → go build → go test", What: "run in this order", Sessions: 3,
			Key: "go vet → go build → go test",
			Skill: skill.Draft{Name: "go-vet-go-build-go-test", Description: "Run go vet, go build and go test in order.",
				Steps: []string{"go vet ./...", "go build ./...", "go test ./..."}}},
	}
}

func (r *proposalRecorder) patterns(t *testing.T) Patterns {
	t.Helper()
	return Patterns{
		Read: func() ([]Proposal, error) { return fixtureProposals(), nil },
		Word: func(_ context.Context, p Proposal) (Proposal, bool) {
			r.worded = append(r.worded, p.Key)
			if r.word == nil {
				return p, false
			}
			return r.word(p), true
		},
		Preview: func(p Proposal) (string, string, string, error) {
			if p.Kind == storage.ProposalSkill {
				return skill.DraftPath(".", p.Skill.Name), "", p.Skill.Render(), nil
			}
			return ".shhh/config.toml", "[behavior]\ndefault_mode = \"manual\"\n",
				"[behavior]\ndefault_mode = \"manual\"\ncommand_allowlist = [\"go test\"]\n", nil
		},
		Write: func(p Proposal) (string, error) {
			r.written = append(r.written, p.Kind+":"+p.Key)
			return ".shhh/config.toml", nil
		},
		Decline: func(p Proposal) error {
			r.declined = append(r.declined, p.Kind+":"+p.Key)
			return nil
		},
	}
}

// patternsModel is a session with /patterns and memory wired to r, and every
// decision it records collected in decisions.
func patternsModel(t *testing.T, width int, r *proposalRecorder, decisions *[]string) Model {
	t.Helper()
	m := frameModel(t, width, 40)
	m.patterns.cfg, m.patterns.wording = r.patterns(t), -1
	m.wiring.Memory = Memory{
		ProjectScope: "/work/app",
		Save: func(scope, kind, text string) (string, error) {
			r.saved = append(r.saved, scope+"|"+kind+"|"+text)
			return "remembered m7 · " + kind + "\n" + text, nil
		},
	}
	m.wiring.Observer = observe.Observer{Decision: func(_ observe.Pos, decision, reason string) {
		*decisions = append(*decisions, decision+"/"+reason)
	}}
	return m
}

// openProposal opens /patterns and takes the row at i, running a reading
// where the row asks for one.
func openProposal(t *testing.T, m Model, i int) Model {
	t.Helper()
	m = sendText(t, m, patternsCommandName)
	if m.state != stateProposals {
		t.Fatalf("/patterns opened state %d", m.state)
	}
	for range i {
		m = press(t, m, "j")
	}
	next, cmd := pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if next.state == stateProposals && cmd != nil {
		updated, _ := next.Update(cmd())
		next = updated.(Model)
	}
	return next
}

// Each kind opens the card it is answered on, and none of them writes
// anything by opening: the allowlist card shows the one line it would add
// with its three answers; the memory card is the memory card's three rows.
func TestPatterns_EnterOpensTheCardAndWritesNothing(t *testing.T) {
	var r proposalRecorder
	var decisions []string
	m := openProposal(t, patternsModel(t, 110, &r, &decisions), 1)
	if m.state != stateProposal {
		t.Fatalf("state = %d, want the proposal card", m.state)
	}
	view := ansi.Strip(m.panelView())
	for _, want := range []string{"Approve allowlist line", `command_allowlist = ["go test"]`,
		"[y] write it", "[n] not now", "[N] never"} {
		if !strings.Contains(view, want) {
			t.Errorf("the card lacks %q:\n%s", want, view)
		}
	}
	if len(r.worded) != 0 {
		t.Errorf("an allowlist line was worded: %v", r.worded)
	}

	m = openProposal(t, patternsModel(t, 110, &r, &decisions), 0)
	view = ansi.Strip(m.panelView())
	for _, want := range []string{"Save (project)", "Save (global)", "Don't save", "convention memory"} {
		if !strings.Contains(view, want) {
			t.Errorf("the memory card lacks %q:\n%s", want, view)
		}
	}
	if len(r.written)+len(r.declined)+len(r.saved)+len(decisions) != 0 {
		t.Fatalf("opening a card wrote something: %+v %v", r, decisions)
	}
}

// Not now and esc leave the proposal for next time and write nothing; never
// writes the durable decline for exactly the proposal on the card, under its
// own kind; yes writes it and records the person's allow.
func TestPatterns_EachAnswerWritesOnlyWhatItSays(t *testing.T) {
	for _, tc := range []struct {
		name              string
		key               string
		written, declined []string
		decision          []string
	}{
		{"not now", "n", nil, nil, nil},
		{"esc", "esc", nil, nil, nil},
		{"never", "N", nil, []string{"allowlist:go test"}, []string{"deny/user"}},
		{"write it", "y", []string{"allowlist:go test"}, nil, []string{"allow/user"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r proposalRecorder
			var decisions []string
			m := openProposal(t, patternsModel(t, 110, &r, &decisions), 1)
			m = press(t, m, tc.key)
			if m.state == stateProposal {
				t.Fatal("the card is still up")
			}
			if !slices.Equal(r.written, tc.written) || !slices.Equal(r.declined, tc.declined) || !slices.Equal(decisions, tc.decision) {
				t.Fatalf("written %v, declined %v, decisions %v", r.written, r.declined, decisions)
			}
		})
	}
}

// The enter that opened a card cannot also answer it: a double tap on a row
// of /patterns leaves the allowlist and skill cards up with nothing written,
// and the card says which key does write. The memory card keeps its rows.
func TestPatterns_ADoubleTappedEnterWritesNothing(t *testing.T) {
	for _, row := range []int{1, 2} {
		var r proposalRecorder
		var decisions []string
		m := openProposal(t, patternsModel(t, 110, &r, &decisions), row)
		m = press(t, m, "enter")
		if m.state != stateProposal {
			t.Fatalf("row %d: the second enter answered the card", row)
		}
		if len(r.written)+len(r.declined)+len(decisions) != 0 {
			t.Fatalf("row %d: enter wrote something: %+v %v", row, r, decisions)
		}
		view := ansi.Strip(m.panelView())
		for _, want := range []string{"[y] write it", "enter opened this card and writes nothing"} {
			if !strings.Contains(view, want) {
				t.Errorf("row %d: the card lacks %q:\n%s", row, want, view)
			}
		}
		m = press(t, m, "y")
		if m.state == stateProposal || len(r.written) != 1 {
			t.Fatalf("row %d: y must write: state %d, written %v", row, m.state, r.written)
		}
	}

	var r proposalRecorder
	var decisions []string
	m := openProposal(t, patternsModel(t, 110, &r, &decisions), 0)
	press(t, m, "enter")
	if len(r.saved) != 1 {
		t.Fatalf("the memory card's own enter must keep working: %v", r.saved)
	}
}

// A memory is worded by the reading and saved as worded, with the person's
// scope; Don't save is the never, under the memory kind and the pattern's
// key rather than the words; a failed reading leaves the code's words.
func TestPatterns_AMemoryIsWordedThenAnsweredOnTheMemoryCard(t *testing.T) {
	r := proposalRecorder{word: func(p Proposal) Proposal {
		p.Text = "internal/cli/session.go assembles every chat session."
		return p
	}}
	var decisions []string
	m := openProposal(t, patternsModel(t, 110, &r, &decisions), 0)
	if !strings.Contains(ansi.Strip(m.panelView()), "assembles every chat session") {
		t.Fatalf("the card does not carry the worded memory:\n%s", ansi.Strip(m.panelView()))
	}
	press(t, m, "enter")
	if len(r.saved) != 1 || r.saved[0] != "/work/app|convention|internal/cli/session.go assembles every chat session." ||
		!slices.Equal(decisions, []string{"allow/user"}) {
		t.Fatalf("saved %v, decisions %v", r.saved, decisions)
	}

	r = proposalRecorder{}
	decisions = nil
	m = openProposal(t, patternsModel(t, 110, &r, &decisions), 0)
	if !strings.Contains(ansi.Strip(m.panelView()), "is read in most sessions here") {
		t.Fatal("a failed reading should leave the code's words")
	}
	m = press(t, m, "j")
	m = press(t, m, "j")
	press(t, m, "enter")
	if len(r.saved) != 0 || !slices.Equal(r.declined, []string{"memory:read in session after session: internal/cli/session.go"}) {
		t.Fatalf("saved %v, declined %v", r.saved, r.declined)
	}

	r = proposalRecorder{}
	m = openProposal(t, patternsModel(t, 110, &r, &decisions), 0)
	m = press(t, m, "esc")
	if len(r.saved)+len(r.declined) != 0 || m.state == stateProposal {
		t.Fatalf("esc on the memory card: saved %v, declined %v", r.saved, r.declined)
	}
}

// A reading that lands after the list was left opens nothing.
func TestPatterns_AReadingThatLandsAfterTheListClosedIsDropped(t *testing.T) {
	var r proposalRecorder
	var decisions []string
	m := sendText(t, patternsModel(t, 110, &r, &decisions), patternsCommandName)
	next, cmd := pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || next.patterns.wording != 0 {
		t.Fatal("enter on a memory row should ask for its words")
	}
	msg := cmd()
	next = press(t, next, "esc")
	updated, _ := next.Update(msg)
	if updated.(Model).state == stateProposal {
		t.Fatal("a card opened on a list that was closed")
	}
	// Nor on the same row of a list opened since, which is waiting on a
	// reading of its own.
	again := sendText(t, updated.(Model), patternsCommandName)
	again, cmd = pressKey(t, again, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || again.patterns.wording != 0 {
		t.Fatal("the reopened list did not ask for its row's words")
	}
	if late, _ := again.Update(msg); late.(Model).state == stateProposal {
		t.Fatal("the first list's reading opened a card on the second")
	}
}
