# Sub-agents

A session can hand part of a job to a child agent. Children are how a large
task gets parallelism and a clean context without the parent losing track of
what is happening.

## Two kinds, and the difference is what they may touch

- **Researchers** read and search. They have no way to change anything, so
  they need no isolation and their answers come back as text.
- **Writers** have the full toolset, pointed at their own isolated copy of the
  repository. What comes back is a patch the parent reviews.

A writer working in the parent's tree would produce changes nobody chose,
interleaved with changes from other children, in a working directory the user
is also using. Isolation is what makes the parent's approval meaningful:
nothing a child did reaches your tree until you take it.

## A child inherits its scope, not more

A writer sees its own working copy plus whatever the parent has already been
granted. Spawning is not an escape hatch: a child cannot reach somewhere the
parent could not.

## Limits are about attention, not resources

Concurrency and total spawns are capped. The binding constraint is that a
person can only follow so many things at once — a session with a dozen live
children is one where nobody knows what is happening, regardless of what the
machine could sustain.

Children run without a round budget by default, because a child has nobody to
ask when it reaches a checkpoint. The parent is the one with a human attached.

## They are visible while they run

Each child appears in the parent's transcript as a status row, and the agent
manager shows what each is doing and how far in. A child's row does not carry
the mutation rail — it is a report, not an act, and the child's own transcript
carries the rails for what it actually did.

A child's approvals route to wherever you are, so detaching to look at
something else does not mean missing a decision.

Attaching to a child is not a separate surface. It changes which agent the
session is looking at, and every agent — the root included — is the same kind
of thing. That equivalence is why the interactive surfaces did not need a
second implementation for children.

## A failed child can be run again

Retry re-runs a child on its original task rather than asking the parent to
reconstruct what it was doing.

## Related

- [`coding-agent.md`](coding-agent.md) — the parent
- [`containment.md`](containment.md) — what scope a child inherits
- [`../interface/surfaces.md`](../interface/surfaces.md) — the agent manager
