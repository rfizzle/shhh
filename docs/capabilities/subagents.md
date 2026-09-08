# Sub-agents

A session can hand part of a job to a child agent. Children are how a large
task gets parallelism and a clean context without the parent losing track of
what is happening.

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
since answered, and there is nowhere else for the failure to go.

This changes what a child starts from and nothing about what comes back.
Approval is still the only way anything reaches your checkout, the lane says
how many of your files the child started from, and a checkout with nothing
uncommitted in it starts a child exactly where it always did.

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

## A child answers to the session

A child's permission mode is clamped to its parent's and can never be looser,
and the parent's session grants travel with it: what you waved through for the
session is waved through for its children, so one grant is not re-asked once
per agent. A profile may start a child stricter — a reviewer in plan mode
under an auto session — and that is the only direction the clamp allows.

What a child cannot do is ask somebody the session cannot reach. In a session
its request becomes a card in front of you. Where there is nobody — a scripted
run, or a served session whose protocol draws cards for the turn it is running
and has no vocabulary for a child's — the answer to the spawn stands as the
answer to the child, and a request the child's own policy still stopped to ask
about is refused. A patch is the exception, and only where the run may write:
the run asked for the work, so what is left to refuse is a patch that overlaps
one already landed.

## What comes back says what happened to it

A child's report is the child's own words, and around them is everything the
session already knows about how it got there: how it ended, what it spent, how
many times it stopped to take stock, the last reading of its work and how
often it had to be redirected. Those last two are on the roster as well, and
they are on the report because the report is what the parent acts on — a child
steered three times that comes back calling its work sufficient is one to
check rather than to take, and nothing in the words themselves would say so.

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

Children run without a round budget by default, because a child has nobody to
ask when it reaches a checkpoint. The parent is the one with a human attached.

A child's token budget is the same kind of limit. It counts new tokens — the
part of each request's prompt the provider did not serve from its cache, plus
what the child wrote — so it bounds what the child has taken in rather than
what it cost. A cached prompt is a fraction of the price and nothing the child
has newly read, and counting it charged a child again for its own standing
context on every round, which ended runs that had barely started. What a child
costs is counted too, in the session's spend ledger and against its cap.

The standing context a child is given is the parent's, cut to a smaller budget
than the session's. A session reads its instruction files once and holds them
for hours; a child pays for them out of a budget that is the whole of its
life, and every child in a fan-out pays again. Where the files do not fit, a
child gets what any reader over the budget gets: the head of each file and its
end, with a note saying how much of the middle is missing.

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

Attaching to a child is not a separate surface. It changes which agent the
session is looking at, and every agent — the root included — is the same kind
of thing. That equivalence is why the interactive surfaces did not need a
second implementation for children.

## Three can steer a child, and none of them can end it

A child is given words by its own reader, by you at its lane, and by the
orchestrator that wrote its task. All three arrive the same way — as a message
in front of the child at its next round boundary — and all three have the same
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

Ending a child is yours alone, from its lane. It is not something the
orchestrator is offered, and the reason is not symmetry: a writer stopped
part-way leaves an isolated copy of the workspace holding an unfinished change
that nobody has judged, and the party that would be stopping it is the one
whose only evidence is a roster line. A run that stops a child on that reading
sometimes stops one doing exactly what it was asked to do, with nobody there
to disagree. A redirect is cheap to be wrong about — the child reads it and
carries on — and a stop is not.

## What they share

A child cannot see the conversation it was spawned from, and the parent only
receives its final message. That is the right contract for one task and the
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
Dropping a note is the person's, through `/notes`, which lists the notebook
grouped by the agent that wrote each entry.

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
commands and the web — the things a tier decides — and never the navigation
tools, the notebook or the skills catalog, which every child gets whatever its
profile says: those are how a child reads, and a profile that could take them
away would be a profile that made its agent worse at the one thing every agent
does.

A profile can make its children stricter than the session — a reviewer that
starts in plan mode under an auto session — and never looser. The clamp that
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
it is told to value is not the same. In a chat, a profile is a colleague: a
standpoint, a voice, a way of citing, and never a way of acting — the
drafter is told so and the result is checked, so a chat persona cannot come
out able to write. In a coding session, a profile is an engineer with one
job: what it changes, how it verifies, what its patch may contain. A single
drafter hedging between the two would draft a persona that hedges too.

Where the file lives follows from what it is. A coding agent's profile can
belong to the work: the project's own `.shhh/agents/`, which travels with
the repository, is read only by coding sessions, and shadows a global
profile of the same name — or the config directory's `agents/`, which every
session has. A chat persona is the person's, not any project's, so chat
reads and writes only the global directory. A project's directory is never
assumed to be committed, and it is not read at all until the checkout has
been trusted: a profile carries a permission set, a tool allowlist and a
prompt, so a clone that could add one would be a clone deciding what a
spawned agent may do
([`approvals-and-safety.md`](approvals-and-safety.md#a-checkout-declares-what-it-runs)).
A drafted profile is spawnable in the session that drafted it — a persona you
made for this conversation should not need a restart to join it.

The drafting happens on [a surface of its
own](../interface/surfaces.md#the-profile-drafter), because a conversation
with three steps in it needs somewhere to say which step it is on. The
questions are asked one at a time and the way back through them is the same
key that leaves: a mistyped answer costs an answer, not the drafting.

## A failed child can be run again

Retry re-runs a child on its original task rather than asking the parent to
reconstruct what it was doing. The child keeps its name, its place in the
fan-out, its declared paths and its slot, so recovering a failed agent costs
nothing that spawning a replacement would.

The new attempt is a new conversation — one that inherited the context that
killed the last one would die of it again — but new is not blind. It opens
with how the previous attempt ended and whatever handoff that one wrote on its
way out, and then the task, unchanged and named as such. Without that a retry
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

Both you and the orchestrator can retry. Yours is the key on the child's lane;
the orchestrator has a verb of its own, so an unattended run — a backlog step,
a fan-out nobody is watching — can recover a failed child instead of stopping
on it. It is not put to a card, unlike a spawn: no agent is started, the task
is the one already approved, and the slot is one already spent. What it spends
again is the child's budget, which the session's own cap counts. Ending a child
stays yours alone.

A child that has just failed is often still stopping — a writer's isolated
copy of the workspace is a directory that has to be taken away, and on a busy
machine that is not instant. The retry waits for it and says so on the lane
rather than refusing: the offer appears the moment the child fails, and a
person pressing it then has no way of knowing that the previous attempt is
still letting go. The wait is bounded, because a teardown that never finishes
must not leave a child queued behind it forever; past the bound the child is
failed again, with the same offer still standing.

## Related

- [`coding-agent.md`](coding-agent.md) — the parent
- [`containment.md`](containment.md) — what scope a child inherits
- [`configuration.md`](configuration.md) — where the profile files live
- [`../agents/README.md`](../agents/README.md) — the profile file format and examples
- [`../interface/surfaces.md`](../interface/surfaces.md) — the agent manager
