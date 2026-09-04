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

## What can ride with a message

A message carries more than a sentence. A screenshot, a PDF, a text file and a
recording — a voice memo, a clip off a call — all arrive by the same three
doors, the clipboard, a path dragged into the terminal, or a name typed at the
paste command, and all four are held to the same two ceilings: one per file
and one per message. The ceiling is not a judgement about which kind is
heaviest. It is where the refusal can still name the file, which is the last
place it can be made cheaply — past it the provider refuses instead, and that
costs the whole turn rather than the one part that was too big.

What happens to the bytes after that is not the same for all four, and the
difference is not shhh's to smooth over. Every provider takes a picture.
Three of them read a PDF. Two accept a recording, and each of those two takes
a shorter list of audio formats than it takes of pictures — and not the same
shorter list. So an attachment is held as bytes and a media type rather
than as any one provider's block, and the session stays free to change model
mid-conversation without the attachments already in the history becoming
unsendable.

Where a provider has no part for what is attached, the model is told in words:
one line naming the file, what it is and how big it is, standing where the
bytes would have gone. Dropping it silently is the alternative, and it leaves
the model answering a question about a file it was never shown and had no way
to know existed. A recording in a format the provider's own list does not name
is degraded the same way rather than sent and refused, for the same reason the
size ceiling sits here: a refusal shhh can predict is worth more than a turn
spent finding out.

The vendors disagree about names as well as about formats — the same format is
spelled one way in one list and another way in the next, and neither always
matches the name a byte sniffer answers with. shhh keeps one name per format,
the standard one, because that is the name the chip above the draft and the
fallback line both show; translating to a vendor's spelling is the last thing
that happens on the way out.

A recording has no preview. The surface that opens a staged attachment full
pane draws what can be looked at — a picture, a body of text — and says so
rather than opening onto a note about bytes it cannot render.

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

## The backlog is here too

The backlog is not a coding surface. One file per item, four statuses, ready
as dependencies archived, a sprint and an archive say nothing about code, and
a conversation that keeps a reading list wants the screen, the card and the
rail as much as a checkout does. So `/todo` is here, on the same list a
coding session in the same project reads
([`todo.md`](todo.md#where-the-backlog-lives)).

What a conversation cannot do is asked of the run it is asked for, step by
step, before the first turn is spent. A run whose steps read, hand work to a
colleague and end in a write-up needs nothing this session lacks, and it
goes. A run with a step that changes the tree, runs a command, or ends in a
commit is refused, and the refusal names the step and what it wanted rather
than saying the backlog is unavailable — which is what "chat changes
nothing" means when it is said about one particular piece of work.

A run that ends in the write-up puts it in the notebook, signed by the run
and titled with the item, and the archived item says where it went. That is
the ending worth spending a turn on here: a report nobody in the session can
read is a report the code could have written itself.

A step that hands work to a colleague may name one of them. Where the
session has a persona by that name it is the one spawned; where it does not
— every coding session — the step falls back to the role that would have
read the work anyway, so the same backlog is workable from both.

## A conversation runs without a screen

`shhh chat --print "…"` is this session with the screen taken away: the same
prompt, the same reads, the same record, and the same statuses and shapes the
coding agent's print run leaves behind, so whatever reads one reads the
other ([`headless.md`](headless.md#the-exit-code-is-the-contract)). It exists
because work that only reads should not have to start a coding agent to get
done — a backlog of readings worked overnight would otherwise load a
containment, a changeset and a command runner it will never touch
([`todo.md`](todo.md#a-run-is-turns-with-gates-between-them)).

What it will not do is what it has nobody to ask. A delegate is a spawn and a
spawn is an approval, so a run behind `--print` has no colleagues; durable
memory proposes nothing, for the same reason. The one decision that is left —
whether a request may leave the machine — is denied unless `--yes` gives the
answer in advance. There is no flag for anything else because there is
nothing else: a tool name this session never offered is answered as unknown
rather than resolved, so a run cannot be talked into an edit through a tool it
does not have.

## Related

- [`coding-agent.md`](coding-agent.md) — the other session, and why it is a
  different thing
- [`todo.md`](todo.md) — the backlog both sessions share
- [`subagents.md`](subagents.md) — profiles, and what a child inherits
- [`sessions-and-memory.md`](sessions-and-memory.md) — resuming, and what
  memory is that notes are not
- [`../interface/surfaces.md`](../interface/surfaces.md) — the rows and
  panels the two sessions share
