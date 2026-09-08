package chat

// Choosing a grant at the card.
//
// The always-allow key used to grant on the press: one keystroke, one
// standing permission, and nothing on the card saying which prefix it took or
// when it would end. `git push` and `git` are very different grants behind
// the same key, and a permission the reader cannot see the end of is one they
// will not remember making.
//
// So the key opens a list instead. Each row states what the grant covers and
// when it ends, and taking a row is what grants — which means the reader
// reads the grant before making it rather than after
// (docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends).

import (
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// grantLength is how long a grant stands. There are two and there is no
// third: a grant that outlives the session is an allowlist entry in the
// configuration file, typed there rather than pressed here, and anything
// between the two would be a duration the reader had to estimate — how long
// does a build take.
type grantLength int

const (
	// forThisTurn ends at the turn's close, which is the unit the reader
	// already thinks in: a turn is one instruction, the round cap is per
	// turn, the check-in is per turn. It is also the only short boundary the
	// session can observe without asking anybody to name a number.
	forThisTurn grantLength = iota
	forThisSession
)

// The two ends a grant can have, in the words the list prints. `/permissions
// grants` repeats them verbatim, so a reader who chose a row recognises the
// listing as the same act rather than as a second description of it.
const (
	endsWithTurn    = "until this turn ends"
	endsWithSession = "until you revoke it"
)

// grantOffer is one row of the list as an act: how long the grant lasts, and
// whether it covers the pattern the card printed or only the thing exactly as
// it stands.
type grantOffer struct {
	length grantLength
	// exact narrows the grant from the pattern to the thing itself — this
	// line rather than every command starting with its leading words, this
	// file rather than its directory.
	exact bool
}

// reason is the code the record files this grant under. The length leads: a
// grant that expires with the turn is the fact worth counting whatever its
// width, and there is no exact turn grant to disambiguate against — the
// narrow row is offered at session length, where the width is what the reader
// is trading against standing permission.
func (o grantOffer) reason() string {
	switch {
	case o.length == forThisTurn:
		return observe.ReasonUserTurn
	case o.exact:
		return observe.ReasonUserExact
	}
	return observe.ReasonUserAlways
}

// grantChoice is the list open under the card: the rows as the card draws
// them, the acts behind them, and which one the pointer is on.
//
// It lives beside the card's two fields and behaves like them — it holds the
// keyboard, esc closes it with the decision still waiting, and the card's own
// keys are inert while it is up — because it is the same kind of thing: a
// surface under the card that has to be answered before the card can be.
type grantChoice struct {
	offers  []grantOffer
	options []components.SelectOption
	focus   int
}

// grantOffers is the list the key opens for the card in front of the reader:
// the two lengths, and — where the card has a narrower width to offer than
// the pattern it printed — the narrow one under them.
//
// A fetch card gets two rows rather than three, and that is the asymmetry
// worth stating: a host grant is already exact. `grantHost` records the host
// the card named and never a suffix, so a third row would either grant the
// same thing again under a different name or invent a narrower kind of host
// than a host. A command's prefix and an edit's directory are both wider than
// what the card showed, which is what makes the third row an offer there.
func (m Model) grantOffers(req *approvalRequest) ([]grantOffer, []components.SelectOption) {
	if req == nil {
		return nil, nil
	}
	// Each row carries the pattern itself and not a sentence about it. What
	// kind of thing is being granted is already on the card the list is
	// pinned under, and the row has to fit a prefix and an end into sixty
	// columns beside a label — a leading clause would be what the terminal
	// dropped, and the pattern is the half that cannot go.
	var covers, narrow, narrowLabel string
	switch {
	case req.kind == approvalExec:
		prefix := agent.GrantPrefix(req.command)
		if prefix == "" {
			return nil, nil
		}
		// The prefix wears the ellipsis and the exact line does not, which is
		// the whole difference between the two widths said in one character:
		// `npm test …` covers what follows it and `npm test --watch` does
		// not. Unquoted, because the quotes exist to keep a multi-word grant
		// readable inside a sentence and this is a column.
		covers = prefix + " …"
		// A command of more than one line has no narrow row: the row would
		// have to print the line it grants, and a pattern the reader cannot
		// read on the row it is choosing is not an offer.
		if !strings.Contains(req.command, "\n") {
			narrow, narrowLabel = strings.TrimSpace(req.command), "this exact line"
		}
	case req.kind == approvalDiff:
		covers = displayDir(filepath.Dir(req.path))
		narrow, narrowLabel = req.path, "this file only"
	case req.host != "":
		covers = req.host
	default:
		return nil, nil
	}
	// The shortest grant is the one the pointer starts on. A list whose
	// default was the standing grant would be the old key with two extra
	// keystrokes in front of it, and the row a reader in a build loop wants
	// is the one that ends with the work.
	offers := []grantOffer{{length: forThisTurn}, {length: forThisSession}}
	options := []components.SelectOption{
		{Label: "this turn only", Desc: covers, Meta: endsWithTurn},
		{Label: "this session", Desc: covers, Meta: endsWithSession},
	}
	if narrow != "" {
		offers = append(offers, grantOffer{length: forThisSession, exact: true})
		options = append(options, components.SelectOption{
			Label: narrowLabel, Desc: narrow, Meta: endsWithSession,
		})
	}
	return offers, options
}

// openGrantChoice puts the list under the card. Nothing is granted by opening
// it: the grant is made when a row is taken, and esc closes the list with the
// decision still waiting, because a key that turned a glance into a standing
// permission would be a key nobody could afford to press
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
func (m Model) openGrantChoice() (tea.Model, tea.Cmd) {
	offers, options := m.grantOffers(m.pendingApproval)
	if len(offers) == 0 {
		return m, nil
	}
	m.grantChoice = &grantChoice{offers: offers, options: options}
	m.syncViewport()
	return m, nil
}

// updateGrantChoice routes a key while the list holds the keyboard. It
// answers a selector's three keys and nothing else — the card's own letters,
// its digits and its chords are all inert until the list is closed, the way
// they are while either of the card's fields is open
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (m Model) updateGrantChoice(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	open := *m.grantChoice
	switch {
	case keys.Match(msg, keys.Select.Cancel):
		// Back to the card with nothing granted and the decision exactly
		// where it was.
		m.grantChoice = nil
		m.syncViewport()
		return m, nil
	case keys.Match(msg, keys.Select.Take):
		return m.takeGrant(open.offers[open.focus])
	case keys.Match(msg, keys.Select.MoveJK):
		// Which half of the pair was pressed is read off the binding rather
		// than off the letter, so a keymap that moves the movement keys
		// moves them here too. The list is replaced rather than written
		// through the pointer: the model is a value every update hands back
		// a copy of, and a shared list would move the pointer in the copy
		// the last frame was drawn from.
		step := keys.Step(msg.String(), keys.Select.MoveJK)
		open.focus = min(max(open.focus+step, 0), len(open.options)-1)
		m.grantChoice = &open
		m.syncViewport()
		return m, nil
	}
	return m, nil
}

// takeGrant is enter on a row: the grant is made, said in the transcript, and
// the act the card was asking about runs — which is what the key did before
// the list stood in front of it.
func (m Model) takeGrant(o grantOffer) (tea.Model, tea.Cmd) {
	req := m.pendingApproval
	m.grantChoice = nil
	if req == nil {
		m.syncViewport()
		return m, nil
	}
	m.recordDecision(observe.DecisionAllow, o.reason())
	if note := m.recordGrant(req, o); note != "" {
		m.noteGrant(note)
	}
	m.syncGrants()
	if req.kind == approvalExec {
		return m.executeRun()
	}
	return m.executeApprovedTool()
}

// recordGrant makes the grant a row stands for and returns the sentence the
// transcript keeps, or "" where there was nothing to grant.
func (m *Model) recordGrant(req *approvalRequest, o grantOffer) string {
	switch {
	case req.kind == approvalExec:
		if what := m.grantCommand(req.command, o); what != "" {
			if o.exact {
				return grantNote(what+" will run", o.length)
			}
			return grantNote("Commands starting "+what+" will run", o.length)
		}
	case req.kind == approvalDiff:
		if what := m.grantEdit(req.path, o); what != "" {
			if o.exact {
				return grantNote("Edits to "+what+" will apply", o.length)
			}
			return grantNote("Edits in "+what+" will apply", o.length)
		}
	case req.host != "":
		if what := m.grantHost(req.host, o); what != "" {
			return grantNote("Fetches from "+what+" will run", o.length)
		}
	}
	return ""
}

// grantNote is what the transcript records when a grant is made: what it
// covers and when it ends, in the words the row the reader took printed. A
// grant that is not said is a grant nobody can revoke — the card is gone a
// frame later, and the status chip can only say that something was granted.
func grantNote(covers string, length grantLength) string {
	if length == forThisTurn {
		return covers + " without asking " + endsWithTurn + "."
	}
	return covers + " without asking. /permissions revoke takes it back."
}

// applyGrantChoice puts the open list on the card. The rows are the model's
// for the reason the card's fields are: the card is rebuilt every frame and
// where the pointer is standing is not.
func (m Model) applyGrantChoice(card *components.ApprovalCard) {
	c := m.grantChoice
	if c == nil || !card.AllowAlways {
		return
	}
	card.GrantOpen, card.GrantRows, card.GrantFocus = true, c.options, c.focus
}
