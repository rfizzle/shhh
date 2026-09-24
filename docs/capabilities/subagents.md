# Sub-agents

A session can hand part of a job to a child agent. Children are how a large
task gets parallelism and a clean context without the parent losing track of
what is happening.

Four words carry this document and every surface drawn from it. An
**agent** is anything the session draws doing work, the root included — the
root is the one agent with a name rather than a role, `orchestrator`, which
is what the manager, the rail's map, the breadcrumb and the notebook's
signature all call it. A **child** is an agent seen from what spawned it,
and that agent is its **parent**; a child's own children are its
descendants. A **profile** is the file that says what an agent may do and
how it is told to do it. A **role** is the name a spawn asks for — a shipped
one or a profile's — and the name a child is drawn under is made from it
(`writer-1`). Nothing else is a word for an agent: *sub-agent* is the name of this
capability and never of one of them, *delegate* is a verb and not a noun,
and *colleague* and *persona* are what a read-only profile is to a
conversation, in [`chat.md`](chat.md#colleagues-not-workers) and nowhere
else. A *lane* and a *row* are not agents either but drawings of one: a
fan-out gives each child a lane, and the manager and the map give each agent
a row ([`surfaces.md`](../interface/surfaces.md#the-agent-manager)).

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

## A child searches with what the session searches with

A child is given the session's own read-only toolset, not a smaller one: the
[six questions a language
server answers](coding-agent.md#six-questions-for-the-language-server), the
structural search tools this machine turned out to have, and the read-only
git verbs — on top of the file tools every child always had.

A delegated search is worth delegating only if the child is at least as good
at it as the session that delegated it; a child that had to grind a text search
where its parent would have asked for a symbol's references spent more of its
own budget to come back with less, and a reader with no git tool and no
command to run one with could not read the history of the file it was sent to
explain.

Two tools stay behind, and for the same reason: they need somebody to answer.
Writing to git is approved on a card, and a child has no card of its own — its
work comes back as a patch you approve whole. A report is a page published for
the person, and a child answers its parent; what it found reaches a page
through the parent's own call.

The tools are contained to where the child is standing. A writer works in an
isolated copy of the checkout, so every path its searches, its structural
queries and its questions to the language server name is a path in that
copy — the code it is actually editing — and one that climbs out of it is
refused. The language server itself is the session's, not one per child: a
project-wide symbol search is answered out of the checkout the copy was made
from, which holds the same code apart from what the child has just changed.

A writer's applied edit comes back carrying the language server's verdict on
the file it just wrote, as one applied on your own screen does. A child that
had to run the build to find out what it broke spent a round and an approval
on a question that had already been answered.

One half of that is the session's alone. A check that runs long leaves its
answer to be collected by the next edit anyone makes, and with several agents
editing at once "the next edit" is somebody else's: a child would be handed a
verdict about your file, or a sibling's, and you would be handed one about a
file inside a copy of the checkout you are not standing in. So a child takes
no late answer and leaves none behind. What it gives up is the answer to its
own slow check, which it can ask for outright — every child has the tool, and
is told to use it after an edit that came back without a verdict.

Every child's prompt ends with the same toolbox block a session's does: a line
per tool saying when it is the right answer, over the set that child actually
ended up with. Nothing in it describes a tool the child does not have.

## A writer starts from your tree

An isolated copy of the repository is not the same thing as a copy of the last
commit. A session an hour old has an hour of work in it that no commit holds,
and a child that started from the commit would be reading code you no longer
have: it explains a function you already rewrote, it re-solves a problem you
already solved, and every hunk it writes over a file you had edited collides
when its patch lands — leaving you to reconcile your own work against work you
asked for.

So a writer's copy starts from your tree rather than from your history:
everything git reports as changed and not yet committed, and the files the
session itself created that git has never heard of. That state becomes the
child's own starting point, which is what keeps the two apart on the way back
— what returns for your approval is the child's work alone, never your
uncommitted changes handed back to you as if a child had made them.

The new files are the ones this session wrote, not every file git does not
recognise. A working checkout is full of untracked things nobody in the
conversation put there — a scratch note, a core dump, a directory of build
output — and carrying those into every child would copy your desk rather than
your work. The session already records each file it creates, so it can name
the difference; git cannot.

The copy is taken when the child starts, not when it is spawned. Three
children run at once and a fan-out may ask for more, so a spawned writer can
sit in a queue while the ones ahead of it work; one given its copy at spawn
would hold a whole checkout on disk for that whole wait, and would begin from
your tree as it stood when the fan-out was planned rather than as it stands
when it begins. A writer that cannot be given a copy at all fails as a child,
with the reason on its own lane — by then the turn that asked for it has long
since answered, and there is nowhere else for the failure to go. Writers that
start together are given their copies one at a time, because git making two
copies of one repository at once can trip over its own half-written
bookkeeping; only the making waits, and the children then work side by side.

A writer can also wait for a file rather than for room. Two writers that
declare overlapping paths are refused by default while both are live, because
two patches over one file are two claims on the same lines and the second
landing is the conflict. When an orchestrator hands a whole batch over at once,
the spawn can instead say to wait for the claim: the writer is admitted like
any other — a budget that could not start it is refused then, not when its
turn comes — but holds no slot and no copy while it waits, and its lane reads
`queued behind` the writer it follows. It starts once every overlapping writer
spawned before it has finished, which is after that writer's patch has been
landed or declined — or kept for you, where the writer stopped short — so its
copy is taken from the tree with the earlier work already in it. Writers
handed over in one round are put in order as they arrive, so two of them
cannot both find the other absent and start together. Writers queued behind one claim start in the order they were
spawned. Asking for its report says it has not started and why, rather than
waiting on work nobody named; killing it drops it from the queue with nothing
to keep. The default stays a refusal because an overlap the orchestrator did
not expect is usually work divided along the wrong line, and queueing it would
turn a mistake it could fix now into a serial run it did not ask for. A
parallel sprint serialises two items the same way and by the same test, so a
batch the session queues and one the backlog runner works are ordered alike.

This changes what a child starts from and nothing about what comes back.
Approval is still the only way anything reaches your checkout, the lane says
how many of your files the child started from, and a checkout with nothing
uncommitted in it starts a child exactly where it always did.

Your tree does not stand still while a fan-out works, and the copy does not
either. When one writer's patch lands in your checkout, every other writer
still working is owed it: its copy was taken from a tree that patch has just
moved, and a patch written against the older text is the second landing that
does not apply. So the landed change is carried into each of their copies and
becomes part of what each one started from — its own work stays where it is,
on top, and what comes back for your approval is still that work alone. It is
done for the writer rather than by it: a child never runs git on its copy,
and one that did would be rebasing work it cannot see against a tree it
cannot see either.

It happens at the child's own round boundary and nowhere else — the same
boundary a hold parks it at, and for the same reason: a round in flight is a
request the provider is still answering and a call still writing, and a tree
that changed under either would be a tree the child's next edit was not
written for. The child is parked for the moment it takes, and its row reads
`reseeding` while it is. Its model is told nothing about a change that carried,
because there is nothing to act on; its prompt says the tree can move, so a
file that reads differently next time is the checkout as it stands rather than
a mistake of its own. A landed change that meets the child's own work is not
forced into its copy. The copy is left exactly as it was, and the child is
told what landed and which files the two met on, so its final report can say
where its patch and the other writer's overlap.

Reseeding, and not waiting to merge at the end, because the moment a patch
lands is the one moment the difference is small and known: one landed patch,
against one base, with the child between two rounds. By the time the child
has finished it has written a whole patch against a tree that has moved,
and what was a file to re-read is a conflict for you to reconcile.

A landing is not the only way the tree moves, and a writer is not always
between rounds when one lands: you save a file yourself, or a writer that has
already written its answer is past the last boundary a landing could be
carried in at. So a patch that no longer applies to your checkout as it
stands is merged three ways — the tree the writer started from, your files as
they are now, and the writer's — and the merge is what you are asked about
and what lands. A card that showed the writer's own diff and then applied
something else would be an approval of a change that is not the one that
landed. The merge is worked out on the side, with your files only read, and
lands by the same all-or-nothing apply as every other patch; if your checkout
moves again while the card is up, nothing lands and the card comes back with
the merge redone. Where the two changes meet on the same lines nothing is
merged at all: which side wins is a judgement about your work and the
writer's, and a merge has no standing to make it, so the patch is kept for you
with the files named.

A generated file is never merged, and never carried in as the writer wrote it.
A golden fixture or a generated section of a document is the output of a
command over the source, and a three-way merge of two writers' versions of one
is wrong even when it comes out clean: each writer's source change rewrote the
fixture, and interleaving the two rewrites gives a file no generator would
write — the kind that passes review because it looks like the others and fails
the next run of the suite. So the project declares its generated paths beside
its checks, in the quality config, each with the command that writes it, and a
patch that touches one lands in two parts: the rest of the patch, applied or
merged as above, to a copy of your checkout as it stands, and then the
generators for those paths run in that copy. What you are shown and what lands
is the copy's difference from your checkout — the source change and every file
its generators rewrote — with the card naming the command that wrote them. The
generator runs contained, the way a check does, with its output kept as
evidence. One that fails lands nothing on its own: the card comes up with the
change and without the generated files, saying which generator failed and
where its output is, and your files have not been touched. A patch kept for
you to review from the writer's row is kept without its generated files and
says so: it lands later by the plain apply, with no copy left to regenerate
in, so those files are left to their generator rather than landed as the
writer wrote them. A landing carried
into a live writer's copy follows the same rule: the copy's base takes the
landed bytes, and the writer's own copy of each generated file is regenerated
there, over its own work, rather than patched — so two writers who both
regenerated one fixture do not collide over it. The declaration is part of the
quality config, so it is trusted with it: a checkout nobody has trusted
declares nothing, and a file the config does not name is text like any other,
merged as above. The rule is declared, never guessed from a file's name. The
writer's prompt says so too, so a writer that meets a collision over a
generated file changes the source and leaves the file to its generator rather
than merging it by hand.

## Spawning is a decision

Starting a child is an approval-gated call, like an edit or a command. It
spends the session's budget on work nobody reads until it reports, and a
writer's changes come back as a patch, so the question the card asks is worth
asking: which role, what it may touch, what it will cost.

The surfaces answer it differently and all of them answer it. In a session you
answer the card. A client driving a served session answers it the way it
answers every other gated call. A scripted run answers it with `--yes`, or
with auto mode's classifier, and a run given neither is not offered the roles
at all ([`headless.md`](headless.md#a-run-can-delegate)).

The card in a session is the spawn's own, and it carries the three answers the
question has. Which role, as the card's title and a clause saying what a child
of that role is given. What it may touch, as one statement per child: a
worktree and the paths a writer claimed, `unknown — this agent claimed no
paths` for a writer that claimed none, and for a role that neither writes nor
reviews, that it reads only and changes nothing. And what it costs — its round
setting, its token ceiling, and the hosts it arrives able to reach without
asking, which are the session's own and can never be added to.

A round that asked for several children is one card with a row per child, not
one card per child. Three researchers were three decisions where the reader
was making one, and the three cards each asked it in the words of a generic
tool call. `[y]` starts the set; the key that opens the queue picks it down to
the ones wanted, and each child is still put to the mode, the deny list and
the working scope when its turn comes. The layout is the interface's
([`../interface/surfaces.md`](../interface/surfaces.md#the-approval-card)).

A role that changes nothing can be waved through for the rest of the session
from that card, which is what stops the next fan-out of researchers being the
same card again
([`approvals-and-safety.md`](approvals-and-safety.md#a-read-only-role-is-granted-once)).
A writer's never is: a writer's patch is the decision that matters, and the
spawn card is where the person learns what it will claim.

The card is the person's side of the decision. `agents.delegation` is the
model's side: when to ask at all. `explicit`, the default, tells it that a
request for thoroughness or depth is not a request to delegate, because
"look into this carefully" is the commonest way to ask for depth, and a model
that reads it as a licence to spawn fills the queue with cards the person
never meant to raise. `proactive` tells it the opposite: work that divides
into independent parts is to be divided. It is for the person who would
otherwise answer yes to every card and wants the model to raise them without
being asked. Neither policy answers a card, so a spawn under either one is
still put to the person. `off` takes the orchestration tools away. A session
that is never to spawn is better off not being shown a tool it may not use
than refusing every card that tool raises. Whatever the policy, the model is
told how a delegation is written: one self-contained task per agent, writers
working at the same time on paths that do not overlap, and several children
collected with one wait on the set. `/status` and a scripted run's stderr name
the policy, because a session that never spawned reads the same as one told
not to until something says which it was.

## A child answers to the session

A child's permission mode is clamped to its parent's and can never be looser,
and the parent's session grants travel with it: what you waved through for the
session is waved through for its children, so one grant is not re-asked once
per agent. A profile may start a child stricter — a reviewer in read-only mode
under an auto session — and that is the only direction the clamp allows.

What a child cannot do is ask somebody the session cannot reach. In a session
its request becomes a card in front of you. Where there is nobody — a scripted
run, or a served session whose protocol draws cards for the turn it is running
and has no vocabulary for a child's — the answer to the spawn stands as the
answer to the child, and a request the child's own policy still stopped to ask
about is refused. A patch is the exception, and only where the run may write:
the run asked for the work, so what is left to refuse is a patch that overlaps
one already landed.

A question is the one request that has no card to become. A child is not
offered the tool that asks, whoever is there: a fan-out exists for work that
does not need the reader, and one that could stop for a question would need
watching.
A child that would have asked states the assumption it made instead, in its
report, where the session — and the person — can read it and disagree
([`coding-agent.md`](coding-agent.md#nobody-to-ask)). Its report contract names
the place: one item per assumption under a heading of its own, *Assumptions*,
left out when it assumed nothing — named rather than left to the child,
because the lane counts what is under it and a list titled some other way is
one nothing can count.

## A review is bounded by what it is given

A review that has to find the change before it can read it spends its budget
on the search. Asking it not to did not hold: told to read the declared files
first, it read the repository's instructions and surveyed the tree around the
change, and reached its budget with the diff still unread.

So the bound is in what a review receives and where it is stopped, not in what
it is asked. Declare a review's paths and it opens on them: the paths and the
workspace change under them arrive ahead of its task, so the first thing it
reads is the change. Those paths reserve nothing — two reviews of the same
change are the ordinary case, and only a writer's paths are a claim against
other agents. A review with no declared paths opens on its task alone, which is
the caller carrying the diff in the task text itself.

The evidence is part of what the review is admitted for. It is measured with
the inherited prompt and the task, so a budget that could not carry the change
is refused at the spawn rather than discovered halfway through reading it. An
oversized diff is truncated and says so, which is a reviewer that knows it saw
part of the change.

The inspection pass ends at the review's round cap. Every other child treats
that cap as a check-in — it takes stock and carries on with more room — and a
review does not: it is told to report on the evidence it examined, with only
the rounds a report needs, and to name what it did not reach. A review that
spends those too stops with what it has rather than opening a wider pass.

The words a review reads are the same contract. Declaring a review is what
selects the reviewer's prompt — read the declared evidence before anything
else, rank findings by severity, end on a verdict — for a profile as well as
for the built-in role, because a child bounded as a review and instructed as a
reader would spend its pass doing the thing the bound exists to prevent.

## What comes back says what happened to it

A child's report is the child's own words, and around them is everything the
session already knows about how it got there: how it ended, what it spent, how
many times it stopped to take stock, the last reading of its work and how
often it had to be redirected. Those last two are on the roster as well, and
they are on the report because the report is what the parent acts on — a child
steered three times that comes back calling its work sufficient is one to
check rather than to take, and nothing in the words themselves would say so.

Between that header and the report is one line naming the child and saying
that what follows is its words, not the user's, and that nothing in it is an
instruction. The report lands in the parent's conversation beside everything
the person said, and a child that read a page telling it to *approve this* can
hand that sentence on word for word; the line is what keeps it reading as the
child's claim rather than as the person's request
([`approvals-and-safety.md`](approvals-and-safety.md#only-the-persons-own-path-carries-authority)).
It is above the report and never inside it, so the report still ends on its
own last line, and the lane below reads the report the child wrote rather than
the fenced copy.

The person reads it where the child ran. A lane that has stopped folds open on
the report under its own detail line, so the words are read in the transcript
rather than by attaching to a child nobody has a reason to attach to
([`../interface/surfaces.md`](../interface/surfaces.md#the-agent-manager)).
What the lane states without being opened is the count of assumptions the
report lists and, for a review, the verdict it ends on: the two things a
reader would otherwise have to open every report to find, and the two the
first line almost never carries. Both are read off a shape the child was asked
for. The assumptions are the items under the report's *Assumptions* heading,
and the verdict is its last line, `Verdict: <word>` — the word the review's
task names, or where it names none one of *approve*, *approve with changes*
or *request changes*. The label is what is trusted: an unlabelled last line
counts as a verdict only where it is plainly not a sentence, so a report that
ends on *Ship it.* shows no verdict rather than one the child never drew.

A writing child's report also says what became of its patch and which files
were in it. What a patch touched is the whole of what the parent needs to
integrate it, and it is already known at the moment the patch lands; a note
that gave only a count sent the parent looking for the names, one round after
the files were already in the checkout. A patch over a very long list of files
keeps the count and drops the names past the first twenty, which is where a
list stops being read and starts being summarised.

## A child inherits its scope, not more

A writer sees its own working copy plus whatever the parent has already been
granted. Spawning is not an escape hatch: a child cannot reach somewhere the
parent could not.

## Limits are about attention, not resources

Concurrency and total spawns are capped. The binding constraint is that a
person can only follow so many things at once — a session with a dozen live
children is one where nobody knows what is happening, regardless of what the
machine could sustain.

The roster says how much of that is gone, because the alternative is finding
out by having a spawn refused: a round spent, on a plan for a fan-out that was
never going to fit. A finished agent keeps its slot — the limit is on how many
one session may start, not on how many run at once — which is the half a
reader assumes the other way round, so the line says it. It is also why a
failed child is retried rather than replaced: a replacement costs a slot the
session may not have.

The person is shown the same number the model is. The agent manager's header
carries `4 of 32 spawned` beside its tally, and a fan-out's header carries it
while any of the batch is still working — the moment the next spawn is being
planned. The count never goes down, because a finished child keeps its slot,
and that is the half of the limit the number is there to say.

The cap is thirty-two starts by default, and `max_children` in the `[agents]`
table of `config.toml` moves it. A finished child costs no attention, so the
figure is set by what one session legitimately starts rather than by what one
person can watch: a batch run of the backlog starts one child per item, and a
measured run started thirty-one in one sitting of under two hours. Sixteen
stopped that run at its sixteenth spawn with fifteen ready items still queued.
A spawn past the cap is refused with the count of starts and says it is one,
because `32 of 32` read with three children live looks like a concurrency
limit, and that is `max_concurrent`'s. The refusal names `agents.max_children`
the way the depth refusal names `agents.max_depth`: it is a limit on attention
you can raise, so the refusal says what to change rather than that you
cannot.

Children run without a round budget by default, because a child has nobody to
ask when it reaches a checkpoint. The parent is the one with a human attached.

A child's token budget is the same kind of limit. It counts new tokens — the
part of each request's prompt the provider did not serve from its cache, plus
what the child wrote — so it bounds what the child has taken in rather than
what it cost. A cached prompt is a fraction of the price and nothing the child
has newly read, and counting it charged a child again for its own standing
context on every round, which ended runs that had barely started: writers
measured on one backlog item each read 65 to 80 million cached tokens against
about a million new. What a child costs is counted too, in the session's spend
ledger and against its cap.

A normal child defaults to 1,200,000 tokens, which is what one writer spent on
one backlog item: four such children, summed over their requests as cache
creation plus fresh input plus output, came to between 0.7 and 1.2 million new
tokens each. A default under that stops a child a quarter of the way through
its item and its doubled retry short again. No spawn is given more than
2,400,000, twice the default, which is where a retry of a child stopped at the
default lands. A call may name no less than
200,000, but this is not a promise that every 200,000-token call starts: before
a slot, worktree or record row exists, the inherited prompt and tool definitions
plus the declared task — and everything else the first turn opens with: the
evidence a review is given, the bounded context a resumed or retried attempt
carries — are estimated and added to a 200,000-token working reserve. The
requested budget must meet that admission floor. Profiles define role
defaults, so their defaults cannot be below 300,000. The roster and the record
retain the effective budget, admission floor, and the tokens attributed to
inherited context, setup, tool results, analysis, and final handoff; this
separates an insufficient budget from work that was too broad or unproductive.

The standing context a child is given is the parent's, cut to a smaller budget
than the session's. A session reads its instruction files once and holds them
for hours; a child pays for them out of a budget that is the whole of its
life, and every child in a fan-out pays again. Where the files do not fit, a
child gets what any reader over the budget gets: the head of each file and its
end, with a note saying how much of the middle is missing.

## A child may delegate, to a configured depth

A child can spawn a child. A task with parts is the same shape one level down
as it is at the top: the researcher that surveyed four packages wants a
reviewer for what it found, and a writer part-way through a change wants a
second reader on the part it is least sure of. Without this, everything a
child needs done comes back up to the orchestrator and goes out again as a
sibling, which is the parent doing the child's dispatching for it and losing
the context that made the request specific.

Depth counts the session and its agents from one. The session you are typing
at is depth 1, the children it spawns are depth 2, and the children of those
are depth 3. `max_depth` in the `[agents]` table of `config.toml` is the
deepest that may exist, and it is 3 by default — orchestrator, child,
sub-child. A spawn that would open a level past it is refused, naming the
depth it would have been and the key that stopped it, and it is refused where
every other admission refusal happens: before a slot, a worktree or a record
row exists.

Three is the default because the third level is where the useful nesting is —
a review, or a research pass, asked for by the agent that knows what it wants
looked at — and the fourth is where a fan-out stops being something a person
can hold in their head. It is a limit on attention like the others, so it is
a number you can raise rather than a wall.

A descendant is never given more than the agent that spawned it. Its mode is
clamped to its spawner's the way a child's is clamped to the session's, so a
child in plan mode cannot delegate its way out of plan mode. Its role cannot
change more than its spawner may: an agent that changes nothing may delegate
an agent that changes nothing, and a writer may delegate either. A writer's
writing descendant claims its declared paths against every live writer, its
own ancestor included, so the two cannot hand back patches that fight over
the same file. Total spawns and the token budget are the session's, counted
once wherever in the tree they were spent.

### The model a depth runs on

A depth can carry a default model. `[agents.depth.2] model` is what children
run on and `[agents.depth.3] model` what their children run on, which is how
a session says "delegate downwards and get cheaper": the reasoning-heavy pass
at the top, mechanical work below it.

Five layers answer the question, and the first that has an answer wins:

1. the `model` the `spawn_agent` call named,
2. the role's own — the profile file's `model`, or `[agents.profiles.<role>]`,
3. the depth's — `[agents.depth.<n>] model`,
4. `[agents] model`,
5. the session's model.

The role is above the depth on purpose. A profile with a `model` is a role
somebody chose a model for, and it takes that model wherever it runs; a depth
default is what stands for everything nobody chose one for. A depth with no
entry inherits exactly the way a child does today, which is what keeps a
config that has never heard of depth behaving as it did.

### What nesting does to the rest of it

Delegation is one mechanism and it reaches every surface a child already
reaches. The rules below settle each of those, so that a person watching a run
three levels deep is reading one session and not three.

- **Who may steer a grandchild.** An agent steers what it spawned and nothing
  else — the party that wrote the task is the one who knows what it was for —
  so the root does not reach across a level it did not spawn; the person
  steers any agent at any depth by attaching to its lane, as they always have.
- **What killing a parent does.** A kill takes the subtree, and the confirm
  counts it (`Kill writer-1 and 2 agents under it?`): a reviewer under a
  writer whose worktree has just been discarded has nothing left to judge, so
  it ends with its parent under the `cancelled` category rather than being
  left to finish a reading of a tree that is gone. The order is deepest
  first, and the killed writer's worktree is removed only once the agents
  reading it have ended — a copy of the checkout deleted underneath a
  reviewer is a report on a tree that went away mid-read. Only the agent the
  person named ends as `killed`; the ones under it ended because it did, and
  their lane says whose kill they went with.
- **What kill-all means.** Every live agent at every depth — which is what it
  already does, since it walks the flat list of every agent the session has —
  and the manager's key row says so (`[K] kill all · every level`).
- **Whose card a grandchild's request is.** The person's, like every other
  child's, with the lineage in the title (`writer-1 ▸ reviewer-1a ▸ Approve
  command`); a blocked grandchild floats to directly under its own parent's
  row rather than to the top of a flat list, and the waiting tally counts it
  like any other.
- **Whose slots and whose budget.** The thirty-two-per-session spawn cap is the
  session's wherever in the tree a spawn happened, because the attention it
  bounds is one person's; concurrency slots are the depth's, for the reason
  above; and a descendant's fresh tokens count against its own budget and the
  session's spend cap and never against its parent's budget, since a parent
  paying for its descendants would make delegating cost more than doing the work.
- **What the person sees of a level they did not ask for.** The root's fan-out
  block grows a nested lane indented under its parent's, the way the rail's
  map already indents a grandchild, and the parent's own lane says how many
  are under it (`2 agents under it`). The manager indents the same agent the
  same way, and a group moves as one wherever a request floats it: a
  descendant is drawn under the row it hangs off and never lifted out of the
  tree, because a corner under nothing says less than no corner at all.
- **What the notebook says about depth.** A grandchild signs its notes with
  its lineage (`writer-1/reviewer-1a`), so a note read weeks later says which
  run wrote it and under whose task. `/notes` files it under the root of that
  signature, so the notes of a task the session handed out are one group
  however deep the agent that wrote each of them was.
- **What the breadcrumb does at depth three on a narrow rail.** The frame
  keeps the nearest two segments and elides the root (`… ▸ writer-1 ▸
  reviewer-1a`): the far segment is the one the map beside it already draws.

## A wait only ever points down the tree

`agent_report` waits for a child with no timeout, and waiting costs nothing —
no round, no token, no slot beyond the one the waiter already holds. That is
the right shape for a parent collecting a fan-out, and it is also how a tree
of agents deadlocks: three children each holding one of three concurrent
slots, each waiting on a child of its own that cannot start until a slot is
free, wait for each other forever.

The rule that prevents it is that a wait can only ever point downwards, and
nothing a waiter is waiting for can be blocked by the waiter. Two things make
it true:

- **An agent's orchestration tools reach its own descendants and nothing
  else.** A child can report on, steer and retry what it spawned; it cannot
  see its siblings, its parent, or its parent's other children. So every wait
  runs from an agent to something below it in the spawn tree, and a tree has
  no cycles — two agents can never come to wait on each other.
- **Concurrency slots are held per depth.** Each level of the tree has its own
  set of `max_concurrent` slots, so a descendant queues behind other agents at
  its own depth and never behind its own ancestor. The deepest agents running
  are waiting for nothing, so they finish; the level above them then finishes;
  and the wait unwinds from the bottom.

**So `max_concurrent` is a per-level number and the ceiling is higher than
it.** Running agents are bounded per depth rather than in total, which at the
defaults is three at depth 2 and three more at depth 3 — **six agents running
at once, not three**, and the same six however the delegation is arranged.
That is worth saying plainly, because `max_concurrent = 3` reads like a
promise about the whole session and is not one. What bounds the whole session
is the thirty-two total spawns and the spend cap; what `max_concurrent` bounds is
how many things are moving at one level of the same job.

**A wait is on a set, and the first of it to finish ends it.** `agent_report`
takes several names as well as one, and returns as soon as any of them has
finished — that one's report, and a line for each of the others saying where
it stands — so a fan-out is collected in the order it lands rather than in the
order it was named, and the parent can act on the first answer while the
slowest is still reading. A child that finishes while nothing is waiting on it
loses nothing: the next wait that names it returns at once. The deadlock rule
does not change, because every name in the set is still one of the waiter's
own descendants and one outside them refuses the whole call.

**A wait also ends when the waiter is steered.** A person typing into the
session, a client's steer to a served one, or any of the three sources
steering a child that is itself waiting on children — each of those ends the
wait with a first line saying a steer woke it and where each named agent
stands. The message is read at the next round, which is after the tool call,
so without this a redirect would sit behind the slowest child it was probably
meant to change the plan about. The kill and the cancelled turn end a wait as
they always have.

The alternatives were considered and are worse. Making a child's
`agent_report` non-blocking turns a free wait into a poll, and a child paying
rounds and tokens to ask "are you done yet" spends its budget on the question
rather than the work. Lending — a descendant running on the slot its ancestor
holds — reaches exactly the same six, since an ancestor that spawns and does
not wait keeps working while its borrower runs beside it, and it buys that for
a hazard the per-depth pools do not have: the lender can finish and release a
slot the borrower is still standing on, so the bookkeeping that would stop a
fourth agent taking it is bookkeeping whose failure mode is the hang the whole
rule exists to prevent.

## They are visible while they run

Each child appears in the parent's transcript as a status row, and the agent
manager shows what each is doing and how far in. A child's row does not carry
the mutation rail — it is a report, not an act, and the child's own transcript
carries the rails for what it actually did.

A child's approvals route to wherever you are, so detaching to look at
something else does not mean missing a decision.

A child is read and steered by the same machinery a session is, and what that
found reaches the parent rather than only the child's own transcript. Its lane
says how many times this turn it has been told the check reads its work as off
its task, and the roster the orchestrator collects says that too, beside the
last reading's own word for the state of the work. One steer is the mechanism
working. Two is a child that answered the check and carried on, which is worth
knowing forty rounds before its final report says where it went — and the
orchestrator is asked to redirect it then rather than wait for it. Both are the
machinery's own words and counts; nothing a child's tools read reaches the
parent's conversation this way.

Every child is read this way unless you say otherwise, on the same interval
and by the same small model a session's own readings use. A child is the least
supervised thing a session runs — nobody in front of it, no round cap, and a
final report that arrives long after the point where redirecting it would have
helped — so it is the surface the reading was built for. What it costs is the
arithmetic to weigh: a fan-out of six is six more readings per interval, and
`summary.subagents` turns them off for children without touching your own
session's. Turned off, the roster says so in a line above the agents, because
an empty steer column then means nothing is checking rather than nothing is
wrong, and the orchestrator is told what is left to judge a child by — an
agent whose line has not moved between two reads several rounds apart.

Both surfaces also say where the last message the child was given came from —
its own reader, this lane, or the orchestrator — because a count with more
than one possible author leaves the question worth asking of it unanswered.
Where more than one of them has spoken this turn, the lane splits the count
instead of naming a last speaker: it states the total and then how many were
yours, and how many the orchestrator's. Two steers of one author is one fact
and keeps one clause; two of different authors is the case a single number
misreports, because a redirect you gave and a drift the check found are
different news about the same child. What is not named is the check's own
share, which is whatever the named ones leave.

A steer you type at a child's prompt is not delivered where you type it. It
waits for the child's next round boundary — an open stream cannot be
interrupted — so the frame's rail says how many of your sentences are still
waiting, and the lane leaves a row saying which round took each one. Without
the pair, a redirect the child has already read and one it has not yet seen
are the same words on the same screen.

Attaching to a child is not a separate surface. It changes which agent the
session is looking at, and every agent — the root included — is the same kind
of thing. That equivalence is why the interactive surfaces did not need a
second implementation for children.

## How far along is three numbers, not one

"How far along is it" has no single honest answer, because every percentage
needs a denominator and a child has several, none of which is the whole of
the work. A lane, the manager's row and the rail therefore draw the ones that
exist, side by side, and never merge them into one bar: a number with no
denominator is a lie, and a bar that blended two would be a number with no
denominator anybody could name.

The three are these. **Steps** are the child's own: a writer is asked to open
its work with a numbered plan of the steps it means to take, and to write a
progress line naming a step's number as each one is finished. The plan is read
in the same grammar the plan card reads a planning answer in, so a step is the
same thing on both surfaces, and a lane reads `3 of 7 steps` with the step the
child is on beside its task where the row has room. **The budget's share** is
what the child has taken in against what it was given, `41% of budget` — the
one denominator nobody has to declare. **The reader's verdict** is the last
reading of the child's work, which says whether the steps it is marking are the
ones it was asked for.

The steps are the one worth asking for, because the child names them itself
and so can be held to them. The reading is handed the count beside the files
the child has changed, and judges the work against what the child said it
would do as well as against the task; every check-in names the step the child
last said it was on, so a child asked to take stock answers about its own plan
rather than describing its work in new words. The mark is a line the child
writes in its own messages and never a tool: a count the model keeps is a
record it could edit freely, and this is one it should write, in the same
breath as the work it is counting.

A plan is taken once, from the message a writer opens its work with, and a
numbered list later on — a report listing what changed — is not mistaken for
one. A plan that does not parse, or one too long to be a plan, is no plan: the
lane then draws the budget's share with whatever count the spawn declared, and
never zero of zero. A retry names its own plan, since it is a fresh
conversation.

The count also decides when a landed patch is carried into a writer's copy
([a writer starts from your tree](#a-writer-starts-from-your-tree)). A writer on
the last step of its own plan is finishing the change its patch will carry, and
moving the tree under it then is the collision the reseed exists to avoid, at
the moment it costs most; it is left alone until it has reported, and its own
landing meets what landed meanwhile.

## Three can steer a child, and none of them can end it

A child is given words by its own reader, by you — at its lane, or on its row
in the manager without attaching at all — and by the orchestrator that wrote
its task. All three arrive the same way — as a message in front of the child
at its next round boundary — and all three have the same
consequences for the turn they land in: what the child is judged against grows
to include them, the reading that was in flight is dropped rather than argued
with, and the reckoning of how often this child has ignored its check starts
again. That is why a redirect is safe to give: the child is not then told it
has drifted for doing what it was just asked to do.

The orchestrator may speak because it wrote the task and is the only party
besides you who knows what the child was for. The alternative is that it tells
you instead — a message you read some rounds later, about a child you were not
watching, which spends exactly the attention a fan-out exists to save. What
the orchestrator gains is only the right to speak: the path its words travel
is the one your own typing takes, and nothing a child reads can reach that
path. A child's tool output cannot become a steer of any kind.

One of your own hooks can speak for you too, once: at a child's end, a
`subagent_stop` hook that does not take the answer as the end sends the child
back to work by the same path, named on the lane as a hook's rather than as
something you typed
([`hooks.md`](hooks.md#a-child-starts-and-ends-at-a-seam)). It is your rule,
written before the run, and like the other three it can redirect a child and
cannot end one.

And a writer can be told something by a landing: when another writer's patch
lands in your checkout and will not carry into this writer's copy over its own
work, the writer is told which files landed and which the two met on
([a writer starts from your tree](#a-writer-starts-from-your-tree)). The lane,
the roster and the record name it as the landing's, because it is nobody's
message — the machinery wrote it from what landed — and like the reading's
it is words and nothing more: it changes neither the task nor what the child
may touch.

Ending a child is yours alone, from the manager, which is where the keys that
act on one child are. It has one name there and no second one anywhere else: a
command that spelled it while attached spelled it with the word that quits the
whole session everywhere else in the product. It is not something the
orchestrator is offered, and the reason is not symmetry: a writer stopped
part-way leaves an isolated copy of the workspace holding an unfinished change
that nobody has judged, and the party that would be stopping it is the one
whose only evidence is a roster line. A run that stops a child on that reading
sometimes stops one doing exactly what it was asked to do, with nobody there
to disagree. A redirect is cheap to be wrong about — the child reads it and
carries on — and a stop is not.

A child that has answered can be spoken to again, by the same three and by
the same path. There the message is a follow-up: one more turn on the
child's own conversation, with everything it read to write its report still
in front of it, and its answer replaces the report it gave — which is kept,
folded above the new one under the turn it closed. While it can still be
asked, a finished child keeps its name, its claimed paths, its conversation
and, for a writer, its copy of the workspace, with what already landed as
that copy's base so the next patch is only the new work; it gives up its
place among the children running at once, and takes one again for the
follow-up. It lets go of the rest when the session ends, or when an agent
above it is killed. The follow-up spends what is left of the same budget, so
a child whose remainder is below the working reserve a turn is admitted with
refuses it and says how much is left. The point is the reading: a question
for a researcher that has just reported is answered from the ground it
already covered, where a second spawn would pay to cover it again.

A follow-up is not a retry. A retry is for a child that failed, and it is a
new conversation that opens on how the last one ended; a follow-up is for a
child that finished, and it is the same conversation carrying on.

## What they share

A child cannot see the conversation it was spawned from — unless the spawn
hands it the last few turns, below — and the parent only receives its final
message. That is the right contract for one task and the
wrong one for a fan-out: four children sent into the same unfamiliar tree
will each work out where the tests live, and three of those readings are
paid for twice.

The session's notebook is the shared channel, and it is a file rather than a
runtime — there is no messaging between children, no mailbox and no lead.
Any agent in the session, the parent and every child, can write a short
titled note and read the notes that exist. A child is spawned knowing what
the notebook already holds — the titles, not the bodies, so a long session's
notebook does not ride in every child's prompt — and reads the ones it wants
in full. Notes are signed with the author's name and stamped with the turn
they were written in, so what came back from one fan-out can be told from
what came back from the next, and the parent's turn closes by saying how many
notes its children left and who wrote them.

A child may add and read; it may never remove. There is no delete tool for
any agent in any mode: without that line a child could quietly unmake a
sibling's finding, and the parent would read a notebook that looks complete.
Dropping a note is the person's, through `/notes`, which opens the notebook as
a screen grouped by the agent that wrote each entry
([the supporting screens](../interface/surfaces.md#the-supporting-screens)):
the note under the pointer is read whole with `[enter]` and dropped with
`[d]`, behind a confirm, and clearing the notebook asks over the whole of it
first. The close of the parent's turn says how many notes are still unread —
written since that screen was last opened — beside how many its children
left and who wrote them.

A writer child works in an isolated copy of the checkout and still writes
into the parent's notebook, because the notebook belongs to the session and
not to a tree. That is not a way into your checkout: a note is prose with the
standing of an instruction file — it can ask, and nothing in it runs. No
approval card consults it, no route into the checkout reads it, and a child's
patch is approved exactly as it was before.

A note is not a report. The report is what the child owes its parent — the
findings, the evidence, the verdict — and it comes back to the parent
unchanged. A note is what a sibling will need. And a note is not memory
(`sessions-and-memory.md`): memory is durable, general, and confirmed by the
person before it is kept; a note is working state, and its lifetime is the
session's.

A spawn may also hand the child the last few turns of the conversation it was
spawned from. The notebook and the inheritance are for different things. A
note is what a sibling will need, written once and read by whoever asks for
it; inherited turns are for a subtask of the work in hand — a review of the
change the parent just made, a check of a conclusion it just reached — where
a briefing the parent writes from its turns would hand the child the parent's
conclusions, and the child needs what the parent read. The turns arrive ahead
of the task under a line saying they are the parent's and not the child's:
what the person and the assistant said, whole; each tool result elided the
way the window trim elides one, to a placeholder naming the id the original
can be read back by; and all of it through the session's scrub, so a secret
the parent's conversation held never reaches the child's. The child's prompt
says which turns it was given and that the rest is not there.

The count is of turns, not of bytes. A turn is what a person can count on the
transcript — the turn they asked the question in, and the one before — and a
byte bound would be a number nobody can see. The byte bound is still there,
underneath: the inherited turns are part of what the child is admitted for,
beside its task and a review's evidence, so a spawn whose turns would not
leave the working reserve is refused before a slot, a worktree or a record is
taken, and the refusal names the turns among what it counted. A retry hands
the second attempt the same turns the first was spawned with, ahead of its
handoff — the text as it was, not a fresh read of a conversation that has
moved on since. A profile may carry a default count, and a spawn may lower it
to nothing.

The default stays at nothing. A child spawned on its task alone is cheaper,
and the task-only contract is what makes the parent say what it wants: a
child handed the conversation is handed the parent's framing with it,
detours included, and pays for every turn of it out of its own budget before
it has done anything. A wide, independent hunt — a survey, a search the
parent has not started — gains nothing from the turns, and that is what most
spawns are.

Handing turns over does not promise that the provider's prompt cache is
shared. A cached prefix is matched from the first byte of a request, and a
child's request opens on its own system prompt and its own tools, not the
parent's; whether any of the inherited text is served from a cache the
parent's requests wrote depends on the provider and the model, and has not
been measured. What the turns are known to cost is what the admission floor
counts.

The machine is shared too, and it is the one thing a fan-out can run out of
without any agent noticing. Five writers that each finish an edit and run the
tests start five builds and five test runs at once, and a host at five times
its load produces the timeouts and races each writer then spends rounds
chasing — five writers slower than one. So the session's checks take turns:
`agents.check_slots` (two by default) is how many may run at once across the
whole session. A child's quality gate run takes a slot, a child's command
takes one when it is one of the checks the project's quality config declares
or one of a short list of builds and test runs (`go test`, `go build`, `make
test` and their like), and the session's own gate takes one too, so your
`/gate run` and a child's never load the machine together. A check that finds
every slot held waits its turn in the order it asked; the child is parked in
front of it, not failed and not polling, and its lane and its row on the rail
say `waiting for a check slot (2 running)` until a slot comes back. A writer's
prompt says a check may wait and that the wait is not a failure, so it does
not retry, cancel or skip the check because it was slow to start. A command
that is not a check — a read, a `git status`, a formatter — never waits.

The checks share a build cache as well. Every contained command of the
session that builds Go — each check the gate runs and each command a child
runs — points `GOCACHE` at one directory in the session's own scratch under
the state directory, so five copies of the tree compile the standard library
once between them rather than five times. The cache is a cache: Go keys every
entry by its inputs, and a test that passes only because of what is in it is
not a test the project has, so nothing a check concludes depends on it. A
command that runs uncontained uses the machine's own cache, which is already
one directory. The directory goes when the session's scratch does.

## A hold reaches the whole fan-out

Holding the session's own turn holds every child with it. Nothing stops where
it stands: each child finishes the round it is in and waits at its own
boundary, which is the only place a turn can be stopped without abandoning a
request the provider is still answering — so a fan-out of four parks four
times, at four different moments, and the rail says which of them have got
there.

One press lets them all go. The hold was asked of the session rather than of
a child, and letting them out one at a time would be a list nobody could be
expected to keep. A held child is still a running one — it keeps its slot, its
worktree and its conversation — and killing it still works, because a child
nobody is coming back to must not sit in its worktree waiting for a release
that is never sent.

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

The allowlist narrows the tiers and nothing else. It names file tools,
commands and the web — the things a tier decides — along with the quality
gate, and never the navigation tools, the notebook or the skills catalog,
which every child gets whatever its profile says: those are how a child reads,
and a profile that could take them away would be a profile that made its agent
worse at the one thing every agent does.

A profile can make its children stricter than the session — a reviewer that
starts in read-only mode under an auto session — and never looser. The clamp that
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

A profile edited from the [agent manager](../interface/surfaces.md#the-agent-manager)
is the running session's, not the next one's: the file is read again when the
editor closes, through the same registration a drafted profile ends on, and
the spawn tool the model holds is rebuilt with it — its roles and what each is
for — so the model is told the role as the file now reads before it asks for
one. A file that no longer loads mid-session does not stop anything: the role
stays as it was and the refusal is said on the way back, because the session
already has a working role and losing it to a half-finished edit would be the
quiet disappearance the rule above exists to prevent. A conversation reads an
edited profile on its own terms too: an edit that grants a tier that writes is
refused there, as it would have been left out at the start.

## A profile that changes nothing can still run the checks

A role that must never run an arbitrary command still has to be able to say
whether a change compiles and its tests pass. Granting it `execute` for that
one answer grants it every other command the session's execute tier allows,
and the distinction the role was drawn around is gone.

So the quality gate is a read. The tool picks a suite by name out of the
checkout's trusted config and can supply no command text at all — the closed
set of checks the project declared about itself, not a shell — so a profile
granting only `read` is offered it, and a profile that narrows its tools may
name it among them. What runs was settled by the person who trusted the
checkout, before any agent existed to ask; that is why it is not the execute
tier's concern.

It arrives on the same terms the session has it on. A checkout nobody has
trusted opens no gate for anyone, so a child is offered none either — the tool
is absent rather than present and refusing, which is the difference between a
capability this checkout does not have and a permission the profile was
denied. A trusted checkout with no suites defined answers a call the way it
answers the session's: no suite is configured, and where to define one.

A profile that writes does not get it, whatever it lists. A writer works in an
isolated copy of the checkout and the gate runs over the checkout that copy
was made from, so the verdict coming back would describe a tree with none of
the writer's changes in it — a pass it did not earn. That is refused when the
profile is read, not silently dropped when the child is spawned.

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
it is told to value is not the same. In a chat, a profile is a
standpoint, a voice, a way of citing, and never a way of acting — the
drafter is told so and the result is checked, so a chat profile cannot come
out able to write. A draft that grants a writing tier or names a tool that
needs one is tidied rather than refused: the tier and the tool are taken
off, and the card names the tools it dropped. In a coding session, a profile is an engineer with one
job: what it changes, how it verifies, what its patch may contain. A single
drafter hedging between the two would draft a profile that hedges too.

How a coding role verifies decides what it is granted, so the drafter is told
the rule above: running the project's own suite is `quality_gate`, which the
`read` tier already grants, and `execute` is for a role that must run
arbitrary commands. A reviewer-shaped role that checks the work and changes
nothing is therefore drafted with no tiers at all, naming the gate among its
tools where it narrows them. Naming the gate beside `write` or `execute` is
refused on the same grounds as the loader's, while the draft is still a card
that can be revised — before a file exists, rather than at the next session's
start.

Where the file lives follows from what it is. A coding agent's profile can
belong to the work: the project's own `.shhh/agents/`, which travels with
the repository, is read only by coding sessions, and shadows a global
profile of the same name — or the config directory's `agents/`, which every
session has. A chat profile is the person's, not any project's, so chat
reads and writes only the global directory. A project's directory is never
assumed to be committed, and it is not read at all until the checkout has
been trusted: a profile carries a permission set, a tool allowlist and a
prompt, so a clone that could add one would be a clone deciding what a
spawned agent may do
([`approvals-and-safety.md`](approvals-and-safety.md#a-checkout-declares-what-it-runs)).
A drafted profile is spawnable in the session that drafted it — a profile you
made for this conversation should not need a restart to join it.

The drafting happens on [a surface of its
own](../interface/surfaces.md#the-profile-drafter), because a conversation
with three steps in it needs somewhere to say which step it is on. The
questions are asked one at a time and the way back through them is the same
key that leaves: a mistyped answer costs an answer, not the drafting.

## A failed child leaves a handoff

A child that ends after it has begun — because it spent its token budget, its
provider failed, its context was cancelled, a cap could not continue, or its
own runtime failed — leaves a durable handoff before it releases its slot or,
for a writer, its isolated copy. A completed child keeps its ordinary final
report. A failed child’s report names the failure handoff and the replacement
budget the parent should use; the parent transcript and the session record
carry the same closed failure category.

The handoff preserves the original task and declared paths, effective budget,
phase spend, end category and detail, completed round, paths read and written,
completed tool outcomes, public progress notes, and opaque evidence handles.
It is not a transcript: private provider reasoning, partial instructions from
tools, and raw tool output do not cross to another child. A replacement calls
`spawn_agent` with `resume_handoff`; its supplied task and scope are replaced
by the immutable originals, and it receives only a bounded context built from
that record. It starts with the unresolved action rather than surveying the
repository again. That context is counted into the replacement's admission
floor: a budget the handoff alone would exhaust is refused before a slot, copy
or row exists, and the handoff it could not start on stays resumable. An
unavailable, malformed, or expired handoff is refused, because silently
starting without its evidence would look like a resume while paying for the
same work twice.

A writer's work that did not reach your checkout has one fate, however the
writer ended. Its budget ran out, it was killed or cancelled, you declined its
patch, or the patch would not apply: in each case the patch is kept in the
session's evidence store — through the same secrets scrub as every copy that
outlives a turn — under an opaque handle, before its isolated copy is removed.
The copy goes either way; the patch is what is kept. The agent manager's row
and the rail's line under that writer say `patch kept · [p] review`, and `[p]`
opens the patch full screen with the card a finishing writer's patch is put on
behind it: apply or decline, with the same overlap warning and the same record
of which agent's patch changed which file. Declining it leaves it kept. A kill
confirm says the patch will be kept when there is one to keep.

The handoff names that handle rather than carrying the patch a second time, and
the patch stays outside the replacement instruction: a valid handle is all a
replacement can receive. Retrying the writer is asking for the work again, so
the attempt it replaces stops offering its patch; the handle stays in the
store. A reader has no copy or patch to preserve. In every case the failed
child has released its concurrent slot before the failure is reported, so kept
work never blocks the fan-out.

## A failed child can be run again

Retry re-runs a child on its original task rather than asking the parent to
reconstruct what it was doing. The child keeps its name, its place in the
fan-out, its declared paths and its slot, so recovering a failed agent costs
nothing that spawning a replacement would.

The new attempt is a new conversation — one that inherited the context that
killed the last one would die of it again — but new is not blind. It opens
with how the previous attempt ended and whatever handoff that one wrote on its
way out, and then the task, unchanged and named as such. A child spawned with
the parent's last turns is handed the same turns again, ahead of all of it
([what they share](#what-they-share)). Without that a retry
is the same attempt run twice: it takes the same first steps, and one that ran
out of budget spends the same budget the same way and stops in the same place.
The handoff is already written — a child stopped by its budget is asked for
one on its way out — and carrying it costs nothing.

A child stopped by its budget is also given a larger one, twice what it had, up
to the ceiling any spawn is clamped to. It is the same escalation a child
applies to its own round limit when it stops to take stock, and for the same
reason: the second attempt at a task that proved too big for the budget is not
worth making on that budget. A child that failed for any other reason was not
short of attention and gets what it had.

Both you and the orchestrator can retry. Yours is the key on the child's row
in the manager; the orchestrator has a verb of its own, so an unattended run —
a backlog step, a fan-out nobody is watching — can recover a failed child
instead of stopping on it. It is not put to a card, unlike a spawn: no agent
is started, the task is the one already approved, and the slot is one already
spent. What it spends again is the child's budget, which the session's own cap
counts. Ending a child stays yours alone.

A child that has just failed is often still stopping — a writer's isolated
copy of the workspace is a directory that has to be taken away, and on a busy
machine that is not instant. The retry waits for it and says so on the lane
rather than refusing: the offer appears the moment the child fails, and a
person pressing it then has no way of knowing that the previous attempt is
still letting go. The wait is bounded, because a teardown that never finishes
must not leave a child queued behind it forever; past the bound the child is
failed again, with the same offer still standing.

A writer's retry is asked the question its spawn was asked. It comes back
minutes later, and a writer spawned in the meantime may now hold the files it
claims; two live writers over one file is what refusing an overlapping spawn
exists to prevent, and a retry is not a way round it. So the retry is refused
naming the writer in its way, or — where the spawn asked to wait for a claim —
queued behind that writer as the first attempt would have been, holding no
slot and no copy of the tree until the claim is released
([a writer starts from your tree](#a-writer-starts-from-your-tree)).

Which of the two a caller wants follows from how the child ended. A failed
child is retried: its conversation is what failed, so the second attempt
starts a new one and does not accept a steer. A finished child is sent a
follow-up
([three can steer a child](#three-can-steer-a-child-and-none-of-them-can-end-it)):
its conversation is what is worth keeping, and a retry of work that
succeeded would throw it away.

## Related

- [`coding-agent.md`](coding-agent.md) — the parent
- [`containment.md`](containment.md) — what scope a child inherits
- [`configuration.md`](configuration.md) — where the profile files live
- [`../agents/README.md`](../agents/README.md) — the profile file format and examples
- [`../interface/surfaces.md`](../interface/surfaces.md) — the agent manager
