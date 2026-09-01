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

## Finding things

The agent is told, in its own instructions, to batch independent calls, to
make one search answer the question, and never to repeat a call it has already
made.

That reads like padding and is not. A real session spent its entire round
budget re-running the same searches, and the instructions are what stopped it.
They are load-bearing and should not be trimmed for brevity.

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
