# Chat

`shhh chat` is a conversation. It answers questions, reads what it needs to
answer them, and can ask a few named colleagues to look into something on its
behalf. It is not a smaller coding agent, and the surface it draws is not the
coding agent's surface with parts turned off.

## Chat changes nothing

Everything chat can reach is a read: files in the working scope, the web,
the session's own notes. There is no command runner, no file editor, and no
way to grant one from inside the session. The one decision the person is
ever asked for is whether a request may leave the machine — a fetch, a
search, a delegate — because that is the one act a read-only session still
has that is not free.

Read-only is a property of the session, not a mode it starts in. A mode can
be cycled; a toolset that was never registered cannot be reached by any key.
That is what lets chat drop the machinery that exists to make mutation safe:
the changeset and its undo, the review view, the quality gate, the git
snapshots behind rewind, process supervision, containment. None of it has a
job where nothing is written.

## It starts where you are, not with what you have

The empty session is a prompt. It does not survey the repository, count the
packages, name the branch or report whether the tree is dirty, because a
conversation that opens by describing the checkout has already decided it is
about code. Where the session is opened still shapes what it can read — the
working scope is the directory and whatever was added beside it — and the
project context file, when one exists, is read into the prompt as it is
everywhere else. What the person sees first is the question mark, not the
inventory.

## The transcript is the conversation

The rows that exist to account for work — the changed-files row on a turn's
close, the plan checklist, the backlog, the diff — are not drawn. What
remains is what a conversation has: the messages, the activity rows for the
reads a turn made, the delegates it sent out and what they came back with,
and the meter that says what it cost. The input frame, the inspector rail
and the keys are the same ones the coding agent uses, so a person who knows
one knows the other; they simply carry fewer blocks.

## Colleagues, not workers

A chat session can delegate to sub-agents, and the roles it may spawn are
the ones that only read: the shipped researcher and any profile in the
agents directory that grants neither writing nor commands. A profile that
can write is not offered, rather than offered and refused — the model is
told the roles it really has (see
[`subagents.md`](subagents.md#a-profile-is-a-file)).

The profiles are what give a delegate a persona: a name the person chose, a
description the orchestrating model chooses by, its own model and reasoning
level, and a prompt that is its standing instructions. A chat session with a
few of these behaves like a small team of specialists that one generalist
routes to, each answering in its own voice, each unable to change anything.

## What they share

A delegate cannot see the conversation it was spawned from, and the
orchestrator only receives its final message. That is the right contract
for one task and the wrong one for a team that keeps working across tasks:
what one researcher found on Monday is exactly what the next one should not
have to find again.

The session's notebook is the shared channel. Any agent in the session — the
orchestrator and every delegate — can write a note and read the notes that
exist. A note is short, titled, and signed by the agent that wrote it; it
persists with the session, so a resumed conversation resumes its notebook,
and a delegate spawned later starts by reading what the earlier ones left.
Notes are not memory (`sessions-and-memory.md`): memory is durable, general,
and confirmed by the person before it is kept; a note is working state, and
its lifetime is the conversation's.

## Related

- [`coding-agent.md`](coding-agent.md) — the other session, and why it is a
  different thing
- [`subagents.md`](subagents.md) — profiles, and what a child inherits
- [`sessions-and-memory.md`](sessions-and-memory.md) — resuming, and what
  memory is that notes are not
- [`../interface/surfaces.md`](../interface/surfaces.md) — the rows and
  panels the two sessions share
