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

### The leading columns

The transcript is one column of text, and every kind of entry in it begins in
the same place. A narrow gutter on the left is held back for a mark *about* an
entry — the reader's own `❯`, a step's fold caret, reading mode's cursor — and
everything that is not one of those marks starts in the first column past it:
a paragraph's first word, a notice's first word, an activity row's mutation
rail and the glyph beside it, a progress checkpoint, a reading of the session,
and the line a turn closes on. An entry with no mark of its own leaves the
gutter blank rather than starting in it, which is what makes a mark's arrival
cost nothing — the cursor lands in a column the entry was already holding for
it, and no text slides sideways to say where the cursor is.

Wrapped text returns to that same column, and a body folded under an entry
starts one step further in ([one grid](principles.md#one-grid): detail bodies
indent, they do not re-grid). Neither ever reaches back into the gutter. A
continuation that started at the left edge would read as a new entry, and an
entry that began there would put its first word under the marks belonging to
the rows above and below it — which is the one thing this edge is for, since a
reader scanning a long session is reading down a column rather than parsing
each line to find out what kind of thing it is.

So a check-in, a tree reading, an error and the session's own bookkeeping are
not exceptions to it. They are lines the session wrote about itself rather
than acts it took, and the temptation is to set them apart by starting them
further left; what that spends is the one edge the whole transcript is read
down, and it buys a distinction the grey they are drawn in and the words they
use were already making for nothing.

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
they are most likely to be looking for. It is about the file it tried to
touch, where its arguments got far enough to name one, and says outright that
they named nothing where they did not — five refusals of one tool are five
different files to the session and one mistake repeated to anyone reading a
column of the tool's name. The sentence the model was given folds under the
row, whole.

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

A call the reader *was* asked about carries the same field, and the other of
the two words: `approved by you`, where a rule's says `auto-allowed` and
names itself. Both are the answer to one question — how this act came to be
allowed — and they are two words for the reason the two refusals are
([`principles.md`](principles.md#two-denials-are-not-one-denial)); the
reader's takes a colour where the rule's stays quiet, because a session
scrolled back a week later is exactly where the calls somebody was shown stop
being distinguishable from the ones they never saw. The account stands last
in the field, after what the act did and after what it counted: `+12 −4 · 2
hunks · approved by you`, `ok · 1 line · auto-allowed · classifier 2.1s`. The
act comes before the decision about it, and the account is the part the row
gives up when it runs out of width, so it is already where the row runs out.

An act that was refused states that on its own row too, and opens to why.
What refused it is the field beside the outcome; the sentence behind the
refusal — the reader's own, or the one the classifier wrote about this call —
is the row's body, folded, because a sentence clipped into a field is a reason
nobody can read. A row is the only place the reason keeps: the feed is the
record, so the refusal and what was said about it are still together after the
turn has moved on, and the frame above carries neither
([`../capabilities/approvals-and-safety.md`](../capabilities/approvals-and-safety.md#a-judged-denial-carries-its-reason)).

A call still in flight is a row already, and it is the only place the live
command is drawn: the outcome field says it is running, the duration field
ticks that call's own clock, and the last line it has printed sits under it
until it finishes. The [frame](#the-input-frame) below states the phase the
turn is in and leaves the command here, where the field that grows has the
width of the pane behind it.

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

The row sits inside the step it was thought in rather than ending it. The
model stopping to think between two rounds of the same step is still that
step's work, and what it does next is still what the title announced, so the
calls after the row stay under the heading and folding the step folds the
thought with them. The header's count is the step's calls, and the row is not
one of them: the count is of acts, and a thought ran nothing, read nothing and
changed nothing. What ends a step is prose — a new title, or an explanation
too long to be one — never private reasoning.

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
the title. A step runs until the next prose: the notices a batch earns and
the think rows between its rounds are members of it, so a step that paused to
reason reads as one step and folds as one, and its count stays the calls it
made. Where there is no structure to find, the transcript is a flat list
and no empty grouping chrome is drawn. A public progress update is ordinary
assistant prose, so one short enough to be a title titles the following group
while the rail continues to state only the immediate phase; one too long to be
a title is drawn as [the checkpoint it is](#the-progress-checkpoint).

The counted row a run of read-only calls collapses into is not the step's,
though the step is where it is most often seen. It is a fact about a run of
rows — three or more consecutive calls that only looked at something and came
back — so a turn that reads its way into a task before it has said anything
folds the same way, under no heading at all. That turn is the one the fold is
worth the most in: a session with a plan buries eight reads under a step, and
a session still working out what to do buries thirty under nothing.

### The turn's close

The question after an agent stops is never "what did it say", it is "what did
it change". So a turn closes on what it did, what changed, and whether the
tests still pass.

The changed-files row carries the mutation rail, so the close of a turn looks
like the rows that produced it. It also carries what git knew about those
files when they were written — which is a statement about the past, not a
promise about what can be undone; that promise is the approval card's job.

A turn that changed no file has no files to state, and what it says instead
depends on what it did. A turn that only read closes on its one summary row:
nothing it did raised the question. A turn that ran a command, or called a
server nobody marked read-only, did raise it — shhh cannot see what such an act
wrote, so it assumes it wrote something
([weight tracks risk](principles.md#weight-tracks-risk)) — and its close
answers on the changed-files row: `changed no files`, under the same rail, with
nothing offered, because there is nothing to review, keep or take back. A
silent close after a command would leave that question unanswered. A command
that was refused, or never started, ran nothing and raises nothing; a commit
row, where the turn made one, is already the answer.

Any turn can be put back, and putting one back is itself recorded as a change
that can be reviewed and put back in turn. Putting one back is `/undo` with the
turn's number: what the turn recorded is each file's two sides, not a history
within the file, so the whole turn goes back a file at a time. A single file of
it is asked of the model, which has the edit tools and the card that goes with
them.

The changed-files row offers review and keep, in that order. Taking the
change back is not a key on the row: it is a command, and the row's note says
which one — `/undo 3 takes it back` beside what git knew about the files —
where there is room, giving that up before the reading of the files when there
is not. A letter on the row for an act that already has a command, and that
the model can do as well, was a second path to keep correct beside the first,
and it was the path nobody took.

Review is the row itself. The row states what one turn changed, so clicking
it, or selecting it and pressing enter, opens that turn's review — its files,
their hunks and the verdict beside them. It is the one door to that surface
that names which turn is meant by where the reader put the pointer, which is
why it replaced a review key: a key printed on every turn a session closed told
nobody which turn it would open, and it opened the newest. Review is its own
surface and not a second way to read a diff — the rail's changed files open one
file's diff across the session, and an edit's row opens that one edit — so it
keeps a door of its own, and `/review` with a turn number is the other one.
Selected, enter on the row says so: `review turn`. That is a label for what
enter does once the row holds the keyboard, not a promise that enter reaches
the transcript while a sentence in the draft owns it; there, enter sends the
sentence and the pointer's own open is the key. A click reaches the row from a
half-typed line without handing the keyboard over at all.

Review is a reading, as `/diff`'s is. It moves between files and hunks, pages
and pairs the two sides, and leaves on esc having changed nothing; it stages
nothing, because what staging selected was an undo, and the undo is `/undo`.
The standing note says so: nothing is committed, and `/undo` with the turn's
number is what restores the files it wrote.

Keeping the change is a commit, and the offer stands for as long as the
changeset is uncommitted. The model can make one when it is asked, through its
own card; the row's offer is for the reader who has just read what changed and
knows whether it is worth keeping. It is reached through the handover — the
one chord that already gives a card the keyboard — on the row the reader has
selected: `[ctrl+space/ctrl+y] commit`. Offers are drawn on a selected close
and on no other. The same keys on every closed turn were keys that did not say
which turn they meant, so a close nobody has selected states what the turn did
and offers nothing, the newest included; selecting it — the pointer from the
prompt, or reading mode's cursor — is what draws its review and its commit.
Both are chords, neither a letter of a sentence being typed, so the pointer and
the cursor draw them the same way, and the handover pressed with nothing
selected never reaches a close. Nothing about a turn ending moves the
pointer: it moves on the reader's key or click and on nothing else.

The handover opens a card, for the reason every act that cannot be taken back
gets one. It states the message that will be written, what will be staged, what
will deliberately not be, the branch this lands on, whether the checkout's own
hooks run, and that nothing is pushed. Those are the facts a reader is
entitled to check before pressing enter and cannot check afterwards. A file
the turn wrote and the reader has edited since is on the card as a statement
of its own: it is neither theirs nor the turn's any more, so it is left out of
the commit rather than folded into either answer. The card's `[s]` opens the
turn's review, to read the hunks it is about to carry. The
message is proposed rather than asked for: it is the turn's own question under
the lead this repository's recent subjects use, and a key opens it as a draft
under its own rail, where the frame's title is the subject-line budget
counting down. A hook that refuses cancels the whole thing — the reason
stays on the card, the index goes back the way it was found, and the changeset
is still there to be offered again.

The receipt is the row the turn's close already draws for a commit. Once a
changeset is committed the row still opens its review and loses the commit,
which has been spent, and the `/undo` in its note: undo restores files from
the session's own records without reaching history, so naming it beside a
commit would read as an offer to take the commit back.

The checks row names how to run its suite again — `/gate run default runs it
again`, in the note column where there is room — and only where there is a
suite. A command the turn happened to run is a line nobody is looking at any
more, and suggesting it again would be shhh choosing to execute it.

A turn that watched the tests fail, fixed the code and ran the repository's
own suite again has one true answer about the tree it leaves behind, and it is
the last one. A passing suite run supersedes every check the turn made before
it: the suite ran over that tree and came back clean, so a failure older than
it has been answered. It supersedes nothing that ran afterwards, because that
is a fact about a tree the suite never saw, and a run the suite itself marked
stale supersedes nothing at all — it has already disowned the tree it is a
verdict about. A run nobody let finish is neither: it reached no verdict, so
the checks row does not count it in either direction.

That resolution is read once and every surface reporting a verdict reads the
same one — the checks row, the verdict pinned beside a review, and the rail's
standing alerts. A session that says the gate passed on one row and the checks
are failing on another has told the reader nothing they can act on, and this
is what makes that state unreachable rather than unlikely.

Superseded is not deleted. The failed attempt keeps its row, its outcome and
the time it took, so what the turn went through is still there to read; the
checks row states how many attempts the verdict answered, in the note column
where a narrow terminal drops it before it drops the verdict. What a
superseded attempt no longer does is decide what the turn says about itself.

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

*Lane* is the fan-out's word, and the run borrows it for its division
rather than coining one of its own because the two are one thing seen before
and after a spawn: each part of the division is handed to one writer, and
that writer is the child the fan-out block gives a lane. The row counts the
parts with the meter the block draws its lanes with, so a second word would
be two names for one count the reader sees twice
([`subagents.md`](../capabilities/subagents.md)).

The words on the row are the words the record keys the run's transitions on.
There is one vocabulary and both readers of it draw from the same place, so a
row and a record cannot describe the same transition differently.

A run that blocked says how its item goes back to open — `/todo open` with
the slug, the command that does it — on the row that says why it stopped. It
is a sentence rather than a key: the command is the path either way, and a
letter on the row was a second one to keep correct beside it.

### The recovery row

Most of a tool's reputation is made in its failures. Every one of them is an
ordinary row plus one offered key.

The row names the model and then the class; the outcome is the one thing that
decides what to do next, never a repeat of the class. The provider's own words
appear underneath, bounded — which is why "unclassified" is a class rather
than an error path. A message we could not name still gets said.

The offered keys are bare letters, and they are answered the way a card is:
through the handover. While the draft holds the keyboard a letter offered on
the row is a letter of the sentence being typed, so the row draws its letters
grey beside the key that hands it the keyboard; the handover gives the
selected row the keyboard, as it gives a waiting decision the keyboard, and
the letters are live from then on — `r` tries again, `c` continues, `e` takes
a new key, `p` switches provider, `+` grants more rounds, `!` lets the turn
run. Answering the row gives the keyboard back to the draft, sentence intact,
and so does esc. Under reading mode's cursor the letters are live already.
There is no second spelling of any of them
([why](principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard)).
A click on a drawn offer is the same letter through the same handler once the
row holds the keyboard; before that, the one live key on the row is the
handover, and a click there hands the row the keyboard as the chord does.
Unlike [a turn's close](#the-turns-close), a recovery row keeps saying what
its keys are when nothing selects it — grey, beside the key that hands the
keyboard over — because they are how the reader gets out of it. A failure row
is the one row a reader most often meets mid-sentence — the turn broke while
they were typing the next thing — and the pointer's first press from the
prompt lands on it, so its letters are one selection and the handover away.

One row is closer than that. While nothing is selected, the handover reaches
the failure or dropped stream the last turn ended on, and that row labels its
retry and its provider switch with the row they act on: `retry the last
failure`, `switch provider for the last failure`. Pressed with the sentence
still in the box, the handover gives that row the keyboard and no other. It is
the one row the handover reaches with nothing selected, because retrying a
turn that just broke is the commonest recovery there is and selecting the row
first made it a key longer. The words are what keep it from being the key the
rule replaced — a bare `retry` that picked the newest row named no row at all.
A failure the session has moved past — a retry that went through, a turn
after it — is not reached with nothing selected, and a selected row's letters
act on that row alone. The handover reaches the selected row and no other,
and a selected row that makes no offer does not hand it to one that does.

Two failures earn a card instead, and only two: the ones that stop the session
dead.

### The compaction receipt

Recovering the window is an act the session took on the conversation, and it
is accounted for the way every other act is: one row, in the same seven
fields. The glyph column carries the outcome directly — a compaction is not a
call, so there is no kind glyph for it to override — the verb is `compact`,
the growing field says which turns went, and the account beside the outcome
says where the window stood before and where it stands after, with what the
summary cost. There is no mutation rail: nothing on the machine was touched.

Under the row, a fold: which turns are behind it, what they were holding, and
how many lines stand in for them now. It opens read, because a reader who has
just lost five turns is owed what replaced them without asking for it, and the
same key closes it again. The summary inside is the model's own words, so it
is the one italic run the transcript draws, and it is bounded like every other
body under a row — the foot counts what the cap swallowed and says where the
turns themselves went.

They went nowhere. The turns a compaction folds keep their rows, and their
step headers say `out of the window`: the search still reaches them, the folds
still open, and the only thing that changed is what the model can be asked
about. A transcript that dropped them would be answering a question nobody
asked it — the record is of what happened on this machine, and compaction is
about the model's memory.

A compaction with nothing left to fold says so instead of pretending: what it
freed, and what is still in the window that no summary can stand in for — a
plan, a changeset, the turns it is keeping. That row is a break, because the
act was asked to recover a window and did not, and it carries no fold, because
there is nothing behind it.

### The progress checkpoint

A turn that has gone a long way on tool calls alone is asked, at a round
boundary, for a short public status: the objective it is working to, the
evidence it has, and the next action. The request is the session's, not the
reader's, and that is the whole of what the rung says. The answer to a
question somebody typed is the brightest prose in the pane after the question
itself; a checkpoint is the session reporting on work still in flight, so it
is drawn a rung under that — in the grey a body under a row is drawn in, and
slanted the way a compaction's summary is, because the slant means model
output nobody asked a question for and this is the second and last place it
means it.

Quieter than the answer is not quieter than the work. A checkpoint never
outranks what is happening now: a command still running, a card waiting to be
answered, the line a turn closes its verification on. Those are the rows that
decide what the reader does next, and a note *about* them drawn heavier than
they are would be the signpost competing with the road.

It is bounded, because it repeats. A run long enough to earn one checkpoint
usually earns three, and three notes on one objective are three paragraphs
that mostly agree — the reason a long session reads as prose accumulating
rather than as progress. So the current note is drawn to a few lines with the
rest folded under it, and a note a later checkpoint has replaced keeps its
first line and folds the rest. Nothing is dropped: the fold counts the lines
behind it and opens on the key that opens every other body in the transcript.

A note that ends the turn is not one. The request is made at a round boundary
and asks for status on work still in flight; a reply that answers it and then
asks for nothing has stopped working, so what it wrote is the turn's answer
and is drawn as one — the brightest prose in the pane after the question.
Reporting and concluding are different acts, and the rung is the only thing
that tells them apart.

The mark outlives the round it was made in. A conversation reopened from the
store, and a child's feed mirrored into the session attached to it, are both
the transcript rebuilt from a record rather than watched as it happened — and
a rebuild that lost the mark would promote every note in a run's history to an
answer, which is the one thing the rung says a note is not. So the record
carries it: a reopened transcript draws the notes at the rung they were
written at and retires all but the last of them the way the live one did, and
a mirrored feed draws the child's notes at the rung the child wrote them at.

The request does not come back as a row. A rebuild draws each message the
session wrote the way the live transcript drew it, and every other one — a
check-in, a steer, the tree notice, a close gate's verdict, a `/run`'s output,
a secret's announcement, a continue, a compaction's summary, a carried plan —
had a row of its own when it was written. The request never had one: the note
that answered it is its row. Drawn on the rebuild, it would stand above the
note as a prompt the reader never saw asked, in words that were the session's
question and never theirs. It is still in the conversation, where the model
reads it; only the row is left out. What leaves it out is a mark the request
carries in the record, not its wording, so a request sent in other words is
left out the same way; a conversation stored before the mark existed is read
by the built-in sentence instead, because that is all it recorded.

A checkpoint short enough to title the batch of calls that follows it is drawn
as that step's header instead. That is not an exception to any of this — the
outline is where a title belongs, and a note that is already a title is
already one row and already folds with its step.

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

Selected looks like one thing wherever the pointer reaches. The row under it
— reading mode's cursor, the pointer from the prompt, a list's cursor in a
picker or the palette, the start screen's offers — takes the same focus
ground across its width with its words in bright, and the pointer mark stands
outside that ground in the column before it. A surface that drew its own
kind of selected would be asking the reader to learn a second one.

It is also where rows that offer keys without expanding are answered — a
turn's changeset, a provider failure. Both are passive renderers; holding
their keys here is what keeps `g`, `u`, `r`, `c`, `e` and `p` available for
typing. Enter on a turn's changeset opens that turn's review, which is the
one row where enter does something other than expand, and the bar says so.

**What is staged is its last target.** With anything staged, the strip of
chips is drawn as the panel's first row and the cursor reaches it below the
last transcript row: `j` off that row lands on it, `k` leaves it back up, and
the arrows pick a chip. Where there is no row above it to stand on — the
first screenshot of a session, pasted into its first sentence — the mode opens
straight onto the strip. The chip under the cursor takes the pointer in its
first column, where its mark was, and the rest of it is lit, so nothing on
the strip moves to say where the cursor is; the handle still names the kind.
The bar is the strip's own — open, drop, and esc back to the draft — and the
draft and its cursor are what they were when the mode opened. What the strip
opens onto is [a staged attachment](#a-staged-attachment).

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

The box itself is chrome, drawn in the tone every rule on the screen is drawn
in, whatever the mode. The mode is stated on the vitals rail, by its word and
the glyph in front of it, and a border coloured by mode would say it a second
time in the accent an [approval card](#the-approval-card) wears to say a
decision is waiting — so with a card up there would be two accent boxes, and
the one that is the decision would stop being the one that stands out. This
departs from the artboards that colour the border by mode:
[the frame's border is chrome](departures.md#the-frames-border-is-chrome-and-the-mode-segment-carries-the-mode).

The top rail states what one turn is doing — which of four phases it is in,
and how long the turn has been running — and it states it while it is
happening rather than after the fact. Nothing else on screen says the same
thing twice: the phase is named here, not also under the transcript.

The four are `thinking…`, `deciding…`, `acting…` and `streaming…`. The third
is not `running`, because that is the word the [row for a call in
flight](#the-activity-row) already carries as its outcome, a few rows above
this one: one word with two subjects on one screen is read as one subject, and
which of the two is meant — the whole turn, or the one command — is the thing
the two lines exist to keep apart. Each vocabulary keeps the word about its own
subject, so the turn acts and the command runs.

The act the phase is for is named the other way round. A running command is
[a row in the feed](#the-activity-row) — the command itself, what it is
doing, the clock it started, and the last line it printed under it while it
runs — and the rail says only that the turn is acting. A command is what
made the rule: the row has the width of the pane and the grammar to bound
what runs past it, while the slot on the rail is a fraction of that, so a
copy there was the same command twice over and the shorter copy was cut off
mid-word. It is the reader's own attention that is spent by the second copy;
what they wanted from the rail was to know the turn had not stalled.

Each clock says whose it is. The rail's is the whole turn's and carries the
word for it, because the row above is ticking the command's own and two bare
figures a few rows apart are two readings of one operation to anyone who does
not already know which is which.

There is one of that clock, and which surface holds it changes when the turn
stops. While the turn runs it is the top rail's: the frame is on screen at
every width, the rail sits beside the cursor, and saying the turn has not
stalled is what it is for. The [inspector's THIS TURN
block](#the-inspector-rail) counts the turn's files and its tools and states
no span — it is up only above the two-pane rung, so a clock there would be the
same figure twice over on wide terminals and one figure on narrow ones, and
where the reader looks for it would depend on how much room they had. When
the turn stops, the clock stops with it and the span belongs to the past,
which is the transcript's: [the row the turn leaves](#the-turns-close)
carries the span and is still carrying it ten turns later, while the rail can
only ever answer for the last one.

The rest of the turn's account goes the same way. The close row states what
the turn ran and what it was billed beside its span, a few rows above the
rail, so a summary that stated them too was every field of that row a second
time at the cursor, less the steps — and the reader who had just watched the
turn end would read the same count and the same bill twice between the last
act and the prompt. What only the rail says there is that the turn is over and
how it ended, so that is all the summary says: `✓ done`, `⊘ cancelled` or
`✗ failed`, the glyph where the spinner was and the word where the phase was. The
turn's account is the close row's, and on terminals wide enough for the
[inspector rail](#the-inspector-rail) its THIS TURN block counts the tools as
well.

The rail states it at its near corner, two rows above the prompt glyph,
because it is the one thing on the frame that moves and the eye watching it is
already on the cursor. Against the far edge of a wide terminal the same words
sit a hundred columns from anything the reader is looking at. The far side
carries the identity instead: nothing at the root session, where the header
above the transcript already names the surface, and attached to a child agent
the breadcrumb — there the rail is the one place that says which session the
keyboard is in. A rail with room for only one of the two keeps the status,
because the breadcrumb answers a question a key can ask again and what the
turn is doing is why the rail carries a label at all.

A figure that changes climbs to its new value over about half a second rather
than cutting to it, on the same tick that draws everything else moving on the
frame. A cut says that a number changed and never by how much, and by how much
is the whole question at the token scale. Nothing climbs that the session has
not measured: through a tool round with nothing streaming, the counts hold.
While a turn is spending them they print every digit, because a hundred tokens
of movement vanish inside the rounding that makes `41.2k` the right shape to
carry a finished session in; once nothing is moving them they go back to it.

The counters that climb are the session's, on the vitals rail, and they carry
the running turn's estimate inside them: what the session has spent is what
the earlier turns cost plus what this one is costing, and a request's report
replaces that turn's estimate instead of being added to it. That is the
account, and the frame draws it once.

The top rail carries none of it. A turn in flight can only be priced at the
full input rate, because the split between the prompt read fresh and the
prompt served from cache arrives with the provider's report and not before; on
a session whose prefix is re-sent every round that is most of the input, so
the figure overstates the bill — and it overstates it beside the vitals rail's
billed total and the spend view's breakdown, which are right. The newest
figure on the frame would be the only wrong one, and the one a reader takes
for the most current. The tokens leave with it: the pair a row below is the
same pair on the turn a session opens with, and on any later turn it is the
question nobody is asking of a line whose job is to say what the turn is
doing. What the turn spent is stated once there is a bill for it, on the row
the turn leaves in the transcript, and the vitals rail's total has it inside.

Attached to a child agent, both rails scope to that child. The top one names
the phase the child is in, read off what the supervisor already reports — a
call the child still has open, prose already arriving, or neither — in the
same closed vocabulary a turn of this session's own is reported in. The call
that decided the phase stays where the session's own does, in the mirrored
row under the rail. It states no elapsed beside it either: the number that
belongs there is how long the turn has been running, and what is reported of
a child is how long the child has been alive, which is a different span. The
vitals rail states the child's permission mode, its own context pressure,
what it has spent against what the whole session has, and which of the
parent's rounds it is running under.

The vitals rail carries what moves: the permission mode, the context
pressure and the spend, at every width, and beyond those only what is live —
the round counter once a turn has used a round, idle included, since how much
of the ceiling the last turn spent is what the reader about to send the next
one is asking; the count of children while there are any; and the token pair
only where no price is known, because a stat that cannot be reported is left
out and the pair is the nearest honest stand-in for the bill
([principles](principles.md#a-stat-that-cannot-be-reported-is-left-out)).
Beside a price the pair is the same account read a second way, so it goes.
The rail says each of these in words a reader who has never opened these
documents already knows. The pressure reads `context 32%` on a terminal of
110 columns and wider and `ctx 32%` below, where the four columns are ones the
rail cannot spare; the counter reads `round 7 of 25`; the token pair ends in
`tok`, because two arrows name no unit; what the session has stopped asking
about reads `granted: edits, 2 commands`, never beginning with a mode's name
beside the segment that states the mode; and a sentence typed while the turn
works is counted `1 queued for this turn`, the one spelling every rail that
counts it uses.
What never moves while a session runs — the directory, the branch, the model
and the reasoning level — is the header's, in dim after the surface's name
above the transcript. Those four used to sit on the rail beside the three
facts that change what the reader does next, and every glance at the rail
became a search through things that had not changed since the session
opened. The header sheds them from the right as the terminal narrows, except
the model, which goes last: a reader checking mid-session which model is
answering looks up, not down.

The fields a rail may shed when it runs out of columns leave in one order:
the round counter first, then the extras; attached, the child's name goes
first. Context pressure, spend, blocked or failed state and the
permission-mode segment are not on that ladder at any width, and the token
pair standing in for a spend nobody can price takes the spend's place: it is
the only account such a session has, and a narrow rail that shed it would say
nothing of what the session had cost. Under a held draft the position the
sentence is held at outranks it, since the frame's own rail still carries the
account and the position is what that block is on screen to say. A rail that goes
quiet about what a child is burning goes quiet exactly where somebody is
watching it, which is the one moment those figures are being read for.

That segment carries the mode's own name, and the mark in front of it carries
the class: `⏵⏵` in add where a mode lets work through, `⏸` in accent where it
asks first or where nothing can be written at all. Five modes and five words —
`⏸ manual`, `⏵⏵ accept edits`, `⏵⏵ auto`, `⏸ read-only`, `⏸ plan` — because
the class is what the mark already says, and a word that repeated it would
leave two modes wearing one name. That is what the segment used to do: it said
`gated` for manual and `auto · accept edits` for the mode that is not auto, so
the reader about to press a key was told the class twice and the mode never.
`auto` is one of the five and stands on the segment only while the session is
in it. The word is the name the picker lists, `/permissions` takes and the
settings row shows — written as the two words `accept edits` where the file
hyphenates them, and one speller does that for every surface, so no mode is
written two ways.
While the classifier is deciding a call the segment says `✦ deciding` instead,
because for that moment the mode is not the answer.

Above it, a notice rail exists only while there is something to say and
disappears when there is not. Under that, on the terminals too narrow for the
[inspector rail](#the-inspector-rail), the status row that stands in for it.
Below both, a staged rail carries whatever is waiting to ride out with the
next message — it sits against the box because what is staged leaves with the
sentence being typed, and the notices do not. Each chip says what the thing
is, what it is called and how big it is, and for text how far it runs, because
a size answers *will this fit* and never *which of these is the stack trace*.
Each chip leads with a handle — its kind and a number, `Image#2`, `Paste#1`,
`File#3` — and the number runs for the conversation, not for the strip: a
chip that has left does not give its number to the next one, and a
conversation reopened later goes on from where its own messages stopped. The
handle is what the two paste verbs take, because the name is often nobody's
choice and three pasted screenshots are all called `clipboard.png`. So when
the row runs short the names go first, then whole chips from the end, then
the counts of the one that is kept; the handle is the last thing given up.
The strip is also the last row [reading mode](#reading-mode) reaches: below
the transcript, drawn in the panel where the box was, the same chips in the
same columns — which is how a chip is looked at or taken back without the
sentence being cleared to type a command ([a staged
attachment](#a-staged-attachment)).

Above all of that, while children are working, one compact row apiece: the
child's name joined to what it was asked to do with the separator every other
row joins two facts with ([one grid](principles.md#one-grid)), then what it is
doing now and what it has spent. Six of them, and then a count of the rest.
They go while a child's request is on the card, because the card's title rail
names the child asking and its lane in the transcript already says why it
stopped — a row between the two states a third time the very thing the reader
is about to answer, in the rows they have to look past to reach the answer.
With no card up they stand: below the [inspector rail](#the-inspector-rail)'s
threshold they are the only drawing of the fan-out beside the lanes
themselves.

A child's ending is said in those same two places and in no third one. A
child drawn as a lane settles into the words on its lane — the outcome in the
state field, the first line of what it wrote under that — so nothing is added
below the block to say it again. A child that ran alone has no lane, only the
row its spawn left, and there the line saying it finished is the only account
of it. What counts children rather than drawing them stays either way: the
vitals rail's tally of how many there are and how many are stuck, and the
frame's own mark for a decision waiting, because a count is not a second
drawing.

A paste past a certain size stops being a sentence and becomes one of those
chips. A log or a stack trace typed into a three-row box buries the sentence
it was meant to go with, and scrolling a draft to find the question you were
asking is not composing — so past ten lines or a thousand columns the paste is
staged as a file of its own and the box is left for the words. Both thresholds
are settings, because how much text a person can hold in a draft is a fact
about their terminal and their eyes rather than about shhh. Every door onto
the staging area reads them, because which key the reader used to paste is not
a fact about how much text they pasted.

The sentence keeps a mark where the paste was. A chip above the box says a
log is riding out with this message and says nothing about where in the
sentence it belongs, so the draft holds the fold itself — `⟨Paste#1 · 214
lines⟩`, drawn as the thing the session is carrying rather than as the words
around it, and one character wide as far as the keyboard is concerned: an
arrow steps over the whole of it, a backspace at its closing quote takes the
token and the log it stands for together, and enter sends it as it is
written. Angle quotes and not square brackets, because square brackets are
how this product writes a key, and a fold that could be read as an offer
would be teaching two notations at once.

Everything that arrives at the cursor leaves a fold, not only a paste. A
screenshot taken off the clipboard or a file dragged into the terminal lands
where the cursor was as `⟨Image#1 · 1440×900⟩` or `⟨File#1 · 3.2 MB⟩` — the
chip's handle, and the one figure that kind is counted by: a picture's size
in pixels, text's height in lines, anything else its size. The fold is what
lets the sentence point at the thing — *Image#1 shows the error, Image#2 is
after the fix* — and it is the same fold a paste leaves in every other
respect: one character to the keyboard, a backspace over it drops the chip,
and a chip dropped any other way takes its fold out of the sentence. A file
named to the paste command leaves none, because the command is the sentence
and there is no cursor in it; it is still named on the chip, and can be named
in the sentence by hand.

What the fold will cost stands on the vitals rail while it waits — `Paste#1
will cost ~6.1k tokens` — because a paste is the one keystroke that can
double the price of a message, and the rail is already where the price of
this session is read. The fold's chip opens it — a click, or reading mode's
cursor on the staged strip ([a staged attachment](#a-staged-attachment)) —
and so does `/paste show` by name; there is no chord of its own, so the bottom
rail offers none. Opening
borrows [reading mode](#reading-mode)'s labelled rail — `──── PASTE#1 ·
lines 1–8 of 214 ────` over the lines themselves — rather than inventing a
pager of its own, and the draft underneath is untouched: the cursor is where
it was left, so the way back is to the sentence rather than to the start of
it. The one key there that changes anything takes the paste back out, and it
takes the token out of the draft with it — a fold standing for bytes that
are no longer staged would be a sentence promising something it is not
carrying.

After the send the transcript keeps the fold and not the flood. The row is
the sentence as it was typed, the token still in it, and under it `▸ Paste#1
· 214 lines · 6.1k tokens · [enter] expand` — bounded the way every other
body in the transcript is, because the paste is in the context window and
does not have to be in the scrollback as well
([fold, never hide](principles.md#fold-never-hide)). A picture's fold leaves
a row of its own beside it, `▸ Image#1 · 1440×900 · [enter] open`, and
opening the message opens the picture on the [preview
card](#a-staged-attachment) with the row left open behind it, so esc comes
back to the row and whatever text was folded beside the picture is there.
The card offers no drop for a picture that has already gone. Anything else —
a PDF, a recording — is a row that counts it and opens onto nothing.

Recalling that sentence brings the paste back with it. ↑ puts a line in the
draft as it was sent, so the fold in it has to be a fold again or stop being
one: the log is on the row the send left, and it is staged again under the
next free number with the token renumbered to match — its chip opens it,
the same rail prices it, and the next send carries it. A picture's fold comes
back the same way, from the bytes its row kept. A conversation loaded
from storage is no different: the log was saved with the message that carried
it, so a reopened session's row has it too. Where the bytes cannot be staged —
a paste that no longer fits beside what is already staged — the fold loses its
quotes and stays as `Paste#1 · 214 lines`, which is words about a log that is
not riding rather than a mark with nothing behind it. Walking on drops what
walking here staged, for the reason a backspace over a token does.

The reverse search is the same recall by another road, and it settles the
folds once, when a match is kept — not as the query moves, since each
keystroke shows a different line and staging a paste for every line glanced
at would churn the staging area for sentences nobody chose. Esc puts the draft
back as it was and stages nothing. Clearing the draft is a fold leaving it
too, so the pastes its folds stood for leave with it; a file attached by hand
has no fold in the sentence and stays.

A paste too big to stage is refused with the limit named, and the draft is
left exactly as it was. What bounds it is not the size a message can carry but
the window it will be read in: a paste has no file behind it, so it goes into
the prompt itself rather than being fetched when it is needed. Typing it in
after all would put a megabyte in the box that the reader then has to get back
out, and the bytes are still on the clipboard either way.

Typing while the agent works is steering, not a queued prompt, and the gutter
says which of the two you are doing. The queued prompt exists too, behind a
chord of its own: a follow-up waits for the turn to end and goes out as the
next message, where steering joins the conversation mid-flight. Each marks
its own rows, because "change what you are doing" and "when you are done,
then" are different promises. A cancel does not send what was queued behind
it — the follow-up was written against work that was just abandoned — so the
queue survives, marked held, for the reader to decide what still applies.

A queued message is still the reader's until it is sent, so the queue is
drawn and it can be edited. Above the box, between the status row and the
staged strip, it is one row per message in the order they will go out —
steering first, since it goes at the next round, then the follow-ups — each
marked with which of the two it is, cut to one line, and naming what was
staged with it, since what was staged when a message was queued leaves with
that message and not with the next one. Past three rows the oldest are kept,
because they go next, and the rest are counted. The notice rail carries the
one key that reaches it, and whether a cancel has held the follow-ups.

That key moves the keyboard into the queue, which becomes a card in the
panel with the pointer on the newest message: the arrows move it, enter
pulls the message back into the draft with its attachments back on the
strip, a letter cancels it — its attachments with it, and one row in the
transcript saying so — and esc goes back to the draft exactly as it was.
Pulling a message back takes it out of the queue: it is a sentence in the
draft again, and sending it queues it afresh at the end like anything else
typed, so nothing is ever delivered twice. The queue chord on an empty
draft is the same pull-back aimed at the newest message. The turn keeps
running while the card is up, so a message can be delivered while the
pointer is on it; the key that arrives second does nothing to it and says it
was already sent, and a sent message is taken back by rewinding to before
it. Nothing about any of this reaches the model: a cancelled message never
does, and an edited one does as it is sent. What the session queues for
itself — an announcement, a line another session sent — is not the reader's
sentence, and is not on the list. Attached to a child, the draft is the
child's and the queue drawn is not: the lane's own queue is not yet
editable.

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

The box is as tall as what is in it and no taller: one row while the draft
fits on one, and from there a row per wrapped line to a ceiling of twelve —
or to whatever a short terminal's bottom panel allows, where that is less.
Past the ceiling it scrolls inside itself, because a draft long enough to page
through is one to write somewhere else.

Nothing else moves it, and the terminal's own height moves it least of all.
An empty draft costs one row on a twenty-row terminal and one on an
eighty-row one, in every state a person waits through — idle, thinking,
deciding, acting, streaming, and the turn resolved — because it is the
transcript that pays for every row the bottom panel keeps, and a blank row
under a cursor nobody is typing at carries no character. What such rows
would reserve is room to type into, and the box grows the instant there is a
second line to put in one, so the room arrives when it is wanted rather than
standing empty until then.

It grows upward, from a bottom rail the panel holds still. The row being
typed on is the same screen row whether the draft is one line or ten, which
is what lets a reader's eye stay where the cursor is while the sentence
wraps under their hands.

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

An empty draft answers one gesture the other harnesses taught: a double Esc,
idle, opens the rewind picker — going back is a gesture, not a command to
remember. The key list was the other, on a question mark, and it is a chord
now for the reason every offer at the input is one — a bare key here is a
letter of the sentence being typed, and "on an empty draft" is a condition a
reader finds out about by having it fire.

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

The box rests at one row and grows to its ceiling as the draft does, so no
length of draft is pushed out of it. A draft you would rather compose somewhere
else can leave for your own editor and come back: shhh writes what you have
typed to a file, opens the editor on it where the cursor was, and takes
whatever the file holds when the editor exits. An empty file is not an
instruction to throw the draft away, so it leaves it standing. The editor has the terminal while it runs, which is why the key is
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

The session's changes are a reading of the changeset and not the changeset,
so the block is bounded by a preset rather than by whatever height the rail
happens to have: a handful of files are drawn, and past that the rest fold
behind a marker that says how many went and what they added and removed —
`… 24 more +1210 −0` — whether or not the rail is short. Without it a session
that has written thirty files draws thirty rows on a rail with nothing else
long enough to give first, and the map and the meters under it are what
leave. Which files stay is a rule, not the first few: the ones the running
turn touched, then the ones edited most recently, drawn in the order they
were first written so a row does not move because another file was edited.
What was banked stays pinned above them, and a path the session did not
write folds behind the same marker, counted as a row and bringing no lines.
The whole list is the session's diff, one command away. When the rail is
shorter still, the block goes on folding below the preset the way every
block does.

One block is neither the turn's nor the session's but the standing bad news
between them: what this session has run that is still broken. It sits directly
under the turn's own block, above everything scoped to the session, because a
workspace that is still wrong about something is the one thing on the rail
that wants an answer now rather than a reading.

An alert is not a command line. Left as one row per line it is the loudest
thing on the rail and the least useful: a formatter run over three directories
fails three times, and by the tenth round the red rows outnumber the files
under them. So an alert is one command — the command's name, which is its
first word and the subcommand after it where that word takes one rather than a
flag or a path. Three runs of the formatter in one turn are one alert that
says `3 runs`, and the outcome it carries is the last run's, because the last
run is what the workspace is currently like.

Nor is an alert one turn. A suite that has failed in four turns running is
one thing wrong with the workspace and not four, and a heading over it
reading `4 standing` counts attempts and calls them problems. So an alert is
an episode: the command's failures from the first one to whatever answers
them, across every turn it keeps failing in. The earlier turns are not rows of
their own; the one row carries the turn the command first broke in and every
run behind it since — `✗ make test  exit 2 · 5 runs  since turn 3`. It also
takes the place of its latest failure, because the alerts drawn are the most
recent ones and a command that broke again a moment ago is the one being
fought now. What the
heading counts is then what the rows are: one standing alert for each command
the workspace is still wrong about, which is one row for each thing there is
to answer.

An alert stops being news in one of two ways, and they are the same fact
stated twice: the command came back clean, or the repository's own suite
passed over the tree that command failed on — [the same resolution the turn's
close reads](#the-turns-close). Neither deletes it: superseded is what the
block calls an episode something has answered, and it is one entry however
many turns the episode stood for, carrying the same runs and turn span its
row did. So one broken command reads `1 standing` until it is answered and
`1 superseded` after, rather than being counted once per turn it failed in.
A failure answered inside its own turn is an episode too, superseded from
the start. It was never news, so it is never a row — the block is what is
broken, and it is not — but it was red, and the fold is the account of how
much red it took to get to green; a count that left it out would call a
session that broke and fixed its build in one turn one that never broke it.
A superseded entry folds behind `… 8 superseded`, so the session can still
give that account without any of that red being on screen as a current
failure. A failure after the suite's pass opens an episode of its own, since
it is about a tree the pass never saw.

Two live alerts are drawn, the two most recent, and everything else folds
behind that marker. A live one is pinned — the last row the rail gives up when
it runs out of height — and a superseded one is never a row at all, so no
changed file is ever pushed off the rail to keep an answered failure on it.
When nothing is live the block goes with them: a block whose only news has
been answered is history, and the transcript is where history is read.

Where the session has declared its own working steps rather than executing
an approved plan, the plan's place is taken by a block that reads as a
child's lane does: `2 of 4 · <the step it is on>`, one row under the label
STEPS. It is not a checklist of every step, because the list is the agent's
own and can be revised; it is where the agent says it is. The two are never
up together — while an approved plan is being executed, the plan is the
checklist — and a session that declared no list draws no block. Every step
marked reads `3 of 3` and nothing more: the count is the agent's account of
its list, and whether the task is done is the turn's close to say. The whole
list is one act away: the heading opens it ([the supporting
screens](#the-supporting-screens)). So does the plan's heading, onto the same
screen drawing the plan instead: the two blocks stand in one place and answer
one question, so they open one surface, and it is the list standing in that
place that it draws.

One block is scoped wider than the session: the project's backlog. It sits
under the plan because it is the same question one step further out — the
plan is what this turn is going through, the backlog is what is queued
behind it — and it shows the first few items in working order with what each
one waits on, then counts the rest. The whole list is one command away, and
the block says which.

A block that ends on the command behind it — the plan's whole list, the
backlog's, the way into the map — ends on chrome rather than on an item of
its own list. When the rail runs short that row is the first thing the block
gives up, and it leaves rather than folding behind the block's count:
folding it would spend the row the fold just saved on the marker, and the
marker would then say an item is hidden that is still on screen.

One block is a map rather than a measurement: every session this run has,
the root and each agent it started, in the order they were started. Each row
carries the state it is in, what it has spent, and — once it has stopped —
the word it ended on, because a run whose finished half is only recoverable
by scrolling is a run you have to reconstruct to see. Finished agents fold
past a count rather than disappearing and the marker says how many went
behind it; what needs an answer from you never folds.

A wide fan-out folds too, whether or not the rail is short. Past a preset of
a few live agents at each level of delegation — one per slot a level can run
at once — the rest go behind the same marker, and where anything behind it is
still live the marker says so in the heading's words: `… 6 more · 6 running`,
with what finished after it. Without the preset the block's height is the
fan-out's width, two rows an agent, and a run of nine writers pushes the
changes and the meters under it off the rail. What folds first is an agent
queued for a slot, then the oldest of the working ones; an agent waiting on an
answer, the one the keyboard is in, and the root never fold, and the waiting
ones take their places in the preset before any working agent does. A parked
agent keeps its row, because it says why the run is not moving. The whole
list is the agent manager's, one chord away.

One row of the map is marked, and the mark is where the keyboard is. That is
what lets the rail stay up while the keyboard is in an agent's session: the
changeset, the window and the bill are the whole session's whichever agent is
on screen, and the mark is what stops them being read as that agent's. A
chord walks the map, in both directions and wrapping at both ends, so moving
between sessions is a keystroke rather than a surface to open and close.
Everything you do *to* an agent — answer it, steer it, retry it, cancel it,
kill it — is still the manager's; the map is for seeing and moving.

The order is the order the sessions were started, with one exception: an
agent waiting on an answer floats to directly under the root, and the ones
waiting keep the started order among themselves. Everything the run needs
from a person is in those rows, so they are where the eye lands rather than
wherever the run happened to reach them. The chord does not float with them.
It walks the started order whole, folded rows included, and that is where
the map and the chord are allowed to differ: a key that reordered
itself under the reader's hand every time an agent blocked or was answered
would be a key nobody could aim.

An agent started by another agent is drawn one column in under the rest,
behind the same corner every nested thing on this interface is drawn behind,
so a run two levels deep reads as two levels rather than as five siblings.
It is drawn directly under the agent that started it, and what floats is
that whole branch: an agent whose own child is waiting floats with it,
and nothing floating or folding lands between an agent and the one it
started, since a corner under the wrong row names the wrong parent. That is
the manager's order too, and the chord walks it: one press is one row down
the map, an agent and then the ones it started before the next sibling,
because the map is what the reader is looking at when they press it.
Leaving a session still goes to whatever started it rather than to the root.

When the rail runs short of height the map gives up its rows in the order it
can afford to lose them: an agent that has stopped goes first and the oldest
of those before the newest, then one that has not started yet, and an agent
still working is the last thing taken — only once nothing else on the rail
has a row to give. The live half of a run is what the block is for; its
finished half is in the transcript and in the manager, and its outcome has
already been read once. Between two blocks of the same length the lower one
gives first, because the rail is read downwards and the block nearer the top
is nearer the turn it is about.

An agent that declared no step count draws motion beside what it is doing,
since a bar against a denominator nobody supplied is a number the interface
invented. There is one denominator nobody had to declare: the fresh tokens
the agent was given to spend. Once half of them are gone the lane draws
that instead — the bar, the intake and the budget, in the same five cells a
declared step count gets — so a ceiling is something seen coming rather than
read about afterwards in the word the agent died on. Under half it stays the
spinner: a quarter spent is not news.

Two things the line under an agent says that its state cannot. An agent told
twice in one turn that it has left its task carries that count in the weight
a failure takes, because once is the machinery working and twice is the case
worth knowing forty rounds before the report says so. And an agent that
failed leaving a record a replacement could resume from says the record was
kept, and names the key that uses it. The row stays inert — that key is the
manager's — which is the reason the block ends the way it does.

The map ends on a line naming how to reach what it draws, in the voice the
plan's own list already uses: the manager, the chord to the next session, and
the pointer. It is there only when there is a session to reach, and it is the
first row the block gives up when the rail runs short, because a map missing
one row is still a map and a hint with no map under it is not a hint.

Two of the rail's lists are places to go from rather than only to read. A
click on a changed file opens that file's diff full screen, and the same
click closes it again; a click on a session moves the keyboard into it, and a
click on the row already marked comes back. Both have the key that reaches
them by name already — the file's diff is a command with a path, and the map
is walked by a chord and by the manager — which is the test a target has to
pass here: the pointer names exactly one thing, and the thing it names is
reachable without a pointer.

A block is a door to the whole of what it bounds. Where a block has a surface
holding all of it, its heading and its fold marker open that surface: SUMMARY
opens every reading the session has taken, THIS TURN every turn the session
has run, ALERTS every alert the session has had, standing and superseded,
CHANGES opens the session's diff, AGENTS the manager, STEPS the whole working list,
PLAN the approved plan's whole checklist on that same screen,
TODO the backlog screen, CONTEXT the occupancy screen and SPEND the session's whole bill — each the surface its command already opens, so
the command is the door's key. The surface is left by its own esc, and by a
click on the cell that opened it: the surface stands over the rail, so the
row is not there to click again, but the cell is, and a click that opened a
thing closes it — the way a changed file's diff always has. A block with no
surface behind it keeps its heading and marker inert, and so does a meter.
Nothing on the rail takes the keyboard: the draft keeps every character it
had, and no door has a key on the rail itself.

One block is not about the work at all: where the session's tools came from.
A server that failed to answer leaves no trace in a transcript — a tool that
was never registered is indistinguishable from one the model chose not to
call — so the sources say whether they are up in a glyph and a word, with the
count of tools each brought or the one thing standing in the way. It is
present only when something outside shhh was configured, because a session
with nothing but its own tools has no way to have lost any, and it folds past
a few rows: whether what was configured is up is the question, and the whole
listing is a command away.

The last block is the bill. Its heading carries what the running turn has
cost; the rows under it are the session's bill in shares, one per model that
answered — the model, what its own requests cost and what kinds of request
they were, then what the children that ran on it cost after their `◇` — and
the last row is `session total`, which the shares add up to. The machinery
around a turn — the permission classifier, the session's readings, the title
— [runs on a smaller model](../capabilities/providers.md#a-bounded-call-runs-on-the-small-model)
than the turn does, so a session's total is rarely what its own model's row
says, and the rows are what explain the difference without a reader having
to know it. A kind of request that spent nothing is not named, and where
everything ran on one model the block is that one row and the total. As the
rail narrows a row gives up its words before its figures: the kinds fold to
the first and a count, then the model's name shortens to its family word,
then goes. When the rail runs short of height the shares fold and the total
stays, because it is the one figure the rest of the block is about. The
heading and the fold marker open the whole bill (`/stats`, [the supporting
screens](#the-supporting-screens)), which reads the same ledger: every share
the block draws, each child's by name and each turn's.

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

**A reading with something to say is also a transcript row.** The rail holds
one reading and bounds it to three lines, which is what a rail is for — it is
a column of standing status, and a block that grew would push the counts under
it off the screen. But a longer reading is then a sentence nobody can finish,
and a reading that interrupted the turn is the reason for the steer below it.
So those readings land in the activity feed as one folded row as well: closed
it is the round it was taken at, its verdict and how many lines opening it
costs; opened it is the reading whole, the verdict in the same marks the rail
uses, the reason behind a departure, and the instruction the verdict was
reached against — the last of which the rail never had room for at all.

A quiet reading gets no row. It is quiet when its verdict is on target or
unclear — the two that never interrupt a turn — and the rail's block draws it
whole at the narrowest the rail is ever drawn. Such a reading is already said
in full where it belongs: at 130 columns and wider it is the rail's block,
below that its verdict is the frame's status row (`· unclear · as of round
2`) and its text is on `/status`. A row under every close carrying the
same verdict a second time is the feed telling the reader nothing, once a
turn. A reading that went off target, found the run has what it needs, or is
longer than the rail's bound always gets its row, which stays the one place a
long reading is read whole and the record of what a steer was earned by —
including one the reader then took back. This is drawing, not recording: the
session's record files every reading, quiet or not.

The rows are every such reading rather than the latest one because those
readings in order are the run's own account of where it went wrong or was
done. What it believed it was doing at round 6 and again at round 24 is then a
thing the transcript can be scrolled for, which is the reconstruction the rail
exists to remove and could only ever perform for the present moment. A failed
reading still writes nothing: the rail keeps what it had, and a line reporting
that one request timed out is not news.

Every reading that lands, quiet or not, is also kept for the session, and the
SUMMARY heading opens them as one list ([the supporting
screens](#the-supporting-screens)): the scroll the rows ask for, without the
turns between them, and with the quiet readings the feed leaves out.

### The agent manager

Sub-agents are visible and steerable while they run: what each is doing, how
far in, what it is waiting on. Attaching to one is not a new surface — it
switches which agent the session is looking at, and every agent including the
root is the same kind of thing.

A child's approvals route to wherever you are, so detaching does not mean
missing a decision.

**A child is drawn twice, and the two drawings disagree about one column on
purpose.** In the transcript a fan-out gives each child a lane; in the manager
and in the rail's map the same child is a row. A lane keeps `◇` in every state
it has, in the colour the state wears, and says how the child is doing in
words in the field on the right — `▰▰▰▱▱ 2/5`, `✓ 5/5`, `⚠ needs you`: a child
is a child from the moment it is queued until it stops being one, and it will
be many acts before it is anything. A row is that same child as one thing you
are about to act on, so it keeps the rule every other row in the product
keeps: `⚠ ✗ ▸ ⊘ ✦` take the lead column from the kind mark and `✓` never does.
A blocked child leads its row with `⚠` and says what it is waiting for
underneath; a finished one keeps `◇` and puts its tick in the outcome field.
Both readings come from one renderer, so the lane and the row can differ in
that column and nowhere else.

A child parked by [a hold](../capabilities/subagents.md#a-hold-reaches-the-whole-fan-out)
says `⏸ held` in that same field, on the lane and on the row alike. It is not
the word an idle child gets: a cancelled turn waiting to be steered and a
fan-out the reader stopped on purpose are two different stopped things, and
only one of them is news. The parks land one child at a time, so the header's
tally is what says how far through the hold is — `2 held · 1 running` — and
the rail's map says `held` where a working child's row says what it is doing.
The word is said once on each surface, and the key that lets the hold go is
named only on the orchestrator's frame, the one place it is live: the map
row's line underneath does not repeat it, and the frame of an attached child
says `held` and names no key, because attached the chord types into the
draft.

**A descendant is drawn under the agent that spawned it, on every surface
that draws more than one agent.** An agent is followed by the agents it
spawned, and each of those is drawn one column in behind the frame's own
corner: `└◇ reviewer-1a` in the manager and in the rail's map, and the same
corner hard against the same glyph on a fan-out lane, where it takes the two
gutter columns a lane never uses. The parent's lane says how many are under it
(`2 agents under it`) while any of them are live, because a lane with nothing
to report of its own is otherwise a lane that has stopped for no stated
reason.

**A lane that has stopped folds open on the report its child wrote.** The
first line of it stays on the lane's detail line, and under that is the fold
every other body in the transcript wears — `▸ report · 14 lines · [enter]
expand` — opened by the reading key that opens all of them. Open, it is
bounded the way a tool's output is, and a report longer than the bound has the
same third depth: the tail counts what it held back and says `[enter] opens
the whole of it`, and that key or a click on the report takes the whole of it
full screen, the block's reports one after another. A child's report
used to reach the model and nobody else; the person holding the approval keys
got the first line and the option of attaching to a child they had no reason
to think was holding anything. The fold is the transcript's alone, because the
transcript is where a turn is read afterwards and the manager is a list of
things to act on now.

Two things the report says that a first line does not, the lane says itself.
How many assumptions the child stated instead of asking is counted beside the
summary (`· 2 assumptions`): a child is never offered the tool that asks, so
an assumption is what stands where a question would have been, and it is the
part of a report a reader may want to disagree with. And a reviewing child's
verdict — the word its report ends on — stands beside the state in the outcome
field, `✓ done · approve with changes`, because what became of the run and what
the run concluded are two facts and only the second is acted on. A report with
no verdict on its last line draws `done` alone. As the pane narrows the cost
gives way first and the verdict after it; the name never does.

What floats is the group and not the row. A request under a descendant lifts
the whole group — the agent, and everything drawn under it — to the top of the
list, so the row a reader has to answer is both at the top and still directly
under the row it belongs to. A corner floated away from the row it hangs off
says less than no corner at all, and the deepest column of the lane's gutter
is one column wide, so how far down a tree runs is a question the rail's map
answers and a lane does not.

A fan-out offers the manager once, on a line under the lane that needs you —
`[f12] agents · the other two keep running`, the key from the pointer and
from reading mode's cursor alike, since the manager has no letter — and
nowhere at all while no lane is waiting, so the key sits where there is
something to answer
([and not the answer itself](departures.md#a-fan-out-offers-the-manager-not-the-answer)).

**A row joins the child's name to its task the way every row joins two
facts**, with the separator, not with a gap: `writer-1 · docs/loop.md`. The
artboard draws the manager's two as adjacent columns and the manager does not
keep that. A column needs a fixed width, and a name is not a word from a
closed vocabulary — clipped to one, two children of the same profile become
the same string, and left unclipped the tasks beside them never line up, which
is a rule drawn where there is no rule. The lane above it reads the same way,
so a reader who has learned the separator on one has learned it on the other.

**The key row states what the list can do, and not what the pointer is on.**
Answering a blocked child in place is offered whenever any child is blocked;
killing every child is offered whenever more than one is still running, and
that key says how far it reaches — `[K] kill all · every level` — because the
rows in front of the reader are one level of a tree it walks the whole of. An
offer a reader has to go hunting for with the pointer is indistinguishable
from an offer that is not there, which is the whole reason the manager is
opened. Answering takes the child under the pointer where that is the one
waiting, and otherwise the first one that is — blocked children sort to the
top, so that is the child the manager was opened for. The keys aimed at one
child — steering it, cancelling, killing, and running a failed one again —
stay with the row the pointer is on, because their target is the one thing
that must never be guessed.

**A writer's kept patch is offered from its row.** A writer that stopped with
work that never reached your checkout says `patch kept · [p] review` in the
outcome field, where a blocked child says `⚠ needs you`: the lead column
already says how it ended, so the field spends itself on the one thing left to
do. `[p]` opens the patch full screen, headed `writer-1's patch` — the view
`[d]` opens from a live card — and esc from it lands on the patch card over
the list, apply or decline, the card a finishing writer's patch is put on. The
rail's line under the same child carries the same words and, like its
`handoff kept · [r] retry`, names the manager's key rather than acting on it
([a failed child leaves a handoff](../capabilities/subagents.md#a-failed-child-leaves-a-handoff)).

**A patch your checkout moved under is shown merged.** Where a writer's patch
no longer applies to your files as they stand, the card's diff is its merge
over them, never the writer's original, and a `merged` row under `touches`
says so — `merged    over 2 files that moved since it started` — because a
reader who has watched the lane expects the writer's own lines and would
otherwise be reading an unexplained difference. Approving it when the
checkout has moved again since lands nothing: the card comes back with
the merge redone. A patch whose changes meet yours on the same lines is
not put on a card at all; it is kept, and its row offers `[p] review`
([a writer starts from your tree](../capabilities/subagents.md#a-writer-starts-from-your-tree)).

**A redirect is typed on the row, not in the child's session.** Over a child
that is queued, running or blocked, `[s]` opens the one-line field the
question card opens, under the row it will reach and labelled with that
child's name; enter sends it as the same message typing at the child's own
lane sends, and the lane says where it came from. The argument is the one
answering in place is built on: opening the manager *because* a child has
drifted should not then send you into that child's session to say so. It is
the cheap half of the pair it sits beside — a child reads a redirect and
carries on, and a kill cannot be taken back — so it is the correction that
should cost the least, not the most. While the field has the keyboard every
letter is a letter of the redirect, so the list's own keys leave the key row
and what is left is the two any surface being typed into keeps: enter sends
it, esc leaves it unsent
([a key is inert until its surface holds the keyboard](principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard)).
A child that failed is not offered the key, because it has nothing left to
redirect — running it again is `[r]`. A child that has finished is, and there
the key reads `follow up`: what is typed is the next question on the
conversation it already holds, and its row goes from `done` back to running
with that question's first words under it
([a finished child can be spoken to again](../capabilities/subagents.md#three-can-steer-a-child-and-none-of-them-can-end-it)).

**Ending a child has one name, and it is here.** `[X]` on the row, with the
confirm that counts what goes with it. Attached, `/exit` used to be a second
name for the same act — and it is the word that quits the whole session
everywhere else in the product, so a reader who typed it to leave a child's
surface ended the child instead. It no longer ends anything: it says where
the act lives, and esc is how you leave.

**Answering in place is answering, and leaving is leaving.** The card the
manager opens over the list is the same card the request would have drawn
anywhere else, and the two answers mean on it what they mean everywhere. Esc
is the third thing and it is not an answer: it puts the reader back on the
list with the request still queued, and the row they land on still says the
child needs them. The argument for esc declining there was that there is no
draft underneath to hand the keyboard back to — but what is underneath is the
list, which is showing the decision as plainly as a draft would have, and
[escape never abandons work](principles.md#esc-is-always-the-safe-answer).

**A routed command card offers the grant, and the grant is the turn's.** A
permission the session makes already reaches every child, so the reader
answering the twentieth identical request from a fan-out can make it from the
card in front of them instead of waiting for one of the session's own. The
offer names both halves of what it covers — the command's shape, and every
agent — and it names its end, which is this turn: the length is fixed here
rather than chosen from a list, because the reach is the thing being decided
and a standing permission is wider than the card is about. A safety-flagged
command is the one place the key is missing, here as everywhere, and the card
says so where the key would have been. The card does not gain the session
card's list of lengths, so the key section `/help` writes while it waits names
its `[a]` in its own words — this command, every agent, this turn — rather than in the
session card's words, which promise a choice of how long that this card does
not draw.

**The manager does not open over one of the session's own decisions, and says
so.** A command, a plan or a question waiting in the panel holds it — [one
panel holds one thing](principles.md#one-interaction-panel) — so the chord is
refused rather than obeyed. The refusal is a notice naming what is holding the
panel and the two keys that free it, because a chord that does nothing at all
is indistinguishable from a chord that is broken. A child's routed card is not
one of these: it steps aside while the list is open and comes back when it
closes. Nor is reading mode: it holds the panel, but as a way of looking
rather than a question, so the chord leaves it the way a typed character does
and the list opens.

**Attached, another agent waiting is not left off the screen.** The card is
narrowed to the agent whose transcript is on screen, so a request from any
other child would otherwise be visible nowhere while the reader reads. The
attached frame's bottom rail carries it — `⚠ 1 other agent waiting`, with the
chord to the manager beside it, which stops shedding for as long as the
warning is up. The frame's top rail counts what is waiting; this says that
some of it is somewhere else, and how to get there.

Attaching does not take the inspector rail with it. What you are looking at
is one agent's transcript; what the rail reports — what this run has changed,
how full the window is, what it has all cost — is the whole session's, and it
is the same whichever agent has the keyboard. Its map is what says which
agent that is, by marking the row, so nothing on screen has to be read twice
to work out whose numbers are whose.

The list ends with a short section that is not agents: the roles this session
can spawn, and under them the offer to draft a new one. The manager is where a
person goes to find out what this session has, which is the argument both rows
are built on — it makes this the one place where *and none of these is what I
want* is a thought somebody is already having, so the answer to it is a row
rather than a command they have to know, and it makes it the place the
question before that one is asked too: *what has it got*. A role was named
nowhere a reader could reach until here.

A role's row says what it is called, what it is for, and where the file that
says so lives — `project`, `global`, or `built-in` for the roles shhh ships,
which live in no file — and `[enter]` opens that file in your editor, the way
editing a memory hands you its text. The way back from the editor reads the
file again, so the next child of that role this session spawns is the file as
you left it, and the note under the list says so. A file that no longer loads
changes nothing: the role stays as it was and the note carries the loader's
reason, rather than leaving it to be discovered from a child that behaved the
old way. A role with no file behind it is offered no `[enter]`, because there
would be nothing to open. The offer to draft stands only where drafting is
wired, and the keys that act on an agent are silent over every row in this
section.

## Cards

### The approval card

The single surface for every approval-gated action, in four body variants:
a command, an edit, a fan-out, and everything else.

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
edits one file under internal/agent*, *⚠ low · changes no files*, *⚠ HIGH*
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

A row of the body is a label and a value, and the sentence after the dash is
drawn only where it says something the value does not about *this* call — a
path and its size, the hosts a list allows, a file of yours the commit leaves
behind, what could not be resolved, a skipped hook with the `/trust` that runs
it. A sentence that would read the same on the next card with the same value —
*the command resolved to reads only*, *no workspace file is modified*, *the
profile allows network access*, *shhh never pushes* — is left off, so a quiet
card is its values and the one row with something to say stands out from
them. Which sentences are fixed is one table beside the rows, not a judgement
each card makes, and a command card's full view keeps every one of them:
they are moved, not lost. This is a departure from the artboards, which gloss every row
([a card row's gloss is a fact about the call, or nothing](departures.md#a-card-rows-gloss-is-a-fact-about-the-call-or-nothing)).

The card's border carries how much the decision on it weighs, and the run of
its top edge between the title and the chips carries nothing — so that run is
drawn as chrome, in the tone a screen's rule is drawn in, while the corners,
the title's lead-in and the chips keep the weight. The frame still says what
it said; the empty part of it stops pretending to.

The title is neither the frame nor the fill. It is the name of the thing being
decided, so it is drawn the way every heading is — the brightest grey, bold —
whatever tone the border around it is carrying. A card in the colour of
failure whose title is also written in that colour says the severity twice and
leaves the reader nothing to read the name off; and where a title is too long
for the rail, the mark that says so is the title's own last cell, in the
title's own weight, because it is the title that was cut short and not the
border.

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
command · [n] deny · [esc] not now — it keeps waiting*, the way out last. A key
that is not offered stays on the card with its reason rather than
disappearing. The compact `[y/n/a]` prompt this card used to print, with what
its keys bought in parentheses beside it, was the one place two notations sat
a row apart — the offers under a frame's rule read one way and the offers
under a card's read another — and a reader who has learned that a bracket
means a live key is worse served by two notations than by one.

The keys are one row, and the card ends on it. Under it sits at most one dim
footnote, which has one use: a key the reader might expect that is not live.
Where a key is deliberately not offered, the footnote names it and says why;
otherwise, on a card that took the keyboard by arriving, it names the
handover chord — *answer it · other keys type into the draft* — which is
what buys the keys the card did not claim. A card with both states the absent
key, because its reason is what keeps the missing offer from reading as a
bug, and the handover goes on working unshown. A card that ended on three rows
of sentences about the keyboard put its explanation where its answers belong.

A run too long for the card takes another row, and an offer that is itself
wider than the card folds at a word onto a continuation row indented under
its words rather than under its bracket — *[a] allow "go test" for every agent
until this turn* over *ends, and internal/agent with it* — so the key column
reads down the block as keys and nothing else. The offer never ends on the
frame's ellipsis, which marks a clipped target: an offer's tail is the half
that states what the key costs, and cutting it leaves a key whose price is
unread ([invariant 4](principles.md#fold-never-hide)). The artboards draw no
card narrow enough to need the fold; this is the shape where they are silent.

Every card holding the keyboard ends its run on the esc offer, whatever it is
about and whoever it came from, because the way out of a decision is the one
thing a reader must be able to find without having pressed anything first
([invariant 3](principles.md#esc-is-always-the-safe-answer)). The block the
run is drawn in never scrolls, so a run that wraps keeps it on screen all the
same. The offer joins the run's last row where it fits there and otherwise
takes a row of its own, so a narrow card breaks the run before it rather than
inside it. The words every gated card shares give up whatever it takes to
stay on the row — the trailing clause first, since *leave it waiting* already
says the decision is not answered, then all but *wait*, and at the narrowest
the words themselves, leaving the bare `[esc]` in the safe answer's colour as
the run's last offer — so the way out never takes a row of its own for them;
but a card's own words join whole or not at all: they are on
the card because the first clause was not enough to tell esc from that card's
no.
A card whose own field or list holds the keyboard states that surface's esc
instead, once: two surfaces cannot both have the key, and the nearer one wins.
A card that does not have the keyboard at all states none — esc there belongs
to the draft, and the line saying so is the handover's.

What the offer says is what esc actually does on that card, which is not one
thing. On a gated card it hands the keyboard back and leaves the request
where it was, which is a different act from the denial `[n]` is; on a card
picked off the [agent manager](#the-agent-manager) there is no draft
underneath, so it hands the reader back to the list instead — the request
still queued, and the offer naming the surface it returns to rather than the
draft it did not come from.

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
Beside *allow* and *deny* sit *run with a note* and *deny with a note* — the
yes taking the card's own verb, so an edit card says *apply with a note* and a
spawn card *start with a note* — each opening the note field under the card,
which asks *what next* or *why not*; the note travels with
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

A fan-out is a variant of its own. Starting an agent is an approval-gated call
like any other and used to arrive as one generic card per child: three
researchers were three cards reading *Approve tool (1 of 3)*, the task buried
in a summary line, nothing to grant on any of them. The spawn card is titled
with the role — *Start a writer* — and its body is a row per child: the mark
every sub-agent surface draws a child with, the child's name, and the line it
was asked to do. One child leaves the profile's own clause under its row and
states what it may touch in the block where every card answers that question;
several put each scope under its own row instead, because the block has one
slot per question and three children have three answers to the first of them.

The children of one round are one decision. `[y]` starts the set and `[n]`
refuses it, and the key over the queue picks that set down to the rows wanted
— the same list every other queue is answered on. A card that is the whole of
what is waiting carries neither the strip above it nor a position in its
title: both would count the same children a second time inside the one panel a
decision is drawn in. What differs between the children is on their rows and
what is the same is in the block, which is why a child differing in what it
costs or reaches is not on the card at all — a block standing for a child it
does not hold to would be the one thing there a reader cannot check.

*Allow without asking* is offered on a spawn card only where the role changes
nothing, and what it grants is the role
([`../capabilities/approvals-and-safety.md`](../capabilities/approvals-and-safety.md#a-read-only-role-is-granted-once)).
There is one row and not three: a role is already exact, and a fan-out happens
once in a turn, so the turn is not a length worth offering. A writer's spawn
carries no such key — a writer's patch is the decision that matters, and this
card is where the person reads what it will claim.

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

The shape also decides what the card costs the screen. A yes-or-no is one row
of answer, so it arrives the way a decision that landed mid-sentence arrives:
it takes the rows it needs above the input frame and nothing else moves. [The
rail on the right](#the-inspector-rail) keeps its columns, the frame keeps
[the vitals](#the-input-frame) under it, and the transcript is still being
read behind the question. That holds once the card has the keyboard too: the
frame does not go away, and the rule between the two names the card as the
surface holding it, which is the whole of what the reader needs told.

A free answer rides the same way, because a sentence has no more to show than a
keystroke does: one field under the question, and nothing beside it that the
width would buy. The field grows with what is typed into it the way [the
draft](#the-input-frame) grows — a row at a time from the one it opens on, up
to the draft's own ceiling, past which it scrolls inside itself — so a
one-line answer costs one row. The card takes the screen only once it has
grown past the forty percent the panel is allowed, which is the point where the
answer has stopped being a line and become something to read, and there it has
the headroom the plan card has, so the room it took the
screen for is room it gets. Cut back under the forty percent, the card gives
the screen back.

Every other shape takes the screen. A list needs the width for its
descriptions and its short fields, a sheet of tabs needs it for the strip, and
[an approval](#the-approval-card) needs it for the rows that say what is about
to happen to the machine — so each of those keeps the whole surface for as
long as it is up, and the rail, the frame and the reading behind them are what
pays for it. The line between the two is what this card is for: a question
that costs a keystroke or a line must not cost the cockpit, and one that has
to be read costs what reading it takes.

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

The plan card's rows are the modes the plan can run in here, plus keep
planning and reject. Where it runs at all is two keys on the card's key row:
`[n]` carries the approved plan into a new session and starts nothing, so it
names no mode and the new session stays in plan mode; `[i]` carries it the
same way and starts the execution turn there at once, so it names the mode it
enters — `implement in a new session — accept edits mode`. Every answer that
starts work says which mode it starts it in
([an approved plan is an artifact](../capabilities/coding-agent.md#an-approved-plan-is-an-artifact)).

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

### The rewind

One key takes the last turn back; the timeline makes the whole session
addressable. It is three surfaces in a row — the picker over the turns, the
card that asks what "back" means here, and the row the act lands as — and the
first of them never acts.

The picker is a card of the [selector](#selectors) family, opened over a
catalog and so opened as a search, under the rule that names the surface
holding the keyboard: `──── REWIND · pick a turn to return to ────`, in the
same treatment DRAFT, DECISION and READING are drawn in. The rail says what
taking a row means, because the picker is the one list in the product whose
rows look like acts and are not: picking a turn opens the question, and the
card behind it is what answers it.

Picking turn N returns to how things stood when turn N ended: turns 1–N stay,
and the turns after it are what the card offers to take back. Every surface
of the rewind names that one moment — the row's number, `/rewind N`, the
card's `Rewind to turn N` and its `turns N+1–M`, and the frame's `at turn N`
— because a picker that meant the end of a turn beside a command that meant
the start of it would put every figure one turn apart. The latest turn is
where the session already stands, so taking it says so and moves nothing;
`/rewind 0` is the start of the session, before anything was said.

A row is a turn — the words that started it, what it changed, and how long
ago. The turn's own number leads it, so a reader comparing a row with a close
row's `turn 6` is comparing the same figure, and the rows read newest first
because the turn a reader wants back is almost always a recent one. What the
turn changed follows the words after the separator every row joins two facts
with: the mutation mark in the accent every write on the transcript wears, the
file count in the grey between them, and the lines added and removed in the
diff's own two registers. A turn that wrote nothing says `reads only, nothing
changed` there rather than leaving the column blank — the column is what the
reader is scanning, and a blank in it reads as a row nobody measured. The age
is the short field at the end, and a turn whose moment is not known reports
none rather than an invented one.

A turn whose end the files cannot be returned to stays in the list. It is drawn
as any unavailable row is — behind `⊘`, with the reason where the diffstat
would be — and it is still selectable, because choosing it is how the surface
says why. Talk only still works past it: the conversation is shhh's to give
back, and what the records never held never was. The boundary is a fact about
the records of the turns after it rather than about the tree: a run of turns
whose records were dropped to stay inside the changeset store's size limit, or
a conversation that came back from the store without them at all.

The picker has one key of its own, and it is a reading rather than an act:
`[d]` opens what a rewind to the row under the pointer would take back — every
turn after it, one net change per file, the same reading the card's code field
states in figures — full screen, and esc comes back to the picker as it was
left. A row is picked by what its turns changed, and the diffstat is the
summary of that; the diff is the thing itself, read before the question is
asked rather than after the answer. Like every letter on a card that opens as
a search it is live once the query row is closed. Where there is nothing to
read — the latest turn, a row past the boundary, a run that wrote nothing on
record — the card says which on its warning line and stays where it was.

The scope card is what a taken row opens. A rewind is two rewinds arriving as
one word — the files a run of turns wrote, and the turns themselves — and the
card states them apart, in the same field block an approval states a blast
radius in: what comes back on disk and where it comes from, what leaves the
window and what the window costs afterwards, and whether the act can be taken
back. The three answers are on one row under the rule, both leading, because
both is the usual reading of "go back"; code only is *the approach was wrong,
keep the knowledge* and talk only is *the model went down a bad path, keep the
files*. Esc is the safe answer in the fullest sense the product has — nothing
restored, no turn out of the window, and the picker still there.

The rewind lands as a row, because a rewind is a turn: it is counted, it is
reviewable, and the key that undoes a turn takes it back. It carries the
mutation rail and the edit glyph where files came back, and neither where the
window moved and the machine was not touched — the reading a compaction's own
row takes, for the same reason. What it says is where the workspace was put
back to, which turns left the window, and what the window holds now. The two
halves land as one row and not two: the file half is answered at the undo
confirm, and the row waits for that answer. The window's figure on the row is
the one the card predicted: the provider's last count covered the turns the
cut takes out, so both read the window afterwards as this session's corrected
estimate of what is kept, and a card that named one figure over a row that
lands on another would be a question answered with a different fact.

The turns the rewind took back stay on the transcript, above that row, as one
fold: `▸ turns 6–7 · rewound · 2 turns, 3 files' worth of work · [enter] read
them · [r] reapply — undo the rewind`. They are out of the window — the model
is never sent them again, and nothing that counts turns or reads a verdict off
the transcript counts them — but they are not out of the record, for the reason
a compaction's folded turns are not: a rewind is about where the conversation
goes on from, and a transcript that dropped the rows would be answering a
question nobody asked it. Opening the fold draws the turns as they were, and
the search counts what it holds while it is closed and opens it onto the match.
The branch the tail is saved as is still there; the fold is the same turns
where the reader last saw them. `[r]` puts them back: the conversation is what
it was before the rewind, the turns' rows return below the rewind's own row,
the frame stops saying `at turn N`, and where the rewind also put files back,
the turn that restore landed as is put to the undo confirm, so the files come
back the way every undo brings them back. The fold stays as the record that the
turns were once taken back. It can only be put back onto the conversation it
was cut from, so once anything moves that conversation — a new turn, a branch
switched to, a compaction — the key goes and the fold is only something to
read.

The frame's top rail says `at turn N` where it would otherwise say `idle`, and
`at the start` after a rewind to turn 0 — a place the session can stand, not
the absence of one — until the next turn makes the default reading true
again. The transcript below
is shorter than it was, and where the session now stands is the one thing
about an idle session worth saying.

## Takeover surfaces

### The palette

One prompt over everything the session can reach: commands, sessions,
anything else addressable. The slash prefix is for a command you are already
typing; the palette is for one you are looking for. Its chord is the slash key
for that reason — the list a chord opens is the list the prefix completes —
and it is declared in both the spellings a terminal delivers that keystroke
in. A terminal that sends neither is not stranded: the help's key section
names the other door beside it, which is the prefix on an empty draft.

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

### The key list

Every key the session answers, over the session. The chord opens it, and so
do the start screen's offer of it and a bare `/help`: one card, whichever door.
It is the palette's shape — the ordinary selector with its filter open from the
first keystroke, in the panel the draft box was in — and it borrows the screen
the way the palette does, so a half-written prompt is exactly as it was when
the card goes and a running turn goes on streaming under it.

Reference is looked up, not written down. The list used to be a system row: the
whole register appended to the transcript, where it scrolled the conversation
away, was saved with it and came back on every resume, and said nothing about
the session it sat in. A card is opened, read and put away, and leaves nothing
behind — not in the transcript and not in the conversation. `/help` with words
after it is the one door that still writes, because a reader who asks for the
whole sheet by name is asking for a row.

The keys are listed by the groups a keymap file names them in, in that file's
order, each with its keystrokes and its words, read from the register when the
card opens — so a key a file moved is listed at what it answers now, never at
what it shipped with. Typing filters by the keys, the words, or the name the
file writes a key by; the title rail counts what the query left of the whole
register. The arrows move, the page keys page, `home` and `end` go to the two
ends, and esc or the chord again closes it. None of those is a letter, because
every letter is the query's. While the keyboard is pointed at a child the chord
keeps its meaning there and opens nothing.

### The start screen

A first launch in a repository shhh has never seen already knows the
repository, and offers work rather than a blank prompt.

The fact line is what shhh already knows that the header row above the
transcript does not say — the toolchain, whether the tree is dirty, how many
packages. Where the session is and on which branch are the header's, and a
screen that said them again one row under it would be saying them twice.
Clauses drop from the right as the terminal narrows and the first never drops.

Two things that govern what happens next are stated without being asked for:
what was read into the system prompt, and which check suite is in effect. A
suite that is not configured names the file it looked for; one that exists and
will not load says so, because a broken gate is not an absent one.

A trust row follows them only when there is something to say about the
checkout's answer
([approvals-and-safety.md](../capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs)):
what was withheld and `shhh trust`, where nobody has answered for it; or, in
the one session after a trusted checkout's declared files moved, the kinds
that changed and `shhh trust off`. A trusted checkout that has not moved
draws no row, because a row that reads the same on every session is a row
nobody reads by the third one.

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

A checkout whose toolchain declaration names tools the `PATH` lacks gets a
`tools` line under the others, naming them
([containment.md](../capabilities/containment.md#a-checkout-declares-the-toolchain-its-work-needs)),
and its last offer becomes the install in place of the check run those tools
would fail: one approval, the same card `/setup` opens, listing every line
before anything runs. The line goes when nothing is missing, and the offer
is not made where the session could not run the install.

Under the offers is one key row, led by the way in that is not a key — `or
just type what you want` — and then choosing, starting and the key list. It
is one row of at most six, like every key hint: the pointer's own chords,
which reach an offer too, are on the key list rather than doubled into the
row. The draft holds the keyboard here, so the list is the draft's chord and
not a bare key. The navigation row under it is a row of its own.

Typing anything dismisses the offers and their key row and keeps the facts,
because the input owns every ordinary key the moment there is a draft. The
navigation row stays, since its keys work with a half-written draft in the
box.

The machine's first session says three things above the key row, in dim, one
line each: enter sends what you type; esc backs out of anything and never
loses work; ctrl+c twice stops a run — each a key and what it does. Those are the three keys every
session turns on, and a reader who meets them first on an approval card is
learning them at the moment they are most needed and least read. The block
outlives the typing dismissal, since backing out and stopping are what a
reader with a draft in the box needs next, and it is never drawn again: the
first session is the one the data directory held no store for, and the mark
that says so is written as it is read.

An offer is reached three ways that are one act: the arrows and enter, the
pointer chords — shift on the arrows, the same chords that go on working in
the pane after the first turn — and a click on its row. Each runs the line
of input the offer names, through the submit typing it would take, so an
offer can never reach somewhere typing could not. The lead, the facts, the
first-run block and the hint lines are not targets: they name nothing to run.

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
offers the move. The doctor's own trust row is the same reading as the start
screen's — trusted, trusted and changed since a session last read it, or
withheld with `[a]` on it — and it only reads: the session that shows a
change is the one that records it, so a doctor run never uses the notice up.
The saved-chat browser is the same cut over conversations:
the list on the left, the one the pointer is on beside it — its title, and
under that the account the session kept of where it left off
(../capabilities/sessions-and-memory.md#a-title-you-did-not-write) — and the
renaming and deleting the picker inside a session already offers
(../capabilities/sessions-and-memory.md#housekeeping).

Sixteen surfaces take the whole terminal this way — those seven, the reading
of what is in the session's context, the ledger of what the session read
(`/sources`, [what was read](../capabilities/chat.md#what-was-read)), the
session's own working list (`/steps`, below), the readings it has taken of its
own run (`/readings`, below), the turns it has run (`/turns`, below), every
alert it has had (`/alerts`, below), its bill (`/stats`, below), the session's shared notebook
(`/notes`, below), the reading of the session's boundary (`/safety`, [the
safety reading](#the-safety-reading)) and the drafting flow for a new agent
profile — and they are one family rather than seventeen screens: the same header, the same
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
browser, **back to the prompt** on the context reading, the sources ledger, the
working list, the readings, the turns, the alerts, the bill, the safety reading, the notebook and the backlog, which is drawn on this chrome too ([the backlog screen](#the-backlog-screen)).
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

The settings screen opens on its flows section: one row for every bounded
call outside the main agent and its children — the classifier, the
explanation, the one-shot's description, the reading, the title, the account,
the compaction, the backlog readings and the profile drafter — with the model
the call will run on and, in the source column, which link of the chain gave
it: `flow key`, `cheap key`, `provider small model` or `session model`, and
the key that link read beside the model. It is the doctor's `flows` row laid
out as settings, read from the same chain the calls are sent with
([a bounded call runs on the small
model](../capabilities/providers.md#a-bounded-call-runs-on-the-small-model)),
so a second model on the bill is answered by looking. In a session a flow's
row opens the list the session's own model picker offers, and its key row
names three places the choice can go: `[enter]` **this session** — the model
answers that flow for the rest of the process and reaches no file, and the
row says `session` in its source column from then on; `[d]` **my settings**;
and `[g]` **this checkout**, offered where the screen stands in one and
refused with the writer's own sentence where the checkout is not trusted or
may not decide the key. The two files are written at once for that one key,
because the key already said which file the question was about, and the
session takes the model too, the way the model picker's own key switches the
session as it makes the default. These are the one exception to the session
keeping the settings it started on, and they are an exception by being asked
for by name. A flow no session sends — the one-shot's description, and the
compaction an unattended run makes — offers only the files. `shhh config`
has no session, so its flows rows stage like every other row, and `[g]` and
`[w]` reach either file.

Neither row repeats the other. The header carries the register's key and the
letter; the foot carries what the screen can do and, last, the way out. A
surface that put `[?]` and the letter on both rows spent its bottom row saying
what its top row had already said, which on the narrowest terminal is the row
that had least to give.

Nine of them list something and preview what the pointer is on — past
commands, saved commands, saved conversations, the pages this session
read, the steps it declared, the readings it took of its own run, the turns
it ran, the alerts it has had, and the notes its agents wrote each other — and
they split the terminal
the same way: two columns where there is
room for two, stacked where there is not, and the preview giving way to the
list when the rows run out, because a screen that cannot preview an item can
still say which items there are.

`/steps` is the rail's STEPS block read whole: every step the session
declared, numbered as the list numbers them, the one the agent is on marked
`current` and the ones it has marked finished `done`; beside the step under
the pointer, the paths it said it would touch and the calls the transcript
recorded for it, each drawn as the transcript drew it. Two different things
meet on this screen. The list is the session's working checklist — what the
agent said it would do. The transcript's steps are the model's titled runs of
calls ([the step](#the-step)). They usually share titles, and they are joined
by title, with case, spacing, a leading number and closing punctuation set
aside; a step no run is titled for says `not started`, because nothing in the
transcript is filed under it. A revised list shows the declaration the agent
is working to now, and the one it replaced is not kept. The screen is built
when it opens, reads and changes nothing, and the turn goes on underneath it.
It is opened by `/steps` and by a click on the STEPS heading
([the inspector rail](#the-inspector-rail)), and a session with no list says
so in a line instead.

While an approved plan is being executed the plan is the checklist, and the
same screen draws it instead of the working list: `/plan` in its header, the
plan's title and how many of its steps are done, every declared step with the
paths it named, and its state as the plan block reads it — `done`, `failed`,
`running`, or `queued` for one the run has not reached — beside the calls
that carried it out. A step's calls are the ones the transcript filed under
its number, not a title matched a second time, so the screen and the outline
cannot disagree about which work was which step. Under the list stands what
the run has done that the plan did not say — work off the plan, a step run
before an earlier one, a step passed over — or a line saying the run has kept
to it; where the rows cannot hold every step and that too, the steps stay and
the departures give way, since the plan block still counts them. It is opened
by bare `/plan`, by `/steps`, and by a click on the PLAN heading; bare `/plan`
with no plan running says so in a line.

`/readings` is the rail's SUMMARY block read as a history: every reading the
session has taken of its own run ([the session
summary](#the-session-summary)), newest first, one row each — the round it was
taken at and its verdict in the rail's own marks (`▸ r 9 · on target`, `⚠ r 14
· off target`), the turn it belongs to, and what became of it where it
interrupted the turn: `steered`, or `withdrawn` once the reader took that steer
back. Beside the reading under the pointer is the reading whole, drawn as the
transcript's opened summary row draws it — the text, the verdict, the reason
behind a departure and the instruction it was judged against as that
instruction stood then — because a reading here and the same reading in the
feed are one thing and are read the same way. A quiet reading, which earns no
transcript row, is kept here too: once the next reading replaces it on the
rail, this screen is the only place it is still said. The header counts the
readings and states what they have cost the session, which only `/status` said
before. The history belongs to the session rather than to the turn — a history
of one turn is the rail again — so it runs across turns and is gone at a new
session; it keeps the last two hundred, and once the oldest have gone a line
under the header says how many. It is built when it opens, reads and changes
nothing, and the turn goes on underneath it. It is opened by `/readings` and by
a click on the SUMMARY heading ([the inspector rail](#the-inspector-rail)), and
a session with no readings yet, or with the summary off, says so in a line
instead.

`/turns` is the rail's THIS TURN block read down the session: every turn it
has run, newest first, one row each — the close's own mark and how the turn
ended (`✓ turn 4 · done`, `⊘ turn 1 · cancelled`), what it wrote in the
mutation mark and its lines, or `changed no files`, and what it cost. Beside the
turn under the pointer is its close drawn whole ([the turn's
close](#the-turns-close)) — steps, tools, time and spend, the files it
changed, its commit and its checks' verdict — from the very block the
transcript's close row was drawn from, so the table and the rows cannot report
one turn two ways; the close's own keys are not offered there, since this
screen answers none of them. Under it are the files the turn changed. The turn
still running is on top, marked `running` and drawn from the rail's own
reading of it: the step it is on, its tools, and what it has written so far.
`[enter]` opens the turn's review, whose esc comes back to the list, and it is
grey on a turn that changed no files. The header counts the turns and their
tools and states the session's spend as `/stats` states it. A resumed
conversation is the gap: a close is never saved with the messages, so a turn
from a sitting that has ended is drawn as its number and the files the
session's records still hold for it, and says `no figures kept` — never a row
of zeros ([a stat that cannot be reported is left
out](principles.md#a-stat-that-cannot-be-reported-is-left-out)); an ended
turn that left no files leaves nothing true to draw, and has no row. It is
built when it opens, reads and changes nothing, and the turn goes on
underneath it. It is opened by `/turns` and by a click on the THIS TURN
heading ([the inspector rail](#the-inspector-rail)), and a session that has
run no turns says so in a line instead.

`/alerts` is the rail's ALERTS block read whole: every alert the session has
had ([the inspector rail](#the-inspector-rail)) — the two the block draws, the
older standing ones behind its marker, and every superseded one it only
counts. Standing come first and then superseded, each newest first, one row
an episode in the block row's own words: the mark (`✗` standing, `✓`
superseded) and the command's name, `standing` or `superseded`, the last
run's outcome and the runs behind it, and the turn it broke in — `since turn
3` where it has gone on breaking. Beside the one under the pointer is its
account: the last run, the runs and the turns they took, the turn it first
broke in, and what answered it — `make check came back clean` or `quality
gate default passed`, with the turn it did — or `not yet`. `[enter]` shows
each run under that: its turn, how it ended, how long it took and the command
line that ran, and at the far end the evidence id its output was kept under
where the result was cut, so the whole output is one read away; where the id
will not fit beside the run it takes a row of its own rather than going.
The screen reads the very episodes the block reads, answered by the same
resolution the turn's close reads ([the turn's close](#the-turns-close)),
so a standing alert here is a standing alert on the rail and never a second
count of the transcript. The header counts the standing and the superseded
as two fields, the way the block's marker does. It is built when it opens,
reads and changes nothing, and the turn goes on underneath it. It is opened
by `/alerts` and by a click on the ALERTS heading or its marker ([the
inspector rail](#the-inspector-rail)); unlike the screens above it, a session
that has broken nothing still opens it, on one sentence saying so, because
that is the answer the reader asked for — and the block itself is gone once
nothing stands, so the command is then the only door.

`/stats` is the rail's SPEND block read whole: the session's bill, from the
ledger the block reads ([the inspector rail](#the-inspector-rail)). The list
opens on the session total and cuts the bill three ways under it: by model,
each row the block's own — the model, what its own requests cost and what
kinds of request they were, and what the children that ran on it cost after
their `◇`; by child, each sub-agent's share by name with the model it ran
on; and by turn, newest first, each turn's cost as its close row states it,
in the turns screen's mark and word. The three cuts are three readings of one
total and not three parts of it — a child's share is on its model's row and
on its own — so the header counts them as fields beside the total rather than
adding them up. Beside the row under the pointer is its account: what it
cost, what it was billed for — the tokens each way and what came from the
cache — and how many requests; on the total and on a model, each kind of
request with its own cost under that. `[enter]` on a turn opens it on the
turns screen with that turn under the pointer, and is grey anywhere else. A
turn whose figures were not kept is left off the bill rather than drawn as a
zero ([a stat that cannot be reported is left
out](principles.md#a-stat-that-cannot-be-reported-is-left-out)). What the
context window is occupied by is the other half the command used to print,
and it is `/context`'s ([the context surface](#the-context-surface)). It is
built when it opens, reads and changes nothing, and the turn goes on
underneath it. It is opened by `/stats` and by a click on the SPEND heading
or its marker; a session that has spent nothing opens it on one sentence
saying so, since the block is absent then and the command is the only door.
While the keyboard is in a child, `/stats` answers for that child alone, in
its own transcript.

`/notes` is the shared notebook — what one
agent found and the next should not have to find again
([what they share](../capabilities/subagents.md#what-they-share)). The list is
grouped under the agent that signed each note, because a fan-out's notes
arrive interleaved and the question somebody asks their own session is who
found what; where a note carries the lineage of the child that wrote it, the
group is the root of that signature — the child the session spawned — so a
task handed out is one group however deep the agent that did the work was,
and the preview names the agent in full. `[enter]` opens the note whole.
`[d]` drops the one under the pointer behind the same inline confirm the
saved-chat browser puts in front of a delete, and `/notes clear` asks over
the whole notebook, on this screen, so what it would take is in front of the
reader while they answer. Dropping is the only thing on this screen that
changes what the session holds, and it is the person's alone: no agent has a
tool that reaches it. An empty notebook prints one line into the transcript
instead of opening — there is nothing to point at.

**What is new is said at the turn's close and not on the rail.** The close
already carries the clause naming what the turn's children wrote, so the
count of what has not been read since the screen was last opened rides on the
end of it — `2 notes from writer-1, researcher-2 · 3 unread` — and it is
stated only where it says something the count before it does not. A NOTES
block on the rail was the alternative and it is not worth two rows: the rail
is the one surface in the product whose height is always spent against
something else on it, and what the block would say is one number that already
has a home in a line the reader is looking at
([the inspector rail](#the-inspector-rail)).

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
clips, because the pane beside the list carries it in full. What the letters
stand for is a line of the `?` reveal, built from the profile's own words — a
second profile's letters explain themselves with nothing written for them. Under the width
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

The last step is the draft over the card that writes it, and the draft is
laid out as its sections, one block each in the order the file keeps them:
the five prose sections, then tools and permissions, commands, and model and
budget. The pointer is on one block at a time. A prose section is revised on
its own — a note to the drafter opens under it, and the answer is taken for
that section alone with the rest sent as fixed context; the editor takes its
text and hands it back as the person's own; a key clears it — so fixing one
section never redrafts another. A mark after each heading says what has
happened to it: refined, written by the person, or empty. Every revision is
kept while the flow lasts, and esc on a revised block takes back its last one;
on a block with none it is the step's own esc and drops the draft. The card
lost its refine row and its note, since revision lives on the sections now:
it is the ways out and nothing else, and tab moves the keyboard between it
and the sections.

The card is the one thing on the surface that never gives ground: on a
terminal too short for both, the sections fold from the bottom and count what
they folded, with the profile's own scroll keys to read on, and the selected
block is kept in view. The card names the profile in its own title, so the
question stays answerable on a surface too short to keep the name above it.

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

### The safety reading

`/safety` (also `/security`) is the session's whole boundary on one screen:
the mode and what it grants, where the session may write and which
directories only a person's own `/add-dir` will open, what contains its
commands and what that mechanism masks and lets through, which hosts it
reaches without a card and which are refused, whether the checkout was
trusted and what it was not let load, each server and whether its calls ask,
the names of the secrets it holds and whether the environment mask is on, and
the registered tools in their tiers. Every one of those already had a command
that answers it; this is the eight answers in one place, in each command's
own words, because a boundary pieced together from eight commands is one the
reader has usually stopped assembling by the fourth
([one reading of the boundary](../capabilities/approvals-and-safety.md#one-reading-of-the-boundary)).

It is a take-over screen in the family rather than a report in the
transcript, and the width decided it: eight sections, several of them lists
of paths or hosts, stop being readable as one transcript row at sixty
columns, and a row the reader has to scroll the whole transcript to get past
is a row that pushed the conversation off the screen to say something the
reader asked for once. On the screen it scrolls instead, and each end of the
pane names the sections it is sitting on rather than only counting rows, so
the part not showing is an answer before the reader moves to it.

Each section ends on the command that changes it — `changed with /add-dir` —
and the screen has no other key than scrolling, `[?]` and the way back to the
prompt. That is the whole of what keeps it from becoming a second place to
edit a fact another command owns: it points at the owner instead. A grant is
listed with when it ends in the same words its card offered it under. What a
session does not have is a section that says so, dim, rather than a section
left out: containment that is unavailable, a checkout whose trust was
withheld, no servers. In a conversation the mode section says there is one
mode and no card, and the web section says a fetch runs without asking,
because a mode drawn there would be one the session does not run under
([a conversation has one mode](../capabilities/chat.md#a-conversation-has-one-mode)).

It reads the session as it stands when it is opened, the way the sources
ledger does, and is built again at the next opening: a directory added or a
mode changed while it was closed is on it the next time. Machine-level
containment — which mechanisms this host has, the probe that found them —
stays with `/sandbox doctor` and `shhh doctor`; this screen reads the one the
session already found. No artboard in the design system draws it yet, so its
layout is this family's chrome over sections that wrap rather than clip, and
that is a gap for the design to close rather than a disagreement with it.

### A staged attachment

A chip above the draft is the right answer to *what is attached* and the wrong
one the moment two screenshots are staged and the question is which of them
has the stack trace in it. That question has no verbal answer at any width, so
there is a surface that shows the attachment at full size.

Text opens here too, for a staged file of it: laid out from the top and from
the left, with whatever did not fit counted at the foot rather than trailing
off ([invariant 4](principles.md#fold-never-hide)).

Neither body scrolls. This is a preview of something staged, not a reader for
it — the question it answers is *is this the right thing to send*, and what
did not fit is counted rather than lost. Reading the whole of it is the
model's job, and it gets the whole of it either way.

No key is written on a chip: it sits above a live draft, and a key printed
there would be an offer nothing accepts while the draft holds the keyboard
([invariant 5](principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard)).
A chip is a door instead, reached three ways. [Reading mode](#reading-mode)
reaches the strip as its last row — the mode that already keeps the sentence
and holds the keyboard, so its keys are live and its own bar offers them —
and enter there opens the chip under the cursor. A click on a chip is the
pointer twin of that enter, and the same cell clicked again closes what it
opened; the count of chips the row gave up names none of them and is not a
target. And by name, which is the form for an empty draft: a command is
read only as the first word of the whole draft, so it is the one door out of
reach once a sentence is half typed, which is exactly when a screenshot has
just been pasted into it. Asking without a name takes the only staged image
when there is exactly one and refuses when there are two — guessing which was
meant is the mistake this surface exists to stop someone making.

Esc hands the pane back and destroys nothing — to the strip, with the cursor
on the same chip, when that is where the card was opened from, and to the
draft otherwise. Removing an attachment stays its own deliberate act, and the
card can do it: the card is where a wrong screenshot is recognised, so that
is the moment to take it back, with the key the paste reader already drops a
paste with. The strip's cursor drops the chip under it with the same key, and
moves on to the next chip, or back to the transcript when none is left; each
drop says what went, and a paste's fold leaves the sentence with it. Nor does
the act need a name typed from memory: asked bare, the drop opens a list of
what is staged — checked rows go, esc drops none — and a lone chip is a
one-line question naming it, defaulting to No.

A picture that will not decode still opens, onto the reason where the picture
would be. That it is staged and unreadable is a fact about the message you are
about to send, and a blank card would not have said it. A PDF does not open at
all: shhh does not render one, so there is nothing the card could say that the
chip has not said already.

A paste asks it harder, and asks it of a surface of its own. It arrived with
no name anybody chose and no file behind it to open in something else, so a
chip is the whole of what a reader knows about bytes they are about to send —
and the questions are whether it is the right log, whether all of it is there,
and whether to send it at all, only the last of which can be answered without
moving. So `/paste show` on a paste opens the reader the fold in the draft
leads to, described with [the frame](#the-input-frame): the same surface by
name and by key, because one thing drawn two ways by the door it was reached
through is two things to learn about one file.

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

The count is owed in the other direction too. Inline means the frame shares
the screen with what was already on it — including the frame before it — so a
row that stops short of the last column is a row whose tail is still whatever
was there. The alternatives card is as wide as it wants to be or as wide as
there is room for, whichever is less, and opening it in a wider terminal left
the result surface's own tail on screen beside it. Every row of every frame
reaches the last column, so a narrower frame after a wider one leaves nothing
of the wider one behind.

So each kind of row breaks the way that kind of row should. A sentence — the
explanation, a risk, the containment line — breaks between words, and reads
the same at every width. The key row breaks between one offer and the next
and never inside one, because half of `[esc] quit` is not an offer anybody
can take. A command breaks at the column, the way code does everywhere else
here: it is the one run that cannot be reflowed between words without
becoming a different command, and a folded command is still every character
in order where a clipped one is not.

**The containment line states what it knows.** It reads the command the way
the approval card does — what it writes, whether it leaves the machine, whose
privileges it runs with — and says each as a fact: `read-only · no network ·
no sudo`. Where the reading could not settle a facet, the facet is left out
rather than drawn as `unknown`: the one-shot's decision is taken on the
command itself and on the risk lines above it, and nothing about it changes
for a word saying that a reading is missing
([a stat that cannot be reported is left
out](principles.md#a-stat-that-cannot-be-reported-is-left-out)). The approval
card keeps its `unknown` rows, because there the reading is what is being
decided on. Whether a command escalates is always known, so the line is never
empty.

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

What a session here would load is readable without opening one. `shhh
skills` lists the skills and `shhh agents` the roles a coding session could
spawn — name, what it is for, and where it lives, a shipped role saying
`built-in` — in the same listing shape. Both read what a session reads, so a
checkout nobody has trusted contributes nothing to either, and the listing
says what it held back rather than looking as though the repository wrote
none.

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
