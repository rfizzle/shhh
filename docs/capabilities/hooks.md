# Hooks

A hook is your own command, run at a moment the session was already going to
have. Run `gofmt` after every edit. Refuse anything that touches `vendor/`.
Say something on the desktop when a long run finally stops. Each of those was
previously a fork of shhh, and each of them is now four fields in a config
file.

Hooks exist because extensibility was the thing shhh controlled least. The
seams were all in the tree — the moment before a tool call, the moment after
it, the turn's close, the session's start — and nothing outside the binary
could reach any of them. This opens them, and opens nothing else: a hook is
not a new place where things happen, it is a place that already happened.

## The five seams

| Event | When it fires | What it is told |
|---|---|---|
| `session_start` | once, as a session opens, before its first turn | the directory the session was opened in |
| `pre_tool` | before a tool call runs or is put to you | the tool and its arguments |
| `post_tool` | after a call has run, before the model reads the result | the tool, its arguments and the result |
| `turn_close` | as a turn's accounting closes | the answer the turn ended on |
| `stop` | as the session or the run ends | the last answer |

There are five and not more because those are the seams that exist. A sixth
would mean a new place in the loop for something to happen, which is a change
to the product rather than a line in a table.

`pre_tool` covers a command as well as a tool: an `execute_command` is put to
the same seam, at the same moment the approval card would have been drawn.
`post_tool` fires wherever a call's result is produced — a read, a server
call, a write, a command.

## Where a hook is written

Two places, and both of them are yours. Your own config file holds a table
per hook, keyed by a name you pick. A checkout holds the same four fields, in
JSON, in its own `hooks.json` — and that file is a trust resource like the
checkout's skills, its quality suites and its servers, so it loads only in a
checkout you have said yes to. A hook is a command line that runs as whoever
cloned the repository, which is the whole reason that answer exists
([`approvals-and-safety.md`](approvals-and-safety.md#a-checkout-declares-what-it-runs)).
A checkout's hook shadows one of yours with the same name, the precedence you
mean when you copy a shared entry into a repository to change it.

The name is not decoration. It is what a diagnostic names, what the doctor
row lists, what a refusal tells the model refused it, and what makes the
order two hooks on one seam run in — name order — the same on every machine.

An entry that cannot be read is named and left out: the session starts, with
less in it. A matcher that will not compile is refused rather than ignored,
because ignoring it would leave a hook matching every tool where you wrote
one matching a few — which is your formatter running over every read in the
session.

## The payload is the event stream

A hook reads one JSON object on stdin. Its fields are the ones the `--output
jsonl` event stream already uses — `turn`, `round`, `id`, `tool`,
`arguments`, `result`, `outcome`, `final` — plus the three a separate process
cannot work out for itself: `event`, `session` and `cwd`. There is one
vocabulary, so a hook written against the record's codes matches the stream's
without a second table to learn
([`headless.md`](headless.md#the-stream-is-the-record-as-it-happens)).

`session` is the session's own row in the local record, so a hook that keeps
its own notes can join them to the table shhh already writes rather than
inventing a second name for the same sitting. It is empty at `session_start`
and on a machine with no local store: the row is opened as the session
finishes assembling itself, after that seam has fired, and a made-up
identifier would join to nothing.

A hook answers on stdout, with a JSON object or with nothing at all:

```json
{"decision": "deny", "updated_input": {"path": "a.go"}, "context": "…", "note": "…"}
```

A hook's stderr is not read. stdout is where the answer goes and `note` is
how a hook says something to you; a hook whose only account of itself was on
stderr would be a hook whose account nothing carries.

`note` is for you and is not sent to the model — not even when another hook
on the same seam fails, which is the case that would otherwise leak it. What
the model is told about a failure is that the hook failed.

`context` is text for the model — it leads the tool result, or joins the
system prompt at a session's start. `note` is text for you, drawn on the
surface and never sent. Output that is not a JSON object is not an answer and
is dropped: the common hook prints what it did and exits zero, and reading a
formatter's file list as a malformed reply would make every working hook look
broken.

## A hook cannot move a call between tiers

The tiers are the codebase's most important invariant: reads and writes are
dispatched by different functions, so a dispatcher with no case for a mutating
tool cannot be talked into running one
([`approvals-and-safety.md`](approvals-and-safety.md#the-three-tiers)). A hook
that could rewrite a read into a write would be a path from the read-only
dispatcher to a mutating tool, however it was guarded.

So the hook sits *inside* one tier's dispatcher and never in front of two. A
call that runs on its own meets the seam inside the read-only chain; a call
put to you meets it at the approval queue, and meets it once. And what a hook
may change is the arguments: its answer has no field that could name a tool.
The rule holds because there is nothing to break it with, rather than because
something checks.

## Nothing decides yes on a failure

Exit 2 refuses the call. Any other non-zero exit is a failure, and a failure
is not a refusal — a hook that crashed and a hook that said no are different
facts, and reading a crash as a refusal would make every broken hook look
like a working one.

What a failure costs depends on whether there is anybody to ask. A call that
was going to be put to you is put to you, whatever standing yes the session
was holding — a mode, an earlier "approve all of these", the classifier. A
read has nobody to ask, so it runs, with a note saying the hook broke; taking
reads away because a formatter will not start would take the session away
over a formatter.

An unattended run has nobody to ask at all, so a hook that asked, or that
failed on a call it was asked about, does not run it. That is what this
surface does with every question it cannot put to a person, and a hook is not
the place to start making it an exception. What the model is told there says
so in as many words — nothing about the call is settled, and the same call in
a session would be a card somebody could answer — rather than borrowing the
refusal's sentence, which would have the model abandon work a person would
have approved in a second.

A hook's `allow` is the absence of an objection, not an approval. The mode,
the lists and the person decide exactly as they would have. Nothing a config
file names turns a card into a call that never asked, because that is the one
direction this product does not go.

## A hook that runs too long has failed

Every hook is bounded, at thirty seconds unless you say otherwise. The number
is short on purpose: something is waiting on the other side of every seam a
hook sits on, and a turn closing waits on the goroutine drawing the screen. A
hook is a formatter or a path check, not a build.

`hooks.timeout_seconds` raises or lowers it for the session, and an entry's
own `timeout` lowers it further for one hook. Neither may go past the command
ceiling: a hook is a command the session runs, and nothing the session runs
may outlast that. There is no way to turn it off — a command with no ceiling
is a dev server somebody started and can see, and a hook with no ceiling is a
seam that never answers.

`turn_close` is the seam this matters most for: it runs while the session is
finishing the turn, because a hook that fired after the turn had already gone
back to the input would be closing nothing — its note would land in the next
turn, and a session that stopped in between would never fire it at all. The
ceiling is what keeps that from being a session that has stopped.

A hook that reaches its ceiling is a failure, which is the paragraph above.
This is deliberately not what happens to a command that reaches its own
ceiling — a command still printing is offered to the process supervisor,
because it is usually a dev server doing its job
([`containment.md`](containment.md#a-command-that-will-not-finish-is-not-waited-on-forever)).
A hook is not that: something is waiting on the other side of the seam it
sits on, and waiting longer is the one thing that seam cannot do.

## A hook is a command like any other

A hook runs through the same shell, with the same environment, contained by
the same mechanism as a command the assistant asked to run. A session that
contains its commands contains its hooks; a session that contains nothing
runs both bare. The session's secrets reach a hook exactly as they reach a
command, and the mask that keeps everything else out applies the same way
([`secrets.md`](secrets.md#a-secret-is-an-environment-variable)).

The one exception is a `--sandbox` run, which creates a disposable container
and runs approved commands inside it. A hook cannot follow them in, and
running it on the host instead would put your own command line outside the
strongest containment the run has — so such a run fires no hooks and says so.
It is the same answer, for the same reason, that a long-running process start
gets there
([`containment.md`](containment.md#a-started-process-is-contained-too)).

This is why a `session_start` hook fires after the session's containment is
in force rather than before its prompt is assembled: what it adds to the
prompt is joined on, and the alternative — firing it early enough to fold in
— would be the one hook in the session that ran on the host.

## A hook's deny is a rule denial

A refused call draws the rule denial and not yours, with the hook's name on
the row. "You said no" and "a rule said no" are different facts and the
reader's next act depends on which
([`approvals-and-safety.md`](approvals-and-safety.md#denials-are-two-different-facts)):
here it is to edit a hook.

What the model is told is that the call will not run however it is spelled,
so no rounds are spent rephrasing it — and it is told the hook's name and
nothing about where hooks live. The hooks are yours, and a refusal that came
with the instructions for editing one would be handing over the way around
it. It is the deny list's rule, for the deny list's reason.

## What the surfaces say

`shhh doctor` has a hooks row: how many loaded, which seams they sit on, what
each of them may take, and any entry that would not load. `/status` in a
session lists them by name and event, because a session is partly what it
does that the session beside it does not. A hook that says something — a
`note`, a failure, a rewrite — writes a line into the transcript, or onto
stderr in a run with no transcript to write to.

## Related

- [`approvals-and-safety.md`](approvals-and-safety.md) — the tiers a hook sits
  inside, and the other rules that answer before a card is drawn
- [`containment.md`](containment.md) — what a hook's command can reach
- [`headless.md`](headless.md) — the event stream the payload borrows its
  shape from
- [`configuration.md`](configuration.md) — where the `[hooks]` table lives and
  what a checkout may not set
