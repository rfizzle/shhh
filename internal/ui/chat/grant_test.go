package chat

// The grants the always-allow key offers, and the one that ends with the turn
// (docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends).

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// grantVia is what a grant costs at the card now: the key, a walk to the row
// that says what the caller wants granted and for how long, and enter. It
// fails rather than falling through where the row is not offered, because a
// test that silently granted the row above the one it named would be testing
// the wrong grant.
func grantVia(t *testing.T, m Model, label string) (Model, tea.Cmd) {
	t.Helper()
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = updated.(Model)
	if m.grantChoice == nil {
		t.Fatalf("the always-allow key should open the grant list, state %d", m.state)
	}
	for i, opt := range m.grantChoice.options {
		if opt.Label != label {
			continue
		}
		for m.grantChoice.focus < i {
			updated, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
			m = updated.(Model)
		}
		updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		return updated.(Model), cmd
	}
	t.Fatalf("the grant list offers no %q row: %+v", label, m.grantChoice.options)
	return m, nil
}

// grantCardModel is a session holding one gated command whose card has the
// keyboard, with a runner that records what ran.
func grantCardModel(t *testing.T, ran *[]string, commands ...string) Model {
	t.Helper()
	m := execModel(t, ran)
	calls := make([]provider.ToolCall, len(commands))
	for i, c := range commands {
		calls[i] = provider.ToolCall{
			ID:        fmt.Sprintf("call_%d", i),
			Name:      "execute_command",
			Arguments: fmt.Sprintf(`{"command":%q}`, c),
		}
	}
	updated, _ := m.Update(toolCallsMsg{calls: calls})
	return handover(t, updated.(Model))
}

// Every row grants what its own field said it would and nothing wider: the
// two lengths over the prefix the card printed, and the narrow row over the
// line exactly as it stands.
func TestGrantList_EachRowGrantsWhatItSaid(t *testing.T) {
	t.Run("this turn only", func(t *testing.T) {
		var ran []string
		m, _ := grantVia(t, grantCardModel(t, &ran, "go build ./one"), "this turn only")
		if got := m.policy.turn.Commands; len(got) != 1 || got[0] != "go build" {
			t.Fatalf("the turn's grant is %v; want the prefix the row printed", got)
		}
		if len(m.policy.commands) != 0 {
			t.Fatalf("a turn grant must not outlive its turn as a session grant: %v", m.policy.commands)
		}
	})

	t.Run("this session", func(t *testing.T) {
		var ran []string
		m, _ := grantVia(t, grantCardModel(t, &ran, "go build ./one"), "this session")
		if got := m.policy.commands; len(got) != 1 || got[0] != "go build" {
			t.Fatalf("the session grant is %v; want the prefix the row printed", got)
		}
		if m.policy.turn.Any() {
			t.Fatalf("the session row granted the turn something too: %+v", m.policy.turn)
		}
	})

	t.Run("this exact line", func(t *testing.T) {
		var ran []string
		m, _ := grantVia(t, grantCardModel(t, &ran, "go build ./one"), "this exact line")
		if got := m.policy.exactCommands; len(got) != 1 || got[0] != "go build ./one" {
			t.Fatalf("the narrow grant is %v; want the line as it stands", got)
		}
		if len(m.policy.commands) != 0 {
			t.Fatalf("the narrow row granted the prefix as well: %v", m.policy.commands)
		}
		// The width is the whole of what that row buys: a line that merely
		// starts with the granted one is a card again.
		m.state = stateStreaming
		updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
			{ID: "call_wider", Name: "execute_command", Arguments: `{"command":"go build ./one ./two"}`},
		}})
		if next := updated.(Model); next.state != stateConfirmRun {
			t.Fatalf("a wider line than the granted one should still ask, got state %d", next.state)
		}
	})
}

// The turn grant answers the next matching call in the turn that made it, and
// the first call of the next turn is a card again — which is the whole of
// what "this turn only" promises.
func TestGrantList_TheTurnGrantEndsWithTheTurn(t *testing.T) {
	var ran []string
	m := grantCardModel(t, &ran, "go build ./one", "go build ./two")
	m, cmd := grantVia(t, m, "this turn only")
	updated, next := m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("the second command should run under the turn's grant, got state %d", m.state)
	}
	updated, _ = m.Update(driveCmdDone(t, next))
	m = updated.(Model)
	if len(ran) != 2 {
		t.Fatalf("both commands should have run, got %v", ran)
	}

	// The turn closes, and the grant closes with it. The close says nothing
	// about it: the row the reader took already did.
	m.turnOpen = true
	before := len(m.transcript)
	m.appendTurnClose()
	if m.policy.turn.Any() {
		t.Fatalf("the grant outlived its turn: %+v", m.policy.turn)
	}
	for _, e := range m.transcript[before:] {
		if strings.Contains(strings.ToLower(e.text), "grant") {
			t.Fatalf("the expiry announced itself in the close: %q", e.text)
		}
	}

	m.state = stateStreaming
	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_next", Name: "execute_command", Arguments: `{"command":"go build ./three"}`},
	}})
	if next := updated.(Model); next.state != stateConfirmRun {
		t.Fatalf("the next turn's first call should ask again, got state %d", next.state)
	}
}

// The listing names a turn grant with the end the row it came from printed,
// and revoke takes it back with the rest — a grant left standing by a revoke
// because it was going to expire anyway is a grant the reader could not take
// back.
func TestGrantList_ListedWithItsEndAndRevokedWithTheRest(t *testing.T) {
	var ran []string
	m, _ := grantVia(t, grantCardModel(t, &ran, "go build ./one"), "this turn only")

	listing := m.grantStatus()
	for _, want := range []string{`"go build"`, endsWithTurn} {
		if !strings.Contains(listing, want) {
			t.Fatalf("/permissions grants does not say %q:\n%s", want, listing)
		}
	}
	if got := m.policyLabel(); !strings.Contains(got, "1 cmd") {
		t.Errorf("the status line does not count a standing turn grant: %q", got)
	}

	if said := m.revokeCommand(nil); !strings.Contains(said, `"go build"`) {
		t.Fatalf("revoke should name the turn grant it took back, said %q", said)
	}
	if m.policy.turn.Any() {
		t.Fatalf("revoke left the turn grant standing: %+v", m.policy.turn)
	}
}

// A child is under the grant its parent made, for as long as its parent is,
// and the fetcher is told the same thing: it is the only place a redirect off
// a granted host is visible, so it has to lose the host when the turn does.
func TestGrantList_ATurnGrantReachesAChildAndIsTakenBack(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) { return "page text", nil }
	var pushed []string
	m := gatedModel(t, executor, fetchPreviews()).
		WithHostGrants(func(hosts []string) { pushed = hosts })

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		fetchCall("call_1", "https://docs.python.org/3/library/json.html"),
	}})
	m, _ = grantVia(t, handover(t, updated.(Model)), "this turn only")

	if len(pushed) != 1 || pushed[0] != "docs.python.org" {
		t.Fatalf("the fetcher was told %v; want the turn's granted host", pushed)
	}
	if !agent.HostMatches(m.liveGrants().Hosts, "docs.python.org") {
		t.Fatal("a child should be under the grant while the turn stands")
	}

	m.turnOpen = true
	m.appendTurnClose()
	if len(pushed) != 0 {
		t.Fatalf("the fetcher kept the host past the turn: %v", pushed)
	}
	if m.liveGrants().Any() {
		t.Fatalf("a child kept the grant past the turn: %+v", m.liveGrants())
	}
}

// A flagged command offers no grant of any length. "Only for a minute" is
// still blanket, and the card says why the key is missing rather than
// dropping it silently.
func TestGrantList_AFlaggedCardOffersNoGrant(t *testing.T) {
	var ran []string
	m := grantCardModel(t, &ran, "rm -rf /tmp/x")
	if card := m.approvalCard(); card.AllowAlways {
		t.Fatal("a safety-flagged command must not offer a grant of any length")
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if next := updated.(Model); next.grantChoice != nil {
		t.Fatal("the key opened the grant list on a flagged card")
	}
	if !strings.Contains(m.View().Content, "never pre-approved") {
		t.Fatalf("the card should say why the key is absent:\n%s", m.View().Content)
	}
}

// Esc grants nothing and leaves the decision exactly where it was, and while
// the list holds the keyboard the card's own answers are inert — the reading
// every surface that holds the keyboard under this card already makes.
func TestGrantList_EscGrantsNothingAndTheCardsKeysAreInert(t *testing.T) {
	for _, key := range []string{"y", "n", "Y", "N", "d", "t", "e", "1"} {
		t.Run(key, func(t *testing.T) {
			var ran []string
			m := grantCardModel(t, &ran, "go build ./one")
			updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
			m = updated.(Model)
			before := m.grantChoice.focus
			m = press(t, m, key)
			if m.grantChoice == nil {
				t.Fatalf("%q closed the grant list", key)
			}
			if m.grantChoice.focus != before {
				t.Fatalf("%q moved the pointer", key)
			}
			if m.pendingApproval == nil || m.state != stateConfirmRun {
				t.Fatalf("%q answered the decision the list was opened over", key)
			}
			if len(ran) != 0 {
				t.Fatalf("%q ran something: %v", key, ran)
			}
		})
	}

	var ran []string
	m := grantCardModel(t, &ran, "go build ./one")
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = press(t, updated.(Model), "esc")
	if m.grantChoice != nil {
		t.Fatal("esc should close the grant list")
	}
	if m.policy.turn.Any() || len(m.policy.commands) != 0 {
		t.Fatalf("esc granted something: %+v / %v", m.policy.turn, m.policy.commands)
	}
	if m.pendingApproval == nil || m.state != stateConfirmRun {
		t.Fatal("esc must leave the decision waiting")
	}
	// And the card's keys are keys again.
	if m = press(t, m, "n"); m.pendingApproval != nil {
		t.Fatal("the card's own answers should be live once the list is closed")
	}
}

// Each length and each width is recorded under a code of its own: a rate that
// could not tell a build loop's turn grant from a standing one would answer
// neither question about how the offer is used.
func TestGrantList_EachGrantHasItsOwnReasonCode(t *testing.T) {
	for _, tc := range []struct{ row, want string }{
		{"this turn only", observe.ReasonUserTurn},
		{"this session", observe.ReasonUserAlways},
		{"this exact line", observe.ReasonUserExact},
	} {
		t.Run(tc.row, func(t *testing.T) {
			var ran []string
			var reasons []string
			m := grantCardModel(t, &ran, "go build ./one").WithObserver(observe.Observer{
				Decision: func(_ observe.Pos, decision, reason string) {
					if decision == observe.DecisionAllow {
						reasons = append(reasons, reason)
					}
				},
			})
			grantVia(t, m, tc.row)
			if len(reasons) != 1 || reasons[0] != tc.want {
				t.Fatalf("the record says %v; want one %q", reasons, tc.want)
			}
		})
	}
}

// An edit card offers the same list with its own widths — the directory the
// card printed, and the one file it is about.
func TestGrantList_AnEditCardOffersTheDirectoryAndTheFile(t *testing.T) {
	m := gatedModel(t, nil, nil)
	dir := t.TempDir()
	first := filepath.Join(dir, "a.txt")
	second := filepath.Join(dir, "b.txt")
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":"one\n"}`, first)},
		{ID: "call_2", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":"two\n"}`, second)},
	}})
	m = handover(t, updated.(Model))

	granted, cmd := grantVia(t, m, "this file only")
	if got := granted.policy.editPaths; len(got) != 1 || got[0] != first {
		t.Fatalf("the narrow row granted %v; want the file the card was about", got)
	}
	if len(granted.policy.editDirs) != 0 {
		t.Fatalf("the narrow row granted the directory as well: %v", granted.policy.editDirs)
	}
	granted = drainApproved(t, granted, cmd)
	if granted.state != stateConfirmRun {
		t.Fatalf("the sibling file should still ask, got state %d", granted.state)
	}

	turnGranted, _ := grantVia(t, m, "this turn only")
	if got := turnGranted.policy.turn.EditDirs; len(got) != 1 || got[0] != dir {
		t.Fatalf("the turn row granted %v; want the directory the card printed", got)
	}
}

// A fetch card's list is two rows and not three, and the card says nothing
// about a third: a host grant is already exact, so there is no narrower width
// to offer under it.
// Taking a row twice records one grant, and taking a wider one after a
// narrower one at the same length records nothing new — but a *longer* grant
// over something the turn already covers is still recorded, because that is
// the length the reader chose and it outlives the one already there.
func TestGrantList_ARedundantGrantIsNotRecordedTwice(t *testing.T) {
	var ran []string
	m := grantCardModel(t, &ran, "go build ./one", "go build ./two", "go build ./three")

	m, cmd := grantVia(t, m, "this turn only")
	if got := m.policy.turn.Commands; len(got) != 1 {
		t.Fatalf("the first grant is %v; want one prefix", got)
	}
	// The turn now covers the prefix, so the narrow row under it adds
	// nothing: the reader is choosing a width they already have.
	m.policy.turn.ExactCommands = nil
	m.grantCommand("go build ./one", grantOffer{length: forThisTurn, exact: true})
	if got := m.policy.turn.ExactCommands; len(got) != 0 {
		t.Fatalf("a line the turn's prefix already covers was recorded again: %v", got)
	}
	// The session row is a different length and is recorded whatever the
	// turn holds.
	m.grantCommand("go build ./one", grantOffer{length: forThisSession})
	if got := m.policy.commands; len(got) != 1 || got[0] != "go build" {
		t.Fatalf("the longer grant was dropped because a turn grant covered it: %v", got)
	}
	_ = cmd
}

// A grant of any length is still answered behind the deny list. A shorter
// grant is not a wider one, and the list is the reader's standing answer:
// nothing pressed at a card reaches past it.
func TestGrantList_NoLengthOutranksTheDenyList(t *testing.T) {
	var ran []string
	m := grantCardModel(t, &ran, "go build ./one").WithCommandDenylist([]string{"go build"})
	m, _ = grantVia(t, m, "this turn only")
	if !m.policy.turn.Any() {
		t.Fatal("the row should have recorded the grant it printed")
	}

	m.state = stateStreaming
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_denied", Name: "execute_command", Arguments: `{"command":"go build ./two"}`},
	}})
	m = updated.(Model)
	if m.state == stateConfirmRun {
		t.Fatal("a refused command should not draw a card at all")
	}
	if len(ran) != 0 {
		t.Fatalf("the turn grant ran a command the deny list refuses: %v", ran)
	}
	last := m.Messages()[len(m.Messages())-1]
	if !strings.Contains(last.Content, "denied") && !strings.Contains(last.Content, "error:") {
		t.Fatalf("the model should read the rule's refusal, got %q", last.Content)
	}
}

func TestGrantList_AFetchCardHasNoNarrowRow(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) { return "page text", nil }
	m := gatedModel(t, executor, fetchPreviews())
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		fetchCall("call_1", "https://docs.python.org/3/library/json.html"),
	}})
	m = handover(t, updated.(Model))
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = updated.(Model)
	if got := len(m.grantChoice.options); got != 2 {
		t.Fatalf("a fetch card's list has %d rows; want the two lengths: %+v", got, m.grantChoice.options)
	}
}

// The register's own row for the list, walked the way the inert-key register
// walks every other surface: the three keys it declares are the three it
// answers, and the always-allow key is what reaches it.
func TestGrantList_TheRegisterRowIsTheKeysItAnswers(t *testing.T) {
	var found *keys.Surface
	for _, s := range keys.Surfaces() {
		if s.Name == "the approval card's grant list" {
			surface := s
			found = &surface
		}
	}
	if found == nil {
		t.Fatal("the grant list has no row in the register")
	}
	if got := len(found.Bindings); got != 3 {
		t.Fatalf("the row declares %d bindings; want move, take and cancel", got)
	}
	var ran []string
	m := grantCardModel(t, &ran, "go build ./one")
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = updated.(Model)
	moved := press(t, m, "j")
	if moved.grantChoice == nil || moved.grantChoice.focus != 1 {
		t.Fatal("the move key declared on the row does not move the list")
	}
	taken, _ := moved.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if taken.(Model).grantChoice != nil {
		t.Fatal("the take key declared on the row does not take a row")
	}
}
