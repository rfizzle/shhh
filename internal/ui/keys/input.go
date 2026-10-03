package keys

// What the input offers, each key declared once with the paragraph /help's
// key list keeps beside it.
//
// The input's row of the register used to be a list of bindings here and the
// paragraphs a list of rows in the help, held together by a test that said
// the two named the same keys. That is two places to add a key and one test
// to find out which was forgotten. So the key and its paragraph are one
// declaration now: the input's row of the register is read off these
// offers, and so is the key list, and a key the input answers with no
// paragraph beside it fails a test here rather than a reader there.
//
// The offers are in the order the register declares the input's keys, which
// is the order the keymap file and the generated reference print them. The
// key list reads in another order — the order a reader meets the keys: what
// sends a message first, then what the draft does, then what takes the
// screen, then the ways out — and that order is each offer's Weight, so both
// orders are kept and neither is derived from the other by accident.

// Offer is one row of the key list: which of the input's keys it is about,
// and the paragraph beside them. The paragraph's own line breaks are kept —
// several are two thoughts and not one long one — and each is wrapped to the
// prose column where it is drawn.
type Offer struct {
	// Weight is where the row stands in the key list: lower reads first.
	Weight int
	// Binds are the bindings the row is about, and the input's row of the
	// register is these, offer by offer. Empty is a row about something the
	// register does not bind at all: a leading character, a paste, the mouse.
	Binds []Binding
	// Sep joins the spellings when the row is about more than one binding: a
	// space for two chords that are one gesture, a slash for the two ends of
	// one act, and a newline for a pair that gets a line of the column each.
	Sep string
	// Key is the column when the register does not spell it the way the list
	// reads it — the recall arrows, whose glyphs beside "recall previous
	// inputs" read as decoration rather than as a key — or when the row binds
	// nothing.
	//
	// It is written exactly as the list draws it, because those two cases
	// want opposite things. A leading character is a key: `@` and `!` are
	// pressed, so they wear the brackets every key wears. A paste, a wheel
	// and a click are not pressed at all, and bracketing them would offer a
	// keystroke that does not exist
	// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
	Key string
	// Help is the paragraph.
	Help string
}

// InputOffers are the input's keys and the paragraphs beside them: the bound
// ones in the register's order, then the rows that bind nothing.
func InputOffers() []Offer {
	return []Offer{
		{
			Weight: 10,
			Binds:  []Binding{Draft.Send, Draft.Newline},
			Help: `send the message; shift+enter inserts a newline
ctrl+j does the same, for terminals that cannot report shift+enter. A draft ending in \ turns enter into a newline too, the shell's own continuation — end in \\ to send a literal backslash`,
		},
		{
			Weight: 20,
			Binds:  []Binding{Draft.Queue},
			Help: `while a turn is live, queue the draft as a follow-up sent when the turn completes. Steering (enter) joins the running turn; a follow-up waits for it to end. After a cancel the queue is held rather than sent — the notice rail says so
on an empty draft the same key pulls the newest queued message — a follow-up first, else a steering line — back (it was the line editor's next-line; ↓ still is)`,
		},
		{
			Weight: 30,
			Binds:  []Binding{Draft.Queued},
			Help: `move the keyboard into what is queued, drawn as rows above the input in the order it will go out: ↑↓ picks a message, enter pulls it back into the draft with what was staged with it — out of the queue, so sending it queues it again at the end — x cancels it, esc goes back to the draft as it was
a message the turn delivered before the key reached it is already sent, and the row says so; /rewind is what takes a sent message back out`,
		},
		{
			Weight: 250,
			Binds:  []Binding{Draft.Editor},
			Help:   `open the draft in your editor: $EDITOR (then $VISUAL, then vi) opens a file holding what you have typed, at the line and column the cursor was on, and whatever is in the file when the editor exits becomes the draft. An empty file leaves the draft alone. Not while a turn is running or a decision is waiting — the editor takes the terminal with it`,
		},
		{
			Weight: 70,
			Binds:  []Binding{Draft.Attach},
			Help:   `attach the clipboard: a copied screenshot or file is staged for your next message, ordinary text still pastes into the draft. Dragging an image into the terminal attaches it the same way. What is staged shows as chips above the input`,
		},
		{
			Weight: 100,
			Binds:  []Binding{Draft.Complete},
			Help:   `complete a slash command (typing / opens the menu; ↑↓ move, enter runs the highlighted command, esc dismisses)`,
		},
		{
			Weight: 120,
			Binds:  []Binding{Draft.Palette},
			Help:   `command palette: one prompt over commands, saved chats and the files this session touched — type to filter, enter runs, tab writes it into the input, esc dismisses. A terminal that cannot send this chord — it is a single byte and Windows conhost sends nothing for it — reaches the same list through the other door: / on an empty draft, then tab`,
		},
		{
			Weight: 150,
			Binds:  []Binding{Draft.Reasoning},
			Help:   `cycle the reasoning level: off → low → medium → high. It changes the next model request, not the one in flight, and the level is stated on the vitals rail beside the model`,
		},
		{
			Weight: 160,
			Binds:  []Binding{Draft.Mode},
			Help: `cycle the permission mode: manual → accept-edits → auto → read-only → plan, and round again (behavior.mode_cycle sets another order)
while the agent is working, enter queues a steering message that joins the conversation before the next model request`,
		},
		{
			Weight: 130,
			Binds:  []Binding{Draft.Pause},
			Help:   `hold the turn between rounds, and press it again to let the turn go on. The hold waits for the round in flight to finish, because a stream nobody is reading backs up until the provider gives up on it — so the rail says "holding after this round" and then "held". Nothing is re-asked and nothing is lost: what you type while it is held rides out with the round it resumes into, ctrl+z is accepted, and quitting and coming back with --continue opens the conversation held. It reaches every agent this session started, each at its own boundary, and one press lets them all go`,
		},
		{
			Weight: 170,
			Binds:  []Binding{Draft.HistoryPrev, Draft.HistoryNext},
			Key:    "[up/down]",
			Help:   `recall previous inputs (when the input is empty)`,
		},
		{
			Weight: 140,
			Binds:  []Binding{Draft.HistorySearch},
			Help:   `search the input history: an incremental reverse search over what you typed before. Typing filters, ctrl+r again steps to an older match, enter keeps the match in the draft, esc puts the draft back exactly as it was`,
		},
		{
			Weight: 40,
			Binds:  []Binding{Draft.TakeSuggestion},
			Help: `on an empty draft with a next step drawn dim in it, put that step in the draft with the cursor at its end, to edit or send like anything you typed
the step is offered after a turn closes and never sent on its own; any other key drops it for that turn. /ui suggest turns the offer off for the session`,
		},
		{
			Weight: 180,
			Binds:  []Binding{Draft.PointUp, Draft.PointDown},
			Sep:    "\n",
			Help:   `move the pointer over the pane's rows — reading mode's cursor seen from the prompt. The draft keeps the keyboard and every letter it has, and the pane scrolls only as far as keeps the pointed row in view. On the start screen the offers are the rows. Esc drops the pointer; ctrl+o opens reading mode on it`,
		},
		{
			Weight: 190,
			Binds:  []Binding{Draft.Open, Draft.Close},
			Sep:    "\n",
			Help:   `open or run the pointed row, and close it: what enter and - do under reading mode's cursor, by the same handler. A row's own letters (a failure's r, a pause's +) are live only once the handover or reading mode has given the row the keyboard, because at the prompt a letter is text. Enter on an empty draft is the same open`,
		},
		{
			Weight: 330,
			Binds:  []Binding{Draft.PageUp, Draft.PageDown},
			Sep:    "/",
			Help:   `page the transcript, leaving the keyboard in the prompt. Scrolling away pauses the follow while a turn streams; the notice rail counts what is below and pgdn walks back to it`,
		},
		{
			Weight: 200,
			Binds:  []Binding{Draft.Reading},
			Help: `reading mode: select transcript rows (j/k, u/d half a page), expand/collapse (enter), y copies the row under the cursor — a command as $ cmd over its output, an edit as its unified diff, a message as markdown source, a card call by call — / searches the transcript and n/N walk what it found, pgup/pgdn page, ? lists every key the mode has, esc or typing returns to the prompt
enter on a step's card opens it onto its calls and enter again closes it, as - does; on an open card ←→ walk the strip of its calls and enter opens that tool, esc there coming back to the strip, and a click on a call's row opens it the same way. enter on an edit row cycles collapsed → expanded → full-screen diff, and on a command or read row the same three depths over its output, the whole of it scrollable at the last one. It opens over a running turn, which keeps streaming underneath; a transcript with nothing selectable opens as a plain pager. /step opens the in-flight step's detail from the prompt`,
		},
		{
			Weight: 210,
			Binds:  []Binding{Draft.Agents},
			Help:   `agent manager: enter attaches to an agent's session, s steers it from its row without attaching, x cancels its turn, X kills it — the one way an agent ends; attached, typing steers the agent, shift+tab sets its mode (clamped), esc detaches`,
		},
		{
			Weight: 220,
			Binds:  []Binding{Draft.Backlog},
			Help: `the backlog screen: the project's items on the left and the one under the pointer on the right, / and s/p/k/r narrow the list, enter reads the body, e edits the file, R runs it, b/o/d/x block, reopen, archive and drop it, tab shows what shipped, ? lists every key it has, esc returns
bare /todo opens the same screen; it opens over a running turn, and the keys that would change a file are grey while one is going, because the model may be reading them`,
		},
		{
			Weight: 230,
			Binds:  []Binding{Draft.NextAgent, Draft.PrevAgent},
			Sep:    "\n",
			Help:   `move the keyboard one session along the inspector rail's AGENTS map — the orchestrator and every agent it started, in the order they were started, wrapping at both ends. The rail stays up while you are in an agent's session and marks the row you are in; everything you do *to* an agent is still in the manager`,
		},
		{
			Weight: 340,
			Binds:  []Binding{Draft.Mouse},
			Key:    "wheel",
			Help:   `scroll the transcript (or the full-screen diff / review), leaving the draft and the keyboard where they are (needs ctrl+x — off by default, so the terminal keeps its own click-drag selection)`,
		},
		{
			Weight: 290,
			Binds:  []Binding{Draft.KeyList},
			Help:   `open the key list over the session: every key, by group, as they are bound now — type to filter, esc or the chord again closes it, and nothing is written to the transcript. It is a chord and not the ? it used to be, because a bare key at the draft is a letter of whatever you are typing — every key live here is a chord but enter and esc`,
		},
		{
			Weight: 260,
			Binds:  []Binding{Draft.Suspend},
			Help:   `suspend shhh and go back to the shell; fg brings it back with the screen as you left it. Refused while a turn is running or a decision is waiting — a stopped shhh is not reading the stream it asked for`,
		},
		{
			Weight: 270,
			Binds:  []Binding{Draft.Redraw},
			Help:   `redraw the screen from what the session already holds, for a display something else wrote over. The draft, the history and any selection are untouched`,
		},
		{
			Weight: 280,
			Binds:  []Binding{Draft.Answer},
			Sep:    "\n",
			Help:   `hand the keyboard to a decision waiting on screen. An approval that lands while you are typing does not take your keys with it: its y, n and a are not live until one of these chords gives them the keyboard, and until then every letter goes into the draft. Esc leaves the decision waiting; n is how you say no. The two are the same act, and a waiting card names both: ctrl+y is for terminals and desktops that never deliver ctrl+space — macOS binds it to the input-source switcher and takes it first. With nothing waiting it hands the keyboard to the row you have selected: on a recovery or round-limit row that row's own letters are live from then on — r tries again, c continues, e takes a new key, p switches provider, + grants more rounds, ! lets it run — and esc gives the keyboard back; with no row selected it reaches the failure the last turn ended on, which says so. On a selected changed-files row it opens that turn's commit card, and a changed-files row opens the turn's review when it is clicked or selected and opened with enter. What a row once offered to do to the work is a command now: /undo takes a turn back, /gate run runs its checks again, /todo open reopens a blocked run's item`,
		},
		{
			Weight: 300,
			Binds:  []Binding{Draft.Clear},
			Help:   `go back, in this order: drop a selection, drop the pointer, dismiss the completion menu, detach one level — then, with any text in the box, clear the input, and on an empty draft fold every row you opened back to its resting form, counted on the notice rail. What /ui verbosity opened is the setting's and stays open. A waiting decision is left waiting. It never stops a running turn — on an empty draft under one with nothing left to fold it does nothing, so ctrl+c is what you want there. On an empty idle draft, esc esc opens the /rewind picker`,
		},
		{
			Weight: 310,
			Binds:  []Binding{Draft.Cancel},
			Help:   `cancel the running turn — press twice, and what the turn already did is kept. Also clears the input, and quits from an empty idle draft (twice again)`,
		},
		{
			Weight: 320,
			Binds:  []Binding{Draft.Quit},
			Help:   `quit — press twice; with a turn running it asks first, saying what is cancelled and what the autosave keeps`,
		},

		// What the input answers that the register does not bind: a leading
		// character, a paste, the line editor's own keys, the mouse, and the
		// card's answers, which are the card's row and not the input's.
		{
			Weight: 50,
			Key:    "[@]",
			Help:   `at the start of a word, open a file menu over what this session changed and the checkout's recent files, filtered by what you type after it. tab or enter inserts the path, esc keeps what you typed; a mentioned image is staged the way a pasted one is`,
		},
		{
			Weight: 60,
			Key:    "[!]",
			Help:   `a draft starting with ! runs as a command through the same confirm card /run uses; !! runs it and keeps the output out of the conversation (its row says local). A ! anywhere else is a letter`,
		},
		{
			Weight: 80,
			Key:    "pasting",
			Help:   `text taller than 10 lines or wider than 1000 columns is staged as paste-1.txt rather than typed into the draft — both through ctrl+v and through your terminal's own paste — so a stack trace does not bury the sentence it came with. Those two numbers are the defaults for appearance.paste_lines and appearance.paste_columns; shhh config shows this machine's, and a negative turns one of them off. A paste over 256 KB is refused rather than staged — it would ride in the prompt itself`,
		},
		{
			Weight: 90,
			Key:    "/paste show\nthe chip",
			Help:   `open the staged paste. What is staged leaves a fold where you pasted it — ⟨Paste#1 · 214 lines⟩ — which moves, deletes and sends as one character, and what it will cost is on the vitals rail before you send. Its chip opens it with the sentence kept: a click on the chip, or reading mode (` + Shown(Draft.Reading) + `) down to the strip and enter on it; /paste show Paste#1 is the same surface by name. It reads the paste back: j/k scrolls, x drops it, q returns to the draft with the cursor where you left it`,
		},
		{
			Weight: 110,
			Key:    "[ctrl+a ctrl+e]\n[ctrl+k ctrl+u]",
			Help:   `the draft is a readline editor: line start and line end, kill to end and to start of line; ctrl+w deletes the word before the cursor, ` + wordMoves(),
		},
		{
			Weight: 240,
			Key:    "rail click",
			Help:   `on the inspector rail, a changed file opens its diff and a session's row moves the keyboard into it. A block's heading, or its … N more, opens the surface holding the whole block — SUMMARY is /readings, THIS TURN is /turns, ALERTS is /alerts, CHANGES is /diff, AGENTS is /agents, STEPS is /steps, TODO is /todo, CONTEXT is /context, SPEND is /stats, TOOLS is /mcp — and a click on the same cell closes it, as its own esc does. The rail never holds the keyboard, so each command is that door's key`,
		},
		{
			Weight: 350,
			Key:    "click-drag",
			Help:   `with the mouse on (ctrl+x), select transcript text: the drag scrolls the pane when it reaches an edge, so a selection can run past the screen; releasing copies it, esc cancels`,
		},
		{
			Weight: 360,
			Key:    "click",
			Help:   `a press and release in the same cell opens the activity row under it, the way enter does in reading mode, or answers the key it lands on in an approval card's [y/n/a]. On a step's card the header is the target: a click there opens the card and a second closes it, a click on its sentence or evidence does nothing, and a click on a call's row inside an open card opens that call's own view, which esc closes. It never takes the keyboard: the draft keeps every character`,
		},
		{
			Weight: 370,
			Key:    "[y/n/a]",
			Help:   `approval prompts: allow / deny / always allow this session. A card taller than its panel counts what is cut and scrolls on shift+↑/↓ (shift+←/→ pan a wide body); d opens an edit's full diff, or a command card's full view; t runs the harmless form of a command that has one, and answers nothing`,
		},
	}
}

// inputBindings is the input's row of the register: the offers' bindings, in
// the order they are declared.
func inputBindings() []Binding {
	var bs []Binding
	for _, o := range InputOffers() {
		bs = append(bs, o.Binds...)
	}
	return bs
}

// wordMoves is how the line editor moves by word, in the keys this platform
// presses for it: alt+b and alt+f where alt arrives, and on a Mac the
// Option arrows, which the stock terminals send as those two with nothing
// set.
func wordMoves() string {
	if Platform() == "darwin" {
		return "option+← and option+→ move by word"
	}
	return "alt+b and alt+f move by word"
}
