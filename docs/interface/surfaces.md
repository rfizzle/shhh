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

Two things in the transcript are not rows, and they are what the rows sit
between. A message the reader sent is a `❯` row: the mark in the pointer
column the rows keep clear, the words beside it in the brightest grey the
pane has, and a faint rule under them saying where the message stops. A reply
is prose and nothing else — indented, in body grey, no mark and no rule.
Neither carries a speaker's name. A transcript is a record of what happened
rather than a conversation being had, it has exactly two voices, and the
brighter of the two is always the one that asked; a label saying so again
spent a row per message on a fact the mark had already carried.

A call that never happened is one too. Something the queue refused before it
could reach a decision — arguments that would not parse, a file that moved
since it was read — is the call's own row saying it was skipped and why,
rather than a sentence beside the acts: the reader is scanning a column for
what became of each call, and the one call that produced nothing is the one
they are most likely to be looking for.

The session's own bookkeeping is on the same grid, a field short. A
conversation reopened and a new one started are lines the session wrote about
itself rather than acts it took, so they take the verb column, the growing
field and the outcome, and leave the glyph column blank. What has no verb to
open with is prose, and prose wraps to the pane instead.

An act nobody was asked about states so on its own row and not on a notice
above it. Two rows for one act doubles the height of a session that was
mostly auto-approved, and the notice was worse than redundant: it spelled the
target a second time without the bounds the row puts on it, so a staging of
twenty files printed twenty paths over a row that says the first and a count.
The account — what allowed the call, and what the judgement cost where a
classifier made it — is a field of the row beside the outcome, the way the
decider on a refused row is.

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

The layering has a direction. On a changed line the verdict carries the
ground: the marker, the ordinary text and the glue between the words all read
as added or removed, and what the register keeps is the tones that name
something — a keyword, a value, the identifiers a reader scans a diff for. A
context line states no verdict, so it takes the register whole. The line
number is chrome on every kind of line, so a number is never read as a second
statement about the line it counts. None of that is the terminal's to settle:
the same edit at any width, in either the unified or the paired layout, states
the same verdict in the same colour.

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

Putting one back works a file at a time: what the turn recorded is each file's
two sides, not a history within the file. Reviewing a turn therefore stages a
file at a time as well — the file is the promoted key, and a selection that
covers part of a file is answered with what it will really do, which is to
revert that file whole. Where the hunks genuinely are separable, as in a patch
a sub-agent is offering, the same surface stages per hunk.

### The backlog run's row

A run of the backlog works one item through five stages over as many turns as
it takes, and it used to arrive as a scatter of one-line notices: a stage
started, a verify passed, a lane landed, a wall of report at the end. The
reader put the run back together by scrolling. It is one row instead — a run
is a step of steps, and a step is what the transcript already has a shape for.

The row is appended when the run starts and redrawn wherever it stands. Under
it the stages run left to right, each with a glyph and its own word: ticked
where the run passed, live where it is now, plain where it has not been yet,
and marked where it stopped. A run picked up from a checkpoint draws the
stages it did not watch as restored rather than as passed, and says so in
words, because a tick is a claim to have seen something happen.

What a stage cannot say in one word is said under the strip, named for the
stage it belongs to: how many of a large item's lanes have landed, which
remediation round is being spent of how many there are, and the word the
review answered with. Opened, the row gives the answers themselves — the
plan, the questions, the lanes and their paths, the findings, the report and
the files. The run's report is the row's final state rather than a paragraph
pasted into the transcript, so what shipped folds like everything else.

The words on the row are the words the record keys the run's transitions on.
There is one vocabulary and both readers of it draw from the same place, so a
row and a record cannot describe the same transition differently.

A run that blocked carries one offer: the item it stopped on goes back to
open, from the row that says why it stopped.

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

The cursor is also reachable without the handover. Shift on the arrows
moves it from the prompt — the pointer — and shift+→ and shift+← are enter
and collapse on the row it names; enter on an empty draft is the same open.
The draft keeps the keyboard and every character, the pane scrolls only as
far as keeps the pointed row in view, and esc drops the pointer before it
does anything else. What the pointer cannot do is press a row's own letters,
because at the prompt a letter is text; that is what the handover is still
for, and ctrl+o opens the mode on the pointed row. Line scrolling from the
prompt went with this: pgup/pgdn and the wheel are the scroll, and ctrl on
the arrows was never a chord shhh could keep ([reserved keys](reserved-keys.md)).

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

The transcript can also be searched. The slash every pager opens a query with
opens one here: a single row where the key bar was, typed into from the first
keystroke, with the terminal's own cursor on it and the count of what has been
found beside it. The pane follows the query as it is typed.

A match is bold, and that is the whole of the mark. It is the picker's
treatment for the same fact about the same query, so the two are not drawn as
two kinds of thing; bold is structural, so it survives mono; it leaves the
syntax colours under it alone; and it spends none of the three backgrounds,
which belong to the selection, the lit row and the diff. The occurrence the
reader is on takes no second mark, because it is already told apart by
something the surface draws: the reading cursor follows the search, so that
one is the bold run on the lit row.

**The search reads the session, not the screen.** A count taken from the
rendered lines is a count of the rows that happened to be open, which would
make `no match` a fact about the reader's folds. So every fold on the
transcript — a step showing only its header, a run of read-only calls showing
only its count — is asked what the query finds behind it, by drawing those
rows as opening the fold would draw them. The number goes on the fold's own
row beside what it already says it swallowed, it is added to the count on the
rail, and enter opens the fold onto the first row inside that holds one, a
level at a time. Where the row is too narrow for both, the count stays and
the key goes: the key is on the mode's bar either way, and the count is the
thing the fold owes the reader. A fold the search opened is put back when the
query clears; a fold the reader opened themselves stays open.

The rail names the surface. While a query row is open or a search is standing
it reads `SEARCH · <query> · 3/9` in place of the reading position — both
answer "where am I in this", the query is what the count is a count of, and
while a search is up the occurrence is what the next key moves. The
occurrences a fold is covering are counted at that fold's row, which is where
they are on the screen, so the position steps over them in the order the
reader would meet them. The pair that walks the occurrences walks the ones the
pane is drawing, so the position skips over the ones a fold is still covering
— those are reached through the fold's own row, which is why the row carries
the count and the key rather than the count alone. Clearing the query gives
the rail's own label back.

An empty result says what it read: `no match in this session · 212 rows
searched, folds and pastes included`, and then names `shhh history --grep` as
the way across the sessions this one is not. A count of nothing is worth
nothing without the size of what was counted.

Enter closes the row and leaves the search standing, which is what hands the
mode's own letters back: a row being typed into keeps every letter as text, so
the pair that steps between occurrences is only live once the row is closed.
Esc on the row clears the query, the marks and the count together and leaves
the mode where the search took the reader. Leaving the mode clears them too —
the marks are painted on the pane the ordinary feed reads through, and the
keys that walk them are this mode's.

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

Which is why the top rail states the turn's tokens and cost only where they
are not the session's. On the first turn of a session the two accounts are the
same three figures, and drawing them on both rails puts one number twice on
one frame, a hand apart, with nothing to say which is which except that they
agree. So the top rail draws them once the turns before it have made them a
different reading, and carries the phase, the spinner and how long it has been
in it until then. Nothing about how a figure is shaped changes with it: a
count still prints every digit while something is moving it and goes back to
`41.2k` once nothing is.

Attached to a child agent, both rails scope to that child. The top one names
the phase the child is in, read off what the supervisor already reports — a
call the child still has open, prose already arriving, or neither — in the
same closed vocabulary a turn of this session's own is reported in. It states
no elapsed beside it: the number that belongs there is how long the turn has
been in its phase, and what is reported of a child is how long the child has
been alive, which is a different span. The vitals rail states the child's
permission mode, its own context pressure, what it has spent against what the
whole session has, and which of the parent's rounds it is running under.

The fields a rail may shed when it runs out of columns leave in one order:
model and provider detail first, then token counts, then the round counter,
then the extras. Context pressure, spend, blocked or failed state and the
permission-mode segment are not on that ladder at any width. A rail that goes
quiet about what a child is burning goes quiet exactly where somebody is
watching it, which is the one moment those figures are being read for.

That segment is written in three words and the mark says which: `⏵⏵` in add
for `auto`, where a mode lets work through, and `⏸` in accent for `gated`,
where it asks first, and for `read-only`, where nothing can be written at
all. There are more modes than there are words, so the one class that covers
two of them carries the mode's own name after it — `⏵⏵ auto` is every gate a
mode can open and `⏵⏵ auto · accept edits` is the same mark narrowed to
edits, the pair in one tone because it is one field. A class covering a
single mode carries no second word: the name it happens to be set under would
be one state said twice, on the one segment that is read before every
keystroke. Those names are the answer to a different question — what the mode
is called where it is chosen, which is what the picker lists, what
`/permissions` takes and what the config row shows — and while the classifier
is deciding a call the segment says `✦ checking` instead, because for that
moment the mode is not the answer.

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

The cursor in the box is the terminal's own rather than a block shhh paints.
It blinks at the rate the reader set, takes the shape they chose, and is where
an input method puts its candidate window and where a screen reader looks for
the caret — none of which a drawn glyph can be. What the surface owes in
return is one coordinate per frame, and only one: a terminal draws a single
cursor, so the block holding the keyboard says where it is and every other
block says nothing. That is what puts it on the reverse search's row while the
match sitting in the box above it goes without.

The box holds three rows while the draft fits in three, and from there grows a
row per wrapped line to a ceiling of twelve — or to whatever a short
terminal's bottom panel allows, where that is less. Past the ceiling it
scrolls inside itself, because a draft long enough to page through is one to
write somewhere else.

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

One press of Esc on an empty draft folds every row the reader opened back to
its resting form, and the notice rail counts what it folded. Reading a turn
opens things — a tool's output, a step's detail, a diff — and putting six of
them back one at a time is six presses of a key that already means "leave what
is open"; so the pane a turn was read through is the pane the next message is
written over, for the press the reflex already produces. It folds to what the
settings say and never past them: a row `/ui verbosity high` opened is not the
reader's to fold here, and Esc says so rather than rewriting the setting. The
press is claimed only when it folded something, so a pane already at rest
leaves the double-Esc gesture above exactly as it was — with a row open that
gesture costs a third press, which is the price of putting the reflex first.

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
and the default is No. Ending the session without quitting asks the same
question in the same words, because it costs the same thing. Keys that end
something already scoped — a running command, the permission classifier, a
decision on its card — keep their single press, because those are reversible
acts, not abandoned work.

### The completion menu

A slash at the start of the draft opens the registry of commands under the
box, and completion does not stop at the command's name: a command whose
arguments are a known set — the model catalog, the saved chats, the branches,
a fixed list of subcommands — offers them for the token under the cursor.

Tab writes the focused row into the draft, ↑↓ move, and esc dismisses the menu
until the draft changes again.

A command the running turn has put out of reach stays on the list, behind ⊘
and greyed, with the reason right-aligned at the end of its row and the
command's own description still in the middle. "Why is /compact missing" is a
worse question to be left holding than "why is it grey", and a menu that
answers the first by showing nothing sends the reader looking for a command
that is two keystrokes away. The reason is the short form; taking the row
anyway is what says the long one. It takes its columns before the description
does, and goes whole or not at all.

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

The first row of the body is the act. The kind glyph the transcript will draw
this call's row with — `$` a command, `✎` an edit, `⚙` a read, `⇄` a call to a
server — and then the act itself in the brightest grey on the card: the
command line, the file, the request. Nobody is named on it. Who asked is not
what the reader is deciding, and a row that opened by saying so put the one
thing being decided at the end of the line.

Severity leads as a word under it. The card says it three ways at once — the
border, the chip on the title rail, and the row under the act — in one
wording, so that a reader checking any one of them against another is not
first working out that they are the same claim. What the body row adds is the
reading behind the level, in the terms the level was decided in: *⚠ medium ·
edits one file under internal/agent*, *⚠ low · writes nothing*, *⚠ HIGH*
followed by the risk that flagged it. Where a variant has nothing to read the
reason off, the row states the level and stops, because a reason invented to
fill it would be the one thing on the card the reader could not check.

The ladder has three colours and not two. *low* and *medium* wear the accent
the mutation rail wears; *HIGH* wears the colour of failure, and so does a
card with nothing containing it, whatever its level says — the missing sandbox
is what the decision turns on then, and all three statements move with it
rather than leaving the body row a colour behind the rail. A rating drawn in that colour at every level it has
teaches the reader that the colour means *a card*, and then the one level
meant to stop them has nothing left to say it with. A card with no rating at
all — a plan, a question, the agent manager, a memory proposal — wears the
tone of a surface waiting for an answer rather than the grey of one that
reports. The queue strip above the card follows the same ladder on the row the
card is showing, and draws the rest of the queue in one grey: five ratings in
three colours over one decision is a wall of warnings, and the words on those
rows do not change.

Resolution is honest about its limits: where the blast radius cannot be
determined, the card says so rather than reporting a confident nothing. What
the containment profile allows is reported from what is actually in force, not
from what was configured, and it is a row of the body under the three the card
always states — not a label on the title rail. A rail sheds its labels from
the front as the terminal narrows, and what a sandbox permits is the last
thing a sixty-column card should be giving up; the rail also paints in the
card's own tone, which drew a statement about containment in the colour of a
flagged command. The one containment fact that does reach the rail is the
absence of a sandbox, because that changes what the decision is.

The card's border carries how much the decision on it weighs, and the run of
its top edge between the title and the chips carries nothing — so that run is
drawn as chrome, in the tone a screen's rule is drawn in, while the corners,
the title's lead-in and the chips keep the weight. The frame still says what
it said; the empty part of it stops pretending to.

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

A command that can be asked what it would do rather than told to do it is
asked here. Where a harmless form of the command in front of the reader can
be derived — the flag `rsync`, `git clean` or `kubectl` were built to take,
`terraform plan` in place of an apply, `sed` without its `-i` — the card
offers a key that runs that form, in the same containment the real command
would have run in, and puts what it printed on the screen the full view uses.
It answers nothing: the decision is still waiting behind it, and the run
leaves a row of its own, because something ran on this machine and the
transcript is the account of what ran. Its output stays out of the
conversation — the model asked to run the real command and is still waiting
for the answer to that. Where no harmless form can be derived there is no
key and the card says nothing about a dry run, because a key that ran the
real command while the card called it a dry run is the one mistake this offer
must never make.

A command the reader does not recognise can also be asked about rather than
run. One key puts a paragraph on the screen the dry run opens on: what the
command in front of them does, written by the same inexpensive model the
permission classifier is configured with, in the words the one-shot explains a
command in — one rule for what an explanation says, not two that drift. It is
the dry run's shape with a model where the subprocess was, and it keeps the
dry run's promise: the request is bounded, the decision is still waiting
behind the screen, and nothing on it reaches the conversation, because the
model asked to run this command and is still waiting for the answer to that.
The screen names the model that answered and what asking took, since an
explanation is a claim and a reader about to act on it is owed who made it;
the spend is a line of its own in the session's cost, because a keystroke
that costs money is one the reader can see. A request that fails or runs out
of time says so there and gives the screen back — never a blank paragraph,
and never an answer. The offer is absent where no model is configured to
answer it, and it is the command card's alone: a diff is already the
explanation of an edit, and the full view already shows it whole.

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

The decision run is written the way every other key row in the product is: a
key in brackets, a lower-case imperative after it, and the answer that costs
nothing in the colour of the safe answer — *[y] run it once · [e] edit the
command · [n] deny*, with *[esc] don't — the safe answer* on a line of its
own. A key that is not offered stays on the card with its reason rather than
disappearing. The compact `[y/n/a]` prompt this card used to print, with what
its keys bought in parentheses beside it, was the one place two notations sat
a row apart — the offers under a frame's rule read one way and the offers
under a card's read another — and a reader who has learned that a bracket
means a live key is worse served by two notations than by one.

Every card holding the keyboard carries the esc line, whatever it is about and
whoever it came from, because the way out of a decision is the one thing a
reader must be able to find without having pressed anything first
([invariant 3](principles.md#esc-is-always-the-safe-answer)). It is a line of
its own rather than the last segment of the run, so whether it is on screen
does not depend on how many offers the card happens to have. A card whose own
field or list holds the keyboard states that surface's esc instead, once: two
surfaces cannot both have the key, and the nearer one wins. A card that does
not have the keyboard at all states none — esc there belongs to the draft, and
the line saying so is the handover's.

What the line says is what esc actually does on that card, which is not one
thing. On a gated card it hands the keyboard back and leaves the request
where it was, which is a different act from the denial `[n]` is; on a card
picked off the agent manager there is no draft underneath, so leaving is
declining and the line says so.

A rail above the card names whichever surface holds the keyboard and says
which of what is waiting this is — `DECISION 1/2`, and `DECISION 1/1` where
there is only one. The count is stated even then, because it is the same fact
as the frame's own count of what is waiting, and a label that took a count
only from the second decision would be one the reader meets for the first time
when they have least attention to spare.

Under it the draft is shown undressed, holding its characters, and it keeps
both of its rails. The top one carries the count again; the bottom one carries
the reading the decision is answered against — the permission mode, the
context pressure, the spend — before the position the sentence is being held
at. Those are the fields the [frame's](#the-input-frame) drop order never
sheds, and a decision is the moment they are being read for.

The decision run has two answers, and each has a spelling that says more.
Beside *allow* and *deny* sit *allow, and say what to do next* and *deny, and
say why*, each opening the note field under the card; the note travels with
the answer, and what it does to the model is the capability's to say
([`../capabilities/approvals-and-safety.md`](../capabilities/approvals-and-safety.md#a-no-can-say-why-and-a-yes-can-say-what-next)).
The plain key stays one press, because most answers are one press and a
reader working a queue must not be made to type past a field. Which letters
the spellings take is the register's decision; the pairing is what the card
promises.

A command card offers to be amended. The key puts the command in the text
input, prefilled, and enter runs the line as it now reads — after the card
has resolved it again, because it is a different command
([`../capabilities/approvals-and-safety.md`](../capabilities/approvals-and-safety.md#an-amended-command-is-a-new-command)).
A heavier line draws the heavier card, with the line the call carried printed
under the act row and a chip on the title rail saying whose line this is.
Esc from the field puts the original back, and the decision is still waiting.
While the field has the keyboard the card's own keys are letters, the way
they are while the note field has it. A command of more than one line is not
offered the key at all: the field is one line, and one that quietly joined a
heredoc into a single line would run something nobody wrote.

A command card can also explain itself. The key puts a paragraph on what the
command does, from the same inexpensive model the classifier uses, on the
screen the full view uses, and answers nothing. The one-shot mode's rule holds
here: explained on request, never by default.

*Allow without asking* is no longer one press. The key opens a list that
spells out each grant before it is made — this turn, this session, and the
narrow one: this exact line, this file alone — with the pattern the grant
would match on the row and, in its short field, when it ends
([`../capabilities/approvals-and-safety.md`](../capabilities/approvals-and-safety.md#a-grant-says-when-it-ends)).
A grant the reader could not see the end of is one they will forget they
made. The prefix row wears an ellipsis and the narrow one does not, which is
the difference between the two widths said in one character on a row too
narrow for a clause. A fetch card's list is the two lengths and no third row:
the host it would grant is already the narrowest a host grant gets. Esc leaves
the list with nothing granted and the card still waiting.

Where several cards are waiting, the queue strip already counts them; one key
renders the queue as the pick-several list, each row carrying the card's
title, its target and its severity chip, and a submit that allows the checked
rows and denies the rest. Each denial is the reader's, drawn as one. The list
opens with every row checked, because that is the answer the key used to give
on its own. The decisions it cannot take — a flagged action, a fetch, anything
reaching outside the working scope, anything of another kind — stay their own
card and are counted on a row of the list rather than left off it
([fold, never hide](principles.md#fold-never-hide)); a queue longer than the
panel scrolls behind the same counted markers every other list uses. Esc gives
the screen back with the queue exactly as it was.

Confirming the list marks rather than runs. Each row is answered when it
reaches the head of the queue, and the deny list, the safety table, the mode
and the working scope are put to it there — a decision does not outrank a
rule, and a call the reader allowed can have become inadmissible while the
calls ahead of it ran.

### The question card

The card the model draws when it asks
([`../capabilities/coding-agent.md`](../capabilities/coding-agent.md#the-model-can-ask)).
It is the selector in one of its dressings with a question above it, and it
arrives the way an approval arrives: over an empty draft it takes the
keyboard, over a sentence it waits inert, and a keyboard still warm gets the
same grace window. It carries no severity, because nothing on it changes the
machine, and its row in the transcript carries no rail for the same reason.

The card is titled *Question*, its place in the call rides the title rail as
*n of m*, and the question itself is the first row of the body. A title is
clipped into the border it is drawn on; the one thing on this card that must
never be half-read is the question, so it goes where it can wrap.

Four shapes, and the shape decides the dressing: one answer is the pick-one
list, several is the pick-several list, a yes-or-no is the inline confirm,
and a short free answer is the note field with no list above it. Every list
has the same last row — *something else* — which opens the note field, and on
every shape one key opens that field beside the pick, for the reason the list
could not have predicted. While the note holds the keyboard every letter is
text, the way the selector's query row already works, and the list's digits
are inert until tab hands the keyboard back. Esc from the note drops the note
and keeps the pick; a way out that lost the pick would not be the safe one.

A recommended answer leads and says *recommended* in a word. An answer that
cannot be taken says why on its row, in the selector's own glyph and phrase.
Every option may carry a short field on the right, which is the model's to
fill — a file count, a cost — so a list of approaches can be compared rather
than walked. A long description of the marked row goes behind the full-view
key, which takes the screen the diff already knows how to take and gives it
back with the question still waiting; the panel is too narrow for two columns.
It answers nothing: reading what an answer means must not cost the reader the
answer. The row the model did not write has a long form of its own there,
because what *something else* means is the card's to say rather than the
model's.

Several questions in one call are tabs on one card, with a submit tab at the
end. The strip marks the tab you are standing on and the tabs already
answered, and says the same thing in words beside them — where you are among
the questions, and how many are still open. Four is the most one call may
carry, because the panel has forty percent of the terminal and a fifth tab
would be a scroll.

The strip steps on the arrows, because the keystroke a tab bar has everywhere
else is the one the note is already on and one keystroke may answer one act on
one surface. Answering a tab steps to the next question still open, so a
reader working through three of them presses no movement key at all. The
submit sends every answer at once, in the order the questions were asked, each
naming the question it answers; a tab nobody answered goes back as *skipped*
rather than holding the reader at the card, and the strip says so from the
first tab rather than at the last one. Esc is still one press and still leaves:
it closes the whole card, not a tab of it, and every question still open goes
to the draft the way one does.

The free answer is the one tab whose field is shut when you step onto it. A
field that opened with the tab would take the arrows the moment the reader
arrived, and the strip they were walking would stop moving on the tab they had
not asked to type into; the note key opens it and hands the keyboard back.

Esc leaves the card and answers nothing. The question is held, the notice
rail counts it — *1 question waiting* — and the next message the reader sends
is delivered as the answer, in their words. The gutter says the draft is
answering rather than steering, because the two reach the model differently —
an answer is the call's result, a steer is a message — and a reader must know
which they are writing. The follow-up chord still queues for after the turn.
The handover brings the card back, because reopening a waiting decision is
the one act that chord already means everywhere else; a reader who left by
reflex is not made to answer in prose to get the list again, and reopening
answers nothing.
Entering the waiting state raises the same one desktop notification an
approval does, on the same terms ([below](#when-you-are-not-there)).

### Selectors

One list component in four dressings — pick one, pick several, pick one with a
note, and the plan card. Every option carries its own description and a
right-aligned short field, so a list of models with prices can be compared
rather than walked one row at a time.

An option that cannot be taken here says so on the row, in a glyph and a
phrase, rather than merely being dimmed.

An unlit row is body text and its number is chrome. Both used to go out
unpainted, which is not a colour the palette issued: it differs between two
terminals side by side, and on half of them it reads brighter than the lit
row's own text ([one grid](principles.md#one-grid)). The run of a row the
query named is still bold and never tinted, and the bold is added to the tone
the row is already in.

A card is either a list of answers or a search, and it says which by how it
arrives. A fixed set of answers — the permission modes, the providers, a
handful of code blocks — comes up as a list: its rows are numbered, a digit
takes one outright, and a bare letter can be a key. A card that opens over a
catalog — the models, the branches, the backlog, the turns a rewind can go
back to — comes up as a search, with the query row already open, because past
a dozen entries walking is the slow way and naming what you are after is the
fastest way in. Spending that first
keystroke on opening the row it would have gone into is the card asking to be
asked. The saved chats are the one catalog that still opens as a list: its
rows carry the keys that delete and rename, and those keys are what the reader
came for.

The cursor on that row is the terminal's own wherever the surface holding the
card places one, and a painted block only where it does not: a card knows
nothing about where on the screen it was drawn, so it cannot place a real
cursor itself, and a filter row with no cursor at all would say nothing about
where the next character goes.

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

The pair is written `[y/N]` and only the capital carries emphasis: the
sentence in front of it is body text, the brackets and the other letter are
chrome, and the letter that is the default is bold and bright. Drawing the
whole pair as an offer said *these are keys*, which the brackets already say,
and left the one fact the pair exists to carry resting on the shape of a
letter alone. The capital is still a capital on a terminal with no colour at
all.

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
list. It is titled with the chord that opened it rather than with a name for
itself, because the card is the answer to a key that was pressed. Its count is
of matches against the whole reach rather than of rows showing, because the
whole point of the count is finding out that there is more; a query that
narrowed nothing says only how much there is. What the card had no room for is
counted on a fold marker at its foot, in the same words and the same grey
every other windowed list folds in — a marker, never a fourth group rail.

It greys an unreachable command exactly as the completion menu does, and for
the same reason: the palette is where you go to look for a command you cannot
find.

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

An offer is reached three ways that are one act: the arrows and enter, the
pointer chords — shift on the arrows, the same chords that go on working in
the pane after the first turn — and a click on its row. Each runs the line
of input the offer names, through the submit typing it would take, so an
offer can never reach somewhere typing could not. The lead, the facts and
the hint lines are not targets: they name nothing to run.

This is the one screen with room for the product to have a face, and the only
one that gets one. Where the pane has the rows, the name is drawn in three of
them out of the half blocks, with the same mark the working label stands an
unarrived cell in for trailing off the end of it — so the first thing on
screen is the entrance every turn after it will make, made once. Where the
pane does not, the name goes in a single row of the rule instead, because
four rows taken from the offers are four taken from the reason the screen
exists. A monochrome terminal gets neither. The face states nothing the line
under it does not, which makes it decoration, and decoration is the first
thing a palette with two greys to spend gives up.

### The supporting screens

Configuration, history, snippets, metrics, doctor and rating are each re-cut
from parts that already exist — the row, the windowed list, the meter, the
card. Nothing new is introduced, and the gain is that a reader who knows the
session already knows these. The MCP server listing is the doctor screen over
servers rather than checks: a connect is a check, so it is the same row, and a
server waiting on the person's trust offers it the way a pending migration
offers the move. The saved-chat browser is the same cut over conversations:
the list on the left, the one the pointer is on beside it, and the renaming
and deleting the picker inside a session already offers
(../capabilities/sessions-and-memory.md#housekeeping).

Ten surfaces take the whole terminal this way — those seven, the reading of
what is in the session's context, the ledger of what the session read
(`/sources`, [what was read](../capabilities/chat.md#what-was-read)) and the
drafting flow for a new agent profile — and they are one family rather than
ten screens: the same header, the same
ground given up in the same order as the terminal narrows, the same key row at
the foot and the same `[?]` behind it. The two that arrived last had each been
drawing a chrome of their own, and the way that showed was not in either of
them: it was reading all of them side by side.

They say the way out in one voice, and each of them says it twice because a
key row and a header are read at different moments — not because there are two
facts. The header ends with the register's key and then the letter, with the
act in one word: `quit` where the screen was opened from a command line,
`back` where it was opened from a session. The foot ends with the same act
under the key a reader reaches for without thinking, and there it is a phrase:
**back to the shell** on doctor, metrics, history, snippets and the saved-chat
browser, **back to the prompt** on the context reading and on the backlog,
which is drawn on this chrome too ([the backlog screen](#the-backlog-screen)).
For leaving a screen and changing nothing there is no third wording.

Three of them leave by doing something rather than nothing, and each says what
it does in this same vocabulary instead of inventing one: the settings screen
*leaves* with nothing staged and *discards, after asking* with something
staged; rating *stops*; and the drafting flow unwinds one exchange at a time,
so what its esc says is which of the four things it is about to do — leave,
go back a step, stop the drafting turn, or drop the draft
([the profile drafter](#the-profile-drafter)). That flow is also the one
header here with no register key to put in front of the way out, so esc is
the whole of its right-hand run.

One of them is reached both ways. The settings screen is `shhh config` from a
shell and `/config` from inside a session, and it is the same screen either
way: the same rows in the same order, the same account of where each value
came from, the same staging, and the same write key. In a session it takes the
transcript the way the context reading does, so the turn underneath goes on
running, and what a write leaves behind is a row in that transcript saying
what reached the file — and that the running session keeps the settings it
started on, because a screen that changed the file is not a screen that
changed the conversation. Two words differ and they are the two that say where
the reader is: what the screen calls itself, and the one word its header ends
with. The four settings that made this worth having are the ones a person
reaches for in the middle of the work rather than before it — how often the
harness checks in, what its steering says, which model summarises, and which
profile the backlog is read under.

Neither row repeats the other. The header carries the register's key and the
letter; the foot carries what the screen can do and, last, the way out. A
surface that put `[?]` and the letter on both rows spent its bottom row saying
what its top row had already said, which on the narrowest terminal is the row
that had least to give.

Four of them list something and preview what the pointer is on — past
commands, saved commands, saved conversations, and the pages this session
read — and they split the terminal the same way: two columns where there is
room for two, stacked where there is not, and the preview giving way to the
list when the rows run out, because a screen that cannot preview an item can
still say which items there are.

Rating is the one of them that asks rather than reports, and it is drawn as
the thing it is: one card, the answers as keys on it, and no list to walk,
because the answer is what moves.

Two rules they share are worth stating: none of them changes how your machine
behaves without a card, and doctor in particular names fixes rather than
applying them — the screen that changes settings is the one that asks first.
It asks in both directions. Nothing on the settings screen reaches the file
until the write key, so the way out of it is a discard of everything typed
there, and that gets the same one-line question over the same count the header
has been carrying ([invariant 3](principles.md#esc-is-always-the-safe-answer)).
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

The rule under a screen's header and the run of a card's top edge are the
same material: the one rule the drawing kit has, in the chrome tone. That is
what makes a card and a screen read as one product instead of as two widgets
in the same binary. A diagonal texture was tried in that run and taken out
again — it collapsed to the flat rule under a two-grey palette, which is the
proof that it carried nothing, and it was a material no artboard draws.

### The backlog screen

The backlog is a directory of files, and it was readable two ways, each
answering a different question. The block on the rail shows the first few
items, which answers *what is next*. The command prints the whole listing,
which answers *what is in here* and then, for anything beyond reading, asks
for a name typed back — a name the reader has just read off their own screen.

So the backlog is also a surface: the items on the left in the order they
would be worked, the one under the pointer on the right as the file it is,
and the keys that would otherwise be composed as verbs. Nothing here is new.
It is the same two panes, the same windowed list, the same header and rule
and key row as every other screen in this section.

The row carries what decides the order and nothing else: the name, the
priority and size as two letters, and where the item stands — ready, waiting
on something, in progress, blocked. The title takes whatever is left and
clips, because the pane beside the list carries it in full. Under the width
that carries two columns the pane folds under the list rather than beside it:
prose two columns wide is prose nobody reads.

Both ends of a dependency are drawn. The row says what an item is waiting on,
which a listing has always said; the pane says what is waiting on *it*, which
nothing has ever said and which is what decides whether finishing it is worth
anything. One key walks the edge.

A file that will not parse is a row in warning tone with the reason as its
body, and it survives every filter that asks a question it has no header to
answer. It is the one row that must never go missing: an item that vanished
from the list reads as work somebody finished.

There is a second tab for the archive, and what an archived item shows is the
report of what was actually done rather than the criteria that were the
question. That is the whole reason it is worth having: what shipped and how
is read in the same place it was planned. An item can come back out of the
archive from there, which is the one act on this screen that no typed verb
has.

The rule the supporting screens share holds here: nothing changes without a
card. Blocking, archiving and dropping each ask first, and the one that
deletes a file says that is what it does. What the screen removes is the
composing, not the asking. And every act goes through the same handler the
typed verb goes through, so a refusal on the screen is the refusal the
command gives.

It can be opened in the middle of a turn, because *what is in the backlog* is
a question a running turn provokes rather than one it answers. What it cannot
do then is change a file: the model may be working from these files this
second, so the keys that would edit one go grey with the sentence saying why
above them, rather than accepting the press and refusing it afterwards
([invariant 5](principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard)).

### The sprint board

A sprint is a set of items in a stated order under a goal, and it was
readable only as a listing. The command prints it, the rail carries its name
and its count, and neither of them says where the set stands. The two
questions actually asked of a sprint — what is this set for, and how far
through it are we — are board questions, so the answer is a third tab of the
screen the backlog already has rather than a fourth place items are drawn.

Above the two panes the tab pins a head: the goal as written, a progress
meter of what is finished over what the backlog still holds, and what the set
has cost so far. Under it the set's own slugs in the file's order, each
carrying where it stands *in the set* rather than its status in the backlog —
the one being worked says which stage it is at, because a slug that only says
"in progress" tells you a sprint is going and not whether it is moving. A
slug the backlog no longer holds is a row saying so; it leaves the ratio
rather than counting as finished, because a set that reported work as done
because its item was deleted would be the one number here nobody could trust.

A set that stopped says what stopped it. A sprint stops on the first block
and attempts nothing after it, so the block and the item that wrote it belong
on the board and not only in the transcript of the session that hit it. And a
sprint that crosses a session boundary — it makes one per item — leaves a
first row on the other side saying which item comes next, because everything
else that boundary carried is gone by design.

Planning is the same tab before there is a file. The proposal is drawn here
as a card that holds the keyboard: the budget it was bounded by is in the
header, the goal sits above the set with the line saying what kind of
release it reads as under it, each row carries the line saying why that item
is in the set, and nothing is written until the card is taken. The goal and
the release line are two rows because they have two authors — the sentence
is the reader's to rewrite and the line under it is the reading's judgement,
and editing one must not take the other with it. Under the set, folded behind its
own key, the candidates the reading left out with one word each — what a
recommendation did not take is half of what makes it arguable, and the folded
row states how many went and which words they took so the count is never the
only thing on screen.

The card keeps `j/k`, which is the one pair the list under it had to give up.
While a card holds the keyboard nothing else is listening, so no filter
letter is competing for the keystroke
([invariant 5](principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard)).
Taking an empty set is refused rather than written: a sprint that names
nothing scopes the ready list to nothing, and it is the one file here nobody
can work out of.

When a sprint closes, its report is a page rather than a paragraph — the
goal, the notes the set leaves, every item with what it produced, what
stopped the rest, and the turns and spend the set took
([reports](../capabilities/reports.md#what-a-report-is)).
The board's last row offers the link, whole, the way an activity row offers a
published report: the file is renamed into the archive the moment it closes,
and that row is the only place the link survives it.

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

The line of what the command does arrives the same way, in its own labelled
section after the command and ahead of the alternatives, so the surface is
complete when the stream ends rather than one round trip later. Neither
section is ever drawn as command text: a label still being typed is held back
until it is clear which it is, so what is on screen mid-stream is the command
and only the command.

**It is laid out against the terminal it was typed into.** Drawing inline,
under the prompt rather than over the screen, is not the same as drawing at
whatever width it likes: the terminal holds one cell per column and keeps
nothing past the last, so a row that overran was never a row that wrapped —
it was a row whose tail nobody was shown, and what went missing was the end
of the sentence and the last keys on the row, `[s] save` and the `[esc]` that
says how to leave.

So each kind of row breaks the way that kind of row should. A sentence — the
explanation, a risk, the containment line — breaks between words, and reads
the same at every width. The key row breaks between one offer and the next
and never inside one, because half of `[esc] quit` is not an offer anybody
can take. A command breaks at the column, the way code does everywhere else
here: it is the one run that cannot be reflowed between words without
becoming a different command, and a folded command is still every character
in order where a clipped one is not.

One blank row separates the containment line from the keys. The keys are what
you do about everything above them rather than the last line of it, and a row
of offers hard against the sentence above reads as part of the sentence.

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

What it cost is the bill the session kept as it went, not a sum re-priced on
the way out. A session pays several rates at once — a cheaper model for the
background, and a fraction of the input rate for the prefix the provider
served from its cache — and the only place those rates are known is the
moment each request came back. A figure recomputed from the token counts
alone charges every cached read as a fresh one, which on a coding session is
most of the input and several times the true bill; a meter that overstates by
that much is one a person turns off. It is also the whole sitting's, children
and background included, because that is what the sitting spent.

The count beside it is the conversation's and the figure is the sitting's, so
the row names its own scope. On a reopened conversation the two describe
different populations, and a price sitting silently under a count is read as
the price of that count.

It ends on one line of voice, and that line is the last row rather than the
first: a banner that opens on something saying nothing is a banner a reader
learns to skip, and everything they came for is above it. The line is drawn
only where a person is watching. A redirected stream is a capture or a script,
and there the facts are the whole of what was wanted — a line of voice in a
capture is one more line to parse past. A terminal with no colour still gets
it, because it is words.

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

A sprint takes the name's place while it is working, and says how far
through the set it is and which item it is on. A sprint makes a session per
item, so the session's own name is the same in every window it runs through;
what tells the reader anything is the count and the slug.

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

A turn spent inside a sprint says which item it was spent on and how far the
set has got, and an item that reached the end is named as *finished* rather
than as being at a stage. A reader who left a sprint running and came back to
one line about a turn would have to go and look up which of thirty items it
was.

There is deliberately no native notification backend. The machine running shhh
is not always the machine you are sitting at: over SSH a native notification is
raised on the server, where nobody is, while an escape sequence travels back
down the connection to the terminal actually in front of you. One dialect that
is right everywhere beats two that are each right sometimes.
