# Sub-agents

A session can hand part of a job to a child agent. Children are how a large
task gets parallelism and a clean context without the parent losing track of
what is happening.

## Two kinds, and the difference is what they may touch

- **Researchers** read and search. They have no way to change anything, so
  they need no isolation and their answers come back as text.
- **Writers** have the full toolset, pointed at their own isolated copy of the
  repository. What comes back is a patch the parent reviews.
- **Reviewers** read a change they did not make and judge it. They have the
  researcher's tools and the read-only mode, so the restriction is visible
  to the child itself; the change is handed to them as a diff, and what
  comes back is a report ending in a verdict.

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

## A profile is a file

The two kinds above are the profiles every session has. A profile is one
TOML file in the `agents/` directory beside the config file, named for the
agent it defines, and a session can spawn it by that name the same way it
spawns a researcher. The file says what the agent runs on (model and how much
it thinks), what it may touch (a permission set and, within it, a tool
allowlist), the permission mode it starts in, what it is told, and the
budgets a spawn that names none falls back to.

Permissions are the tiers the tools are already split into — read, write,
execute, web — rather than a list of tool names, because the tiers are what
the approval machinery reasons about. A profile that can write or execute is
a writer in the sense above: it gets its own copy of the repository and hands
back a patch. A profile that can only read and browse is a researcher. There
is no third shape, and a profile cannot ask for one.

A profile can make its children stricter than the session — a reviewer that
starts in plan mode under an auto session — and never looser. The clamp that
keeps a child inside its parent's mode applies to a profile's mode the same
way, so writing a file is not a way around the mode the person chose.

One file per agent rather than a section per agent in the config file,
because a prompt is most of a profile and a prompt is a document: it wants
its own file, its own history, and to be shared by copying one thing. The
built-in roles can be redefined by a file of the same name, which is how the
shipped researcher gets a cheaper model or a different set of instructions
without a config key for each field.

A profile that does not load stops the session with the file's name and what
was wrong with it. The alternative — skipping it — is a role that quietly
went missing, and the model would be told a smaller set of roles than the
person wrote, with no way for either to notice.

## A failed child can be run again

Retry re-runs a child on its original task rather than asking the parent to
reconstruct what it was doing.

## Related

- [`coding-agent.md`](coding-agent.md) — the parent
- [`containment.md`](containment.md) — what scope a child inherits
- [`configuration.md`](configuration.md) — where the profile files live
- [`../agents/README.md`](../agents/README.md) — the profile file format and examples
- [`../interface/surfaces.md`](../interface/surfaces.md) — the agent manager
