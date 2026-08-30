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

## A profile is drafted in conversation

Writing a profile by hand is fine, and the reference says how. But the
person who most wants one — "something that checks my claims", "a reviewer
that only cares about security" — often has a sentence, not a file. So a
session can draft one: the person says what they want, the model proposes
the whole file, and the person decides where it lives, asks for changes in
their own words, or drops it. A brief that is too thin to draft from gets at
most three questions; a brief that is a full specification gets a draft and
no questions, because someone who knows what they want should not be
interviewed about it.

The same mechanism serves both sessions and drafts the same file, but what
it is told to value is not the same. In a chat, a profile is a colleague: a
standpoint, a voice, a way of citing, and never a way of acting — the
drafter is told so and the result is checked, so a chat persona cannot come
out able to write. In a coding session, a profile is an engineer with one
job: what it changes, how it verifies, what its patch may contain. A single
drafter hedging between the two would draft a persona that hedges too.

Where the file lives follows from what it is. A coding agent's profile can
belong to the work: the project's own `.shhh/agents/`, which travels with
the repository, is read only by coding sessions, and shadows a global
profile of the same name — or the config directory's `agents/`, which every
session has. A chat persona is the person's, not any project's, so chat
reads and writes only the global directory. A project's directory is never
assumed to be committed. A drafted profile is spawnable in the session that
drafted it — a persona you made for this conversation should not need a
restart to join it.

## A failed child can be run again

Retry re-runs a child on its original task rather than asking the parent to
reconstruct what it was doing.

## Related

- [`coding-agent.md`](coding-agent.md) — the parent
- [`containment.md`](containment.md) — what scope a child inherits
- [`configuration.md`](configuration.md) — where the profile files live
- [`../agents/README.md`](../agents/README.md) — the profile file format and examples
- [`../interface/surfaces.md`](../interface/surfaces.md) — the agent manager
