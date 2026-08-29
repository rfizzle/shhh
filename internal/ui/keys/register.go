package keys

// The register of keyed surfaces, as data (S-153).
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
				Draft.Send, Draft.Newline, Draft.Attach, Draft.Complete,
				Draft.Palette, Draft.Reasoning, Draft.Mode,
				Draft.HistoryPrev, Draft.HistoryNext,
				Draft.ScrollUp, Draft.ScrollDown, Draft.PageUp, Draft.PageDown,
				Draft.Reading, Draft.Detail, Draft.Agents, Draft.Mouse,
				Draft.Answer, Draft.Clear, Draft.Cancel, Draft.Quit,
			},
		},
		{
			Name:     "reading mode",
			Section:  "docs/interface/surfaces.md#reading-mode",
			Position: Takeover,
			Reached:  Shown(Draft.Reading),
			Bindings: Reading.All(),
		},
		{
			Name:     "a transcript row's own offers",
			Section:  "docs/interface/surfaces.md#the-turns-close, docs/interface/surfaces.md#the-recovery-row",
			Position: Beside,
			Reached:  Shown(Draft.Reading) + ", then the cursor on the row",
			Bindings: []Binding{
				Row.Review, Row.Undo, Row.Retry, Row.Continue,
				Row.Key, Row.Provider, Row.Rounds, Row.Uncap,
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
			},
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
			// The same family with the query line open, which is why it is
			// a row of its own: a list being typed into keeps every letter
			// as text, so j/k are not keys and the arrows are the movement
			//. Nothing here is a bare letter.
			Name:     "a selector being typed into",
			Section:  "docs/interface/surfaces.md#selectors",
			Position: Takeover,
			Reached:  Bracket(Select.Filter) + " on the list",
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
			Name:     "the full-screen diff",
			Section:  "docs/interface/surfaces.md#the-diff-view",
			Position: Takeover,
			Reached:  "the key that opens it",
			Bindings: []Binding{
				Diff.Scroll, Diff.Hunk, Diff.SideBySide, Diff.Back, Diff.Leave,
			},
		},
		{
			Name:     "the staged image preview",
			Section:  "docs/interface/surfaces.md#a-staged-picture",
			Position: Takeover,
			Reached:  "/paste show <name>",
			Bindings: []Binding{Picture.Back, Picture.Leave},
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
			Name:     "the one-shot's action bar",
			Section:  "docs/interface/surfaces.md#the-one-shot-result",
			Position: Takeover,
			Reached:  "shhh <prompt>",
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
			Reached:  "shhh chats",
			Bindings: []Binding{Browse.Move, Browse.Open, Browse.Filter, Browse.Quit},
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
