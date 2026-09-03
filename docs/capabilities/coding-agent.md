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

A file the turn deleted comes back with the permissions it had, so a script
the agent removed is executable again without anyone reaching for chmod. A
file that is still there keeps the permissions it has now: an undo puts
content back and does not touch access somebody set since.

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

## Five questions for the language server

Where a language server was detected, the agent asks it rather than guessing
at a spelling. It can jump to a declaration, list every real usage, search the
project's symbol index by name, outline a file, and read a symbol's type and
documentation without opening the file it is declared in.

The last three are the ones that change how a session looks for things. A
pattern has to anticipate how a declaration was written — the receiver, the
keyword, the spacing — and one that guesses wrong returns either nothing or
every mention of the word. The index has the answer exactly and is asked by
name. And a nine-hundred-line file read to learn its shape costs most of what
the reduction exists to save, where the same file as an outline is a screen
and usually settles which part to read.

Every server answers the first two; support for the other three is uneven, so
each of those is asked only of a server that advertised it. One that indexes a
file but not the workspace refuses that question by name and answers the
rest. This is the difference between an
answer and a wait: a request a server never advertised is answered by nothing
at all, and a call that ends at its timeout reads to the model as a broken
tool rather than as a no.

What comes back is plain text, bounded like every other result with the
truncation said out loud, and a hover's markdown flattened rather than passed
through with its fences — markup the model did not write is markup it can
mistake for its own.

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

**It widens as a turn goes on.** Often enough early to catch a turn working on
the wrong thing, rare enough later to stay out of the way of one that is
committed and going somewhere. The widening stops after two doublings, because
a turn that survives a few check-ins should not become one that is never
questioned again — that is the same failure on a longer timescale.

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

Where there is no repository the absence is stated too, because it is the
fact that makes an edit unrecoverable.

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

The reading happens once, at session start, with the rest of the prompt. It
is a set of files on disk that nobody is editing mid-turn, and re-reading
them each round would spend a syscall and a cache invalidation on an answer
that has not changed.

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

The reading costs one status call per boundary, and a checkout where that
call is slow keeps only the turn boundary, where the wait is against a person
typing rather than a model answering. It can be turned off. What it does not
see is content: a path that was already changed when a stranger changed it
again has the same status line before and after, and that case is caught
where it always was — by the fingerprint a read leaves behind, checked at the
edit.

## It can check itself

The session can run a named suite of checks defined in the workspace, and
report the verdict as part of the turn's close. The suite is authored by you;
the model chooses a name from it and never supplies a command.

Every verdict is fingerprinted against the tree it ran over, so it cannot
vouch for code it did not see.

## Related

- [`subagents.md`](subagents.md) — handing work to children
- [`approvals-and-safety.md`](approvals-and-safety.md) — what it may do
- [`../architecture.md`](../architecture.md) — why the loop is passive
