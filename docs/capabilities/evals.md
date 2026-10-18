# Evals

Everything else that is tested here tests the harness: that the loop records
what it dispatched, that a tier cannot be crossed, that a row wraps at the
right column. None of it can answer the question that decides whether a change
to a prompt was an improvement.

## The check decides, not the transcript

A case is a workspace, a sentence asking for something, and a command that
says whether it was done. The session is handed the task in a copy of the
workspace, and when it stops, the case's own command runs over what it left
behind. Exit zero is the pass, and nothing else about it is interpreted.

Nothing reads the transcript for a verdict. A rubric over the model's own
words grades the explanation rather than the change, and under one of those an
agent that says it fixed the bug scores exactly as well as one that fixed it.
The tests have to pass.

That also means a case is only as good as its check. A check that a session
can satisfy without doing the work is a case that measures nothing, and the
way to find out is to write the check first and watch it fail.

## A pass is not the measurement

A change that keeps every case passing and doubles the rounds is a regression,
and nothing that only counts passes can see it. So every verdict carries what
it took: the rounds, the tokens, the wall clock and the cost, as medians
across the attempts rather than means — one attempt that spent three times the
rounds is exactly the sample a mean should not be allowed to follow.

A workspace run also records its direct mutation calls, calls before its first
mutation, validation calls, and—when the case asks for analysis only—the dirty
paths it left behind. These are operational evidence rather than another
verdict: the case's check still decides completion. Together, the implementation
and failing-test cases show whether a coding request reaches a change and
validation, while the analysis-only case shows whether the same tools were left
unused. It counts the calls that went through the shell as well, beside the
calls that went to a built-in tool, so a case whose work the built-in tools
answer shows which of the two the model reached for — and the bytes of every
result the session read back, so a built-in that answered in more than the
command it replaced shows as that rather than as a pass.

## A case can say how the session should have gone

A check that passes after twelve calls that repeated each other is a pass, and
it is not the same session as one that read, edited and tested. The numbers
above show the cost; they do not say that a call should not have been made.
So a case can declare what it expects of the route, in two keys: `budget`,
a limit on the calls to one tool or to all of them, and `assert`, words from a
closed set. Each is decided from the calls and decisions the session's own
event stream carries — the record's words, plus the arguments and results the
record leaves out — never from what the session said. The stream is read once,
in memory, and dropped with the attempt.

| Word | Holds when | What it does not measure |
|---|---|---|
| `budget <tool> N`, `budget calls N` | the calls to that tool, or every call, number at most N | whether the calls were useful |
| `no-repeat` | no two calls share the loop's own key: the tool, its arguments and a byte-identical result | a sweep of different patterns over one place, which the loop notices separately |
| `read-before-edit` | every write or edit of a file the fixture held was preceded by a read of it; a new file, a file the session wrote itself and an append are exempt | a read through the shell: the word is about the built-in reader |
| `no-mutation` | no call that writes a file, runs a command or a process, or writes to git, and no result saying the session asked for a tool it was never offered | the final tree; an analysis-only case measures that as unintended mutations |
| `no-shell` | no call to the shell | what the shell did |
| `no-denial` | no call was refused, and the report names the reason code of each refusal | whether a refusal, once made, was final: that is an invariant the approval path is tested for, not a measurement |
| `asks` | the session asked at least one question | what it asked; only a conversation somebody can answer holds it, so the loader takes it only on a case with a persona |

A tool the session was never offered is also a misspelt one. Both are asking
for a tool that is not there, which is the fact a read-only conversation is
measured on, and the report does not repeat the name the model gave it.

**A broken assertion fails the attempt and leaves the check's verdict alone.**
The row says the check passed and an assertion broke, and draws the assertions
on a line of their own after the behaviour: each declared word, held or
broken, with a count. A session that did the work by a route the case forbade
is a different finding from one that did not do the work, and a single
"failed" would hide which.

**A report or a baseline may name a tool, an assertion, a reason code and a
count, and never an argument, a path, a command, a prompt or an answer.** That
is the session record's own rule, applied to the one place this writes.
Nothing of the stream is kept: there is no flag to save it and no debugging
copy, because a copy of a session is a persisted tool output and the scrub it
owes belongs to the session, not to the harness. The baseline row carries how
many assertions a case declares and the median that broke, and a rise in that
median under a verdict that did not move is drawn as a control opening
further.

A case that declares none is graded on its check as it always was. Declaring
one changes the case's digest, so the row reads as a different test until its
baseline is refreshed: the first declarations on an existing case belong in
the change that refreshes it.

## A case can have somebody on the other side

Some journeys are not settled by one prompt: the request is ambiguous and the
right move is to ask, or the agent proposes something and the person says no.
A case with a `persona` measures those. It is served — the binary runs as
`serve --stdio` in the copied workspace, started exactly as any other attempt
starts it, with the same forwarded flags and its own data directory — and the
harness is its only client. The case's prompt is the first turn. Every
question and every gated call the session puts to the client is answered from
the persona, and when a turn closes the persona says what comes next.

The served surface is the only one this can be measured on. The question tool
is offered only where somebody can answer, so "the agent never asked" is only a
finding where it could have; and a printed run approves everything up front,
so it cannot be told no for one call. Served with no mode, every gated call is
put to the client one at a time.

A persona is a script, never a model. Each line is
`<event> <expression> => <answer>`, the event one of `question`, `approval` and
`close`, and the first line whose expression matches answers:

- `question` is matched against the question's text. An answer that is one of
  the offered labels is a pick from the card; any other is the person's own
  words. A question no line matches is answered "no preference" and counted on
  the row, because the case is then measuring that answer.
- `approval` is matched against the tool's name and answers `allow` or `deny`.
  A call no line matches is allowed, which is what a printed case answers.
- `close` is matched against the final text of the turn that closed. Its
  answer is the next turn's prompt, or `stop`; a close no line matches stops.

`turns` caps the user turns, the first one included, and the run's `--timeout`
still bounds the attempt: at the ceiling the turn is interrupted and the
session ended. A malformed line is refused when the suite loads, naming the
line.

**The persona sees what the wire shows and nothing else**: a question's text
and the labels it offers, an approval's tool name, and a turn's final text.
Never a tool result, the workspace, the check or the facts. Its answers are a
pure function of those, so the asking side cannot drift between attempts and
`--repeat` reads the agent's side exactly as it does for one turn.

**The persona answers; it never grades.** The verdict is the check and the
declared assertions, as for any case: `asks` for "it asked", and a check that
tests the thing the answer named for "the answer was used". Every turn's events
are the stream's own lines, read as one trace, so rounds, calls, usage and
every assertion span the whole conversation. The session's tokens are its own
across every turn and priced as any session's are. The asking side spends
nothing, and the row says "simulated user: scripted" with the turns it took
rather than putting a zero-dollar figure beside the session's cost.

## Flaky is its own verdict

A case is run more than once, because the thing being measured is not
deterministic and one pass is not evidence. A case that passes four times in
five is not a case that mostly works — it is a task this setup can lose, which
is a different fact from either passing or failing and the one most worth
reading. Averaging it into a percentage is what hides it.

## A run that broke is not a task that failed

A session that never finished — no provider, a stream that dropped, an attempt
that hit its ceiling — says nothing about the task, and is never reported as
the task having failed. The check does not even run. The two readings lead to
opposite actions: one is a case to look at, the other is a machine to fix.

A case whose toolchain is not on this machine is skipped and says which one is
missing, for the same reason: failing it would blame the agent for the
machine. The suite reaches past Go — a task is only evidence about coding if
the code is not all one language, and the argument a model has read more Go
than TypeScript is exactly the kind of thing a suite of Go cases cannot
settle — so most machines will skip something, and a skip has to read as a
skip rather than as a bad afternoon.

## A table case measures a call with no workspace

A session makes calls beside the coding loop: auto mode asks a classifier
whether a proposed tool call matches what the user wanted, and the rail asks a
summarizer what the run is doing and whether it is still doing what was asked.
Neither of them writes a file. The shape above cannot see them at all — a
classifier that answers nothing and a summarizer that comes back empty pass
every case in the suite and every check in the repository, because the verdict
is what the workspace looks like afterwards and they never touch one.

So there is a second shape: a table of evidence a person has already labelled,
put to the real call, and scored by comparing the answer with the label. It
keeps the rule that nothing grades a transcript. These answers are a word from
a closed set, and comparing two words is not a judgement.

The call is built the way a session builds it, and left on the bounds a
session leaves it on. That is the point: the instruction, the ceiling, the
reasoning level and the shape the answer is asked in are what actually ships,
so a change to any of them moves this score. It is also the one place the
suite does not run the binary — an auxiliary call assembles nothing from the
project or the machine, so starting a session to reach it would add a
session's assembly to a measurement that is not about one.

The numbers are the same numbers. A pass over a table reports its tokens, its
wall clock and its cost the way an attempt at a workspace does, so a model
change or a ceiling change can be read as a cost change and not only as a
score change.

## A false allow is not a false deny

A classifier that refuses too much is an annoyance. One that allows too much
is the security control failing open. A single accuracy figure reports the two
as the same number, which is why the report never adds them together.

A row that came back with nothing is a third outcome again, reported apart
from both. The failure this shape was built to catch produces no answer at
all — an output ceiling spent on reasoning returns an unfinished thought and
no verdict — and a suite that scored that as a deny would report an outage as
a cautious security posture and pass.

**The probability is read off the table.** Run on the decisions backend
(`--classifier-backend decisions`), the report prints every row's probability
beside its verdict, the rows that were right as well as the ones that were not,
because a threshold is chosen by seeing where the two sit against each other.
`--classifier-threshold <n>` takes a whole percentage, written as
`behavior.classifier_threshold` is, and holds the run against it instead of the
built-in bar, so one run can be read at several. The median time of a verdict
is taken over the rows that answered: a request that timed out took the
timeout, and a run that mostly timed out would otherwise report the timeout as
its median. The report says how many failed beside it.

## A research case is graded against what it read

A run that reads the web leaves no workspace to check and produces prose
rather than a word from a closed set, so neither shape above can see it. The
third shape gives the run a site — the case's own pages, served over loopback
for the length of the attempt — and grades the write-up against the ledger of
what the fetcher actually returned.

That keeps the rule that nothing grades a transcript. Every comparison here is
mechanical: a URL against the ledger's URLs, a quoted sentence against the
bytes of the pages the run read, a required fact against an expression
the case author wrote. No judgement of the prose, and no rubric.

**Three rates, and never their average.** Every URL the write-up cites has to
be one the ledger says was read; every sentence it quotes has to be on a page
the run read; every fact the case requires has to be there. A write-up
can carry all of the facts and cite a page it never opened, and that is exactly
the failure this shape was built to catch — a single score would report it as a
good run with a rounding error in it. A case passes when all three are whole.

A sentence that is verbatim on a page the run read but not on the one it is
attributed to is a wrong page, and is reported as its own count beside the
rates rather than in any of them. A disagreement write-up has a paragraph per
source, and a quotation that lands after the other source's URL is a real
sentence misplaced, not an invented one. It does not fail the case by itself;
a sentence on no page the run read does.

**A fact is an expression, not a phrase.** "Could not confirm" arrives as
"I was unable to verify" just as often, and a case written in one wording
measures the model's diction rather than its answer.

**The words are the page's; the punctuation is the write-up's.** A model
quoting a clause closes it with a full stop where the page has a comma, and a
literal comparison would score an accurate quotation as an invented one. The
edges are trimmed and nothing else is: a changed word still misses.

**The three rates are kept in a baseline** and compared like a table's
outcomes, because the failure they exist for does not move a verdict. A
write-up that started citing pages the run never fetched failed before and
fails now, and only the citation rate says what changed.

**One case's right answer is that there is no answer.** The commonest research
failure is a confident answer to a question the sources do not settle, and the
only way to test for it is a case whose site does not hold the answer and whose
required fact is the admission.

**The site is the only thing the run can reach.** The address guard refuses
loopback like any other private address, and the exception is one origin's: a
second server on the same address is still refused, and so is the public web,
because a case that could read the real internet would not be reproducible. It
is not a setting — nothing in the configuration turns it on, and the only way
to a policy carrying one is the harness that just started the server.

**A research case runs its loop in this process**, unlike a workspace case and
like a table one. Its site is on loopback and its search endpoint is the
fixture's, and the honest place to lift the guard for one server is the process
that knows which server that is. The prompt, the tools, the fetch result the
model reads and the ledger the write-up is graded against are all the shipped
ones, which is what the measurement is about.

## A scripted case measures the harness

The three shapes above all measure a model, and that is also why none of them
can see the machinery around it. A steer that never quotes the instruction it
was judged against, a window recovery that drops the tool result the next
round was about to read, a child whose report reaches its parent without
saying what it did, a gate that answers "pass" for a check that could not be
started: every one of those is the harness failing rather than the model,
every one of them is silent at the surface it happens on, and no case that
asks a model can report any of them.

So the fourth shape scripts the model's part and measures what is done with
it. The reading a steer acts on is written in the case file rather than asked
for; so is the summary a compacted conversation is rebuilt from, and the
replies a spawned child answers with. Everything the case is about runs
exactly as it runs in a session — the intervention policy, the trim and the
rebuild, the spawn, report and patch loop against a real worktree and a real
`git apply`, the gate over a workspace the row describes.

The row states what the mechanism's output has to carry, and the label is what
it did. A steer that arrives without the task in it is `unquoted` and not
`steered`; a rebuilt conversation missing the result the next round needs is
`lost` and not `kept`; a gate whose check could not be started is `blocked`
and never `pass`. Those are comparisons of words, so the rule that nothing
here grades a transcript holds the way it holds for a table.

Two things follow from there being no request in it. A scripted case costs
nothing, which is what lets a suite have a committed baseline at all — these
are the rows any machine can measure, with or without an account. And its
answer is a fact rather than a judgement: a row that misses is a defect, not a
model having a bad day.

## A run can be compared with the last one

A suite costs real requests, which makes every run worth keeping. Without one
kept, the reader who wants to know whether a prompt edit helped has this run in
front of them and the last one in their memory of a report they read a week
ago, and memory is the wrong place for a median.

So a run can be written to a file and a later one read against it. What is
written is what the report showed and nothing derived from it: the verdict, the
medians beside it, and — for a case with no workspace — its outcomes. Those go
in as figures of their own rather than folded into a pass count, because a
control that let one more action through has failed a little further open and a
verdict that read "failed" both times says nothing about that.

The delta is the same report shape as the run above it, with two numbers where
the report had one. It leads on the two a comparison is actually read on — how
much work the task took and what it cost — and leaves the rest of what was
written for the reader who opens the file, because a row that carries every
figure clips the ones that moved. A verdict that moved is the finding, whatever the numbers
did — a case that now fails is not redeemed by having failed in fewer rounds.
Under an unchanged verdict a control that opened further comes next, and the
medians last, because they are what says a kept verdict was paid for.

A case the run measured and the baseline does not hold is named in the report
and left out of the totals, which say they cover only the cases both runs
hold. A suite that gained a case is behind its baseline by exactly that case,
and refusing the whole comparison until the file is refreshed would cost the
reader every other row. A case the baseline holds and the run did not measure
is still refused: that is a run that skipped part of the suite, and totals over
what is left would read as a whole. A case that could not be compared is never
counted by `--fail-on-regression`, as a regression or as a pass.

## A suite keeps the run it is read against

The baseline a run is compared with by default is a file in the suite,
`baseline.json`, beside the cases. It is content the way the cases are: what
this suite is expected to produce belongs with it, a change to that gets
reviewed like a change to anything else, and a comparison that has to be
asked for with a flag is one nobody remembers to ask for.

Nothing writes it on its own. `--refresh-baseline` is how it is replaced, and
the deliberateness is the point — a file that updated itself after every run
would agree with every run and could never report anything. A suite with no
baseline yet is not an error: the report says there was nothing to read the
run against, and how to write one.

A refresh needs every measured case measured. A run that skipped a case the
file holds a measured row for (no model to ask, a toolchain the machine lacks)
is refused before anything is written, with each case and its reason named and
a non-zero exit, so a machine that could not ask a model cannot replace
measurements with skips. A skipped case new to the suite is named and left out
of the file rather than written as a skip, and a run that measured nothing
writes nothing.

A run narrowed to named cases is compared against those rows of the file and
not the rest. That is not the skipped suite the section above refuses: the reader
said which cases they wanted.

A machine with no account still measures the scripted cases, and the cases
that need a model are skipped rather than failed — the same fact as a case
whose toolchain is not installed. The report says so on every row and once at
the top, so a mistyped key cannot read as a suite that passed, and the
baseline such a run writes names no model, because it asked none.

## A case that changed is a different test

A comparison measures one change only if everything else held still. A
prompt edit measured against a suite whose cases were edited in the same week
cannot say which of the two moved a row, and a case whose task or check was
rewritten is not the test its baseline row measured. So the set of cases is
held fixed while a prompt is measured against it, and the baseline is what
holds it: each row records a digest of everything in its case's directory —
the case file and with it the check, the table, the workspace or site it
brings.

A row whose case has changed since is not comparable, with the reason saying
so, and never improved or regressed. Like any row that could not be compared,
`--fail-on-regression` does not count it either way. Refreshing the baseline
records the case as it is now, and because each digest sits under its case's
name in the file, the diff a reviewer reads names each case that changed.

The digest comes out the same on every machine that checks the suite out, or a
baseline recorded on one would read as a suite of changed cases on the next. It
reads paths relative to the case and contents with line endings normalised,
and nothing a checkout can vary without changing the case: no timestamp, owner
or permission bit, no empty directory, and no file the desktop leaves in a
folder someone opened.

A baseline written before rows carried a digest is still read. Its rows compare
the way they always did, and the report says once that an edited case cannot
be told from one left alone there, rather than refusing every row until the
file is refreshed.

## A rate needs enough samples to be a rate

A delta prints counts always and a percentage only where there are enough
samples behind it. Three attempts either side produce swings of thirty-three
points from nothing at all, and a row that prints one invites acting on it —
which is the whole difference between a comparison and a random number
generator. The threshold is small, and argued rather than hidden: it is the
smallest denominator at which one sample cannot move the figure by more than
twenty points.

What counts as a sample depends on the shape. A workspace case's samples are
its attempts, because the attempt is the draw. A table case's are its rows: a
twenty-row table put once is twenty independent answers, and repeating it is
more of them.

It is one rule and not an eval rule: any comparison this product prints over a
handful of samples owes the reader the same restraint. This is the first
surface that needed it, which is why it is written down here.

## A run can fail a pipeline when asked

A run's exit status says whether it ran, not whether it found anything: a
report with five failed cases and a regression exits clean, because the report
is the answer and a person reads it. A pipeline cannot read it, so
`--fail-on-regression` makes the command exit non-zero and name each case that
regressed. It is a flag and not the default because a run that exits non-zero
on a bad result would make every local run look like a broken command, and
because what counts as bad is the maintainer's to decide for their own gate.

The rule is narrow on purpose: a case that passed in the baseline and failed on
every attempt now. A case that is merely flaky is not a regression, and neither
is a median that moved, since both are findings the report already makes and
neither is a reason to refuse a merge. One attempt cannot tell flaky from
broken, so the flag is refused below three attempts, with the sentence saying
why, before anything is spent. It is refused too when there is no baseline to
compare against, because a gate that passes for want of anything to read the
run against is the failure it exists to prevent.

It takes the second half of a gate — nothing that passed before fails now — and
leaves a pass rate to a number a person sets, if they want one. The manual
workflow passes the flag only when the person starting it ticks
`fail_on_regression`, which is off by default: the workflow's default of one
attempt would be refused by the flag, and a run started to read a report should
not turn red for having found something. Nothing schedules a run: each costs
real requests, and what to spend them on is a decision.

## The session under measurement is a real one

A case runs the actual binary, as a headless session, in a real repository. Not
the agent loop driven in-process: the system prompt is assembled from the
project, the machine and the configuration, and a harness that skipped that
assembly could not tell you whether a change to it helped — which is most of
what there is to change.

The workspace is a copy, always. A case that ran in its own directory would
grade its second attempt against the leftovers of its first, and drift a
little further every time it ran. It is made a git repository too, because a
session in a checkout is a different session: it can undo a turn, its prompt
states the branch and what was already changed, and its containment grants
differ.

## A measured session keeps nothing

A session leaves its conversation, its record and its evidence in the data
directory, and an attempt that used the reader's would put every measured
session in their chat history and in the cohorts `shhh observe` exists to keep
honest. So each attempt gets a data directory of its own, beside its
workspace, and both are removed when it ends. The configuration is not moved:
it is part of what is measured.

Two things follow. Measured sessions do not appear in the reader's history or
metrics. And a session under measurement reads no durable memories, because
those live in the data directory too — which is also what makes a case's
result the same on two machines.

## The other modes are measured too

`shhh code` is not the only mode with a promise to keep, so two more kinds
measure the others.

A chat case is shaped like a workspace case and run the same way, as the real
binary, but as `chat --print --output jsonl` in the copied checkout. It has a
`prompt` and `facts` and no check: it passes when every fact is stated in the
close line's final text and the built-in `no-mutation` held. Chat's promise is
a property of its toolset, so a model cannot call an edit there; what it can
do is ask for a tool it was never given, and that comes back as a result of
class `unknown`, which `no-mutation` counts. A misspelt tool name reads the
same way, and both are the fact the case wants to see. The tree is checked as
well, and a dirty path is recorded as an unintended mutation. `budget` and
`assert` apply as they do to any session, and the report names facts by count,
never by their text or the answer.

A one-shot case is a table of requests for a command, asked in this process
through `raw.Generate`, the function `shhh cmd --raw` prints from, so the
prompt and the fence stripping are the ones that ship. A row has an
`instruction`, a `command` (an expression), an `expect` from `expected`,
`other` and `dangerous`, and a `shell`. The suite pins the shell to `bash` for
every row, so an answer does not depend on the login shell of whoever ran it;
a row may name another explicitly. The command is labelled by the safety table
first, because a command it flags is `dangerous` whatever the row hoped for,
and by the expression second. A `dangerous` answer to a row that expected
`expected` is reported apart from a plain miss: the model wrote what the
safety table exists to catch, for a request that asked for nothing
destructive. A row starts no program.

Inline is the same seam. The zsh and bash widgets run `shhh cmd --raw
"$BUFFER"`, so the model's part of inline is measured by the one-shot table.
That the widget then replaces the buffer and puts the cursor at its end is
shell script, and belongs to the `init` tests and a driven scene.

## It is not part of the gate

Every case that names a model costs real requests, so a suite is something you
run when you want an answer — after a prompt change, before choosing a model,
when a session starts feeling worse than it did. The offline gate stays
offline. The workflow that runs the suite is one a person triggers, and it
says what the run costs.

The scripted cases cost nothing and are still not in the gate, which is a
deliberate choice rather than an oversight: a case is where a measurement goes
that is allowed to move, and a suite whose rows became a build failure would
be a suite nobody dares add an exploratory row to. What belongs in `make ci`
is the assertion a mechanism must never break; what belongs here is the
reading of how it is doing.

## Related

- [`coding-agent.md`](coding-agent.md) — what is being measured
- [`approvals-and-safety.md`](approvals-and-safety.md) — why a case runs with edits approved
- [`providers.md`](providers.md) — what a case is priced against
- [`chat.md`](chat.md#what-was-read) — the ledger a research case is graded against
