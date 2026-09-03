# Surfaces

What each surface is *for*, and when the session puts it in front of you. How
each one is drawn is normative in the `shhh Design System` project in Claude
Design; the rules every one of them obeys are
[`principles.md`](principles.md).

Surfaces are grouped by weight, which is the same order as
[weight tracks risk](principles.md#weight-tracks-risk): rows are the cheapest
thing on screen, cards are the most expensive, and the escalation from one to
the other is always a claim that a decision is being asked for.

## Rows

### The activity row

The unit the transcript is made of. Every act the session takes is one — a
read, a search, an edit, a command, a spawned child, a failure, a folded group
of them.

Because it is one shape, a reader learns it once. A provider failure using the
same row as a file read is the point rather than an accident: a failure is
part of the turn, not an interruption of it.

A row's output is bounded, and the bound is a fold rather than a loss: a body
cut at the cap ends by counting what it swallowed. Opening the row widens the
window in place — enough to read a failed test run whole — and opening it
again gives the whole output the screen, scrollable, the same three depths an
edit's diff has always had. A window that already shows everything skips the
screen, because a press that changes nothing is a press wasted. Command
output, a read's file contents and a search's matches all open this way, and
at every depth what a program painted is re-painted into the palette, so
nothing arrives with colours of its own.

The three depths are three presses of one key, and a pointer reaches them by
position instead: the row line opens and closes the window, and a click in the
body under it takes that body whole. A pointer has a cell to spend where the
key has only another press, and spending it this way is what keeps a click
undoable by the identical click — a second press that took the screen would
not be giving the row back.

### The think row

What the model thought before it acted, folded into one row among the acts it
led to. A round that reasoned gets one; a round that did not gets nothing,
because a row reporting no thinking is a stat nobody measured.

It is a row rather than a panel because thinking is one of the things a turn
did, and the transcript has one shape for those. The row states how much it
swallowed, so folding hides the words without hiding that there were words,
and it opens through the diff's three depths: closed, the end of the thought,
then all of it. The end rather than the beginning, because a model that
thought for four hundred lines is being read for where it arrived. A block
short enough to fit the window skips that middle step.

Opened, it wraps rather than clips. Every other body under a row is the
output of a program, where the head of a line is the information and the tail
can be cut; this one is prose, where a paragraph is a single line hundreds of
characters long and cutting it keeps a sentence and loses the thought.

The row also ends the group above it, because the model stopping to think is
where one round of work ends and the next begins. What follows stands as its
own rows until the model says what it is doing: a step is a titled group, and
private reasoning is not a title anybody asked for. So a round that thinks and
then works without a word costs the fold its calls would otherwise have had —
which is the cheaper of the two mistakes, because the other one was a fold
that hid the thinking and did not count it.

The row fills while the model thinks, so the wait is legible as work rather
than as a spinner. Thinking is the model talking to itself: it changed
nothing, ran nothing and read nothing, which is why it carries no rail and
sits at the bottom of the weight order. The least dense verbosity drops it
first, for the same reason.

What is shown is only what the provider let through. Reasoning that comes back
redacted, or as a signature with no words, is carried into the next request
and shown as nothing — there is nothing to show.

### The diff view

An applied edit is one row until you open it. Opening it shows the change in
place, bounded; opening it again gives it the whole screen. By pointer, as
with any row: the row line toggles the in-place view, and a click on the
change itself is what opens the screen.

Three depths rather than two, because the middle one is the common case:
enough to see what changed without losing the transcript around it.
Highlighting is the session's own and the diff colouring layers over it, so a
diff in a card and a diff in the transcript are the same object at different
sizes.

The same view backs the edit approval card — what you are approving is what
you will see afterwards.

### The step

A titled group of consecutive rows. A forty-tool turn is four lines until you
ask for more.

Where the session declared a plan, the steps are the plan's — same numbers,
same titles — so the outline, the plan block and the plan command are reading
one list rather than three that agree by coincidence. Work done off the plan
is marked as such rather than renumbered into it, because renumbering would
hide the fact that it happened.

Where nothing was declared, the prose that preceded a batch of calls becomes
the title. Where there is no structure to find, the transcript is a flat list
and no empty grouping chrome is drawn.

### The turn's close

The question after an agent stops is never "what did it say", it is "what did
it change". So a turn closes on what it did, what changed, and whether the
tests still pass.

The changed-files row carries the mutation rail, so the close of a turn looks
like the rows that produced it. It also carries what git knew about those
files when they were written — which is a statement about the past, not a
promise about what can be undone; that promise is the approval card's job.

Any turn can be put back, and putting one back is itself recorded as a change
that can be reviewed and put back in turn.

### The recovery row

Most of a tool's reputation is made in its failures. Every one of them is an
ordinary row plus one offered key.

The row names the model and then the class; the outcome is the one thing that
decides what to do next, never a repeat of the class. The provider's own words
appear underneath, bounded — which is why "unclassified" is a class rather
than an error path. A message we could not name still gets said.

Two failures earn a card instead, and only two: the ones that stop the session
dead.

## Panels

### Reading mode

The transcript gets a cursor, and the keyboard moves to it. This is the one
mechanism behind every "expand" in the session, which is what lets the input
keep every other key.

It is also where rows that offer keys without expanding are answered — a
turn's changeset, a provider failure. Both are passive renderers; holding
their keys here is what keeps `v`, `u`, `r`, `c`, `e` and `p` available for
typing.

One key copies the row under the cursor, shaped by what the row is: a message
as its markdown source, a command as the command over its output, an edit as
the unified diff, a read as what the read returned, a folded group member by
member. What a program painted is stripped on the way — the escape codes are
this terminal's, not part of what was said — and the copy rides the same
clipboard path `/copy` and the drag selection use. The mode's rail captions
what was caught and how far it ran, until the next key says the reader has
moved on. Copy is why a message is an addressable row here at all: it expands
nothing, and it holds the one thing most worth carrying away.

Half-page keys move the cursor through a long transcript at a pace that keeps
context — half the pane per press, with the cursor following the pane rather
than staying lit on a row nobody can see.

### The input frame

Where you type, and where the session's vitals live. Its borders carry
information rather than being dead lines: the running turn's live account of
itself on the top rail, the session's counters on the vitals rail, and
contextual key hints on the bottom rail that change with what the session is
doing.

The top rail states one turn's four facts — which phase it is in, how long it
has been there, the tokens it has spent and what they cost — and it states
them while they are still moving: before the provider reports a request's
usage, the prompt is the context estimate and the output is the reasoning and
the prose as they arrive, replaced by the reported count the moment there is
one. Nothing else on screen says the same thing twice: the phase is named
here, not also under the transcript.

It states them at the rail's near corner, two rows above the prompt glyph,
because the account is the one thing on the frame that moves and the eye
watching it is already on the cursor. Against the far edge of a wide terminal
the same figures sit a hundred columns from anything the reader is looking
at. The far side carries the identity instead: nothing at the root session,
where the header above the transcript already names the surface, and attached
to a child agent the breadcrumb — there the rail is the one place that says
which session the keyboard is in. A rail with room for only one of the two
keeps the account, because the breadcrumb answers a question a key can ask
again and the numbers are why the rail carries labels at all.

A figure that changes climbs to its new value over about half a second rather
than cutting to it, on the same tick that draws everything else moving on the
frame. A cut says that a number changed and never by how much, and by how much
is the whole question at the token scale. Nothing climbs that the session has
not measured: through a tool round with nothing streaming, the counts hold.
While a turn is spending them they print every digit, because a hundred tokens
of movement vanish inside the rounding that makes `41.2k` the right shape to
carry a finished session in; once nothing is moving them they go back to it.

The session's counters on the vitals rail carry the running turn's estimate
the same way, so the two rails are one account rather than two: what the
session has spent is what the earlier turns cost plus what this one is costing,
and a request's report replaces that turn's estimate instead of being added to
it.

Above it, a notice rail exists only while there is something to say and
disappears when there is not. Under that, on the terminals too narrow for the
[inspector rail](#the-inspector-rail), the status row that stands in for it.
Below both, a staged rail carries whatever is waiting to ride out with the
next message — it sits against the box because what is staged leaves with the
sentence being typed, and the notices do not. Each chip says what the thing
is, what it is called and how big it is, and for text how far it runs, because
a size answers *will this fit* and never *which of these is the stack trace*.

A paste past a certain size stops being a sentence and becomes one of those
chips. A log or a stack trace typed into a three-row box buries the sentence
it was meant to go with, and scrolling a draft to find the question you were
asking is not composing — so past ten lines or a thousand columns the paste is
staged as a file of its own and the box is left for the words. Both thresholds
are settings, because how much text a person can hold in a draft is a fact
about their terminal and their eyes rather than about shhh. Every door onto
the staging area reads them, because which key the reader used to paste is not
a fact about how much text they pasted.

A paste too big to stage is refused with the limit named, and the draft is
left exactly as it was. What bounds it is not the size a message can carry but
the window it will be read in: a paste has no file behind it, so it goes into
the prompt itself rather than being fetched when it is needed. Typing it in
after all would put a megabyte in the box that the reader then has to get back
out, and the bytes are still on the clipboard either way.

Typing while the agent works is steering, not a queued prompt, and the gutter
says which of the two you are doing. The queued prompt exists too, behind a
chord of its own: a follow-up waits for the turn to end and goes out as the
next message, where steering joins the conversation mid-flight. The notice
rail counts the two queues separately, because "change what you are doing"
and "when you are done, then" are different promises. A cancel does not send
what was queued behind it — the follow-up was written against work that was
just abandoned — so the queue survives, marked held, and one chord takes a
line back into the draft for the reader to decide what still applies.

Two prefixes turn the draft into something other than a message, because
every other harness taught the same two. A word starting `@` opens the
completion menu over files — what this session changed, then what the
checkout touched most recently, never what its ignore file hides — and
choosing a row writes the path into the sentence and nothing more: the
model reads files through its tools, so a mention is a name, not an
attachment. An image is the exception, staged like a pasted one, because no
tool reads an image. A draft starting `!` is a command, and it goes through
the same confirm card `/run` uses — nothing runs unseen, whichever door it
came in by. Doubling the bang keeps the command's output out of the
conversation entirely: the transcript shows it, the model never sees it,
and the row's outcome says so. The gutter swaps its glyph while the draft
is in bang form, for the same reason it does while typed text is steering.

A draft that ends in a backslash holds its send: enter eats the backslash
and breaks the line instead, which is what that character has meant at
every shell prompt the reader has ever typed a continuation into. A
doubled backslash sends, carrying the one literal character it spells.

The draft edits like the shell's own line. The chords a shell user's hands
already know — line start and end, kill to end and to start, delete a word,
move by word — reach the text rather than opening surfaces, because muscle
memory that opens something you did not ask for is a key working against its
owner. The same allegiance gives the shell's reverse search a home here:
one chord opens an incremental search over what was typed before, typing
filters it, the chord again steps to an older match, and the search states
itself on a row under the draft — the same row the completion menu uses,
because both are the input explaining what the next keystroke will do to it.
Backing out restores the draft exactly as it was.

An empty draft answers two gestures the other harnesses taught. A double
Esc, idle, opens the rewind picker — going back is a gesture, not a command
to remember — and a question mark prints the key list as a transcript row.
Both keys stay ordinary the moment there is any text in the box: the input
owns every ordinary key while a sentence is being typed.

When a release moves keys, the first launch after the upgrade says so: one
row on the notice rail names the new homes, once, and never again. A rebind
paid for silently is a session of surfaces nobody asked for; a notice
repeated on every launch is one nobody reads.

A draft too long to compose in three rows leaves for your own editor and comes
back: shhh writes what you have typed to a file, opens the editor on it where
the cursor was, and takes whatever the file holds when the editor exits. An
empty file is not an instruction to throw the draft away, so it leaves it
standing. The editor has the terminal while it runs, which is why the key is
refused rather than queued while a turn is in flight or a decision is waiting
— neither can be watched from inside somebody else's editor.

Two chords belong to the terminal rather than to the session, and shhh
answers both because a terminal in raw mode will not. One suspends shhh back
to the shell, and it is refused while a turn is in flight or a decision is
waiting, for the reason the editor is: a stopped process is not reading the
stream it asked for, and a request that times out while nobody is there is
worse than being told to press the key again in a moment. The other redraws
the screen from what the session already holds — the way back from a display
something else wrote over — and it changes nothing: the draft, the history and
any live selection are the same afterwards, because none of them lived on the
screen.

Stopping is not the same act as abandoning, and the frame offers both. A turn
can be held between rounds: the key marks it, the round in flight finishes,
and the turn parks with everything that round put in the conversation still
in it — so leaving and coming back costs one keystroke rather than a cancel
and the whole question asked again. What cannot be paused is the round itself.
An open stream is a socket somebody has to keep reading, and a reader that
stops backs it up until the provider gives up on the request, which is the
same reason suspending is refused while a turn works. So the rail says
"holding after this round" first and "held" after, because those are different
promises, and it wears the mark a waiting decision wears: both mean the
session has stopped and is waiting on you. A held turn is idle in every other
way — suspending is accepted, because there is no stream to abandon — and what
is typed while it waits rides out with the round it resumes into, the way
anything typed mid-turn does. The hold reaches every agent the session
started, each parking at its own boundary rather than where it stands, and one
press lets them all go.

A conversation quit while held comes back held. The slot remembers that the
turn was parked and where it had got to, so reopening it is the same place
rather than an idle prompt with an unanswered round in front of it.

Abandoning work is never one keystroke. A turn in flight is minutes of work,
and the keys that end things are the keys a reflex produces — so the first
press of an interrupt opens a short window and the rails say what a second
press will do; only the second press inside the window cancels, and a window
that expires costs nothing and says nothing. The cancel chord is that
interrupt and the only key that is: Esc means go back, and a key that backs
out of a diff, a menu and a selection cannot also be the one that abandons a
turn on the press where the draft happened to be empty. So an interrupt costs
two presses of a chord no reflex produces, and interrupting keeps everything
the turn already did. Quitting from an idle session takes the same two
presses. Quitting over a live turn is a real question rather than a window:
the inline confirm states what will be cancelled and what the autosave keeps,
and the default is No. Keys that end something already scoped — a running
command, the permission classifier, a decision on its card — keep their
single press, because those are reversible acts, not abandoned work.

### The completion menu

A slash at the start of the draft opens the registry of commands under the
box, and completion does not stop at the command's name: a command whose
arguments are a known set — the model catalog, the saved chats, the branches,
a fixed list of subcommands — offers them for the token under the cursor.

Tab writes the focused row into the draft, ↑↓ move, and esc dismisses the menu
until the draft changes again.

Enter is the one key with two readings, and which it has is decided by what
the reader has done, never by what the menu happens to be showing. A menu
narrowed to a choice — a typed prefix, or a row arrowed onto — is a choice,
and enter takes it. A menu that opened on an empty token is a list of what
*could* follow, and enter belongs to the line as it stands: completing
`/model` and pressing enter opens the model picker, because that is the line
in the box, rather than switching to whichever model sorts first. The hint row
names the line it would run, so the two readings are never guessed at.

The file mention's menu is the exception that proves the rule: nothing there
is ever run. Enter writes the path into the sentence, which is still being
written.

### The inspector rail

Past a width threshold, a rail on the right answers the standing questions —
what is this turn doing, what has this session changed, what is it costing —
so you stop running commands to recover what the session already knows.

One block is scoped to the turn and the rest are scoped to the session, and
both kinds say their scope in words, because two of them count files and would
otherwise be read as contradicting each other. A file edited in turn 2 is
still on screen in turn 8: "what has this session done to my machine" does not
reset when the agent starts a new turn.

One block is scoped wider than the session: the project's backlog. It sits
under the plan because it is the same question one step further out — the
plan is what this turn is going through, the backlog is what is queued
behind it — and it shows the first few items in working order with what each
one waits on, then counts the rest. The whole list is one command away, and
the block says which.

One block is a map rather than a measurement: every session this run has,
the root and each agent it started, in the order they were started. Each row
carries the state it is in, what it has spent, and — once it has stopped —
the word it ended on, because a run whose finished half is only recoverable
by scrolling is a run you have to reconstruct to see. Finished agents fold
past a count rather than disappearing and the marker says how many went
behind it; what needs an answer from you never folds.

One row of the map is marked, and the mark is where the keyboard is. That is
what lets the rail stay up while the keyboard is in an agent's session: the
changeset, the window and the bill are the whole session's whichever agent is
on screen, and the mark is what stops them being read as that agent's. A
chord walks the map, in both directions and wrapping at both ends, so moving
between sessions is a keystroke rather than a surface to open and close.
Everything you do *to* an agent — answer it, retry it, cancel it, kill it —
is still the manager's; the map is for seeing and moving.

Two of the rail's lists are places to go from rather than only to read. A
click on a changed file opens that file's diff full screen, and the same
click closes it again; a click on a session moves the keyboard into it, and a
click on the row already marked comes back. Both have the key that reaches
them by name already — the file's diff is a command with a path, and the map
is walked by a chord and by the manager — which is the test a target has to
pass here: the pointer names exactly one thing, and the thing it names is
reachable without a pointer. Everything else on the rail is inert, including
the headings and the blocks that do have a surface behind them, because a row
that opened a whole surface would be somewhere the same click could not
leave. Nothing on the rail takes the keyboard: the draft keeps every
character it had.

One block is not about the work at all: where the session's tools came from.
A server that failed to answer leaves no trace in a transcript — a tool that
was never registered is indistinguishable from one the model chose not to
call — so the sources say whether they are up in a glyph and a word, with the
count of tools each brought or the one thing standing in the way. It is
present only when something outside shhh was configured, because a session
with nothing but its own tools has no way to have lost any, and it folds past
a few rows: whether what was configured is up is the question, and the whole
listing is a command away.

The rail takes the room a wide terminal gives it. Its width is a rule rather
than a number: it is at its narrowest at the threshold, and above that it
grows by about one column for every four the surface gains, up to a ceiling.
Both ends of that are deliberate. The transcript keeps the larger share at
every width, because it is what is being read; and the ceiling is where the
blocks stop having anything to do with the columns — past it a path is already
whole and a meter is already a bar rather than a shape, so more room would be
gap. What the rail gains goes to the blocks: the meters and the burn run get
longer, and a file path spends the columns before its counts are clipped,
because the counts are the number and a clipped number is a wrong one.

The width can also be set, for the person whose rail has to fit a pane they
chose the size of. A number is held to the same two limits the rule is —
the rail's own floor, and what the surface has room for at this width — and
the readout names which of the two moved it, because they are different
answers to "why is this not the number I typed": one goes away on a wider
terminal and the other never does. Setting it is a session command and a
configuration key, and both go through the same rule, so a terminal that is
resized still lands somewhere the rule allows.

Below the threshold the rail is dropped rather than compressed — but one row
stands in for it above the input, in the vitals grammar the frame's own rails
use: what the last reading of the session said and the round it was taken at,
and what the running turn or the whole session has changed. It drops its
clauses from the right as it runs out of columns, and it is absent when there
is nothing to say, so a narrow terminal is never carrying an empty row. The
reading in full is still one command away, and asking for it is what forces a
current one.

### The session summary

The rail answers the standing questions in numbers. It cannot answer the one
you ask after looking away for five minutes — *what is this actually doing,
and is it still doing what I asked* — and reconstructing that means scrolling
the transcript, which is the work the rail exists to remove.

So every few rounds a cheap model reads a digest of the session and writes the
two sentences the numbers could not: what it is doing, and whether that is
still what was asked.

It is scoped to the turn and stamped with the round it was taken at, because a
new instruction is a new target — last turn's narrative held on screen while
the agent works on something else would be exactly the stale status the block
exists to prevent. A finished turn's last reading does stand while the session
is idle; that is the one you come back to the terminal for.

**A failed reading changes nothing.** The previous summary stands and is
marked stale. This is the one place the classifier's rule is deliberately
inverted: the approval classifier
[fails closed](../capabilities/approvals-and-safety.md#the-classifier-fails-closed)
because a wrong yes is unsafe, and the summariser fails soft because a status
block that vanishes when one request times out is a block nobody trusts again.

**Every reading is also a transcript row.** The rail holds one reading and
bounds it to three lines, which is what a rail is for — it is a column of
standing status, and a block that grew would push the counts under it off the
screen. But a longer reading is then a sentence nobody can finish, and the
reading before it is gone entirely. So each reading lands in the activity feed
as one folded row as well: closed it is the round it was taken at, its verdict
and how many lines opening it costs; opened it is the reading whole, the
verdict in the same marks the rail uses, the reason behind a departure, and
the instruction the verdict was reached against — the last of which the rail
never had room for at all.

It is every reading rather than the latest one because the readings in order
are the run's own account of itself. What it believed it was doing at round 6
and again at round 24 is then a thing the transcript can be scrolled for,
which is the reconstruction the rail exists to remove and could only ever
perform for the present moment. A failed reading still writes nothing: the
rail keeps what it had, and a line reporting that one request timed out is not
news.

### The agent manager

Sub-agents are visible and steerable while they run: what each is doing, how
far in, what it is waiting on. Attaching to one is not a new surface — it
switches which agent the session is looking at, and every agent including the
root is the same kind of thing.

A child's approvals route to wherever you are, so detaching does not mean
missing a decision.

Attaching does not take the inspector rail with it. What you are looking at
is one agent's transcript; what the rail reports — what this run has changed,
how full the window is, what it has all cost — is the whole session's, and it
is the same whichever agent has the keyboard. Its map is what says which
agent that is, by marking the row, so nothing on screen has to be read twice
to work out whose numbers are whose.

The list ends with the one row that is not an agent: the offer to draft a new
profile. The manager is where a person goes to find out what this session has,
which makes it the one place where *and none of these is what I want* is a
thought somebody is already having, so the answer to it is a row rather than a
command they have to know. It is offered only where drafting is wired, and the
keys that act on an agent are silent over it.

## Cards

### The approval card

The single surface for every approval-gated action, in three body variants:
a command, an edit, and everything else.

Every card answers three questions before it offers a key: what the action
touches, whether shhh can take it back, and whether the network is open. A
prompt that says only what the action *is* asks the reader to do the risk
assessment themselves, at speed, twenty times a session — and they will stop.

Severity leads as a word. Resolution is honest about its limits: where the
blast radius cannot be determined, the card says so rather than reporting a
confident nothing. What the containment profile allows is reported from what
is actually in force, not from what was configured.

A card can outgrow the panel it is allowed, and what does not fit is
never merely clipped. The body scrolls in place behind counted tails — the
last visible line says how many more rows there are and names the key that
brings them — and a body wider than the panel pans by columns, a line still
running past the edge ending in a marker that says the rest is one press
away. The decision run and the stated way out never move, because a decision
whose keys can scroll off is not one. The scroll describes one card's body
and starts over with the next card. The full view is one key on a command
card too, not only an edit's diff: the whole command, its warnings and its
blast radius take the screen the diff already knows how to take, and give it
back with the decision still waiting.

Almost every card arrives unasked, and the rest of this section is about that
one. The exception is a card the reader summoned — the offer to write
something the session proposed, taken up by a command or a suggestion — which
is a takeover like any other summoned surface: it holds the keyboard from the
moment it opens, because the reader was looking at it before it was there,
and there is no draft behind it for a letter to belong to. That also changes
what its no means. A card that arrived has nowhere to send the reader back
to, so declining and leaving are one answer on one key; a card that was
summoned has the screen the reader left, so esc goes back to it and settles
nothing, and the letter is the answer that does.

A card arrives when the agent needs it, which is not when the reader is ready
for it. What it may do to a half-typed draft, and when its letters become live
keys at all, is governed by
[invariant 5](principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
With a sentence in the box the card arrives inert and the letters stay the
sentence's; over an empty box it takes the keyboard, because there is nothing
for a letter to belong to.

A keyboard still warm is the one case between: nothing to protect in the box,
but keys may be in flight — a reflex, the tail of a buffered burst. The card
still takes the keyboard, and for a grace window its decision keys are
discarded rather than answered; the run draws dimmed and says the keys are a
moment away. The window ends when the keyboard has been quiet for a beat, and
at a hard cap however the typing goes, so the decision is never locked away.
Three keys stay out of it: the chords no sentence can produce keep denying and
gating, and esc keeps its way back to the draft, because the safe answer must
stay reachable to be one. A card replacing one just answered gets no window at
all — that keystroke was an answer, not typing, and a reader working through a
queue is never made to wait between questions.

### Selectors

One list component in four dressings — pick one, pick several, pick one with a
note, and the plan card. Every option carries its own description and a
right-aligned short field, so a list of models with prices can be compared
rather than walked one row at a time.

An option that cannot be taken here says so on the row, in a glyph and a
phrase, rather than merely being dimmed.

A card is either a list of answers or a search, and it says which by how it
arrives. A fixed set of answers — the permission modes, the providers, a
handful of code blocks — comes up as a list: its rows are numbered, a digit
takes one outright, and a bare letter can be a key. A card that opens over a
catalog — the models, the branches, the backlog — comes up as a search, with
the query row already open, because past a dozen entries walking is the slow
way and naming what you are after is the fastest way in. Spending that first
keystroke on opening the row it would have gone into is the card asking to be
asked. The saved chats are the one catalog that still opens as a list: its
rows carry the keys that delete and rename, and those keys are what the reader
came for.

While the query row is open every bare letter is text, so for as long as a
card is being typed into it has no letter keys of its own. Clearing a filter
that is already empty closes the row and hands them back — the model card's
[d], the saved chats' [x] and [r] — without leaving the card, and the key row
names that reading rather than offering to clear a query that is already
clear. Esc still leaves outright: a filter you have to escape twice is a mode.

### The inline confirm

A one-line question for a decision that does not need a card. Anything that
would destroy work states what it would restore and what it would delete, and
the default answer is the one that loses nothing.

## Takeover surfaces

### The palette

One prompt over everything the session can reach: commands, sessions,
anything else addressable. The slash prefix is for a command you are already
typing; the palette is for one you are looking for. Its chord is the slash key
for that reason — the list a chord opens is the list the prefix completes —
and it is declared in both the spellings a terminal delivers that keystroke
in. A terminal that sends neither is not stranded: the key list names the
other door beside it, which is the prefix on an empty draft.

It is the ordinary selector with its filter always open, not a fourth kind of
list. Its count is of matches against the whole reach rather than of rows
showing, because the whole point of the count is finding out that there is
more.

### The start screen

A first launch in a repository shhh has never seen already knows the
repository, and offers work rather than a blank prompt.

The header is what shhh already knows — where it is, the toolchain, the
branch, whether the tree is dirty. Clauses drop from the right as the terminal
narrows and the path never drops: a header that cannot say where it is has
nothing left to say.

Two things that govern what happens next are stated without being asked for:
what was read into the system prompt, and which check suite is in effect. A
suite that is not configured names the file it looked for; one that exists and
will not load says so, because a broken gate is not an absent one.

Three suggestions follow, ordered by what the working tree suggests — a
session to resume, then something read-only, then something needing a single
approval — and each says what it will cost you in permission.

A checkout with no state directory of its own has told the model nothing
about itself, and the last of the three becomes the offer to scaffold one.
Choosing it opens an approval card listing the files, because a suggestion
that wrote something on being chosen would be the one place on the screen
where a row is worth more than it says. A refusal is remembered for that
repository, so the offer is made once and the command behind it stays
available afterwards: what was refused was being asked, not the file.

Typing anything dismisses the offers and keeps the facts, because the input
owns every ordinary key the moment there is a draft.

### The supporting screens

Configuration, history, metrics, doctor and rating are each re-cut from parts
that already exist — the row, the windowed list, the meter, the card. Nothing
new is introduced, and the gain is that a reader who knows the session already
knows these. The MCP server listing is the doctor screen over servers rather
than checks: a connect is a check, so it is the same row, and a server
waiting on the person's trust offers it the way a pending migration offers
the move.

Rating is the one of them that asks rather than reports, and it is drawn as
the thing it is: one card, the answers as keys on it, and no list to walk,
because the answer is what moves.

Two rules they share are worth stating: none of them changes how your machine
behaves without a card, and doctor in particular names fixes rather than
applying them — the screen that changes settings is the one that asks first.
Rating writes on a keystroke and is not the exception it looks like: what it
writes is a record of something that already happened, and the card the
keystroke answers is on the screen while it is pressed.

Doctor has one key that changes the machine, and it is the shape of the
exception rather than a hole in the rule. A pending migration is not a repair
and not a judgement about what you meant: the machine is shaped an older way,
the move is mechanical, and the alternative to offering it here is a fallback
that never ends. So the row that found it offers to make it, and puts the same
confirm in front of it that the settings screen puts in front of a write
(docs/capabilities/configuration.md#a-migration-is-a-doctor-check).

### The profile drafter

Drafting a profile is a conversation with a shape — a brief, at most three
questions, a draft — and it runs on a surface of its own rather than through
the transcript. It ran through the transcript first, and what that cost is the
argument for this: the starting points were a numbered list in system text you
answered by typing a digit into the ordinary input, and the drafter's
questions arrived as a list to be answered *in one line, in order*. Every
other list in the product is a selector, and there is nowhere else where three
answers are typed into one line with no way to see which one you are on.

The surface holds the keyboard for as long as the flow lasts, which is what
lets a step be typed into and picked from at the same time, and a rail across
the top says which of the three steps you are standing on. The rail is not
decoration: a flow whose length is not stated is one nobody can decide to
start. A brief that was already a specification gets a draft and no questions,
and the rail says the middle step was skipped rather than ticking an exchange
that never happened.

The first step is a field with the cursor in it and the starting points
underneath, which is the start screen's arrangement and its reason — someone
who already has the sentence types it, and the offers are there for someone
who does not. The drafter's questions are asked one at a time, with the
answers already given still on screen, and esc unwinds the flow one exchange
at a time instead of cancelling it: an esc that always meant *cancel the whole
thing* made a mistyped answer cost the drafting.

The wait while the drafter writes is on the surface too, and that is not only
so it can be seen. A drafting turn that could not be stopped was a cancel
nobody had — the session held the cancel and no key reached it.

The last step is the draft over the card that writes it, and the card is the
one thing on the surface that never gives ground: on a terminal too short for
both, the profile pane shrinks and then goes, then the drafter's reason, then
the fields from the bottom — the permission line is the one nobody should
decide without, and the budget is the one they can look up. The card names the
profile in its own title, so the question stays answerable on a surface too
short to keep the name above it.

Nothing on the surface writes anything until that card's own row is taken,
which is the rule the scaffold card keeps: a decision gets a card, and the
card is the end of the flow rather than a step in it.

### The context surface

The window has always been reported as a percentage on a rail and, once it is
nearly full, as a card that asks what to do about it. Neither answers the
question a percentage provokes, which is *what is in there*, and the card only
asks it at the moment it is too late to act calmly on the answer.

So the same accounting is reachable by name at any time, and it is drawn as
the thing it is. The window is one block meter wrapped to the inspector rail's
own width and ten rows deep, so the whole of it is on screen and what is left
is a shape rather than a number to read. Each category takes one unbroken run
of cells in its own colour, in the order the legend lists them, so the
composition can be read without reading a number and a run can be found by
counting down the legend.

None of those colours is a new one, and that is the constraint the tinting had
to earn its way past rather than the decoration it might look like. The system
prompt is chrome, in the one sense that matters here: it is there whatever you
do. Project context is the heading tone, because it is a document read in
whole and the only category you shrink by editing a file. Tool definitions are
accent, which is already the colour of every tool glyph in the product. The
conversation is body, the ordinary text this interface is made of — and it is
the category nobody should be encouraged to read as waste. Tool results are
the tone tool output is drawn in everywhere else, which is also the category
the window trim elides first, so the grid shows which cells will go before
they go. Free space keeps the empty cell's grey *and* its own glyph, which is
what holds used and free apart on a terminal with no colour, where all five
tints collapse into two shades.

Pressure is not in the grid. It stays on the number, which climbs the usual
ladder in the header and again beside the total, because a grid that turned
red at ninety percent would have to stop saying what filled it at exactly the
moment that became the useful question.

Below both, the categories that are made of many things fold open: what each
registered tool costs to have available, what each tool's output is costing
now that it has been used, and which exchange the conversation spent itself
on. They arrive folded and each folded row counts what it swallowed, so the
breakdown is an answer before it is opened and opening one is a question the
reader chose to ask. A part too small to name is still counted in the tail
rather than dropped, and a turn the session opened on the reader's behalf — a
compaction summary, a command's output — is named for what it is rather than
quoted back as if they had asked it.

It reads and changes nothing, which is why it has no key that asks anything —
the surface that decides what to do about a full window is the card, and this
one only says what filled it. Because it changes nothing, it is also the one
occupancy surface that can be opened in the middle of a turn: a window filling
up while the agent works is exactly when the question gets asked.

### A staged attachment

A chip above the draft is the right answer to *what is attached* and the wrong
one the moment two screenshots are staged and the question is which of them
has the stack trace in it. That question has no verbal answer at any width, so
there is a surface that shows the attachment at full size.

A paste asks it harder. It arrived with no name anybody chose and no file
behind it to open in something else, so a chip is the whole of what a reader
knows about bytes they are about to send — and the two things they want to
check, that it is the right log and that it is all of it, are both answered by
looking at it. So text opens here too: laid out from the top and from the
left, with whatever did not fit counted at the foot rather than trailing off
([invariant 4](principles.md#fold-never-hide)).

Neither body scrolls. This is a preview of something staged, not a reader for
it — the question it answers is *is this the right thing to send*, and what
did not fit is counted rather than lost. Reading the whole of it is the
model's job, and it gets the whole of it either way.

It is reached by name rather than by a key: a chip sits above a live draft,
and a key written on it would be an offer nothing accepts
([invariant 5](principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard)).
Asking without a name takes the only staged image when there is exactly one
and refuses when there are two — guessing which was meant is the mistake this
surface exists to stop someone making.

Esc hands the pane back and destroys nothing; removing an attachment stays its
own deliberate act. That act does not require typing a name from memory:
asked bare, the drop opens a list of what is staged — checked rows go, esc
drops none — and a lone chip is a one-line question naming it, defaulting to
No.

A picture that will not decode still opens, onto the reason where the picture
would be. That it is staged and unreadable is a fact about the message you are
about to send, and a blank card would not have said it. A PDF does not open at
all: shhh does not render one, so there is nothing the card could say that the
chip has not said already.

### The one-shot result

`shhh cmd`'s whole interface. The command, one line of what it does, and
the keys. Where the command is flagged as dangerous, the *default key moves* —
the safe key states the blast radius and a second key runs it — so the
decision is taken once, on screen, rather than as an afterthought prompt.

**And the ones it did not pick.** A generator that can only say one thing has
already chosen for you: asked to find what is listening on a port, the model
weighs three utilities, picks one, and throws the reasoning away — and the one
it kept is the portable one when you wanted the fast one about as often as
not. The alternatives were free the whole time; only the surface was missing.

The key says how many there are, because whether there is anything behind it
is the one thing worth knowing before pressing it. Nothing is drawn when the
generation offered none, which is most of the time.

The response is command-first rather than structured. JSON is the obvious
envelope and the wrong one: the command streams onto the screen as it arrives,
and a front door whose first frames are punctuation is worse than the one it
replaced. Parsing is total, so asking costs nothing — a response with no
alternatives section is simply one choice, which is every provider that cannot
produce one.

## Outside the TUI

Help, the line a mistyped flag prints, the man page, and what is left in the
scrollback after the session hands the terminal back.

All of it obeys the same rules. Help is sectioned rather than dumped, for the
same reason the transcript is a grid: a list you scan needs an axis. A failure
is a labelled block naming one thing and one way out, with no usage dump —
the shape a recovery row asks for. Labels are words, so `NO_COLOR` loses the
tint and keeps every distinction.

Help is sectioned by what a command *is*, not by how it sorts. One
alphabetical list puts the two commands that are the product between two that
maintain it, and a reader arrives already knowing which of three things they
came for: to work, to look something up, or to set the machine up. The groups
are those three, and the description above them is the product's own first
sentence, so the list has something to be a list *of*.

A flag appears only on the commands that can act on it. A flag inherited by
every command in the tree is a promise most of them do not keep — `--model`
on a command that deletes rows says the deletion can be sent to a model — and
the reader cannot tell the real ones from the decorative ones without trying.
The same rule applies to what a flag's help *says*: where the answer is a set
the program already holds, help states the set rather than a copy of it that
was accurate once.

The exit banner exists because a session on the alternate screen leaves
nothing behind. What it drew is gone in one frame, and with it the answer to
which conversation that was, what it cost, and whether any of it was written
down. The banner is what the terminal keeps.

### What the tab says

A session borrows a window, and the window has a frame shhh does not draw:
the tab's own name, and the progress state a terminal shows beside it. Both
are worth something to a reader with eight tabs open, and neither is worth
guessing at — so both are what the current frame says they are, and both stop
the moment the session does.

The tab is called after the session: the command that is running and the
directory it is running in, shortened to the couple of segments that tell one
checkout from another. A waiting decision moves to the front of that name
under the same glyph every gated state wears, because it is the one thing
happening in here that the reader has to come back for, and the tab is where
they will see it from the next window over. It is a switch of its own, and a
different one from the switch that names the saved conversation: one is what
the window manager shows, the other is what the transcript is filed under.

The progress state is indeterminate while a turn runs, red for a moment when
one breaks, and absent otherwise. There is no percentage and there will not be
one — a turn does not know how much of itself is left, and a bar that guesses
is a bar that lies. It rides the notification switch rather than the title's,
because it makes the notification's promise without words: shhh getting your
attention while you are looking somewhere else. Someone who turned the summons
off did not mean *but keep the light on*.

A terminal that said in advance it is a dumb one is told neither. That is a
different fact from a capability query that came back empty, and it is the
terminal's own word rather than an inference.

### When you are not there

A turn runs for minutes and then stops on a question. The person who started
it went to do something else, and came back to find it had been waiting on one
keystroke — which is the entire cost of an agent that asks permission.

So shhh raises one desktop notification, on **the transition into waiting**
rather than while waiting: the single moment the session stops needing shhh
and starts needing you. That moment is derived from the session's state before
against after, rather than sent by the dozen handlers that can reach it —
three of them cancellations — because a property of a transition cannot be
trusted to every place that causes one.

**And only when the terminal has said its window is not in front.** A terminal
that never reports focus never sends a blur, so shhh never decides it is being
ignored on a guess.

What it says is what the screen it is calling you back to says, word for word.
A summons that describes the screen in different words is one you have to
reconcile when you arrive.

There is deliberately no native notification backend. The machine running shhh
is not always the machine you are sitting at: over SSH a native notification is
raised on the server, where nobody is, while an escape sequence travels back
down the connection to the terminal actually in front of you. One dialect that
is right everywhere beats two that are each right sometimes.
