# The coding agent

The mode with the most delegated to it: an agent that reads the repository,
edits it, runs things, checks its own work, and can hand parts of the job to
children.

## A turn ends with what changed

The question after an agent stops is never "what did it say". Every turn
closes on what it did, what changed on disk, and whether the checks still
pass.

Every turn can be put back, from records the session kept rather than from
git, so a turn is undoable whether or not the repository is clean. Putting one
back asks first, states what it would restore and what it would delete, and
leaves alone anything that has changed since — overwriting that takes a
deliberate second answer. The undo is itself recorded as a change, so it can
be reviewed and undone in turn.

A file the turn deleted comes back with the permissions it had — whether the
session deleted it or a writer's patch did — so a script the agent removed is
executable again without anyone reaching for chmod. A file that is still there
keeps the permissions it has now: an undo puts content back and does not touch
access somebody set since.

A change of permissions and nothing else is still a change, so a writer's patch
that made a script executable and moved not a byte is a turn with something in
it. There are no lines to count for one, so the turn's row and its review state
the two modes where they would state the counts — a patch that changed the
lines as well states them beside its counts — and taking the turn back puts the
old mode back. A mode somebody changed by hand since counts as drift the
same way edited content does, and is left alone without a deliberate second
answer.

The session's own account of what it has changed says it too. A file the
session changed the permissions of and nothing else is on the list of what it
has touched, with the two modes where the row would put the lines it counted,
and opening that file shows the same thing rather than answering that the file
has not been changed. Leaving it off the list was the older reading: it kept
the list from claiming a file had changed by nothing, at the price of the one
thing that had.

## A rewind can put the files back

Going back to before a turn used to mean the conversation and nothing else,
and the message said so: the transcript was rewound and the files on disk were
left exactly as the abandoned turns had made them. That is a state neither the
person nor the model asked for — a conversation that never mentions the edits
in front of it — and the sentence explaining it was the product describing a
gap rather than filling one.

So a rewind asks which of the two is meant: the conversation, the files, or
both. The conversation half is what it always was, tail and all, kept as a
branch. The file half is every turn from the chosen point onwards, folded into
one net change per file — where the run of turns found each file, and where it
left it — and put back through the same question an undo asks: what it would
restore, what it would delete, and which files have changed since and are
therefore left alone unless a deliberate second answer says otherwise. Putting
them back is itself a change, recorded like any other, so a rewind's restore
can be reviewed and undone in turn.

**What a command changed is not in the records, and the card says so.** The
session records what the file tools wrote, both sides of every edit; a shell
command that moved, generated or deleted something wrote nothing through them
and is invisible here. A restore that quietly missed half of a turn's work
would be worse than one that never offered, so the row that would write files
names the hole before it is chosen.

**Where the tree is too large to read, the message says so rather than
guessing.** The line reporting whether the working tree still matches the
checkpoint is read off the same digest a quality verdict is pinned to, and
that digest covers the content of the changed files only up to a bound. Past
it — more dirty paths, or more changed bytes, than it will read — it stands
for their names and not for what is in them, so two readings can come out
identical over files that differ, which is what happens when a run of edits
stays inside files that were already dirty. The message then names the bound
and says the comparison could not be made, in place of the sentence claiming
nothing has changed. It is a warning and not a refusal: the restore is still
offered, because what it puts back comes from the session's own records rather
than from that reading.

A conversation opened again from an earlier sitting can still take its turns
back: the records are kept with the conversation rather than in the process
that made them, so closing the terminal is no longer the same act as
accepting every edit the session made. They are kept
for as long as the conversation is — the same window, and the same default of
keeping everything until one is set — and the size of what one sitting can
hold in memory is no longer what decides whether a turn can be taken back. A
conversation restored from a saved transcript is the one exception on the file
half: its messages say where each turn began and not what it was numbered, so
the offer there is the conversation alone rather than a guess at whose edits to
put back.

## Several places in one file are one call

Changing three places in one file used to be three calls: three rounds, three
diffs, and in manual mode three cards for something the person had already
decided once. The edit tool takes a list of replacements, so one file's worth
of changes arrives as one decision.

The list describes places, not steps. Every quoted snippet is matched against
the file as it was read rather than against the result of the replacement
before it, so the order the model listed them in cannot change the file that
comes out. That is what makes a batch worth trusting — and it is also why two
edits that would claim the same text are refused instead of resolved: there is
no order to resolve them by, so the refusal names both and the model splits
them or combines them into one.

Nothing is written unless every replacement applies. A single quote that does
not match refuses the whole call and leaves the file exactly as it was,
because a file changed halfway is worse than a file not changed at all:
nothing on screen says which half.

A quote that matched nothing is told what it nearly matched. The commonest
miss is an indent — text taken from a search result, a diff or a wrapped paste
carries whitespace the file does not have — and it is the one miss that cannot
be seen, because the quote and the line are identical on screen. So a failed
match looks for the single place in the file that differs from the quote in
leading and trailing whitespace alone, and names that line with the text as
the file writes it. It is never applied: whitespace is meaningful in some
languages, and this is a guess about intent, which is the model's to confirm
by re-quoting the line it has just been shown. Unnamed, the same miss costs a
re-read of a file the model already has.

The preview and the write run the same check, so a card never offers a change
the write would go on to refuse. And the staleness rule is unchanged, covering
the call rather than each element of it — it is one question about the file,
and every edit in the call was matched against that one answer.

A second file is a second call. What is batched is one file's several places,
which is the shape most changes actually have; a list of files would be one
decision covering changes a person would want to answer separately.

## A long call is counted while it is written

A round that rewrites two hundred lines spends most of itself writing the call
that does it, and until the call is finished there is nothing to put on a row:
no target, no outcome, no duration. The transcript's last act is whatever the
model said before it started, and the longer the file, the longer the screen
sits on it. Nothing is wrong, and the interface has no way to say so.

So the round counts what it is writing. The arguments stream in as fragments,
and one row says how many bytes of them have arrived, growing while they do.
It is drawn as the model's own work, like the reasoning above it — this row
read nothing, wrote nothing and ran nothing yet — and it sits under whatever
the round has said so far, which is where a reader watching a turn is looking.

It is a reading of the round in flight and not a line of history. When the
calls land they take its place, saying what each one touched and what came of
it; a compose row left behind them would be a second row about one act, and
the grid gives an act one row.

**A counter, not the arguments.** What is arriving is half-written JSON with
the file's new contents escaped inside it, and putting that on screen would
be an unreadable block that reflows on every frame, in a transcript whose
whole grammar is one row per act. The bytes are the part a reader can use:
they say the thing is moving and roughly how far it has to go. The contents
arrive properly a moment later, as the diff of the change, which is where a
person reads what was written.

**It appears once it is worth appearing.** A fragment says which call it
belongs to and nothing more — the tool's name arrives with the finished call,
because that is the first moment it is true — so the row cannot say whether it
is watching a file being written or a search being spelled. What it can use is
size. Below a kilobyte the row stays off, and a kilobyte is well past every
call that reads, searches or globs: those are a path and a pattern, a couple
of hundred bytes at the outside. What passes it is a file being written or a
batch of edits being described, which are the calls long enough to be worth
watching in the first place.

## Finding things

The agent is told, in its own instructions, to batch independent calls, to
make one search answer the question, and never to repeat a call it has already
made.

That reads like padding and is not. A real session spent its entire round
budget re-running the same searches, and the instructions are what stopped it.
They are load-bearing and should not be trimmed for brevity.

The search itself is asked in the terms the question was asked in. A pattern
can be searched for as written, so `foo(` and `$1` need no escaping and a
mis-escaped one is not a refusal to spend a round on; it can be required to
be a whole word, so looking for `Add` does not return every `AddMemory`; and
the number of matches it stops at can be raised, because a cap with nothing
above it is answered by re-running the same search with a longer pattern,
which costs a round and finds the same lines. The defaults are unchanged:
these are the three narrowings a reader would otherwise do by rewriting the
regular expression, and getting one of them wrong is the round that gets
spent.

## A call the session has already made is answered by saying so

The instruction is one half. The other is that the session watches what it
runs: a call whose tool, arguments and result are exactly those of a call
inside the last two dozen comes back with a line at the top of the result
saying how many times it has now run and that nothing about it has changed.
The result itself is left standing, because it is still the answer — what has
changed is that asking again cannot be the way forward.

The whole interaction is the signature, and that is what makes it safe to
apply to a tool without knowing anything about it. Running the tests twice is
two different interactions the moment the output differs, and one interaction
only when nothing has moved — which is exactly when running them again buys
nothing.

There is one thing that is not a call to a machine, and it is the one
exception: a question put to a person ([below](#the-model-can-ask)). What came
back is exactly what two askings of one question are allowed to disagree
about, so the answer is left out of its signature and the question itself is
the whole of it.

**It watches the calls that have to be answered for as well as the ones that
run on their own**, and those are where a session circles hardest: the failing
command run for the fifth time, the edit issued again after it was declined, a
call that fails the same way every time it is made. A refusal that stands is a
result like any other, so the second identical one says so — which is why the
prompt no longer asks the model to respect a decline. A rule the harness
enforces is a rule the prompt can stop spending words on.

## Many questions about one place are a sweep

The identical call is the tail of that failure, not its body. A session going
in a circle over one directory rarely sends the same search twice; it sends
forty different patterns at the same package, each a perfectly reasonable
question, and the arguments and the output differ every time. Nothing about
any one of those calls is wrong, which is why nothing that looks at one call
can catch it.

So a second signal counts the shape rather than the call. **Many calls of one
kind over one place, with nothing written between them, is a sweep**, and the
session is told about it the way it is told about a repeat: a line at the head
of the result, naming what has been swept, how much of the run's recent work
that has been, and that none of it has changed anything. That last part is the
half that makes it act. A session that believes each of its searches is a new
question answers "you are repeating yourself" by searching again; it cannot
answer "twelve searches of this directory and nothing written" the same way.

**The pattern is deliberately no part of the shape, and the place is.** The
pattern varying while the ground does not is the failure itself, so a signal
keyed on the pattern would be the identical call again under another name. The
tool is part of it, because searching a package and then listing its files are
two different questions being asked, and one tool over one place with the
pattern changing is the shape that was actually observed.

**A write ends a sweep and starts the count again.** A run that has changed
something is acting on what it found, whatever it read to get there, and the
threshold sits above half the length of the failure it is for so that a long
investigation with a change in the middle of it is never told it is circling
on the strength of the reading it did before the change. The negative case is
as much the point as the positive one: a narrowing sweep — a broad pattern, a
narrower one, then the file they pointed at — is three or four calls, and a
thorough survey of an unfamiliar package is six or seven. Neither is circling
and neither is told it is.

**The reading is given the sweep as a fact.** A search's row leads with its
pattern, so twelve searches of one directory arrive at a reading as twelve
legible and genuinely different questions — and working out that they were all
put to one place and that none of them came to anything is exactly the
judgement a reading was getting wrong. It is told instead, in the same grid as
every other row, and a run that has swept without writing is not on target.

## Six questions for the language server

Where a language server was detected, the agent asks it rather than guessing
at a spelling. It can jump to a declaration, list every real usage, search the
project's symbol index by name, outline a file, read a symbol's type and
documentation without opening the file it is declared in, and ask what is
currently wrong with a file or with everything it has checked.

Symbol search, the outline and hover are the ones that change how a session
looks for things. A pattern has to anticipate how a declaration was written —
the receiver, the keyword, the spacing — and one that guesses wrong returns
either nothing or every mention of the word. The index has the answer exactly
and is asked by name. And a nine-hundred-line file read to learn its shape
costs most of what the reduction exists to save, where the same file as an
outline is a screen and usually settles which part to read.

A question is addressed by file, line and the text on it, and the position
that resolves to decides the whole answer — which the model never sees, only
the answer. So the three ways of resolving it wrongly are refusals that name
what to send instead, rather than a best guess: text that occurs on the line
only inside a longer name is refused naming the name the line actually writes,
because a confident answer about a symbol nobody asked about is worse than no
answer; text that occurs more than once on the line is refused with every
column it stands at, because two spellings of one name on a line can be two
symbols with different declarations; and a
file that has moved since the model read it is refused with the same sentence
an edit built on a stale read is refused with, since a line number taken from
a read the file has changed under is a coordinate in a file that no longer
exists.

Every server answers definition and references; support for symbol search,
outlines and hover is uneven, so each of those is asked only of a server that
advertised it. One that indexes a file but not the workspace refuses that
question by name and answers the rest. This is the difference between an
answer and a wait: a request a server never advertised is answered by nothing
at all, and a call that ends at its timeout reads to the model as a broken
tool rather than as a no. Diagnostics need no such gate, because they are not
asked for on the wire at all — the server publishes them when it has them.

What comes back is plain text, bounded like every other result with the
truncation said out loud, and a hover's markdown flattened rather than passed
through with its fences — markup the model did not write is markup it can
mistake for its own.

### Diagnostics that arrive late still arrive

An applied edit waits a few seconds for the server to re-check the file and
carries what it says back with the result, so the model reads its own mistake
in the round that made it. A server that has just started rarely answers that
fast. The first load of a large module is tens of seconds, and it falls
exactly on the opening edits of a session — the ones with the most left to go
wrong, and the ones a check is least likely to have reached.

So the wait is a deadline for that result, not for the question. When it
passes, the question stays open, and the answer — whenever it lands — rides in
front of the next tool result the model reads, as a short bracketed block
naming the file and tallying what was found. There is no other message going
its way: the round that made the edit is over, and a server publishing on its
own schedule has nobody to publish to. The wait itself is unchanged, and a
machine with no server for the file says nothing at all, which is the shape of
every language-server feature here — present when the machine has it, silent
when it does not.

Silence is not one of the verdicts. From where the model sits, a clean check,
a check that has not finished and a file nothing covers are the same empty
result, so an edit that comes back with nothing is either taken for a clean
bill — wrong exactly on the opening rounds, when the server is still loading —
or answered with a diagnostics call after every edit to find out which it was.
So a check that found nothing says so in one line, naming the server that
looked; a wait that ran out says the file has not been checked yet and names
the call that will say. Nothing at all is left to mean the one thing it can
only mean: there is nobody to ask.

One open question per file. A file edited again replaces its own, because
diagnostics for the file as it was are not a report on the file as it is, and
two blocks about the same lines is how a reader learns to skim past both. An
answer that turns out to be a clean file is dropped rather than announced: the
block exists to say what is wrong, and a paragraph reporting that an edit two
rounds ago was fine is what gets the useful ones skipped.

The set can also be asked for outright — one file, or every file the session
has had checked. That is the question the model has when it wants to know
whether what it has been doing still compiles, and it is the same answer, so
it is one tool rather than a habit of making a trivial edit to provoke a
re-check. Asked about a file whose check has not come back, it says so rather
than repeating what the last finished check found: problems found in the file
as it was are worth reading whenever they were found, but nothing found in the
file as it was, handed over as nothing found now, is the same false clean bill
coming through a second door.

## Where a map would sit

A map of the repository — every file with its top-level symbols, ranked by
how often the rest of the tree names them — would sit exactly here: after
the instruction files in the prompt prefix, before the first search, as the
answer to "which file" that search and the language server can only answer
once they have been asked something.

It is not built, and the reason is a measurement rather than a preference.
The record counts what each session spends on reads, searches, globs and the
language server before it first changes a file, and sessions here reach that
first change in a handful of calls. A map is tokens on every turn of every
session, cached with the prefix and paid for again after every compaction;
against a few calls of searching it buys nothing, and it would be a second
description of the tree to keep true beside the tree itself.

That is a number and not a verdict, and it can move. `shhh observe` prints
it — the middle session's count, and the share of sessions that got as far
as a write — so the case for a map is a reading anyone can take rather than
an argument anyone has to win. What would make it worth building is that
count climbing — sessions reading a dozen files before they can change one;
what will not is a preference for having one.

An embedding index is refused outright and separately, on grounds that no
measurement changes: it is a persistent store that goes stale the moment the
next edit lands, and a read that needs the network.

## The readers refuse before they spend

A reader that cannot help is cheapest when it says so in a line.

Asking to read a file says nothing about what the file is, and the two ways
that goes wrong both cost the whole window. A path that lands on a database,
an archive or a compiled binary returns a screenful of mojibake, priced as
text and worth nothing. A path that lands on a large log returns the first
part of it and an invitation to page through the rest, which is a plan for
spending the rest of the turn.

So the reader looks before it reads. A file past the size ceiling is refused
outright, with its size and the ceiling named, and the refusal says that a
narrower line range will not help — otherwise the next call is the same call,
scoped smaller. A file whose opening bytes are not text comes back as one line
saying what it is: a type, a size, and nothing else. Naming the type is the
part that matters, because "this is a PNG" tells the model to stop reaching
for this tool, where "not text" invites another attempt.

An image is the case where the answer is not text but the file is still worth
seeing, so it rides back on the result as a picture wherever the model can
take one. Where it cannot, the line stands on its own — the same answer, one
sentence shorter.

## What git will not look at, the agent does not offer

The file walks skip what `.gitignore` names, for the reason the completion
menus do: a build directory git refuses to see is not the answer to "where
does this live", and listing it buries the file that is.

This started as a difference between two machines. Search shells out to
ripgrep when it is installed, and ripgrep skips ignored files on its own — so
the same search over the same tree returned different answers depending on
whether ripgrep happened to be there. Every walk asks one matcher now, so
finding a file, listing a directory and searching a tree all agree about which
files exist.

A directory named directly is still listed. Ignoring is about what a walk
offers unasked; a path the caller typed is one they have already decided to
look at.

Being hidden is not the same question, and the answer to it is yes. A
project's continuous integration, its linter configuration and its own
written guidance all live in files that start with a dot, and they are
tracked files a reader is expected to find. This was the same
machine-dependent split: ripgrep skips a dotfile unless told not to, so
searching for the linter's configuration returned nothing wherever ripgrep
was installed and returned the file everywhere else. A model told "No matches
found" does not conclude that the search was wrong. It concludes the file
does not exist, and writes a new one.

## The model can ask

Most of what the model needs from the reader is in the request, the tree and
the documents the checkout wrote down. What is left is the choice it cannot
make for them: which of three approaches, whether a change should reach a
second package, what the list it built left out. It had one way to put that
choice — stop, write the options into its answer, and wait for the next
message — which ends the turn, and reads back into the transcript as prose
rather than as a decision the reader took.

So a question is a tool call. The model names what it is asking, offers the
answers it can see, and the reader is shown a card. The answer comes back as
the result of the call, the turn carries on, and the transcript records the
decision as one row.

**A question is a decision, so it is a card; it changed nothing, so it
carries no rail.** That is the weight the interface gives it
([`../interface/principles.md`](../interface/principles.md#weight-tracks-risk)),
and what the card looks like is the interface's to say
([`../interface/surfaces.md`](../interface/surfaces.md#the-question-card)).
What is settled here is what a question may carry and what an answer is.

**The answers offered are never all the answers.** A list the model wrote is
a list the model may have got wrong, so every question can be answered with
something the list did not offer: a row for it is always there, and beside
any pick the reader may leave a note in their own words. The model may say a
note is required where a pick alone would not be enough to act on. What comes
back is the pick by its label and the note verbatim, and an empty note is an
empty note rather than a missing one, so the model never has to guess whether
a note was possible. The model may lead with one answer as its recommendation,
and may say an answer cannot be taken and why; both are words on the row, not
a colour.

**A question can be answered by talking.** Leaving the card does not answer
it. The question is held, the session says one is waiting, and the next
message the reader sends is delivered as the answer, unedited, in place of a
pick. A reader who would rather explain than choose is not made to choose
first. The model is told which kind of answer it got, from a fixed set: a
pick on the card, typed text, skipped, nobody to ask.

**A question is the model's to ask and the reader's to pay for.** The tool's
own description says when one is worth stopping for — only where the answers
would lead to materially different work — and what to do everywhere else:
state the assumption you would have asked about and carry on. That belongs to
the tool rather than to a prompt block because it is read at the moment of the
call. A model that asks instead of reading is doing to the reader what a sweep
does to a round budget.

**A turn has a small number of questions, and the number is a count rather
than a rate.** One turn runs for four minutes or for forty, and what the
reader feels is how often one instruction of theirs stopped to interrupt them,
which is a fact about the piece of work. The allowance is spent by the asking
and not by the answering: a question that reached the screen cost the
interruption whatever the reader then did with it — picked an answer, or set
it down for their next message to answer — so a question they have not got
round to does not buy another one. One past the count is answered *skipped*
without being drawn at all, which is the only thing that answer now means, and
the run is told why, so a model that has run out of questions states its
assumption and carries on instead of asking again. The row on the reader's
screen names the budget rather than a decision they did not make. The count is
the turn's, and it starts over when a turn opens rather than when one closes —
a question still outstanding when the next instruction arrives belongs to the
turn that asked it.

**A question asked twice is a repeat**, and it is the one interaction whose
signature leaves the result out
([above](#a-call-the-session-has-already-made-is-answered-by-saying-so)). The
person may answer differently the second time — that is what makes it a
question rather than a lookup — so a signature that included the answer would
be one that could never see the same question asked twice. Only the question's
own words are in it, so a re-ask with the options reworded is caught too. What
the model is told arrives in the answer itself rather than as a line in front
of it, because that result is the JSON the answer is read out of; what it says
is the opposite of the other notices — not *this will not change*, but *you
already have the answer*.

### Nobody to ask

Where there is no reader there is no question. A scripted run, a child agent
and a served session told nobody is attached are not offered the tool at all,
on the rule the unattended run already holds: a tool the run can only be
refused is worse than one it never saw
([`headless.md`](headless.md#everything-the-session-has-unless-somebody-has-to-answer)).
Nothing answers in the reader's place there: not a flag, and not the
classifier, which judges whether a call is the work that was asked for and
has no standing on which of two designs the person prefers. That is the
commitment the approval path makes — it never silently guesses yes
([`approvals-and-safety.md`](approvals-and-safety.md#the-classifier-fails-closed))
— applied one step earlier, where the question would have been asked.

The answer *nobody to ask* is for the one case where somebody was there and
left: a served session whose client started the turn, was shown the question,
and disconnected before answering it. The call comes back with that answer
and the instruction to state the assumption the question was about and carry
on, so a turn is never left waiting on a decision that is not coming.

A child never asks. A fan-out exists for work that does not need the reader,
and a child that could stop it for a question would be a fan-out that needs
watching. What a child would have asked goes into its report as the
assumption it made
([`subagents.md`](subagents.md#a-child-answers-to-the-session)).

## A long turn is asked what it has got

A turn has no way to notice it is finished. From inside one, every round looks
like progress — one more file, one more pattern — and the signal that enough
is known is a judgement, not a tool result. Nothing in the loop ever asks for
it.

What that looked like in practice was a hundred and fifty rounds of reading
and searching that ended only when the person watching asked whether the agent
had enough yet. It said yes and started work. The question was the entire
intervention: it already had what it needed and had never been prompted to say
so.

So the turn asks itself, on an interval well short of the cap. The wording
asks about the work rather than announcing a budget, because a turn told it is
running out apologises and stops, where one asked what is left says so and
carries on — and it is given somewhere to go other than more reading, which is
what a turn that is quietly already done needs.

The person is not the check-in mechanism. They were doing that job by hand,
and only when they happened to be looking.

## Public progress during a long turn

A check-in asks the model to reconsider its work. Public progress does not: it
makes an extended silent investigation legible to the person following it. The
coding prompt therefore asks for a short status before an extended
investigation, after a material finding or plan change, and before a long edit
or test phase. It names only the objective, evidence and next action; it never
asks for private reasoning or a narration of every call.

The session has a second backstop for models that make only tool calls. After
twelve calls or ninety seconds without assistant prose, it inserts a bounded
machine instruction at the next safe round boundary. The instruction cannot
arrive while a tool batch is owed results, while a command runs, or behind a
surface that owns the screen. It requests ordinary assistant prose, so that
prose titles the activity group that follows it without creating a second
transcript form or changing the tool-call protocol.

The public text is not stored in the content-free observation record and is
never confused with provider reasoning. A headless text run keeps stdout for
its final answer; JSONL emits the status as a distinct `progress` event, while
the record carries only that the checkpoint occurred.

## Two failures, two interruptions

A turn can fail by going somewhere it was not asked to go, and it can fail by
arriving and not noticing. These are not the same failure, and one question
does not catch both.

The session summary already reads, every few rounds, whether the run still
serves the instruction it started from, and that reading is what an off-target
turn is interrupted by: it arrives with the instruction it was judged against
and the reason the reading gave, so the interruption names the departure
instead of asking in general. It fires only on a verdict of off target — an
intervention on a shrug is worse than no intervention.

The reading judges the other failure too — a run still on its instruction
that has found what it needs and is still looking — and where it says so, the
check-in arrives then instead of at its interval. The message is the
check-in's own, unchanged: there is nothing to accuse the turn of, only a
question worth asking sooner.

**The reading never becomes the only trigger.** A verdict needs a summary that
is configured, enabled and answering, and a session with none of those is
exactly a session with nothing else watching it — so a check-in that could
only fire on a reading would go missing precisely where it is the last thing
left. The clock stays underneath, unconditional, asking the generic question
and costing the round it takes. What a reading buys is timing, not the
mechanism.

Both are held to the same three rules. **They arrive at a round boundary**,
because a message may not join a conversation between an assistant's tool
calls and their results, and a round now dispatches several at once — so a
verdict that lands mid-round waits. **They do not lift the round cap.** A
person typing into a running turn is asking for it to continue, and their
message resets the counter; an automatic mechanism doing the same would
quietly postpone the checkpoint the person is there for. **They do not arrive
together** — a steer is a check-in with better evidence, so it counts as one
and the interval restarts from it.

The steer is written to ask rather than to accuse. The judge is a cheap model
reading a digest of tool activity, not the agent's reasoning, so it can be
wrong; a confident accusation against a session that is in fact on task costs
more than the steer saves.

**The reading after an interruption starts again from it.** A steer that is
delivered and then never mentioned leaves the next reader with the evidence
that earned it, the verdict it earned written down as the summary that stood,
and a question asking for a revision of that — so it says off target a second
time, the cooldown holds the next steer a couple of intervals away, and the
status on screen goes on describing a departure that ended. The reader is told
instead what the machinery said and at which round, and asked to judge the
work since on its own evidence and say whether the session came back. It is
also asked sooner: an interruption restarts the reading clock the way the
start of a turn does, because the interval is a cost and an interruption is a
reason. One interruption buys one early reading, not a permanently shorter
interval.

Three things reach the reader about it and no more: that an interruption
happened, at which round, and the reason the earlier reading itself gave. All
three were already inside the boundary the digest draws, so telling the reader
this costs nothing a fetched page or a command's output could ever write.

A queued steer is also withdrawn rather than delivered late. A reading takes
several rounds to come back and the interruption waits for a boundary, so a
fresher reading can arrive first and find the session on target — and
delivering the earlier one then would accuse a session of a departure the
machinery itself had already stopped believing in.

**A second steer says it is the second.** The reading that earns one is taken
after the answer to the last one — an interruption pulls the next reading
forward for exactly that purpose — so a turn told twice has been read again
and read the same way, and told in the same words it has no way to know that.
The count goes into the message from the second on, and with it the one thing
the repetition establishes: answering in words did not change what the check
sees, so the next round is where the answer has to be. It offers the same two
ways out as the first, in the same order, because two readings of a digest are
still two readings of a digest and the judge is no more reliable for having
agreed with itself. A wording that replaces the built-in one is told the count
too — it may place it itself, and gets a sentence under it when it does not,
because an operator replacing the words must not have to notice that they were
replacing the escalation with them.

**What a third steer does not do is end the turn.** Ending one is a stop this
machinery does not own: the round cap hands control back to a person, a person
interrupts, and a child can be redirected by whoever gave it its task and
ended by the person at its lane.
The turn's own reader is none of those. It is a cheap model reading a digest,
it is wrong often enough that the steer above is written to ask rather than to
accuse, and a mechanism that can stop a turn on that reading is one that will
sometimes stop a run doing exactly what it was asked to do, with nobody there
to disagree. So the count escalates by leaving the turn rather than by ending
it: it reaches the parent of a child, which can redirect the child itself and
reports what it could not settle to the person who can end it.

**A verdict has an age, and past one reading interval it earns nothing.** A
reading is asked at a round, takes as long as the reader takes, and is applied
at the first moment the run can act on it, so a run of fast read-only rounds
against a slow reader can be several rounds beyond the departure by the time
the verdict arrives. Delivered then, the steer names something the next digest
no longer shows, the run compares the two and correctly answers that it is on
target, and a round and a cooldown have been spent on a question about work
that is already finished. One interval is the bound because it is the run's
own answer to how long a reading stands for: past it another reading is due,
and the case for interrupting should be made by that one instead. The bound is
the interval actually in force, so a run backing off from a failing reader —
which reads half as often — does not throw away every verdict it manages to
get. Only the interruption is withheld: the reading itself still reaches the
rail, the record and the next digest, because what it says about the work is
true whatever it costs to act on it. The record files the withheld one under
the same code as the interruptions that were delivered, since a run whose
reader is slower than its rounds otherwise reads exactly like a run that never
drifted.

## A steer can be taken back

The judge is a cheap model reading a digest, and it is wrong often enough that
the steer above is written to ask rather than to accuse. Until the reader could
answer it, that was the whole of the defence: the message was already in the
conversation, the turn was already re-orienting around it, and the next reading
a couple of intervals later would say the same thing again.

So the notice a steer leaves carries a key, and the whole of what it costs to
disagree with the machinery is one press. The message leaves the conversation
— it was appended alone at a round boundary, with nothing paired to it, so
what stood either side of it was already adjacent — and the row says it was
withdrawn while keeping the reason the check gave, because the reader
disagreeing with a verdict is not the same as it never having been reached.

**A withdrawal silences the readings for the rest of the turn, not for a
cooldown.** It is the reader saying the check was wrong about this turn rather
than that it spoke too soon; a cooldown would deliver the same false positive
against the same instruction a few rounds later, and the reader would spend the
same key on it again. The readings themselves go on landing — the rail, the
record and the next digest all still get them, the way they do for an
interruption withheld as stale — and the next digest is no longer told about an
interruption that is not in the conversation to be answered. The clock
underneath is untouched: it is not a verdict, it is the floor beneath one, and
it is not what was withdrawn. The next instruction starts a turn nobody has
read yet, and the machinery speaks again.

**Only a steer offers it, and only while its turn runs.** The interval's own
check-in alleges nothing, and a row offering to switch off the last thing
watching an unattended turn is an offer nobody should be given; the early
check-in a sufficiency reading buys is that same message arriving sooner. And a
finished turn has already spent the round the withdrawal would have saved and
has already answered the steer, so taking the question out would leave the
answer stranded in the conversation with nothing above it — which is editing
history, not correcting a check.

## An approved plan is what a reading judges against

A turn's readings are judged against the instruction it started from, anchored
at its start so a run that has drifted cannot drag its own yardstick along. The
reader is the exception to that anchor, because the rule is about the run and
not about the person it works for: what they type into a running turn joins
what the turn is serving.

Approving a plan is that same act. The person read the steps and said these are
what they asked for — once, instead of typing them — so the steps join the
target the execution turn is read against. Without it the expensive check reads
a ten-step turn against the one line that asked for a plan, while the steps
themselves reach the digest as a checklist nobody is judged on, and the two
halves of the same question are answered from different evidence.

The plan joins as one instruction rather than one per step. The digest shares
one budget between the things the person has asked for, so that the newest is
never the part a bound drops; ten steps added a step at a time would leave the
ask that earned the plan a tenth of the room and a steer typed afterwards
another tenth, which is that bound dropping exactly what it exists to carry. A
planning response that never adopted the step shape moves nothing: the prose is
already in the conversation as the model wrote it, and quoting it back as the
instruction would put the model's own words where the person's belong.

## The interval is the last thing watching

The check-in fires on a clock, and how long that clock should be depends
entirely on what else is watching the turn — which is not the same on every
surface.

A session has the most: a reading every few rounds that asks sooner when it
has a reason to, a round cap that hands control back, and a person who can
ask at any moment. Its interval is the third line of defence and can afford
to be long. A sub-agent has the least: it runs uncapped, because a cap used
to be a hard stop and a child that hit one failed with its work half done; it
takes no readings unless it is asked to, because a fan-out multiplies that
cost by its width; and there is nobody in front of it. For a child left on the
defaults, the check-in is the only question it will ever be put.

So the interval is the surface's, not one number for all three, and a child's
is shorter. The failure modes are not symmetric either, which is what decides
the direction: an interval that is too short costs one round of a turn being
asked a question it can answer in a sentence, and an interval that is too long
costs the whole investigation nobody interrupted.

**So is the exit the check-in offers.** The one line the question exists for
is the one giving a turn that is quietly already done somewhere to go other
than more reading, and where that is depends on what ends the turn. A session
says so to the person in front of it. A sub-agent's final report is its whole
deliverable, and one told to say so instead says so into a transcript nobody
reads and carries on — so its check-ins point at the report, whichever of them
asked.

**It widens as a turn goes on.** Often enough early to catch a turn working on
the wrong thing, rare enough later to stay out of the way of one that is
committed and going somewhere. The widening stops after two doublings, because
a turn that survives a few check-ins should not become one that is never
questioned again — that is the same failure on a longer timescale.

## A child's other clock is its budget

The check-in fires on rounds, and a sub-agent does not die of rounds. It dies
of tokens: a budget is the whole of its life, and one making a few large calls
reaches the end of it in a handful of rounds. The child this was found on ran
out at round twenty-seven, having been asked its first and only question at
round twenty-five. The clock worked. It was measuring a quantity that had
almost nothing to do with what killed it.

So the same machinery runs on a second clock. A turn given a budget is asked
to take stock every quarter of it as well as every interval of rounds,
widening off the same count, so a turn already asked twice is not asked at the
narrow interval again by the other one. Whichever clock is due, the question
is the surface's own, and being asked once answers both.

**What the budget question adds is the writes.** Asking on spend rather than
on rounds is only worth doing because spending is not progress, and the fact
that says which it was is what the turn has changed — so the check-in names
it. A child that has spent a quarter of its attention and written nothing is
told so, and told that reading costs what changing something costs. Nothing
else it is ever asked can say that: the round check-in asks about rounds, and
the drift reading is a judgement about the child that the child never sees.

It still says nothing about the budget itself, for the reason the check-in
never does. A turn told it is running out apologises and stops.

## A reading for a run nobody is watching

The reading began as a status block, which is a thing only a session with a
person in front of it has. It interrupts a turn now, and that inverts who
needs it: the surfaces with no rail and nobody watching — a non-interactive
run, and every sub-agent — are the ones where the verdict is the only thing
that can say a run has drifted or already has what it needs. A session has a
reader who can say either by hand.

So the account of a run's activity is assembled in one place every surface can
reach, and a run that has no transcript to read it from collects the same rows
from the tool calls as they happen. The rule the session scheduler is built
around holds there too: **a summary is never the reason a run is slower.** The
request goes out in the background at a round boundary and whatever has come
back is collected at a later one, so a run that finishes first simply never
uses it.

**A run ends on a reading of how it ended.** Otherwise its last word is
whatever the interval happened to catch, which for a run that was steered and
came back is the departure rather than the return, and a reading that had not
answered yet when the work finished is paid for and read by nobody. The
closing reading is not waited for either — the answer is what the run is for,
and holding a child's report until a summariser has finished describing it
would be the reading costing the fan-out. It arrives afterwards, so a surface
that outlives the run records it and a one-shot command that has already
printed its answer and exited simply does not have it. Nothing is steered by
it: the turn it describes is over.

**A run that is on target with a red suite is not a run that has drifted.**
The reading of an unattended turn is given the checks that came back broken —
on that surface, the gate that runs when the model stops calling tools is what
knows — because a reader who cannot see them has one verdict for two
situations and only one of them is worth an interruption: work that has
drifted needs redirecting, work that is on the plan with a failing check needs
leaving alone to fix it. What the reading is told is the suite, how it came
back and which checks are not green, never a byte a check printed.

**Which surfaces take readings is the reader's, because the cost is per
agent.** A non-interactive run is one agent and takes them by default. A
fan-out is as many agents as it is wide, and six children are six more
readings every interval, so a child takes them only when asked. Neither
default is a claim about which run deserves watching — a child is exactly as
unwatched as a headless run — only about what the arithmetic does.

## The verdict is a steering signal, so the digest is a boundary

The digest carries tool names, what they were pointed at, and an outcome word
from a closed set. It has never carried tool output or file contents, and that
was a cost and privacy property when the reading only had to be rendered.

Acting on the verdict changes what the rule is for. A fetched page, a
dependency's README, a test's stdout — anything an outside party can write —
must not be able to reach the thing that writes the instruction the agent is
then steered with. The same reasoning anchors the target at the turn's start
rather than re-deriving it, so a run that has drifted cannot drag its own
yardstick along, and keeps the verdict a closed enum rather than prose, so the
policy branches on a value instead of on a sentence written by whatever it is
judging.

The run never moves its own target, and a steer typed into the running turn is
added to what was already asked rather than replacing it — the anchor is a
rule about the run, not about the person it is working for, and a reading that
judged their correction against the instruction they have moved on from would
report them as the departure. A child's target moves the same way for the
orchestrator that wrote its task: it is not the run being judged, it is the
author of the instruction that run is judged against, and its redirect is the
same kind of thing as one typed at the child's lane.

## The round cap is a checkpoint, not a limit

Hitting the ceiling pauses for input rather than terminating. The work so far
is intact, the transcript is intact, and the way forward is offered on the row
that stopped it.

A hard limit would throw away a mostly-finished turn to enforce a number that
was a guess. Children default to uncapped, because they have nobody to ask.

## A turn can be held between rounds

Stopping a turn used to mean cancelling it, which made "I am about to lose
this network" and "you are doing the wrong thing" the same act with the same
cost: the work kept, the instruction gone, the question to ask again. A hold
is the other answer. The turn stops at the next round boundary, the
conversation keeps everything that round put in it, and letting it go asks
for the request the loop was about to make rather than starting a turn.

The boundary is not a compromise, it is the only place a turn can stop. A
round in flight is an open stream, and a stream nobody is reading backs up
until the provider gives up on the request — so a hold that took effect where
it was asked for would be a cancellation wearing another word. A network that
flips while a stream is still open is a different failure with an answer of
its own: the request is waited out and retried, and a reply that stopped
halfway is offered back rather than thrown away.

The hold reaches whatever the session started. A parent holding its own turn
parks every child at that child's own boundary, in its own time, and one
release lets the whole fan-out go. The loop's own seam is a hook the child
runner sets and nothing else does: a scripted run has no keyboard to ask for
a hold with, and nothing in it would ever release one.

## An unattended run runs the same loop

A run with nobody in front of it — `-p`, an eval, every sub-agent — drives the
same agent the session does, and the two used to diverge in the two places
that decide how long a turn takes.

**A round's independent reads go out together.** The agent is told it can ask
for several at once, and that advice is what stops a turn spending a round per
question. Running them one at a time made it false everywhere the session was
not watching, which is exactly where fan-out lives: four writers each reading
four files paid for sixteen waits instead of four. The bound on how many run
at once is the machine's, not the loop's, and it is the same bound on every
surface. Calls that change something are still resolved one at a time, because
each is a decision and a decision may depend on the one before it.

**A request the provider never answered is waited out.** A rate limit or an
overloaded provider is a stall, not an ending: the request never reached the
conversation, so asking again is the same question. The wait is bounded — a
few attempts, doubling, believing the provider when it names its own wait —
and the bound is across the stall, so a request that is actually answered is
what clears it. Every surface waits on the same schedule and says so in its
own way: a meter you can press out of in the session, a line on stderr in a
scripted run, a lane that says *waiting* in a fan-out. A run that has gone
quiet for a minute and one that has hung are otherwise the same thing to
watch, and each wait is on the record, so what a population of runs spent
waiting for a provider can be told apart from what it spent working.

What does **not** cross over is resuming a reply that stopped halfway. A
session can offer to keep the words already on the wire and let the model
carry on from its own last sentence, because whether half a sentence is worth
having is a judgement someone has to make. The loop cannot make one, and in an
unattended run there is nobody to ask — so it asks again from the top, which
is the honest version of the same recovery. What the broken stream had already
written is handed back with the wait rather than dropped quietly, because a
run that has already printed half a sentence has to close it off before the
answer that replaces it starts.

## The window recovers where nobody is watching

A long run fills its context window, and what happened next used to depend
entirely on whether somebody was there. A session says so out loud: the rails
change colour, a card states where the window went and offers to compact the
conversation, and you decide. A scripted run and a sub-agent have no card and
nobody to show one to, so they ran at the line until a request would not fit
and ended on it — at the worst moment there is, because everything the run had
worked out was still only in the conversation it had just lost.

The same line now has the same two answers everywhere. **Eliding old tool
output is the first one**, because it is the cheap one: what it costs is the
prompt prefix the provider was caching, and the conversation itself survives
intact. **Replacing the conversation with a summary of itself is the second**,
and it happens only where the first could not clear the line — a run whose
window is full of prose, or one already elided to the bone, has nothing left
to give and the summary is the only thing that recovers it. What survives a
compaction is the same everywhere too: the system prompt, the summary, and the
most recent turns word for word, bounded so that one enormous turn cannot be
the whole of what is kept.

It happens between rounds and never inside one. At a round boundary no request
is open and no tool call is waiting for its result, so the conversation can be
cut where a turn ends rather than in the middle of one — a tail that began
inside a round would open with results for calls the model can no longer see
it made, which is a request the provider refuses rather than a summary that is
merely lossy.

**The session recovers at that boundary too, and the card is not what does
it.** The card is offered when a turn ends, which is the right moment to put a
decision to somebody — and a turn of a hundred and fifty rounds that fills its
window at the thirtieth does not reach one for an hour. What that turn used to
get was the trim, alone, until there was nothing left to elide; every request
after that went out oversize until one was refused, at the worst moment there
is, because everything the turn had worked out was still only in the
conversation it was about to lose. So the same two answers run at the same
boundary in a session as in a run nobody is watching, and the card goes back
to being what it reads as: the offer made before the line is reached rather
than the thing standing between a long turn and its window.

Where the session takes that second answer, it takes it the way it takes any
request — the screen says a summary is being asked for, the round's own
request follows the summary rather than going out in front of it, and the turn
carries on under the round budget it already had. It declines to take it at
all over a screen somebody else is using: a card is up, a child's lane is
attached, something has been typed at the turn. A compaction empties the
transcript and reopens it on a summary, which is no smaller a thing to do to a
reader mid-sentence than opening a card over them.

Three things keep it from making matters worse. The request that asks for the
summary forbids the model calling a tool, because it is asking for prose from
a conversation that is nothing but tool calls and would otherwise get one
more. A summary that does not arrive is not asked for again until the window
has fallen back under the line and crossed it afresh: the request carries the
whole conversation, and asking once a round is the most expensive way there is
to keep failing. And a model whose window nothing can vouch for gets no
compaction at all — recovering against a guessed window would throw away the
work of a run that had most of its room left, which is a far worse mistake
than the one this replaced.

Where a scripted run is on a workspace that names a model for summaries, the
summary is asked of that one, since this is a bounded piece of prose about a
conversation rather than more of the work. Never of one with a smaller window
than the conversation being handed to it, though: the moment a compaction is
called for is the moment there is no room to fail. A sub-agent asks its own
model whatever the workspace says, because the single stream it was built with
is the only way out it has.

Whichever surface recovers, the event is on the record, and a trim and a
compaction are recorded apart — one costs a cached prefix, the other costs a
request and the history. A sub-agent that compacts says so on its lane, where
the parent can see it: an answer that came out of a summary of the child's own
work is a different reading from one that still had every result in front of
it.

## Steps are a reading of the turn, not a protocol

A forty-tool turn is unreadable as forty rows, so consecutive calls group
under titles. Where the session declared a plan, those are the plan's steps
with the plan's numbering; otherwise the prose that preceded a batch becomes
the title; where there is no structure to find, the transcript stays flat.

The grouping is a layer over what the agent already emits. Inventing a step
message on the wire would couple every provider to the interface for nothing.

## The agent knows what this machine has

Language servers, external search and edit tools, web access — each is present
only if this machine has it. So the base instructions **name no tool at all**.
The set that was actually registered is described and appended once everything
has joined.

A prompt that names a tool promises a capability the session may not have, and
a model that has been promised a tool will try to use it.

### Two programs answer to `yq`

Structured queries are split by format. One tool answers JSON, another answers
YAML and XML — the query engines are separate programs with separate flags,
and this project's own surface is on the second side: the linter
configuration, the release workflow, the CI job matrix. Without the second
tool a question about any of them is a text search, which returns whichever
indentation happened to match rather than the value that was asked for.

The YAML one is also the only optional tool where being on PATH is the wrong
question. Two unrelated programs install under the name `yq` — one written in
Go, one in Python — sharing the name, most of the purpose, and almost none of
the flags. Only one takes the two flags that shut off reading files and
environment variables from inside an expression, and those flags are the whole
containment argument here: the expression language reaches a file directly, so
without them the path check in front of the tool is decorative and the most
permissive field of the schema is an arbitrary file read.

So this tool's registration asks a second question, and the binary has to say
which program it is. One that does not is treated as absent — a silent success
under the other program would mean nothing was ever disabled, which is worse
than not having the tool. `shhh doctor` reports it in those words rather than
as a plain absence, because "it is installed and shhh says it is not" is a
question that needs an answer.

## The agent knows where and when it is standing

The session already surveys the checkout before the first keystroke — the
language and its toolchain, whether this is a repository, which branch is
out, how many paths are already changed. That survey used to be for the
person: it drew the start screen and stopped there, while the model was told
the shell, the operating system and the working directory and left to spend
rounds asking git for the rest.

It is told now, because each of those facts changes what a good first move
is. A model that knows the ecosystem reaches for the right build command
instead of probing for one. A model that knows the branch does not have to
ask before committing, and does not assume it is on the default one.

The dirty count is the one that prevents a wrong action rather than a wasted
round. Uncommitted work present when the session opened is not the agent's,
and an agent that does not know this reads its own diff, finds changes it has
no memory of making, and starts explaining or reverting them. So the count is
given with the one thing that has to be said about it: those changes were
already there.

That rule is also enforced rather than only stated. Staging and committing are
a tool with a closed verb set, not a command line
([`approvals-and-safety.md`](approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too)),
and it can stage only paths this session's own record of what it changed
holds — so the work that was in the tree when the session opened cannot be
carried into a commit even by an agent that forgot it was there.

Where there is no repository the absence is stated too, because it is the
fact that makes an edit unrecoverable.

The survey is read once, but the block built from it does not freeze. A
conversation that is rebuilt out of a stored message carries a prompt written
for a moment that has passed: a compaction keeps the prompt the session
opened on and discards everything under it, and loading a saved conversation
brings back the prompt that was stored with it, which may be days old. Either
way the model went on reasoning from the branch and the dirty count of a
minute that has since seen a pull and two branch switches. Both moments ask
git again now and keep the rest of the survey: the package walk is the
expensive question and it is also the one that does not go stale, since a
checkout does not change ecosystem while somebody is working in it.

A count taken again says so. "Already there before this session started" is a
claim about work that predates the conversation, and it stops being true once
the session has been editing for an hour — so a re-read count is dated
instead, and says the session's own edits are in it. A model told none of it
was its own would set about disowning the file it wrote ten minutes ago.

Sub-agents are handed the block too, read at the moment each one is spawned,
because a child sent to look at the tree is being sent to look at it as it is
then. A child that writes is told one thing more: it is not standing in that
directory. Its workspace is an isolated copy stood on a commit of its own, so
git in there reports a clean tree and no branch, and a child left to
reconcile that with the block goes looking for changes it will never find.

A child is also handed the session's shared notebook, so a fan-out passes on
what it learns instead of finding it four times; what an agent may put in it
and what it may never take out are in
[`subagents.md`](subagents.md#what-they-share).

The date is environment, so it sits with the shell and the working directory
in every prompt that has an environment. A model reasons from its own
training cutoff unless something tells it otherwise, and left to that it
misdates a changelog entry, assumes the newest release it knows of is still
the newest, and computes a range from the wrong year.

## The agent reads what the project already wrote down

The survey is what shhh worked out about the checkout. The instruction files
are what someone wrote for whoever works in it, and they are the more
valuable of the two: they say the things a walk cannot see, and in most
checkouts they were written before shhh ever ran there. So they are read as
they are found, whatever tool they were addressed to, rather than only the
one file shhh names itself.

A session collects them at the root and at every directory down to the one
it opened in, states them outermost first under headings naming their paths,
and caps the set in bytes with the outermost cut first. The file list, the
precedence within a directory, the order, the cap, the trust they carry and
what a session does with an `@path` line are all one question, answered in
[`configuration.md#project-context-is-opt-in-and-lives-with-the-project`](configuration.md#project-context-is-opt-in-and-lives-with-the-project).

The reading happens once, at session start, with the rest of the prompt:
re-reading them each round would spend a syscall and a cache invalidation on
an answer that is nearly always the same one. Nearly always is not always —
the agent itself edits these files, and a pull brings a different one — so the
block is not re-read and it is not left to stand unchallenged either. Where
the reading of the tree names an instruction file, it says in one sentence
that the block in the prompt is the older reading of it.

## The tree can move under a session

The survey is taken once, and a session that is alone in its checkout can
reason from it for as long as it runs. Sessions are not alone. A second one
is open on the same tree, an editor is beside it, a pull lands in the next
terminal — and the branch switches, HEAD moves, a path the model has never
read is rewritten, with nothing in the transcript to say so until an edit is
refused for touching a file that changed.

So the session is told. At the start of every turn and after every round's
results are in — the boundaries the loop already takes its other readings at
— it reads the tree again and compares: the commit, the branch, and the set
of changed paths. What its own edits account for is subtracted first, so
what is reported is what the model could not already know from its own
transcript. Commands are the one thing a subtraction cannot see through,
because a command may write anything; a change that follows one is reported
as *since your last command*, and the model, which has the command in front
of it, is left to reconcile.

**The session is told when the tree moves; it is never told what moved it.**
Git does not know, and a guess presented as a fact is exactly what the model
would act on — reverting a colleague's work as an accident, or explaining its
own as somebody else's.

There is one exception, and it is not a guess. Where the record says another
session has this checkout open right now, the block ends by saying so. That
is a fact about who is here rather than a claim about what they did, and it
is the difference between a change with no author and a change with somebody
to ask.

The reading costs one status call per boundary, and a checkout where that
call is slow keeps only the turn boundary, where the wait is against a person
typing rather than a model answering. It can be turned off.

What the status call does not see is content: a path that was already changed
when a stranger changed it again has the same status line before and after.
That half is answered from the other side. Every file the session has been
shown is re-checked at the same boundaries, and the ones holding something
else now are named in the same block, under *files you have read changed* —
so a session that has to go back and read something hears it with everything
else that moved
([`approvals-and-safety.md#a-file-is-changed-from-what-was-read`](approvals-and-safety.md#a-file-is-changed-from-what-was-read)).
Each file is named once. A file the session has not gone back to is still
stale at the next boundary and the one after it, and a clause that repeats
every round is what teaches the model to skip the block it is in; it is named
again if it moves again.

One of the paths named can contradict the prompt itself. The project's
instruction files are read once and the block built from them stands for the
whole session, so a notice that names one of those files adds a sentence
saying the block is the older reading. Nothing is put back: a block rewritten
mid-conversation costs the cached prefix of everything in front of it, and the
model is already being told, in the one place it is reading.

The session can also move the tree itself, and one verb moves all of it. A
branch switch rewrites every tracked file that differs between the two
branches, which is the one change the status call is blind to — it compares
the tree with its new HEAD, so a file the switch restored is clean afterwards.
That is answered where it happens rather than here: the record of what the
model was shown is dropped at the switch, and the switch says so
([`approvals-and-safety.md#a-file-is-changed-from-what-was-read`](approvals-and-safety.md#a-file-is-changed-from-what-was-read)).

## It can check itself

The session can run a named suite of checks defined in the workspace, and
report the verdict as part of the turn's close. The suite is authored by you;
the model chooses a name from it and never supplies a command.

Every verdict is fingerprinted against the tree it ran over, so it cannot
vouch for code it did not see.

Asking is not the same as checking, and where nobody is watching the
difference is the whole of it. An unattended run ends when the model says it
is finished, and "it said so" is the only signal a script downstream of it
has. So the config may name a suite to run as a turn closes: a turn that
changed something runs it after its last tool call, the verdict joins the
transcript as the same row a gate call leaves, and the turn's close reports
it. A failing verdict is handed back the way a tool result is, once by
default, and the turn goes on under the round budget it already had — a
turn that could not finish inside its budget is not given more of one for
having failed a check. Whatever the last verdict was, the close says so: a
turn never ends on a failure nobody was shown. An unattended run also states
it in its exit code, so a script that never reads a word of the output still
learns that the checks did not pass.

It runs by itself only where nobody is watching — an unattended run, and each
stage of a backlog run that leaves changed code behind. A session with
somebody in front of it turns it on by hand, because five small edits would
otherwise become five waits for a suite, and the reader waiting through them
would have seen the breakage anyway. A turn that changed nothing, or changed
only shhh's own state directory, runs nothing and says nothing about it: no
suite has an opinion about a checkpoint file. Neither does a workspace whose
config is missing or will not parse — the gate is optional, and a fault
reported at the close of every turn is reported to whoever is least placed to
fix it.

## Related

- [`headless.md`](headless.md) — driving it without the screen: the exit codes and
  the event stream
- [`subagents.md`](subagents.md) — handing work to children
- [`approvals-and-safety.md`](approvals-and-safety.md) — what it may do
- [`../architecture.md`](../architecture.md) — why the loop is passive
