package keys

// The register of keyed surfaces, as data.
//
// It is an audit: every surface that offers a bare letter, which of the two
// positions it is in, and how it gets the keyboard. It was written as a
// markdown table, which is the form a rule takes when nobody can check it —
// and the whole point of it is that "a rule nobody can check against a list
// is a rule each new surface gets to rediscover".
//
// So the list is here, beside the bindings, and the tests check the code
// against it: every binding belongs to exactly one surface, no surface
// answers one keystroke with two bindings, and no surface that is not a
// takeover offers a bare letter without naming the key that hands the
// keyboard over.
//
// It is also what `?` renders and what /help's key section is built
// from, so the register a reader is shown is the register the handlers use.

// Position is where a surface stands relative to the keyboard. The register
// allows two and says there is no third; Home is the thing those two are
// defined against, not a third position.
type Position int

const (
	// Home is the framed input. It holds the keyboard whenever nothing
	// has taken it, which is most of the time, and it is why every key in
	// this position is a chord — a bare letter here is a letter.
	Home Position = iota
	// Takeover holds the keyboard exclusively. Its state is routed before
	// the input sees a key and the input is not live while it is up, so its
	// letters are live because nothing else is listening.
	Takeover
	// Beside is a surface that does not hold the keyboard: it renders its
	// keys grey and offers the one key that hands the keyboard over, live,
	// next to them.
	Beside
)

func (p Position) String() string {
	switch p {
	case Home:
		return "holds the keyboard"
	case Takeover:
		return "takeover"
	default:
		return "beside a live draft"
	}
}

// Surface is one row of the register of keyed surfaces
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
type Surface struct {
	// Name is what the surface is called in the interface documentation,
	// lowercase, because it is read inside a sentence as often as above a
	// list.
	Name string
	// Section is the documentation that is normative for it.
	Section string
	// Position is where it stands relative to the keyboard.
	Position Position
	// Reached is how it gets the keyboard, in words — the last column of
	// that register.
	Reached string
	// Bindings are its keys, in the order it offers them.
	Bindings []Binding
}

// Surfaces is the register in the order a reader meets them: the input first,
// then what takes the keyboard from it, then the rows that wait for it.
func Surfaces() []Surface {
	return []Surface{
		{
			Name:     "the input",
			Section:  "docs/interface/surfaces.md#the-input-frame",
			Position: Home,
			Reached:  "it has the keyboard unless something has taken it",
			Bindings: []Binding{
				Draft.Send, Draft.Newline, Draft.Queue,
				Draft.Editor, Draft.Attach,
				Draft.Complete, Draft.Palette, Draft.Reasoning, Draft.Mode,
				Draft.Pause,
				Draft.HistoryPrev, Draft.HistoryNext, Draft.HistorySearch,
				Draft.PointUp, Draft.PointDown, Draft.Open, Draft.Close,
				Draft.PageUp, Draft.PageDown,
				Draft.Reading, Draft.Agents, Draft.Backlog,
				Draft.NextAgent, Draft.PrevAgent,
				Draft.Mouse, Draft.KeyList,
				Draft.Suspend, Draft.Redraw,
				Draft.Answer, Draft.Clear, Draft.Cancel, Draft.Quit,
			},
		},
		{
			// The reverse search over the input ring. It is typed into from
			// the first keystroke, like the palette: every letter filters, so
			// the only keys on the row are the three that do not.
			Name:     "the input history search",
			Section:  "docs/interface/surfaces.md#the-input-frame",
			Position: Takeover,
			Reached:  Shown(Draft.HistorySearch),
			Bindings: []Binding{Search.Older, Search.Keep, Search.Cancel},
		},
		{
			Name:     "reading mode",
			Section:  "docs/interface/surfaces.md#reading-mode",
			Position: Takeover,
			Reached:  Shown(Draft.Reading),
			Bindings: Reading.All(),
		},
		{
			// Reading mode with its query row open, which is a row of its
			// own here for the reason the selector's is: a surface being
			// typed into keeps every letter as text, so none of the mode's
			// bare letters are live while it is up and the two keys that are
			// not letters are the whole of what it answers.
			Name:     "the transcript search",
			Section:  "docs/interface/surfaces.md#reading-mode",
			Position: Takeover,
			Reached:  Bracket(Reading.Search) + " in reading mode",
			Bindings: Find.All(),
		},
		{
			Name:     "the context surface",
			Section:  "docs/interface/surfaces.md#the-context-surface",
			Position: Takeover,
			Reached:  "/context",
			Bindings: Context.All(),
		},
		{
			// The backlog as a screen rather than as a command that prints:
			// the list on the left and the item's own prose on the right,
			// with the keys that would otherwise be typed as verbs.
			Name:     "the backlog screen",
			Section:  "docs/interface/surfaces.md#the-backlog-screen",
			Position: Takeover,
			Reached:  Shown(Draft.Backlog) + ", or /todo",
			Bindings: Backlog.All(),
		},
		{
			// The sprint plan card, on that screen's sprint tab. It is a
			// surface of its own rather than a mode of the screen because
			// it answers every keystroke while it is up: the list under it
			// is drawn and not live, which is what lets the card keep the
			// pair the screen had to break.
			Name:     "the sprint plan card",
			Section:  "docs/interface/surfaces.md#the-sprint-board",
			Position: Takeover,
			Reached:  "/todo sprint plan",
			Bindings: Sprint.All(),
		},
		{
			Name:     "a transcript row's own offers",
			Section:  "docs/interface/surfaces.md#the-turns-close, docs/interface/surfaces.md#the-recovery-row, docs/interface/surfaces.md#the-backlog-runs-row",
			Position: Beside,
			Reached:  Shown(Draft.Reading) + ", then the cursor on the row",
			Bindings: []Binding{
				Row.Review, Row.Undo, Row.Retry, Row.Continue,
				Row.Key, Row.Provider, Row.Rounds, Row.Uncap, Row.Reopen,
			},
		},
		{
			Name:     "the approval card, the /run confirm, the plan card",
			Section:  "docs/interface/surfaces.md#the-approval-card, docs/interface/surfaces.md#selectors, docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard",
			Position: Beside,
			Reached:  Shown(Draft.Answer),
			Bindings: []Binding{
				Decision.Allow, Decision.Deny, Decision.Always,
				Decision.Batch, Decision.Diff,
				Decision.ScrollUp, Decision.ScrollDown,
				Decision.PanLeft, Decision.PanRight,
			},
		},
		{
			// The one approval card the reader asks for rather than is
			// handed: it writes a file the session offered to write, so it
			// holds the keyboard from the moment it opens. That is also why
			// its no is Refuse rather than Deny — see the binding.
			Name:     "the scaffold card",
			Section:  "docs/interface/surfaces.md#the-approval-card",
			Position: Takeover,
			Reached:  "/init, or the start screen's scaffold offer",
			Bindings: []Binding{Decision.Allow, Decision.Refuse, Select.Cancel},
		},
		{
			Name:     "the inline confirm and the undo confirm",
			Section:  "docs/interface/surfaces.md#the-inline-confirm",
			Position: Takeover,
			Reached:  "the key that opens it",
			Bindings: []Binding{Confirm.Yes, Confirm.Force, Confirm.No},
		},
		{
			Name:     "the selector family, the model and rewind pickers",
			Section:  "docs/interface/surfaces.md#selectors",
			Position: Takeover,
			Reached:  "the command or key that opens it",
			Bindings: []Binding{
				Select.MoveJK, Select.Take, Select.Alt, Select.Filter,
				Select.ClearQ, Select.Toggle, Select.All, Select.Note,
				Select.Cancel,
			},
		},
		{
			// The one card in the family with keys of its own: only the
			// saved-chat picker answers them, so only its row offers them.
			Name:     "the saved-chat picker",
			Section:  "docs/interface/surfaces.md#selectors, docs/capabilities/sessions-and-memory.md#housekeeping",
			Position: Takeover,
			Reached:  "/chats, or bare /load",
			Bindings: []Binding{Select.Delete, Select.Rename},
		},
		{
			// The same family with the query line open, which is why it is
			// a row of its own: a list being typed into keeps every letter
			// as text, so j/k are not keys and the arrows are the movement
			//. Nothing here is a bare letter.
			Name:     "a selector being typed into",
			Section:  "docs/interface/surfaces.md#selectors",
			Position: Takeover,
			Reached:  "a card that opens over a catalog, or " + Bracket(Select.Filter) + " on one that does not",
			Bindings: []Binding{
				Select.Move, Select.Take, Select.ClearQ, Select.Cancel,
			},
		},
		{
			Name:     "the command palette",
			Section:  "docs/interface/surfaces.md#the-palette",
			Position: Takeover,
			Reached:  Shown(Draft.Palette),
			Bindings: []Binding{
				Select.Palette.Prev, Select.Palette.Next,
				Select.Palette.Run, Select.Palette.Write, Select.Cancel,
			},
		},
		{
			Name:     "review mode",
			Section:  "docs/interface/surfaces.md#the-turns-close",
			Position: Takeover,
			Reached:  Bracket(Row.Review) + ", /review, /diff",
			Bindings: []Binding{
				Review.MoveFile, Review.MoveHunk, Review.StageHunk,
				Review.StageFile, Review.StageAll, Review.SideBySide,
				Review.PageUp, Review.PageDown, Review.Apply, Review.Back,
			},
		},
		{
			Name:     "the agent manager",
			Section:  "docs/interface/surfaces.md#the-agent-manager",
			Position: Takeover,
			Reached:  Shown(Draft.Agents) + ", /agents",
			Bindings: []Binding{
				Agent.Move, Agent.Attach, Agent.Answer, Agent.Retry,
				Agent.Cancel, Agent.Kill, Agent.Back,
			},
		},
		{
			// The drafting flow, which is a takeover for the reason every
			// summoned surface is: it asks three things in order and each
			// answer is typed, so the input it would otherwise borrow is the
			// input it has to own.
			Name:     "the profile drafter",
			Section:  "docs/interface/surfaces.md#the-profile-drafter",
			Position: Takeover,
			Reached:  "/agents new, or the manager's own row",
			Bindings: []Binding{
				Profile.Move, Profile.Take, Profile.Note,
				Profile.ScrollUp, Profile.ScrollDown, Profile.Back,
			},
		},
		{
			Name:     "the full-screen diff",
			Section:  "docs/interface/surfaces.md#the-diff-view",
			Position: Takeover,
			Reached:  "the key that opens it",
			Bindings: []Binding{
				Diff.Scroll, Diff.Hunk, Diff.SideBySide, Diff.Back, Diff.Leave,
			},
		},
		{
			Name:     "the full-screen output",
			Section:  "docs/interface/surfaces.md#the-activity-row",
			Position: Takeover,
			Reached:  "the key that opens it",
			Bindings: []Binding{
				Output.Scroll, Output.PageUp, Output.PageDown,
				Output.Collapse, Output.Back, Output.Leave,
			},
		},
		{
			Name:     "the staged attachment preview",
			Section:  "docs/interface/surfaces.md#a-staged-attachment",
			Position: Takeover,
			Reached:  "/paste show <name>",
			Bindings: []Binding{Preview.Back, Preview.Leave},
		},
		{
			Name:     "the retry countdown",
			Section:  "docs/interface/surfaces.md#the-recovery-row",
			Position: Takeover,
			Reached:  "it opens on its own and takes the keyboard",
			Bindings: []Binding{Wait.Fallback, Wait.Stop},
		},
		{
			Name:     "the context-pressure card",
			Section:  "docs/interface/surfaces.md#the-recovery-row",
			Position: Takeover,
			Reached:  "it opens on its own and takes the keyboard",
			Bindings: []Binding{Wait.Compact, Wait.NewSession, Wait.KeepGoing},
		},
		{
			Name:     "the masked key prompt",
			Section:  "docs/interface/surfaces.md#the-recovery-row",
			Position: Takeover,
			Reached:  Bracket(Row.Key) + " on an auth failure's row",
			Bindings: []Binding{Wait.UseKey, Wait.KeepKey},
		},
	}
}

// Programs are the keyed surfaces outside a chat session: the supporting
// TUIs, the one-shot's action bar and the saved-chat browser.
// Each is its own Bubble Tea program with its own key row and its own `?`,
// so none of them is part of a session's answer to that key — but a key is a
// key, and the register is not worth having if it is only most of them.
func Programs() []Surface {
	return []Surface{
		{
			Name:     "shhh config",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "shhh config",
			Bindings: []Binding{
				Screen.Move, Screen.Take, Screen.Filter, Screen.ClearQ,
				Screen.Reset, Screen.Write, Screen.List, Screen.Quit,
			},
		},
		{
			Name:     "a setting's picker or field",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  Bracket(Screen.Take) + " on a setting",
			Bindings: []Binding{
				Select.Move, Screen.Take, Screen.Filter, Screen.ClearQ, Screen.Keep,
			},
		},
		{
			Name:     "shhh history",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "shhh history",
			Bindings: []Binding{
				Screen.Move, Screen.Rerun, Screen.Copy, Screen.Snippet,
				Screen.Delete, Screen.Filter, Screen.ClearQ, Screen.List, Screen.Quit,
			},
		},
		{
			Name:     "shhh doctor",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "shhh doctor",
			Bindings: []Binding{
				Screen.Move, Screen.Fix, Screen.Copy, Screen.Again,
				Screen.List, Screen.Quit,
			},
		},
		{
			Name:     "shhh metrics",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "shhh metrics",
			Bindings: []Binding{Screen.Quit},
		},
		{
			// The one supporting screen that asks rather than reports, which
			// is why its three answers are its whole register: there is
			// nothing on it to move between, and the way out is the way out
			// of every other one.
			Name:     "shhh rate",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "shhh rate",
			Bindings: []Binding{
				Screen.Worked, Screen.Failed, Screen.Skip, Screen.List, Screen.Quit,
			},
		},
		{
			Name:     "the one-shot's action bar",
			Section:  "docs/interface/surfaces.md#the-one-shot-result",
			Position: Takeover,
			Reached:  "shhh cmd <prompt>",
			Bindings: []Binding{
				OneShot.Run, OneShot.Confirm, OneShot.Step, OneShot.DryRun, OneShot.Edit,
				OneShot.Revise, OneShot.Back, OneShot.Alternatives,
				OneShot.Explain, OneShot.Copy, OneShot.Save, OneShot.Quit,
			},
		},
		{
			Name:     "first contact and the provider card",
			Section:  "docs/interface/surfaces.md#the-start-screen",
			Position: Takeover,
			Reached:  "a session with no key to run on",
			Bindings: []Binding{Setup.Wizard, Setup.Paste, Setup.Local},
		},
		{
			Name:     "the saved-chat browser",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "shhh chats, or --resume on shhh chat and shhh code",
			Bindings: []Binding{
				Browse.Move, Browse.Open, Browse.Filter, Browse.Delete, Browse.Rename,
				Browse.Quit,
			},
		},
		{
			Name:     "a saved chat's detail",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  Bracket(Browse.Open) + " on a chat",
			Bindings: []Binding{
				Browse.Action, Browse.Prev, Browse.Take, Browse.Back, Browse.Leave,
			},
		},
	}
}
