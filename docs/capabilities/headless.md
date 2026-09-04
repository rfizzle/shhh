# Driving it without the screen

## A run nobody is watching is still answerable to somebody

`shhh code -p "…"` is the coding agent with the screen taken away. Everything
else about it is the same — the same loop, the same round cap, the same
approvals resolved from policy instead of from a person, the same record — and
the difference is only in who is on the other end. Nobody reads the answer as
it arrives; something else runs the command, waits for it to finish, and has
to decide what to do next from what it got.

That is the whole design constraint. A person watching a session can see a
round cap being hit, can read a provider's rate limit on the meter, can tell a
finished turn from an interrupted one at a glance. A script sees a process
that exited and some bytes. So what an unattended run leaves behind has to
carry those distinctions itself: a status that says which of them happened,
and — for anything that has to act while the run is still going — a stream
that says it as it happens.

## The exit code is the contract

The status is drawn from a closed set. A code means one thing, it goes on
meaning it, and a script can branch on it without reading a word of output.

| Code | What happened | What to do about it |
|---|---|---|
| `0` | The turn finished and nothing objected | Take the answer |
| `1` | shhh could not run at all | Fix the invocation: a flag, the config, a provider that will not resolve. It is not a run that went badly, it is a run that never started |
| `2` | The turn used up its tool rounds | The work is unfinished, not wrong. Raise `--max-rounds`, or `--max-rounds 0` for a run you are content to leave going, and continue it with `-p --continue` |
| `3` | The run was interrupted | Somebody or something stopped it. The conversation is saved and well-formed; re-run or continue it |
| `4` | The provider stopped answering | The waits are already built in — this is what is left after them. Retry later; nothing about the request was wrong |
| `5` | The checks failed | The turn finished and the suite it closes on did not pass. The tree changed; look at it before you ship it |
| `6` | A call was refused | Policy denied the last approval the run asked for, so it did not do what it was asked. Re-run with `--yes`, or with `--allow` for the command shapes you meant to permit |
| `7` | A backlog item blocked | `shhh todo run` only. The item was worked as far as it could go and stopped with the evidence written on it; the work so far is in the tree, uncommitted. Read the item, settle what it names, and reopen it |

The last row is the backlog runner's, and it is one more code in this set
rather than a set of its own. A blocked item is not a turn that broke — every
turn in it ended the way turns end — and it is not a failing suite or a
refusal either. It is the runner's own terminal state, which is the one fact
none of the codes above can carry.

**The code is a projection, not a second table.** The record already keeps how
every turn ended, from its own closed set, and the exit status is read off
that one value rather than decided again beside it. Two closed sets that have
to agree by hand agree until somebody adds a case to one of them, and nothing
fails when they stop — the table would say a run was capped while the script
that ran it was told it crashed. Deriving one from the other is what makes
that impossible rather than unlikely.

Two readings sit on top of the turn's own ending, and both belong to a turn
that finished. A failing suite is not a turn that broke: the loop did
everything asked of it and a second opinion arrived afterwards. Neither is a
refusal: the policy answered, the model was told, and the turn ended on that
answer. They are ordered — the suite before the refusal — because a verdict
about the tree as it now stands is the more actionable of the two facts, and
because a refusal that mattered usually leaves nothing behind for a suite to
have an opinion about.

A refusal is the *last* verdict and not any verdict. A run that was denied one
command, found another way and finished did the work; reporting that as a
refusal would teach every script to ignore the code. A denial still standing
when the model stops is the one that ended the turn.

## What a signal does to a run

A run is one turn, and the first interrupt or termination signal it is sent
stops that turn rather than the process. The stream it was reading is dropped,
the conversation is closed off well-formed and saved to its slot, the record
says the turn was cancelled, and the status is `3` — the same shape of ending
every other way a run can stop already has, instead of a process that
disappeared with a signal status and nothing written down. What it had done up
to the interruption is in the slot, so `--continue` carries it on.

The second signal kills the run where it stands, as it always did. One grace
is what an interrupt is worth here: the loop stops at checkpoints, and a run
stuck somewhere between two of them has to answer to being told rather than
asked.

## Three shapes for the same run

`--output` says what the run writes on stdout. Tool activity, waits and
notices go to stderr in every shape, because a script reading stdout for an
answer is not the reader those lines are for.

- **`text`** — the default. The answer as it is written, so `$(shhh code -p
  …)` is the answer and nothing else.
- **`json`** — the whole transcript once the run is over: the outcome, the
  totals, and every message including the tool calls and their results.
  `--json` is the older spelling of this and goes on meaning it.
- **`jsonl`** — one JSON object per line, written while the run happens.

Naming both `--json` and a `--output` that disagrees with it is refused rather
than resolved, because either answer would be somebody's script quietly
reading the wrong stream.

The transcript also says when the answer it quotes is half of one. A reply cut
off at the model's output ceiling is continued once by the run itself, and a
second attempt that stopped at the ceiling too still stands — but it stands
labelled, because nothing in the words says the sentence was cut and a caller
that grades the answer would be grading half the work
([`providers.md`](providers.md#a-reply-says-why-it-stopped)). A whole answer
carries no such label, which is how every reader of this shape already read it
before there was one.

The token totals state the cached share of the prompt as well as the prompt
and completion counts. It is billed at a fraction of the rest and cannot be
recovered from the other two figures, so a script pricing a night of runs
against a rate card needs it stated
([`providers.md`](providers.md#the-prompt-prefix-is-paid-for-once)).

## The stream is the record as it happens

`jsonl` exists for the thing that has to act before the run is over: a lane in
a dashboard, a log that has to be tailed, a wrapper that kills a run which has
started doing something it should not.

Every line carries its kind, where in the run it happened, and its payload:
the answer arriving in pieces, a call the model asked for, what that call came
back with, an approval verdict, one of the loop's own safeguards firing, the
running totals, and a close line stating how the turn ended and the code the
process is about to exit with.

**The words are the record's own.** A denial on the stream is spelled the way
the metrics tables spell it; so is a failed call's class, a compaction, a
retry, a turn's outcome. There is one vocabulary and the stream is a second
view of it, so a reader who has learned either one has learned both, and a
future hook receiving the same shapes on stdin needs no second dictionary. No
field that names a kind, an outcome or a reason ever holds prose the run
composed: a code you have to parse a sentence out of is not a code.

Replaying the events rebuilds the conversation the transcript states at the
end — the same messages, the same calls, the same results. The two are
readings of one run rather than two accounts of it, so nothing has to read
both.

## Everything the session has, unless somebody has to answer

An unattended run registers what a session registers: the same tools under the
same conditions, in one definition rather than a copy per surface. A copy
agrees on the day it is written, and after that the surface with the older one
quietly offers a tool it cannot dispatch, or fails to offer one the other has.

What it does not get is the things that need somebody there. Nothing pops a
browser for a page it published — there is no guarantee of a desktop, and the
URL reaches the transcript anyway. Durable memory proposes nothing, because a
proposal is confirmed by a person. Sub-agents are not offered, because their
spawn is an approval. And approvals themselves are policy's and never a
classifier's: `--yes` and `--allow` opt in, the default denies, and a
safety-flagged command is denied whatever the flags say
([`approvals-and-safety.md`](approvals-and-safety.md)).

A flag the run cannot honour is a usage error rather than a silent no-op.
`--resume` with no chat named opens a picker, and a run with nobody in front
of it can neither draw one nor be answered — so it says so, instead of
starting from nothing while claiming to have resumed.

## Something else can drive it

A script starts a run, waits, and reads what it left behind. That is the whole
of the arrangement above, and it rules out everything that has to happen while
the run is going: a question put to somebody, a correction sent mid-turn, a
second window watching the same work.

`shhh serve` is the same agent with a protocol in front of it instead of a
terminal. It speaks JSON-RPC — one JSON object per line, over stdio or a unix
socket — and a client opens a session, starts a turn in it, steers or
interrupts that turn, and answers the calls the turn may not make unasked. The
events it receives while all that happens are the ones above, unchanged: the
same lines, the same words, forwarded rather than rewritten, so a client that
can read a run's output can read a session it is driving with the same code.

Nothing about the run itself moves behind the protocol. It is assembled the
way an unattended run is — the same tools registered on the same conditions,
the same containment around a command, the same hooks at the same seams, the
same record written — because a second way to build a session is a second set
of answers to every question about what one may do. shhh's own screen has not
moved behind it either, and is not going to as part of this: what the surface
buys is that something else *can*, and the first thing worth pointing at it is
an adapter to a protocol somebody else's editor already speaks.

A server has one working directory, which is the checkout it was started in.
Sessions on it are several conversations over that one tree, and each is
addressed by a name the server minted; a second client naming one is handed
the conversation so far and joins the audience for the rest of it. Two clients
on one session see one transcript, because there is one.

## A client answers one call at a time

The approval queue belongs to the protocol and not to the client. A request is
put to everyone watching the session under an id the server minted, and an
answer names that id. There is no name for a request that has not happened, so
there is nothing for a client to approve a tier with in advance — no standing
yes, no `--yes`, no mode. Every gated call is one question and one answer.

The answer is a decision, and a decision does not outrank a rule. What a
client allows still goes to the same approver a `--yes` run's calls go to, so
the deny list refuses it, an uncontained host refuses it where containment was
required, the safety table refuses a dangerous command, and the working scope
answers a write outside it — in that order, exactly as they answer a run
nobody is driving. A directory that can never be granted, or one that
is sensitive, is refused there whatever the client said; an ordinary one is
added, because the path was in the call the client was shown and answering yes
to the call is answering yes to where it writes. Containment, the checkout's
trust answer and the deny mask are the same whoever is at the other end. A
refusal, on the other hand, is final on its own: nothing behind it is
consulted, because an allowlist that could run a call the client had just
declined would make the question a formality.

An approval nobody is left to answer is a refusal. A client that disconnects
with a request outstanding does not leave the turn waiting for a decision that
is never coming.

## The backlog, worked without you

`shhh todo run` is the other unattended shape: not one question answered, but
one backlog item taken from research through to a commit — or the whole ready
list, one item at a time, with `--all`.

It is the same machine the session drives and it drives it the same way, with
the screen taken away. Each stage of a run is one `shhh code --print` in the
checkout, so a stage is a session and an item is a handful of them; nothing
carries between two stages except the item's checkpoint, which is what the
checkpoint has always been for. What a stage produced is read out of its
transcript whatever status that process left, because a stage that ran out of
rounds still did work and the machine judges a stage on its answer.

Two of the gates cannot be taken here. The pause asks a person, and there is
nobody to ask, so a run that reaches it stops with the questions written on
the item rather than guessing; and a review that would have gone to a second
agent is taken in the session instead, which is what a session with no
supervisor already does. Everything else is unchanged — shhh runs the
verification, shhh makes the commit, and only paths the run itself changed are
staged.

The status is the run's own ending: `0` for an item that finished, `7` for one
that blocked. A sprint stops on the first block, so `7` from `--all` means the
list was not finished and the item that stopped it says why. A sprint that
dies with its process leaves a checkpoint, and the same command picks it up
([`todo.md`](todo.md#a-sprint-is-runs-with-a-session-between-them)).

## Related

- [`todo.md`](todo.md) — the backlog itself, and what a run does with an item
- [`coding-agent.md`](coding-agent.md) — the loop behind it, the round cap,
  and the checks it closes on
- [`sessions-and-memory.md`](sessions-and-memory.md) — the slot a run leaves
  behind, and the record it writes
- [`approvals-and-safety.md`](approvals-and-safety.md) — what policy will and
  will not allow where nobody can be asked
