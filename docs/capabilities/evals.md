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
machine.

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

## It is not part of the gate

Every case costs real requests, so a suite is something you run when you want
an answer — after a prompt change, before choosing a model, when a session
starts feeling worse than it did. The offline gate stays offline.

## Related

- [`coding-agent.md`](coding-agent.md) — what is being measured
- [`approvals-and-safety.md`](approvals-and-safety.md) — why a case runs with edits approved
- [`providers.md`](providers.md) — what a case is priced against
