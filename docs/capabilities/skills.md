# Skills

A skill is a folder of instructions for one kind of task: a `SKILL.md` that
says what the skill is for and how to do it, and beside it whatever scripts,
references and templates the instructions point at. The format is the Agent
Skills specification, so a skill written for any other coding harness works
here as it is, and one written here works there.

Skills exist because the useful instructions for a task are often long, and
most of them are irrelevant to most sessions. A project's documentation
conventions are two pages that matter when a comment is being written and
never otherwise. Put in the system prompt they cost every turn; left in a
document the model has to be told about, they are followed when someone
remembers. A skill is the middle: always announced, loaded when it applies.

## A skill is read in three tiers

Every skill costs the session a line — its name and what it is for — from
the first turn. That is the whole catalog, and it is how the model knows the
skill exists. When a task matches, the skill's full instructions enter the
conversation, once, and the files beside them are listed but not read; the
model opens the one the instructions send it to, with the file tools it
already has.

The tiers are the reason the mechanism scales. Twenty skills are twenty
lines until one is needed, not twenty documents.

What has been activated stays. Context trimming elides old tool results on
the assumption that they were consumed when they arrived, and a skill's
instructions are the one result that assumption is wrong about: they are
guidance for the rest of the session, and the failure of losing them is
silent — the model just stops doing what the skill said, and nothing on
screen says why. A compacted conversation is a different matter: the summary
replaces everything, the catalog is still in the prompt, and the model can
load the skill again.

## Where skills live

A project's skills travel with the checkout, under `.shhh/skills/`, or the
cross-harness `.agents/skills/` and `.claude/skills/`, in the working
directory or any directory above it up to the repository root — so a package
in a monorepo can carry skills of its own. A user's skills live in the same
three names beside the config file and in the home directory, and apply to
every project.

The project half of that search is not read at all until the checkout has
been trusted, once, as a whole
([`approvals-and-safety.md`](approvals-and-safety.md#a-checkout-declares-what-it-runs)).
A user's own skills are read either way, and the start screen names what was
withheld — a session quietly missing the skills a repository ships is one
whose behaviour nobody can account for.

A project skill shadows a user skill of the same name. That is the
precedence every other harness applies, and it is what a user means when
they copy a shared skill into a checkout and change it.

A skill that cannot be loaded is a diagnostic, not a failure of the session:
a `SKILL.md` with no description is skipped and named, a name that does not
match its folder is loaded and warned about. This is the opposite of the
rule for agent profiles, where a broken file stops the session, and the
difference is who wrote the file. A profile is the user's own configuration,
and a spawn with the wrong tools is a real risk. A skill is often somebody
else's, arrived with a clone, and a strict reader would reject skills that
every other harness accepts — the value of a shared format is being lenient
about the same things.

## The user can activate one too

The model decides a skill applies by reading the catalog. The user can decide
it first, naming the skill as a command with the task after it, and the
model receives exactly what it would have loaded itself, with the task in
its light. The transcript shows the command, not the two pages behind it:
those are the model's to read, and they were not what the user said.

## A skill cannot grant itself anything

The specification lets a skill list the tools it expects to be pre-approved.
shhh reads the field and shows it, and grants nothing from it. A skill is a
file in a checkout, and a file in a checkout is not a place a permission can
come from — the same clone that brings a skill could bring one that approves
the command it was written to run. What runs without asking is decided by
the mode, the grants and the allowlist, all of which the user set, and by
nothing a repository can add to. The checkout's skills are not even read
until the user has answered for the checkout. See
[`approvals-and-safety.md`](approvals-and-safety.md#a-checkout-declares-what-it-runs).

Loading a skill is itself a read. The instructions enter the conversation
without approval, the way a file the model opened would, and anything they
tell the model to *do* meets the same gates as an instruction the user typed.

## Related

- [`coding-agent.md`](coding-agent.md) — what the session is told it has
- [`subagents.md`](subagents.md) — children see the same catalog
