# Approvals and safety

shhh runs things on your machine. Everything here exists to make sure that
happens only when you meant it to, and that when you decide, you are deciding
with the facts.

## The three tiers

Tools are separated by what they can do, and the separation is structural
rather than a flag consulted at call time. Reads run without asking, because a
read changes nothing and asking about it teaches you to stop reading prompts.
Commands and writes need an answer.

The dispatch paths are different functions rather than one function with a
branch. A dispatcher with no case for a mutating tool cannot be talked into
running one, by a bug or by a model that has learned to ask nicely. This is the
codebase's most important invariant and the easiest to erode, because merging
the paths always looks like a simplification.

## The four modes

How much has been decided in advance:

- **Manual** — every command and every write is asked.
- **Accept-edits** — writes proceed, commands are asked. This is the mode for
  work where the edits are the point and you will review them at the end.
- **Auto** — a classifier decides, and asks when it is not sure.
- **Plan** — nothing runs at all; the session proposes an ordered list of what
  it would do, and you approve the plan rather than the steps.

Plan mode is not a safety mode with the volume turned up. It is a different
activity: deciding whether the approach is right, before any of it is worth
approving individually.

## The classifier fails closed

Auto mode's classifier never approves on error. A timeout, a malformed answer,
an unreachable provider — each falls back to asking the human. There is no
path through the code where "we could not decide" becomes "yes".

This is worth stating as a commitment because the opposite is the natural way
to write it. A classifier that returns a boolean gets a zero value, and the
zero value has to be the one that costs nothing.

## Blast radius

An approval that names the action but not its consequences pushes the risk
assessment onto the reader, at speed, twenty times a session. They will stop
doing it, and the prompt becomes a keystroke.

So every approval answers three questions before it offers a key:

- **What it touches.** Resolved paths, described from the filesystem — how
  many files, how large, or that it does not exist yet.
- **Whether it can be taken back.** Whether the paths are tracked, partially
  tracked, or not tracked at all, or whether nothing in the workspace changes.
- **Whether the network is open.** What containment actually allows right now
  — not what the command appears to want, and not what was configured.

**Resolution is honest about its limits.** Where the paths a command will
touch cannot be determined, the card says that instead of reporting a
confident nothing. A blast-radius line that quietly under-reports is worse
than no line, because it is trusted.

## Severity moves the default

Where a command is flagged as dangerous, the safe key becomes the default and
running it takes a deliberate second key. The decision is taken once, on the
screen where the command appears, rather than as an afterthought prompt after
it has already been chosen.

A command that reaches execution without having been confirmed somewhere still
gets asked. There is no path that skips both.

## Denials are two different facts

"You said no" and "a rule said no" are reported differently, and neither is
confusable with "it failed". The reader's next action depends on which one it
was — change your mind, or change your configuration — so collapsing them
destroys the only information they needed.

A denial is recorded as an act, and carries the mutation rail, because the
point of that rail is finding the moments that mattered.

## Quality gates run what you wrote

A session can run a named suite of checks, and the check commands come from a
file in the workspace that *you* author. The model can ask for a suite by
name; it can never supply an executable or arguments.

Every result is fingerprinted against the tree it ran over, so a passing
verdict can never silently vouch for code it did not see. A gate that reports
on stale state is worse than no gate.

## Related

- [`containment.md`](containment.md) — what stops a command that was approved
- [`../architecture.md`](../architecture.md) — why the tiers are structural
- [`../interface/surfaces.md`](../interface/surfaces.md) — the approval card
