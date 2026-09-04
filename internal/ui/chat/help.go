package chat

// /help and the `?` key list.
//
// The command list is prose: what each command is for, in the words the
// product uses for it. The key list is not — a key is declared once in the
// register (internal/ui/keys), and a help text that spelled one out again
// was a second place for it to be wrong. So the rows below name bindings and
// the spellings come from the register, which is how a rebind moves the help
// with the handler.
//
// What the register cannot hold is the paragraph beside a key: its own words
// are a phrase for a one-line hint, and this list is where a reader who
// cannot find something comes to read a paragraph. So the paragraph is here
// and the key is the register's, and a test holds the two together — every
// binding the input frame offers has a row, and a row naming none is a
// gesture the register does not bind.

import (
	"strings"
	"unicode/utf8"

	"github.com/rfizzle/shhh/internal/ui/keys"
)

func helpText() string {
	return strings.TrimSpace(`Commands:
  /help          Show this help
  /clear         End this session and start another (also /new)
  /paste [path]  Attach the clipboard — a screenshot, or files copied in a
                 file manager — to your next message; /paste <path> attaches
                 a file by name, /paste show <name> opens a staged image or
                 paste full-pane, /paste drop <name> takes one back out and
                 /paste clear drops what is staged (Ctrl+V)
  /copy [code]   Copy the last response (or just its code blocks)
  /run [n]       Run a code block from the last response (with confirmation)
  /model [name]  Switch the model (bare /model opens an interactive picker)
  /model default [name]   Show or persist the default model for new sessions
  /model agents [name]    Show or persist the model sub-agents run on
                 ("inherit" follows the session model)
  /permissions   What runs without asking, and the permission mode that
                 frames it (also /perms; was /mode)
                 [name]   manual, accept-edits, auto or plan; bare opens a picker
                 why      the latest auto-mode denial's reason
                 grants   what this session has stopped asking about
                 allow <commands|edits>   grant a whole category
                 revoke [commands|edits]  take the grants back
  /reasoning     How much thinking the model does before it answers:
                 off (the default), low, medium or high — ctrl+t cycles them
                 [level]           set it for this session (also /think)
                 default [level]   show or persist the level new sessions
                                   start on (provider.reasoning)
  /context       The window as a meter, by category, with the tools itemised
  /stats         Context occupancy breakdown and cumulative session spend
  /step          Open the in-flight step's detail: every row in it shows its
                 output body, bounded; run it again to close (/ui verbosity
                 high is the same thing for every step at once)
  /ui            Activity feed density, pane layout, monochrome and mouse:
                 /ui verbosity <low|normal|high> · /ui mono <on|off> · /ui mouse <on|off>
                 (low hides counts, med collapses rows, high expands rows;
                  mouse is on by default so the wheel scrolls the transcript,
                  click-drag selects it, and clicks open rows or answer keys.
                  Off hands selection back to the terminal. Ctrl+X flips it and saves it)
                 terminal  what this terminal answered when shhh asked what
                           it can do: inline images, desktop notifications,
                           focus events, cell size
  /add-dir       The working scope: which directories this session may write
                 to. Bare lists it; <path> adds one (contained commands can
                 write there, and edits there stop asking about leaving the
                 scope); drop <path> takes it back
  /sandbox       Containment status and container sandboxes (doctor|scope|list|status|destroy <id>|prune)
  /evidence      Tool-output evidence store: reduction stats and size (purge to clear)
  /gate          Quality gate: run [suite] starts the project's checks in the background, result shows the verdict, on|off runs them as a turn closes
  /ps            List the long-running processes this session owns (process tool)
  /memory        Durable memories: list (default) · add [global] [kind] <text> ·
                 edit <id> (opens the entry in your editor) · forget <id>
  /todo          The project's backlog (.shhh/todo): bare opens a picker · show|edit <slug> ·
                 add (reads this session into proposed items you accept or drop) ·
                 add <text> · block <slug> [why] · open|done|drop <slug> ·
                 run [slug|--next] works an item through to a commit in auto mode ·
                 status · stop
  /init          Scaffold this project's .shhh/ context file — the card lists
                 what it would write, and nothing is written until you say so.
                 The start screen offers it in a checkout that has no .shhh
  /skills        The skills this session loaded (SKILL.md directories), and
                 why any did not
  /skill <name> [task]
                 Activate a skill now: its instructions go to the model with
                 your task, as the model would load them itself. /<name>
                 does the same for a skill whose name is not a command
  /agents        Agent manager: attach, steer, cancel, kill sub-agents (also ctrl+b)
  /agents new [brief]
                 Draft an agent profile from a sentence with the model's help:
                 answer its questions if it has any, then keep, refine or
                 discard the draft on a card. Bare offers starting points
  /attach [name] Attach to an agent's session and steer it (bare /attach lists)
  /detach        Back to your own session (also Esc while attached)
  /plan          The approved plan as a checklist, with anything that has
                 departed from it · save [name] writes the last plan/response
                 to .shhh/plans/ · drop forgets an approved plan
  /diff [path]   Show what this session changed, full screen, or one file's —
                 read from the session's own changeset, so it works outside a
                 git repository
  /review [turn] Review what a turn changed: file list, hunks, staging per
                 hunk (bare reviews the last turn that changed anything).
                 Also [v] on a turn's changeset row. Nothing is applied.
  /undo [turn]   Put back what a turn changed, from the session's own records
                 (not git). Asks first, names anything that changed since,
                 and is itself recorded as a turn. Also [u] on the row.
  /compact       Continue from a summary plus the most recent turns
  /rewind [n]    Rewind to before a user turn (bare /rewind picks interactively);
                 the abandoned tail is kept as a branch. Conversation only —
                 files on disk are not restored.
  /branches [n]  Switch this session's branches (bare /branches opens a picker)
  /save [name]   Save this chat
  /load [name]   Load a saved chat (bare /load opens a picker)
  /chats         Saved chats — opens the same picker; enter loads, [x]
                 deletes (asks first), [r] renames
  /exit          Quit (also /quit, /q)

Commands run while the agent is working — including while sub-agents are in
flight, which is the only time they exist. The exceptions are the ones that
rewrite or replace the running conversation (/compact, /rewind, /branches,
/load, /chats, /model, /run); they say so and wait for the turn. /clear asks
instead: ending the session over a turn that is not over cancels it, which is
a question rather than a wait.

`) + "\n\n" + helpKeysText()
}

// helpKeysText is /help's key section on its own: what `?` on an empty draft
// prints as a system row, so the door Claude Code taught opens the same list
// /help holds. Every key the input offers has a row below and every spelling
// in the column is the register's, so a rebind moves the list with the
// handler and a key added to the register with no row here fails the test
// rather than quietly not being in the help.
func helpKeysText() string {
	var b strings.Builder
	b.WriteString("Keys:")
	for _, r := range helpKeyRows {
		col := r.column()
		for i, line := range strings.Split(r.text, "\n") {
			head := ""
			if i < len(col) {
				head = col[i]
			}
			b.WriteString("\n  " + head +
				strings.Repeat(" ", helpKeyWidth-utf8.RuneCountInString(head)) + line)
		}
	}
	return b.String()
}

// helpKeyWidth is the key column. It is wide enough for the longest spelling
// a row shows as one line (`ctrl+a ctrl+e`) plus the two spaces that separate
// a column from its prose.
const helpKeyWidth = 15

// helpKeyRow is one row of the key list: which keys it is about, and the
// paragraph beside them. The paragraph's own line breaks are kept — several
// of these are two thoughts and not one long one — and each of its lines
// after the first is indented under the prose column.
type helpKeyRow struct {
	// binds are the register bindings the row is about. The column is their
	// spellings, so a rebind moves the list with the handler, and the test
	// that holds this list to the register reads them.
	binds []keys.Binding
	// sep joins the spellings when the row is about more than one binding: a
	// space for two chords that are one gesture, a slash for the two ends of
	// one act, and a newline for a pair that gets a line of the column each.
	sep string
	// key is the column when the register does not spell it the way the list
	// reads it — the recall arrows, whose glyphs beside "Recall previous
	// inputs" read as decoration rather than as a key — or when the row is
	// about something the register does not bind at all: a leading character,
	// a paste, or the mouse.
	key string
	// text is the paragraph.
	text string
}

// column is the row's key column, one entry per line of it.
func (r helpKeyRow) column() []string {
	if r.key != "" {
		return strings.Split(r.key, "\n")
	}
	shown := make([]string, 0, len(r.binds))
	for _, b := range r.binds {
		shown = append(shown, keys.Shown(b))
	}
	switch r.sep {
	case "":
		return shown[:1]
	case "\n":
		return shown
	default:
		return []string{strings.Join(shown, r.sep)}
	}
}

// helpKeyRows is the key list, in the order a reader meets the keys rather
// than the order the register declares them: what sends a message first, then
// what the draft does, then what takes the screen, then the ways out.
var helpKeyRows = []helpKeyRow{
	{
		binds: []keys.Binding{keys.Draft.Send, keys.Draft.Newline},
		text: `Send message        shift+enter  Insert newline
(ctrl+j does the same, for terminals that cannot report
 shift+enter; so does alt+enter while nothing is running.
 A draft ending in \ turns enter into a newline too, the
 shell's own continuation — end in \\ to send a literal
 backslash)`,
	},
	{
		binds: []keys.Binding{keys.Draft.FollowUp},
		text: `While a turn is live, queue the draft as a follow-up sent
when the turn completes. Steering (enter) joins the running
turn; a follow-up waits for it to end. After a cancel the
queue is held rather than sent — the notice rail says so`,
	},
	{
		binds: []keys.Binding{keys.Draft.PullQueued},
		text: `Pull the newest queued message — a follow-up first, else a
steering line — back into the draft`,
	},
	{
		key: "@",
		text: `At the start of a word, open a file menu over what this
session changed and the checkout's recent files, filtered
by what you type after it. tab or enter inserts the path,
esc keeps what you typed; a mentioned image is staged the
way a pasted one is`,
	},
	{
		key: "!",
		text: `A draft starting with ! runs as a command through the same
confirm card /run uses; !! runs it and keeps the output out
of the conversation (its row says local). A ! anywhere else
is a letter`,
	},
	{
		binds: []keys.Binding{keys.Draft.Attach},
		text: `Attach the clipboard: a copied screenshot or file is staged
for your next message, ordinary text still pastes into the
draft. Dragging an image into the terminal attaches it the
same way. What is staged shows as chips above the input`,
	},
	{
		key: "Pasting",
		text: `Text taller than 10 lines or wider than 1000 columns is
staged as paste-1.txt rather than typed into the draft — both
through ctrl+v and through your terminal's own paste — so a
stack trace does not bury the sentence it came with. Those
two numbers are the defaults for appearance.paste_lines and
appearance.paste_columns; shhh config shows this machine's,
and a negative turns one of them off. /paste show
paste-1.txt reads it back before you send it, and a paste
over 256 KB is refused rather than staged — it would ride in
the prompt itself`,
	},
	{
		binds: []keys.Binding{keys.Draft.Complete},
		text: `Complete a slash command (typing / opens the menu;
↑↓ move, enter runs the highlighted command, esc dismisses)`,
	},
	{
		key: "ctrl+a ctrl+e\nctrl+k ctrl+u",
		text: `The draft is a readline editor: line start and line end,
kill to end and to start of line; ctrl+w deletes the word
before the cursor, alt+b and alt+f move by word`,
	},
	{
		binds: []keys.Binding{keys.Draft.Palette},
		text: `Command palette: one prompt over commands, saved chats and
the files this session touched — type to filter, enter runs,
tab writes it into the input, esc dismisses. A terminal that
cannot send this chord — it is a single byte and Windows
conhost sends nothing for it — reaches the same list through
the other door: / on an empty draft, then tab`,
	},
	{
		binds: []keys.Binding{keys.Draft.Pause},
		text: `Hold the turn between rounds, and press it again to let the
turn go on. The hold waits for the round in flight to finish,
because a stream nobody is reading backs up until the
provider gives up on it — so the rail says "holding after
this round" and then "held". Nothing is re-asked and nothing
is lost: what you type while it is held rides out with the
round it resumes into, ctrl+z is accepted, and quitting and
coming back with --continue opens the conversation held. It
reaches every agent this session started, each at its own
boundary, and one press lets them all go`,
	},
	{
		binds: []keys.Binding{keys.Draft.HistorySearch},
		text: `Search the input history: an incremental reverse search over
what you typed before. Typing filters, ctrl+r again steps to
an older match, enter keeps the match in the draft, esc puts
the draft back exactly as it was`,
	},
	{
		binds: []keys.Binding{keys.Draft.Reasoning},
		text: `Cycle the reasoning level: off → low → medium → high. It
changes the next model request, not the one in flight, and
the level is stated on the vitals rail beside the model`,
	},
	{
		binds: []keys.Binding{keys.Draft.Mode},
		text: `Cycle the permission mode
(while the agent is working, enter queues a steering message
 that joins the conversation before the next model request)`,
	},
	{
		binds: []keys.Binding{keys.Draft.HistoryPrev, keys.Draft.HistoryNext},
		key:   "up/down",
		text:  `Recall previous inputs (when the input is empty)`,
	},
	{
		binds: []keys.Binding{keys.Draft.ScrollUp, keys.Draft.ScrollDown},
		sep:   "\n",
		text: `Scroll the transcript a line, without leaving the prompt —
the draft keeps the keyboard and every letter it has
(ctrl+up and ctrl+down do the same, for terminals that
 report them)`,
	},
	{
		binds: []keys.Binding{keys.Draft.Reading},
		text: `Reading mode: select transcript rows (j/k, u/d half a page),
expand/collapse (enter), y copies the row under the cursor —
a command as $ cmd over its output, an edit as its unified
diff, a message as markdown source, a folded group member by
member — / searches the transcript and n/N walk what it
found, pgup/pgdn page, ? lists every key the mode has,
esc or typing returns to the prompt
(enter on an edit row cycles collapsed → expanded → full-screen
 diff, and on a command or read row the same three depths over
 its output, the whole of it scrollable at the last one;
 opens over a running turn, which keeps streaming underneath;
 a transcript with nothing selectable opens as a plain pager.
 /step opens the in-flight step's detail from the prompt)`,
	},
	{
		binds: []keys.Binding{keys.Draft.Agents},
		text: `Agent manager: enter attaches to an agent's session, x cancels
its turn, X kills it; attached, typing steers the agent,
shift+tab sets its mode (clamped), esc detaches`,
	},
	{
		binds: []keys.Binding{keys.Draft.NextAgent, keys.Draft.PrevAgent},
		sep:   " ",
		text: `Move the keyboard one session along the inspector rail's
AGENTS map — the orchestrator and every agent it started, in
the order they were started, wrapping at both ends. The rail
stays up while you are in an agent's session and marks the
row you are in; everything you do *to* an agent is still in
the manager`,
	},
	{
		binds: []keys.Binding{keys.Draft.Editor},
		text: `Open the draft in your editor: $EDITOR (then $VISUAL, then
vi) opens a file holding what you have typed, at the line and
column the cursor was on, and whatever is in the file when
the editor exits becomes the draft. An empty file leaves the
draft alone. Not while a turn is running or a decision is
waiting — the editor takes the terminal with it`,
	},
	{
		binds: []keys.Binding{keys.Draft.Suspend},
		text: `Suspend shhh and go back to the shell; fg brings it back
with the screen as you left it. Refused while a turn is
running or a decision is waiting — a stopped shhh is not
reading the stream it asked for`,
	},
	{
		binds: []keys.Binding{keys.Draft.Redraw},
		text: `Redraw the screen from what the session already holds, for a
display something else wrote over. The draft, the history
and any selection are untouched`,
	},
	{
		binds: []keys.Binding{keys.Draft.Answer},
		text: `Hand the keyboard to a decision waiting on screen. An
approval that lands while you are typing does not take your
keys with it: its y, n and a are not live until this chord
gives them the keyboard, and until then every letter goes
into the draft. Esc leaves the decision waiting; n is how
you say no. ctrl+y does the same thing, for terminals and
desktops that never deliver ctrl+space — macOS binds it to
the input-source switcher and takes it first`,
	},
	{
		binds: []keys.Binding{keys.Draft.KeyList},
		text: `On an empty draft, print this key list; with any text in the
box it is a letter like any other`,
	},
	{
		binds: []keys.Binding{keys.Draft.Clear},
		text: `Go back: clear the input, dismiss the completion menu, drop
a selection, detach one level, leave a waiting decision
waiting. It never stops a running turn — on an empty draft
under one it does nothing, so ctrl+c is what you want there.
On an empty idle draft, esc esc opens the /rewind picker`,
	},
	{
		binds: []keys.Binding{keys.Draft.Cancel},
		text: `Cancel the running turn — press twice, and what the turn
already did is kept. Also clears the input, and quits from
an empty idle draft (twice again)`,
	},
	{
		binds: []keys.Binding{keys.Draft.Quit},
		text: `Quit — press twice; with a turn running it asks first,
saying what is cancelled and what the autosave keeps`,
	},
	{
		binds: []keys.Binding{keys.Draft.PageUp, keys.Draft.PageDown},
		sep:   "/",
		text: `Page the transcript, leaving the keyboard in the prompt.
Scrolling away pauses the follow while a turn streams; the
notice rail counts what is below and pgdn walks back to it`,
	},
	{
		binds: []keys.Binding{keys.Draft.Mouse},
		key:   "Wheel",
		text: `Scroll the transcript (or the full-screen diff / review),
leaving the draft and the keyboard where they are
(needs ctrl+x — off by default, so the terminal keeps its
 own click-drag selection)`,
	},
	{
		key: "Click-drag",
		text: `With the mouse on (ctrl+x), select transcript text: the drag
scrolls the pane when it reaches an edge, so a selection can
run past the screen; releasing copies it, esc cancels`,
	},
	{
		key: "Click",
		text: `A press and release in the same cell opens the activity row
under it, the way enter does in reading mode, or answers the
key it lands on in an approval card's [y/n/a]. It never takes
the keyboard: the draft keeps every character`,
	},
	{
		key: "y/n/a",
		text: `Approval prompts: allow / deny / always allow this session.
A card taller than its panel counts what is cut and scrolls
on shift+↑/↓ (shift+←/→ pan a wide body); d opens an edit's
full diff, or a command card's full view`,
	}}
