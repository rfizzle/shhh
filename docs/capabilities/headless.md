# Running it from a script

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

## Related

- [`coding-agent.md`](coding-agent.md) — the loop behind it, the round cap,
  and the checks it closes on
- [`sessions-and-memory.md`](sessions-and-memory.md) — the slot a run leaves
  behind, and the record it writes
- [`approvals-and-safety.md`](approvals-and-safety.md) — what policy will and
  will not allow where nobody can be asked
