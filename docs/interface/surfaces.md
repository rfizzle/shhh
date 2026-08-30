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
place, bounded; opening it again gives it the whole screen.

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

### The input frame

Where you type, and where the session's vitals live. Its borders carry
information rather than being dead lines: identity and live activity on the
top rail, the session's counters on the vitals rail, and contextual key hints
on the bottom rail that change with what the session is doing.

Above it, a notice rail exists only while there is something to say and
disappears when there is not. Below that, a staged rail carries whatever is
waiting to ride out with the next message — it sits against the box because
what is staged leaves with the sentence being typed, and the notices do not.
Each chip says what the thing is, what it is called and how big it is, and for
text how far it runs, because a size answers *will this fit* and never *which
of these is the stack trace*.

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
says which of the two you are doing.

A draft too long to compose in three rows leaves for your own editor and comes
back: shhh writes what you have typed to a file, opens the editor on it where
the cursor was, and takes whatever the file holds when the editor exits. An
empty file is not an instruction to throw the draft away, so it leaves it
standing. The editor has the terminal while it runs, which is why the key is
refused rather than queued while a turn is in flight or a decision is waiting
— neither can be watched from inside somebody else's editor.

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

Below the threshold the rail is dropped entirely rather than compressed.

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

### The agent manager

Sub-agents are visible and steerable while they run: what each is doing, how
far in, what it is waiting on. Attaching to one is not a new surface — it
switches which agent the session is looking at, and every agent including the
root is the same kind of thing.

A child's approvals route to wherever you are, so detaching does not mean
missing a decision.

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

A card arrives when the agent needs it, which is not when the reader is ready
for it. What it may do to a half-typed draft, and when its letters become live
keys at all, is governed by
[invariant 5](principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).

### Selectors

One list component in four dressings — pick one, pick several, pick one with a
note, and the plan card. Every option carries its own description and a
right-aligned short field, so a list of models with prices can be compared
rather than walked one row at a time.

An option that cannot be taken here says so on the row, in a glyph and a
phrase, rather than merely being dimmed.

### The inline confirm

A one-line question for a decision that does not need a card. Anything that
would destroy work states what it would restore and what it would delete, and
the default answer is the one that loses nothing.

## Takeover surfaces

### The palette

One prompt over everything the session can reach: commands, sessions,
anything else addressable. The slash prefix is for a command you are already
typing; the palette is for one you are looking for.

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

Typing anything dismisses the offers and keeps the facts, because the input
owns every ordinary key the moment there is a draft.

### The supporting screens

Configuration, history, metrics and doctor are each re-cut from parts that
already exist — the row, the windowed list, the meter, the card. Nothing new
is introduced, and the gain is that a reader who knows the session already
knows these. The MCP server listing is the doctor screen over servers rather
than checks: a connect is a check, so it is the same row, and a server
waiting on the person's trust offers it the way a pending migration offers
the move.

Two rules they share are worth stating: none of them writes to your machine
without a card, and doctor in particular names fixes rather than applying them
— the screen that changes settings is the one that asks first.

Doctor has one key that changes the machine, and it is the shape of the
exception rather than a hole in the rule. A pending migration is not a repair
and not a judgement about what you meant: the machine is shaped an older way,
the move is mechanical, and the alternative to offering it here is a fallback
that never ends. So the row that found it offers to make it, and puts the same
confirm in front of it that the settings screen puts in front of a write
(docs/capabilities/configuration.md#a-migration-is-a-doctor-check).

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
own deliberate act.

A picture that will not decode still opens, onto the reason where the picture
would be. That it is staged and unreadable is a fact about the message you are
about to send, and a blank card would not have said it. A PDF does not open at
all: shhh does not render one, so there is nothing the card could say that the
chip has not said already.

### The one-shot result

The prefix mode's whole interface. The command, one line of what it does, and
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

The exit banner exists because a session on the alternate screen leaves
nothing behind. What it drew is gone in one frame, and with it the answer to
which conversation that was, what it cost, and whether any of it was written
down. The banner is what the terminal keeps.

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
