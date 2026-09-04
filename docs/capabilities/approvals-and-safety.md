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

## The classifier fails closed

Auto mode's classifier never approves on error. A timeout, a malformed answer,
an unreachable provider — each falls back to asking the human. There is no
path through the code where "we could not decide" becomes "yes".

This is worth stating as a commitment because the opposite is the natural way
to write it. A classifier that returns a boolean gets a zero value, and the
zero value has to be the one that costs nothing.

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

**A conversation that comes back does not come back with its reading.** The
transcript says which files were read; nothing on the machine says what they
held, and they have had however long the conversation was closed to move. So
reopening one records each of those files as read-with-unknown-content: the
first change to one is refused and costs a round, against an edit applied to a
picture nobody can vouch for. A file the transcript never read keeps the
ordinary rule, because a quoted snippet is its own evidence. This is what
reopening means at either door — a session resumed from the command line and a
saved conversation loaded over the one on screen. Loading empties the record
first, because what the conversation being replaced was shown is no evidence
about the one arriving. Starting a new conversation empties it and stops
there: a conversation that has said nothing yet has read nothing.

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
