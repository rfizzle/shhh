# Chat

`shhh chat` is a conversation. It answers questions, reads what it needs to
answer them, and can ask a few named colleagues to look into something on its
behalf. It is not a smaller coding agent, and the surface it draws is not the
coding agent's surface with parts turned off.

## Chat changes nothing

Everything chat can reach is a read: files in the working scope, the web,
the session's own notes. There is no command runner and no file editor, and
no way to conjure one from inside the session. A page read off the web is a
read like a file read, so it is not put to the person either
([below](#a-conversation-has-one-mode)); what is still asked is whether a
colleague may be started, because a child spends the session's budget on
work nobody reads until it reports.

A read that leaves the machine is also the most expensive one, so a page is
kept whole. What the conversation carries is the opening slice of it; the rest
is in the session's evidence store under the id the result names, a read away
at any point in the conversation. Fetching the same URL a second time buys the
same first slice, which is the loop the notice exists to stop —
[`evidence.md`](evidence.md) is where that store and its paging live.

Read-only is a property of the session, not a mode it starts in. A mode can
be cycled; a toolset that was never registered cannot be reached by any key.
That is what lets chat drop the machinery that exists to make mutation safe:
the changeset and its undo, the review view, the quality gate, the git
snapshots behind rewind, process supervision, containment. None of it has a
job where nothing is written.

## A conversation has one mode

Read-only is a property of the session, so there is no mode to choose. The
coding agent's five — manual, accept-edits, auto, read-only and plan — each
decide how much of the acting is decided in advance, and a conversation has no
acting to decide about. The frame says `read-only` and does not change;
`shift+tab`, `/permissions` and a mode named to it answer that a conversation
has one mode rather than opening a picker, and `--mode` on `shhh chat` is
refused with the same sentence rather than taken and ignored. Plan mode goes
with the rest: there is no plan card here, no plan record, and no new session
carrying a plan, because a plan is a proposal about work this session cannot
do.

A fetch and a search run without a card. They leave the machine, but what
leaves is an address and what comes back is a page; nothing either does can
change the tree or the machine, so there is no answer the person could give
that protects anything. What stays is the refusal the person wrote down:
`web.deny_hosts` is read before the fetch is allowed, here as everywhere, and
the fetcher still will not follow a redirect off a granted host to one nobody
answered for ([`approvals-and-safety.md`](approvals-and-safety.md#a-host-is-granted-once)).
Every page still lands in the ledger ([below](#what-was-read)). The session's
children read the web the same way, since a card routed up from a colleague
would be the same question asked one level down. The record files these reads
under a reason of their own, so a comparison can tell a conversation's reads
from the hosts a coding session was granted.

Read-only means the tree and the machine are left as they were, not that
nothing is produced. A conversation still writes three things, all of them
shhh's own: a report page (`report`) into shhh's report store, served on
loopback; a note into the session's notebook; and a durable memory, which the
person confirms on a card before it is kept. None of them is a write to the
checkout, so all three stay, and the first two run without asking.

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
reads a turn made, the children it sent out and what they came back with,
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

Everything attached is given a handle beside its name, and the handle is the
word for it: `Image#1` for a picture, `Paste#1` for text pasted into the draft
that was too long to keep there, `File#1` for anything else — a PDF, a text
file, a recording, however it arrived. The name cannot do this job. It is
often nobody's choice — every screenshot taken off the clipboard is
`clipboard.png` — and a word that three attachments share points at none of
them. A picture is the one kind with a word of its own because it is the one
a person refers to by what it shows — *the second screenshot*, *the one with
the error* — and so the one most often attached several times in a row. A
paste has its own because it is the one attachment with no file behind it: its
name is only ever its number. Each word counts from one for the conversation,
not for the message, so a handle keeps meaning the same thing for as long as
the conversation it was used in — the number a dropped attachment had is not
handed to the next, and a conversation reopened later goes on from the highest
number its own messages carry. The count starts over with a new conversation.

A message can point at what it carries. The handle is written into the
sentence where the thing arrived — a fold the draft leaves at the cursor — and
the sentence goes to the model as it was written, folds and all. On the way
out each attachment's part is led by one line in the handle's words,
`Image#1 (clipboard.png, 1440×900):`, so "Image#1 shows the error, Image#2 is
after the fix" resolves to the bytes under each line rather than leaving the
model to guess which picture is which. The line carries the name as well, so
a file attached by typing its path, which leaves no fold, can still be named
in the sentence by hand. The bytes still lead the message and the sentence
still follows them: a model reads a picture better with the question about it
after it, and the guidance for several pictures in one message is that each
is labelled, which is what the line is. Putting each part where its fold
stood — every provider could take that — is deliberately not done: it would
reorder a message by where the cursor happened to be, and the label gives the
reference without moving anything. The line is the only thing the model is
told about it; no prompt describes the convention, because a label that
explains itself does not need one.

## Colleagues, not workers

A chat session can delegate to sub-agents, and the roles it may spawn are
the ones that only read: the shipped researcher and any profile in the
agents directory that grants neither writing nor commands. A profile that
can write is not offered, rather than offered and refused — the model is
told the roles it really has (see
[`subagents.md`](subagents.md#a-profile-is-a-file)).

The profiles are what give a child a persona: a name the person chose, a
description the orchestrating model chooses by, its own model and reasoning
level, and a prompt that is its standing instructions. A chat session with a
few of these behaves like a small team of specialists that one generalist
routes to, each answering in its own voice, each unable to change anything.

A colleague can be named. `@` in the draft offers the session's colleagues
beside its files, each with what it is for, and choosing one writes `@name`
into the sentence. Naming is a hint and not an order: the model reads the name
in the message and is told what `@name` means, and it still decides whether to
delegate and to whom. Reaching the security reviewer then no longer depends on
phrasing the question so the model picks her description — and the one who
routes is still the one who routes, so a spawn made under the hint is an
ordinary spawn, on an ordinary card, answered like any other. A name that
spawned directly would be a second way to delegate that skipped the decision
the orchestrator exists to make.

## What they share

The session's notebook is the shared channel between a conversation and its
colleagues: what one researcher found on Monday is exactly what the next one
should not have to find again. Any agent in the session — the orchestrator
and every child — can write a short, titled, signed note and read the
notes that exist. It persists with the session, so a resumed conversation
resumes its notebook, and a colleague spawned later starts by reading what
the earlier ones left. A backlog run whose finish is a write-up rather than a
commit puts the write-up here, which is where it is read.

`/notes` is that notebook as a screen, grouped by the agent that signed each
note, with the note the pointer is on beside it, the whole of it under
`[enter]`, and `[d]` to drop one — the only way anything leaves the notebook,
and the person's alone. What has been written since that screen was last
opened is what the turn's close calls unread.

The notebook is not a conversation's alone: a coding session and its children
share one on the same terms, and what an agent may put in it, what it may
never take out, and why it is a file rather than a messaging runtime are in
[`subagents.md`](subagents.md#what-they-share).

Notes are not memory (`sessions-and-memory.md`): memory is durable, general,
and confirmed by the person before it is kept; a note is working state, and
its lifetime is the conversation's.

## What was read

The other thing a session and its children share is the record of what they
read. Every fetch and every search — the session's own and every child's —
is one row: which agent made it, in which turn, the URL asked for and the
URL that answered, the status, the size, whether the cache answered instead
of the host, and the evidence entry holding the page where the fetch left
one. `/sources` is that ledger as a screen, grouped by the host each page
came from, with the row the pointer is on beside it and the page the fetch
kept under `[enter]`. It persists with the session, so a resumed conversation can
still say where an answer came from.

An unattended run keeps the same ledger, and states it in both of its JSON
shapes ([`headless.md`](headless.md#a-run-says-what-it-read)). Once the run
has saved its conversation, the rows are filed under the slot it saved to, so
a session that resumes that slot has what the run read on `/sources` beside
what it reads itself. A served session does the same at every turn's save,
and its client is told only what the session read — never the rows a
resumed slot already held.

It is content-free beyond the address, the query and the page's own title.
The page itself lives in the evidence store, which has its own retention and
its own purge ([`evidence.md`](evidence.md#a-page-is-kept-whole)); this is
the index, and it is small enough to keep.

**It is recorded by the tool that read the page and never by the model.** That is the whole
point of it. A sources list a model writes is a claim like any other — a
model that remembers reading a page writes the same sentence whether or not
it did. So a backlog run that ends in a write-up gets its *Sources* block
built from this ledger, and any URL the write-up cites that is not in the
ledger is listed underneath as *cited, not read*. That list is the review
step's first check, because a fabricated source is the failure this exists
to catch, and the place to catch it is the write-up rather than the reader's
browser.

Nothing the model can call reaches the ledger. There is no tool that writes
a row and none that removes one: a record an agent could edit would answer
the question it exists to answer with whatever the agent preferred.

A page can also arrive through an MCP server's tool, and that is a row too,
of its own kind and marked `⇄` on the screen: the page came through a
boundary shhh did not fetch across, so it has no status of shhh's own and
nothing here can vouch that the text is what the address holds. Which calls
count is read off the result rather than declared: a result that embeds a
resource at an `http` or `https` address is the server handing back that
page, and a bare link or a URL mentioned in text is not. Reading one of a
server's resources at such an address is the same act and files the same
row. Such a row is never one of the pages a write-up's *Sources* block is
built from, for the same reason; the screen's header counts them as a figure
of their own beside the pages, so a session that read everything through a
server does not report having read nothing.

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

## The next step is offered, not typed

After a turn, the obvious next message is often one the session could have
written: run the checks that failed again, take up the step still unmarked,
commit what was changed. So when a turn closes at the input, a cheap model
reads what the session's other close readings already hold — how the turn
ended, the checks and the files on its close row, the working steps and
which are marked, the last reading's verdict, the standing account, and the
last thing asked — and writes that message, one or two sentences in the
person's own voice. The empty draft draws it dim, and `→` takes it into the
draft to be edited or sent. Both sessions offer it, since both have turns and
a draft.

A suggestion is never a message. It is not sent, not saved with the
conversation and not shown to the model; it reaches the model only once the
person has taken it into the draft and sent it, and then it goes through
every gate a typed line does. That is why it can be read from the session's
own evidence without being trusted: nothing it says acts until a person has
made it theirs. It is never read from a tool result, so a fetched page
cannot put words in the draft.

It costs a request per closed turn, and the flow is read like the others: its
model comes off the cheap chain with a key of its own
(`behavior.suggestion_model`), its spend is on the bill under its own name,
its wording is a prompts file like any other, and the record says how often
an offer was taken against how often it was ignored — the one number that
says whether it earns the request. A turn that broke, was cancelled, stopped
at a card or ran while the keyboard was in a child's lane is not asked for
one. `behavior.suggestions` turns it off and `/ui suggest` flips it for the
session; off means no request is made after a close, not an answer drawn
nowhere. A keystroke does not end an offer: it waits under what is typed and
comes back when the draft is empty again, ending only when a message is sent
or the session does. `/suggest` asks for one on request, on or off, from the
same model. An
unattended run, a served session and a child never ask, because none of
them has a draft to put an offer in.

## The start screen is read for this checkout

The start screen's read-only rows are a table: what the checkout states —
a changed tree, a branch not pushed, a ready backlog item, an instruction
file due an assessment — and, where it states nothing, a tour. A table
cannot name a repository's own work in its own words, so once the screen has
drawn, a cheap model takes one reading of what the checkout says about
itself: the instruction block exactly as the system prompt got it, the
screen's fact line and gate, the names of the files the tree has changed,
the last ten commit subjects and the ids and titles of the backlog's ready
items. No other file's contents are read for it. It writes at most two
offers, each a short title and a prompt that reads and reports.

The offers are placed, not appended: three rows stay three rows. A written
offer takes a tour's row, or the row of a stated fact when it names the same
thing — the item's id, the branch, the instruction file — and a fact it does
not name keeps its row. The resume row and the row that costs an approval
never move, and the price on a written row is the slot's, `reads only, no
writes`: the writer does not set what its row costs, and the line the row
sends says to change nothing.

It follows the next step's rules. It is asked in the background, so the
screen never waits for it, and it lands only where nothing has moved: a
reading that comes back after a key, a chosen row or a turn is dropped, and
the record says so. A failed or slow reading changes nothing, and the screen
never says one is out — the fixed rows are the screen, not a placeholder.
Nothing of it is saved with the conversation or shown to the model; a chosen
row is the person's sent line and nothing else. It costs one request per
session open, on its own key (`behavior.start_offers_model`) off the cheap
chain and on the bill under its own name, and `behavior.suggestions` and
`/ui suggest` are its switch as well: one switch for the two offered things.

## A conversation runs without a screen

`shhh chat --print "…"` is this session with the screen taken away: the same
prompt, the same reads, the same record, and the same statuses and shapes the
coding agent's print run leaves behind, so whatever reads one reads the
other ([`headless.md`](headless.md#the-exit-code-is-the-contract)). It exists
because work that only reads should not have to start a coding agent to get
done — a backlog of readings worked overnight would otherwise load a
containment, a changeset and a command runner it will never touch
([`todo.md`](todo.md#a-run-is-turns-with-gates-between-them)).

What it will not do is what it has nobody to ask. A colleague is reached by a
spawn and a spawn is an approval, so a run behind `--print` has no colleagues; durable
memory proposes nothing, for the same reason. A fetch is not one of those
decisions: it is a read here as on the screen, refused only for a host on the
deny list, so `--yes` has nothing left to answer
([`headless.md`](headless.md#everything-the-session-has-unless-somebody-has-to-answer)).
There is no flag for anything else because there is nothing else: a tool name this session never offered is answered as unknown
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
