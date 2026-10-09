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

## One classifier names a call's tier

Four surfaces put a call to a decision: the session's approval card, a
scripted run, a session a client drives over the protocol, and a child
answering to its parent. Each of them used to work out for itself which tier a
call sat at, and four copies of that reading agree on the day they are
written. The next tool that has to be answered for is added to whichever copy
the author was looking at, and on the other three it runs as something it is
not, with nothing going red. So there is one classifier, beside the policy it
feeds, and the four surfaces are its callers.

It is asked three things: the tool's name, the call's arguments, and whether
the surface asking has an answer for that tool at all — what it registered,
and the cards or rules it can answer one with. It returns the tier, the
action the policy is asked about, and whether the call is put to a decision.

- **Read** runs without one. It is dispatched by the path that has no case for
  anything else, which is what makes the tier safe to reach by default: a name
  nothing recognises lands there and is answered as a tool that does not
  exist.
- **Write** changes the tree: the two file tools and the writing half of git.
  It is answered the way an edit is — it applies where an edit applies, asks
  where an edit asks, and the read-only modes refuse it — and it carries the
  command line it stands for, so the deny list reads the act whatever tool
  spells it.
- **Command** runs a program or reaches past the tree: a command, a process
  start, a fetch, and anything else a surface registered as needing an answer
  — a server's tool, a child, a memory, a question. Each is answered by its
  own kind: a command by the allowlist and the safety table, a fetch by its
  host, the rest by being asked. An edit grant answers none of them.

A name the classifier does not know, on a surface that has an answer for it,
sits at the command tier as the strictest kind there is: asked in every mode
that asks, refused in the two that write nothing. A surface that has no answer
for a name does not put it to a decision at all. The tier is a property of the
call and never of the surface, so no surface can resolve a write below write
or a command as an edit.

What a surface still decides is everything that is its own:

- **Which tools it has.** A conversation registers no runner, a child holds
  only its role's tools, and a fetch exists only where a fetcher does; that
  answer is the surface's and is handed to the classifier rather than
  re-derived by it.
- **How it asks.** The card, the run's flags and its judge, the client, the
  parent: each surface keeps its own answerer, and the standing refusals in
  front of it — containment, the deny lists, the safety table, the working
  scope — are asked in the order they always were.
- **What its card reads.** The path an edit names and the host a fetch leaves
  for are read by the surface's own previewer, because they depend on what
  the session has read and on its fetcher's policy, and a person can amend a
  command on the card after it was classified.
- **More, never less.** A surface may hold a call it could have let through;
  it may not let through one the classifier put to a decision.

What each site gives up:

- **The approval card** no longer decides which calls join its queue or what
  tier a generic card sits at. A tool's preview used to declare that it was a
  write; it now states only what the card shows, and the tier comes from the
  name.
- **The scripted run** no longer reads a call's tier off a chain of names. It
  keeps its flags, its judge and its runners.
- **The served session** no longer holds its own copy of which calls stop for
  the client. The gate it shares with the scripted run asks the classifier,
  and what the client is asked, and what a decline says, is unchanged.
- **A child** no longer classifies its own calls. It keeps rooting them in its
  worktree, reading what a command reaches against its scope, and its route to
  the parent's card.

The model is told nothing new. Every refusal it reads and every paragraph in
its prompt are the sentences they were.

## The five modes

How much has been decided in advance:

- **Manual** — every command and every write is asked.
- **Accept-edits** — writes proceed, commands are asked. This is the mode for
  work where the edits are the point and you will review them at the end.
- **Auto** — a classifier decides, and asks when it is not sure.
- **Read-only** — nothing is written. Reading, searching and the inspection
  commands run as they do in every mode; a file edit and any other command are
  refused rather than asked about, because there is no answer that would let
  one through.
- **Plan** — read-only, and it ends on something: the session proposes an
  ordered list of what it would do, and you approve the plan rather than the
  steps.

The last two are one policy and two activities, which is why they are two
modes. Read-only is a bound on the session and nothing more — it is the mode
for a question you want answered without the answer being acted on, and it
does not ask for a plan or wait for one. Plan mode is the bound plus a piece
of work: deciding whether the approach is right, before any of it is worth
approving individually, and finishing with [a plan you can start a fresh
session from](coding-agent.md#an-approved-plan-is-an-artifact).

Neither is a safety mode with the volume turned up. A mode that refuses is not
a stricter manual: manual asks, and being asked is how work gets through.

A conversation has none of the five. Its toolset is the bound, so the one
policy it runs in is fixed, and it reads the web without a card
([`chat.md`](chat.md#a-conversation-has-one-mode)).

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
allowlist refuses to match a line carrying shell punctuation the shell would
act on, because a prefix it could be talked into misreading would be a grant; over-reading
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

The standing rules are one assembly, and every door a gated call comes
through asks it. There are two doors. The session's screen puts what it
cannot answer to a person on a card; a run with nobody in front of it — a
scripted run, a served session — answers from its flags, its judge, or a
refusal. Both ask the same rules first: the two deny lists, what a command
destroys that no session may, the containment requirement, and what a call
reaches behind the containment's deny mask. A rule added for one door is
therefore answered at the other. What each door answers once the rules have had
their say stays its own: grants, the mode and the card on the screen;
`--yes`, `--allow`, the judge and the refusal where nobody can be asked on
the other. Nothing the assembly holds can allow a call — it only refuses —
so sharing it gives neither door a yes it did not already have.

### The allowlist reads quotes the way the shell does

An allowlist entry, and the inspection list the two read-only modes run, is a
prefix, and the one thing a prefix must never do is carry a second command:
`git status; rm -rf ~` begins with `git status`. So the guard refuses a line
in which the shell would act on any of `;`, `&`, `|`, `<`, `>`, `(`, `)`, `$`
or a backtick — a chain, a pipe, a redirect, a subshell or a substitution.

It reads the line's quoting as the shell will. Inside single quotes every
character is text. Inside double quotes the punctuation is text too, except
`$` and the backtick, which still substitute there and are refused. Outside
quotes a backslash makes the next character text, which is also how a quote
character can stand outside any quoted part. So `go list -f
'{{.ImportPath}}|{{len .GoFiles}}' ./...` is a `go list`, because the shell
never sees that pipe, while `git status "$(rm -rf ~)"`, `` git status `id` ``
and `cat a | sh` match nothing. A line whose quote never closes matches
nothing, and neither does a line that runs past a newline, quoted or not: a
`#` in front of the quote makes it a comment and the next line a command, and
telling the two apart is not worth the prompt it would save. The reading is
for the POSIX shell commands run through; on Windows, whose shells quote
otherwise, any of the punctuation anywhere still refuses the line.

The deny list is not given the same leniency, and that is the agreement
rather than a break in it. A line the allowlist reads as one command, the deny
list reads that command in too; a line the shell would run as two, the
allowlist refuses and the deny list reads both. Where the deny list over-reads
a quoted string as a command, it costs a refusal the reader can see is wrong.

The inspection list's own flag guards read the same unquoted words the
program will be handed, so `find . '-delete'` is the `-delete` it is.

## The model is told what read-only mode runs

A read-only session refuses every command but inspection, and a model that
does not know which commands those are learns them the expensive way: it
tries a linter, a build, `make`, a test run, and each guess is a refused call,
a card in the feed and a round of the turn. One turn has been seen to spend
five of them before it answered. So the paragraph the two read-only modes add
to the prompt prints the inspection list itself — the built-in commands and
the person's own additions — together with one line of the quoting rule above:
each command runs on its own, and a pipe or a chain outside quotes is
refused. One sentence says what is refused by its shape, any other command and
every write, and names the reading tools as the way to read instead, each only
where the session holds it, as the toolbox does.

**The list is generated, not written.** A paragraph that described the list in
prose ("e.g. `ls`, `wc`") was a second copy of it, and a second copy drifts:
an entry added to the list would never be offered, and one taken off would
go on being promised and refused. Printed from the list the policy reads, the
paragraph cannot name a command that will be refused or leave out one that
would run. A child in a read-only mode reads the same paragraph, from the same
list, with its own toolset.

**The refusal repeats it.** The paragraph is read once, near the top of a long
conversation; the refusal is read at the moment the model is choosing its next
command, which is when it needs the list. So a refused command comes back
named, with the list that would have run and the fact that no approval can run
anything else. A model that has just been told what runs picks from it rather
than guessing again.

**The list is capped.** The paragraph rides every request the mode makes, so
its size is paid for on each of them. The cap is above the built-in list's
length, so the built-in list is always printed whole; only a person's long
list of additions is cut, and what is cut is counted ("and 40 more") rather
than dropped silently, so the model knows the printed list is not the whole of
it. The paragraph's growth is held by a test the way the toolbox's is.

## A host is granted once

In a coding session a fetch is asked about like any act that leaves the
machine (a conversation reads without asking, since nothing it does can change
anything), and the card that asks about it names one fact above the others:
the host the request leaves for. So the host is what `[a]` grants. Pressing it on
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

A host the public lists warn about — young, disposable or listed — is never
offered the grant, at any width and in any mode: a grant is a standing yes,
and a warning is the lists asking for each fetch to be answered on its own,
so the answer on that card is for that one fetch and the card says why the
key is missing. Vouching for such a host for
good is `web.allow_hosts`, which is written down rather than pressed.

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

## A host is read against the world before it is judged

Auto mode's classifier judges a fetch mostly on where it goes, and a host's
name alone says little: a documentation site everybody reads and a domain
registered yesterday are the same string to a model that has never heard of
either. So before a fetch is judged its host is read against public lists,
and the reading is one of five words:

| Standing | What says so | What it changes |
|---|---|---|
| `known` | shhh's own short list, or the top ten thousand of the [Tranco](https://tranco-list.eu) ranking | auto mode lets the fetch through without asking the classifier |
| `young` | a feed of domains registered in the last ten days ([NRD](https://github.com/cenk/nrd)) | a fetch the classifier would have allowed is put to you |
| `disposable` | the [disposable-domains](https://github.com/disposable-email-domains/disposable-email-domains) list | the same |
| `listed` | [URLhaus](https://urlhaus.abuse.ch) malware hosts, or the [StevenBlack](https://github.com/StevenBlack/hosts) blocking list | the same |
| `unknown` | no list names it, or no list could be read | nothing |

**The reading advises and never widens.** It is evidence for a classifier and
stands in for one where it is sure; it is never asked to stand in for a
person. `manual` and `accept-edits` still put every fetch on a card, a known
host included, and a conversation reads the web without asking whatever the
lists say. What a list warns about can only move an answer toward asking: the
classifier's no still refuses, and its yes becomes a card saying which list
spoke — `ask · registered in the last 10 days (nrd)`. A known host is let
through with the list named on the row: `auto-allowed · known host (tranco
top 10k)`. In a run with nobody to ask, the card is a refusal naming the
same list. A sub-agent's fetch and an unattended run's are read by the same
function and answered by the same rule.

**Your own lists outrank every reading.** `web.deny_hosts` refuses a host the
ranking calls known, and `web.allow_hosts` lets through a host a list warns
about (a grant made with `[a]` before a list named the host stands too; the
key is not offered once one has). A list is somebody else's opinion of
the internet; the two host lists and a grant are yours about this session.
`web.reputation_off` turns any list off, shhh's own included.

**A list that cannot be read says nothing.** A list is third-party data
landing in a decision, so a copy that is missing, malformed, or older than
its window names no host — and a host no list names is `unknown`, which
changes nothing. A failure can therefore never make a host known. The lists
are downloaded into the cache directory the way the model data is: behind the
first fetch a session decides, at most once per process, never while
anything waits, with a failed download remembered for an hour. The ranking
and the disposable list ship inside the binary as the floor under the
download; the new-domain and malware feeds do not, because a snapshot of
this week's new domains is wrong by the time the binary is installed. Each
list is asked for again after its own refresh — a day for the feeds that
move daily, a week for the ranking — and stops answering past its window.
`shhh doctor` names each list's age.

**The ranking vouches for a site, not for what is sent to it.** Tranco ranks
registrable domains, so its word stops at the public suffix list's boundary:
`anything.github.io` or `anything.workers.dev` is a site of its own and is
not covered by the platform's rank. And a URL carrying a query string is
never known by the ranking, because a query string is how a GET carries data
out and a popular site says nothing about who reads it — Telegram's bot API
sends a message on a GET. Such a fetch goes to the classifier as before.

**shhh's own list is short, and it is shhh's.** It holds the sites a coding
session reads every day, each run by one organisation across every host
under its name, so an entry covers its subdomains: anthropic.com,
claude.com, openai.com, github.com, gitlab.com, stackoverflow.com,
stackexchange.com, serverfault.com, superuser.com, go.dev, golang.org,
python.org, pypi.org, rust-lang.org, docs.rs, crates.io, nodejs.org,
npmjs.com, typescriptlang.org, developer.mozilla.org, ruby-lang.org,
rubygems.org, kotlinlang.org, swift.org, dev.java, docs.oracle.com,
learn.microsoft.com, cppreference.com, php.net, postgresql.org, sqlite.org,
git-scm.com, kubernetes.io and docs.docker.com. It is short because every
entry is a site auto mode reads without a second opinion; anything a person
wants beside it belongs in `web.allow_hosts`, where it is theirs.

## A read-only role is granted once

Starting a sub-agent is gated for the same reason a fetch is: it spends
something. The card names one fact above the others — which role — so the
role is what `[a]` grants, exactly as the host is on a fetch card. Pressing
it on a researcher means the next fan-out of researchers is not the same
card again, and the reader has answered the question the card asks, which is
what a child of that role is given.

Only a role that changes nothing is offered it. A writer hands back a patch,
and the patch is the decision that matters; its spawn card is where the
person reads what it will claim, so there is nothing there to wave through.
The grant is the role and never the child: a name is one agent, which would
grant nothing worth having, and the roles are a closed set the session
loaded.

There is one length and no choice to make. A role grant is already exact —
there is nothing narrower than a role — and a fan-out happens once in a turn,
so a grant that expired with the turn would cover the card in front of the
reader and nothing after it. It is a session grant like the others: listed by
`/permissions grants` with when it ends, counted on the status line while it
stands, and taken back by `/permissions revoke agents` or by a revoke of
everything. The mode still answers first — a grant answers a question the mode
left open, never one the mode has closed — so plan mode refuses a spawn the
session waved through in some earlier mode.

## The classifier fails closed

Auto mode's classifier never approves on error. A timeout, a malformed answer,
an unreachable provider — each falls back to asking the human. There is no
path through the code where "we could not decide" becomes "yes".

This is worth stating as a commitment because the opposite is the natural way
to write it. A classifier that returns a boolean gets a zero value, and the
zero value has to be the one that costs nothing.

**A classifier's no is put to the person where one is there.** The classifier
is told to say no when it is unsure, which is right for what it guards: it
must never be the reason something ran. But a no from something unsure, in
front of somebody who can answer, is a question for them — and refused
instead, it turns a wrong judgement into a round of the person re-explaining
the task to a model they cannot see. So in a session with a person in front of
it, the call becomes a card: the classifier's sentence is on it, under the
severity, and the answer that costs nothing is offered last, the way a flagged
command's is ([below](#severity-moves-the-default)). Nor is a grant offered,
as none is on a flagged card: a grant answers before the classifier, so it
would wave through every later call of the shape just judged, none of them put
to anyone. A yes runs this one and a no
refuses it, and the model reads either as it reads any card's answer. The
classifier only ever moves a call toward a person, never away from one: the
one flagged command it may answer for is put in its hands by a proof the
rules make, not by anything it says ([a delete of
scratch](#severity-moves-the-default)).

The same holds for a child: its classifier's no is routed to the session's
card with its other requests
([`subagents.md`](subagents.md#a-child-answers-to-the-session)).

Nothing the classifier is never asked about is moved by this. The deny lists,
a required sandbox that is missing, a path no grant can reach, and plan or
read-only mode all answer before it, and still refuse without a card.

The record keeps the two apart: the classifier's verdict is a row of its own,
a denial by the classifier, and the person's answer is the row after it — so
how often a person overturned the classifier is a share of the denials they
answered, which `shhh observe` draws and a comparison carries
([`sessions-and-memory.md`](sessions-and-memory.md#observations-are-what-the-session-did)).

Where there is no human to fall back to — a scripted run in auto mode, a
served session told nobody is attached, a backlog stage, a child with no route
to a card — the fallback is a refusal instead, and a classifier's no stands as
the refusal it is: the same commitment with the one remaining answer taken
away ([`headless.md`](headless.md#auto-mode-fails-closed)).

## The classifier can answer as a probability

The classifier is asked for a verdict in words by default: allow or deny,
and a sentence saying why. A gateway that serves a model's Decisions API can
ask it a different way, with `behavior.classifier_backend = "decisions"`:
the same evidence and the same rules, put as one question — may this call
run unasked? — that the model answers with a probability rather than a
reply.

**The threshold is the bar that probability has to reach.**
`behavior.classifier_threshold` is a percentage: at or above it the call
runs, below it the classifier has said no, and a no is what it always was —
a card where a person is there, a refusal where nobody is. The default sits
well above even odds because the two mistakes do not cost the same: a false
allow runs something nobody watched, and a false deny in front of a person
is a question they answer. It is provisional until the eval's comparison of
the two backends settles it. A checkout may set neither key: a lower bar
removes refusals, the backend leaves a replaced wording unsent and with it
whatever refusals that wording added, and a checkout may add a refusal and
never take one away.

**Every way of not getting a probability fails closed.** A model that does
not offer the API, a request that fails or runs out of time, an answer
missing for the question, and a refusal to answer are each the classifier
failing, and each ends where [any failure does](#the-classifier-fails-closed):
a card in front of a person, a refusal with nobody there. A refusal is the
model's answer to this evidence, so it is not asked again; a failed request
gets the retries a completion would.

What this gives up is the reason. The card's sentence becomes the
probability and the threshold rather than the model's account of the call,
which is a worse sentence for someone deciding whether to overturn a no —
and whether that matters is what the comparison is for. A replaced wording
is not sent to this backend, since it is written for a reply in words that
the backend does not give, and the doctor says so. The explanation a card
offers reads with the classifier's model on the other backend; on this one
it takes the rest of the cheap chain instead, because a model that answers
decisions may answer nothing else.

The record keeps the backend beside the classifier's model on every
session, and the time each verdict took on its row, so sessions on one can
be set against sessions on the other
([`sessions-and-memory.md`](sessions-and-memory.md#what-a-session-ran-under)),
and `shhh eval --classifier-backend decisions` puts the classifier table to
it so two baselines can be read against each other
([`evals.md`](evals.md#a-false-allow-is-not-a-false-deny)).

## A write that adds a secret is always asked

An edit or a write whose added lines hold a credential shape is put to the
person in every mode that would have run it unasked: accept-edits, auto, and a
session grant of edits all answer before the card, and none of them answers
this one. The card says so first — a warning row beside the level, `⚠ adds 1
anthropic key · line 12`, naming the kind and the line and never the value —
and offers no grant, as a flagged card offers none. The lines are read with
the scrub's own table of shapes, and only the lines the change adds count: a
key the file already held is not the edit's to answer for
([`secrets.md`](secrets.md#the-shapes-it-knows-without-being-told)). A
sub-agent's write is asked the same way: its routed card carries the same row,
and the child's own mode does not answer it
([`subagents.md`](subagents.md#a-child-answers-to-the-session)).
The classifier is not consulted about one: where the policy asks, the answer
stays an ask, and a classifier that would have allowed it is never run.

Why it asks and does not refuse: the person may mean it — a fixture, a
template with a placeholder that happens to match. But a key written to disk
is a key in the next commit, the next backup and the next paste, and the
cheapest place to stop it is the first door, before the file exists, rather
than the last, where the commit refuses it
([`secrets.md`](secrets.md#a-secret-does-not-get-committed)). It is the
fail-closed stance of [the classifier](#the-classifier-fails-closed) applied
to a different doubt: where the product cannot tell an intended write from a
leak, the answer is a person, never a yes.

Plan and read-only mode still refuse the write outright, and the deny lists
still answer first. A no is the ordinary denial; the model is told nothing
new, and reads it as it reads any declined edit.

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

## A profile can narrow the classifier, never widen it

A sub-agent started from a profile that says what its commands are for
([`subagents.md`](subagents.md#a-profile-is-a-file)) carries that sentence to
the classifier as evidence of its own, `profile_scope`, on each command the
classifier is asked about for that child — placed after the conversation
that holds the user's request, because it qualifies that request rather than
making one. The instruction says what it may do with it: deny a command
outside it, and never allow one because it covers what the request does not.
A test-runner's profile can therefore turn a `curl` the request might have
stretched to cover into a no; it cannot turn a `git push` nobody asked for
into a yes.

It rides commands and nothing else. It is a statement about commands, and
read against an edit or a fetch it would narrow calls it never described.

Only the person's own profiles send it. Evidence trusted to narrow is still
evidence the classifier weighs, and a checkout's profile is the checkout's
words about the checkout's agent — the party being judged writing part of
the brief it is judged against. So a profile read from anywhere but the
person's own agents directory keeps its sentence off the classifier, and the
spawn card shows it instead, where the person reads it before the child
exists. Which side a file falls on is decided by where it was read from, and
a location neither reading expected falls on the side that sends nothing.

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

**One flagged command is judged rather than always asked: a delete of
scratch.** In auto mode, with a person in front of the session, a recursive
delete, a deleting search or a forced `git clean` goes to the classifier like
any unflagged command, and runs on its yes, where the rules prove all of this
about it:

- every target is resolved — no variable, glob, substitution or relative path
  after a `cd` — and sits below the workspace root, not at it, inside the
  working scope, and reached through no symlink;
- nothing at or under a target is in the repository's index, whether committed
  or only staged, and none of it is a repository of its own — no `.git` at a
  target, inside one, or in a directory between the workspace root and one;
- nothing under a target is a link that leads out of it, and the tree is small
  enough to walk in full (the same bound the card's blast radius walks under);
- nothing else on the line is flagged. A download piped into a shell or run
  in a second step, an interpreter handed what was fetched, a force push, a
  `chmod 777` — any of them beside the delete, and the line keeps its card;
- every other command on the line is an inspection command, one that changes
  nothing. The proof is taken before the line runs, so a move or a link made
  earlier on it (`mv src .tmp/gone && rm -rf .tmp/gone`) would put somewhere
  the proof never looked under the delete. Nor may the line branch or loop,
  or carry the delete behind an escalation.

`rm -rf .tmp/test-build` and an ignored `node_modules` are the cases it is
for: routine clean-ups that raised a card every time and taught the person to
press yes without reading. The row says why no card was drawn: `auto-allowed ·
scratch inside the workspace (untracked)`.

Everything short of that keeps the card it had — a target partly tracked, one
the reading cannot resolve, one outside the scope, a link under the target to
the home directory, a workspace that is not a repository, a git that does not
answer. It is not the classifier that moves the call: the proof decides which
calls the classifier may answer for, and the classifier is asked only what it
is asked about any other command. A classifier's no on one of these is still
a card with its reason, and a classifier that failed is still a card. An
unattended run and a sub-agent are not given the exception; a flagged command
there keeps the answer it always had.

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
visible refusal. A wrapper that changes only how a command runs — `nice`,
`ionice`, `timeout`, `stdbuf`, `setsid`, `exec` — is read the same way, since
its options and `timeout`'s duration are as opaque as sudo's. The shell's own
flow words are read past too: `if x; then rm -rf /; fi`, `while :; do rm -rf
~; done`, `! rm -rf /` and `{ rm -rf /; }` each run an rm, and a reader that
took `then` or `!` for the program saw nothing to flag. A flow word takes no
options, so the word after it is the command, and a parenthesis — a
subshell's, a `case` pattern's — ends one command and begins the next. A
variable is a word like any other: `rm -rf $DIR` is a recursive delete of
whatever it names, not a delete of nothing followed by a program called
`DIR`. The rule [below](#some-targets-are-never-destroyed) reads past the
same wrappers, flow words and parentheses, so a target it can prove is
refused behind any spelling here that the card flags.

Force is not what makes a recursive delete permanent — it only stops `rm`
asking about a write-protected file — so recursion alone is enough to move
the key. The same reading covers the spellings people reach for when they are
in a hurry: a `find` with `-delete`, a `git clean -fdx`, a `git checkout` of
a pathspec rather than a branch, a `chmod 777` with or without `-R`, and an
`mkfs` by any of its names — `mkfs -t ext4` and `mkfs.ext4` format the same
disk.

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

## Some targets are never destroyed

A recursive delete, a search that deletes what it finds, a `git clean`, a
recursive `chmod` or `chown`, a write onto a device and an `mkfs` of one are
read for what they are pointed at, and where that is somewhere no session may
destroy the command is refused by rule: the filesystem root, the home directory, the
workspace root, a repository's git store, a path behind the containment deny
mask, and anywhere outside the working scope. It is answered where the deny
list is — before the mode, before any grant and before the classifier — so
no card is drawn, no earlier batch approval reaches it and no classifier is
paid to think about it. A session, an unattended run, a served session and
every sub-agent refuse it through the same function.

It is a rule and not a grade because a grade would have to be believed. A
model's sense of how risky a command is was never calibrated, and it is read
from evidence a fetched page can write into; a block that depends on it can
be talked down. The rule reads the text, the filesystem and the working scope,
which is what the safety table, the blast radius and the scope already read,
and none of those is anything a page can reach. The classifier can only ever
move a call toward a person; this is the one door it never opens.

**The rule refuses only what it can prove.** A target is resolved the way the
card's blast radius is — the paths the text names, stat-ed from the directory
the command runs in, symlinks followed where the command would follow them —
and a target it cannot resolve is not refused: a variable other than the home
directory, a glob, a substitution, a brace expansion, a relative path after a
`cd` earlier in the line, a relative path in a process started somewhere of
its own. Those fall back to the flagged card they always had. `~`, `$HOME` and
`${HOME}` are resolved, because their answer is known, and so is a glob over
every entry of the filesystem root or the home directory (`/*`, `~/*`), which
is that directory. A device is refused whether or not the surface has a
working scope, and the streams every script writes to (`/dev/null`, a
terminal) are not devices here. A symlink removed
without its trailing slash is the link, not what it points at. A search that
deletes only what its tests matched destroys some of a tree rather than the
tree, so it is refused for reaching outside the scope and not for starting at
the workspace root; `git clean` is refused at the workspace root, because what
it deletes there is everything git cannot bring back. `rm -rf .tmp/test-build`
and `rm -rf node_modules` are none of these; they go to the card as before,
or, where they are untracked scratch in auto mode, to the classifier
([above](#severity-moves-the-default)).

**It cannot be lifted**, per session or by configuration. There is no setting
for it and no grant reaches it. A person who means it types the command
themselves — `!` and `/run` are the person's own and never reach the policy —
and the explanation `/permissions why` gives says so, since that reader is the
one person it is true for.

What the model reads names the target and nothing about a way around it:

> error: this command destroys `<target>`, which is outside what this session
> may destroy. It is refused in every permission mode and no approval can
> allow it, and retrying it under another spelling — another path to the same
> place, a variable, a wrapper — will be refused too. Do the work without
> destroying it, or say what you were trying to do and let the user decide.

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

## A judged denial carries its reason

Most rules only match. The deny list, plan mode, a path no grant can reach —
each answers with the rule's own name, and the name is the whole of what it
had to say. One rule judges instead: auto mode's classifier reads this call,
in this conversation, and writes a sentence about it. That sentence is not the
rule's name and cannot be reported as one.

**Where a person is there, the sentence is on the card.** A judged no is put
to the person rather than refused
([above](#the-classifier-fails-closed)), and what they are answering is the
judgement, so the card states it whole, under the severity, as
`classifier: <reason>` — wrapped rather than cut, because a clipped reason is
the one line on the card the reader cannot check. It goes nowhere else: the
answer they give is theirs, and the row it leaves reads as their allow or
their denial like any other card's.

**Where a no stands as a refusal, it is folded under the row of the call it
refused**, where the reader finds it by opening the refusal they are already
looking at. A refusal is a
moment in a record, and the record is the feed: the row is still there after
the turn has moved on, it is still there ten turns later, and what refused it
is still on it. Anything else asks a reader who scrolled back to a denial to
go somewhere else and hope the answer there is still about this one.

**The prompt frame says nothing about it.** The frame is current state, read
before every keystroke; a judgement about one call three rounds ago is not
state, and a label left there would go on saying something true of a moment
and false of the session. A standing rule the reader wrote *is* state — it
will refuse the next call too, and going and changing it is an act — so a rule
that matched still says so there until the next turn.

`/permissions why` keeps its own answer, and the two are not the same
question. It reports the latest denial, wherever the reader happens to be; the
row reports this one, wherever the denial happens to be.

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
distinct from a rule's, with the note folded under it. A rule that only
matched carries no note there, because it has nothing to say beyond which rule
it was; the one rule that judged folds its own sentence into the same place
([above](#a-judged-denial-carries-its-reason)).

The yes has the same second spelling. A note beside an allow is steering: the
act runs, and the sentence joins the conversation before the next round, the
way a message typed while the turn works already does
([`../interface/surfaces.md`](../interface/surfaces.md#the-input-frame)). It
is offered on the card because the card is where the reader has the thought,
and a thought held until the act has finished is usually a thought lost.

## Only the person's own path carries authority

Text from a child, a server or a page is a claim, and only what the person
types — the draft, a steer, a note on a card — is an instruction; so each of
the three reaches the model under a line saying whose words follow, and that
line is part of the code rather than a wording a checkout could replace
([`subagents.md`](subagents.md#what-comes-back-says-what-happened-to-it),
[`mcp.md`](mcp.md#a-server-cannot-vouch-for-itself)).

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
never a suffix of it, so a fetch card offers the two lengths and stops. A
spawn card offers neither a width nor the shorter length, for reasons of its
own ([a read-only role is granted once](#a-read-only-role-is-granted-once)).

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

That leaves the reader without the shell's way of keeping a big answer small.
The inspection allowlist refuses any line carrying shell punctuation the
shell would act on, so in
the two read-only modes `git diff --stat … && git diff …` and `git log … |
head` are refused before the analysis starts. The tool's own arguments do the
same work as separate calls: a per-file summary first, then the patch
narrowed to the paths that matter, and a commit count instead of a pipe. That
is why the model is taught the staged read in the tool's own description and
told to prefer the tool over git on a command line in its toolbox line — and
why the read-only and plan paragraphs name no git command, since a paragraph
cannot know whether the session it lands in has the tool or any command at
all. A per-file summary is not a list of which files were added, deleted or
renamed; nothing here pretends it is.

It also reaches past the arguments, because a repository carries configuration
and some of that configuration names a program to run. A repository you
cloned this morning can ask git to run something on every status. The reader
turns those settings off for its own calls, which matters more here than
anywhere else in the tool set: this is the one tool that runs unattended, in
every mode, with nobody asked first.

The SQLite reader takes a language rather than a verb, so its closed set is a
different one: the engine's own. The file is opened in SQLite's read-only
mode with writes switched off for the whole connection, so a statement that
writes is refused by the database, not by shhh's reading of it — a guess
about SQL is not a boundary, and a reader that relied on one would be one
misread keyword from a write. What that mode does not cover is refused before
anything runs, for reasons of its own. Attaching a second database opens
another file by path, around every check the call's own path went through,
and on a read-only connection it would still create the file it names; so
the connection is allowed no second database at all, and the statement is
refused besides. A pragma that sets a value is how the read-only switch
itself would be turned off, so only the pragmas that report are answered,
and only by name. Loading an extension runs a library's code. A statement
holds one statement, since several in one string would run in turn, and a
word that writes anywhere in it refuses it, since a query can end in a
write. The check errs towards refusing — a column named after a keyword has
to be quoted — because the cost of a refusal is a rewritten query and the
cost of the other mistake is not.

## The writing half of git is a tool too

Reading history became a tool because a read that asks is a read the agent
skips. Committing became one for the opposite reason: it is the act that
always asks, however many times you have said yes to it.

The allowlist cannot help. An entry for `git commit` would pre-approve every
flag a commit takes, and a message in double quotes that mentions a `$` or a
backtick is refused anyway — that refusal is what keeps a pre-approved shape
from becoming a chain. So `git commit -m "…"` is a classifier round in
auto mode and a card everywhere else, every single time, and the last thing a
turn does is the thing it interrupts you for.

Carrying the message as a field costs neither. The writing half of git is its
own tool with four verbs and nothing else: stage, commit, create a branch,
switch to one. Push, reset, clean, checkout of paths, rebase, merge, stash,
tag, `--amend`, `--force` and `--author` have no field to arrive in, exactly
as the reader's excluded verbs have none.

It is not a read, so it is not on the reader's tier. It sits at the write tier
and is answered the way an edit is answered: it applies where an edit applies,
it asks where an edit asks, and read-only and plan refuse it. The deny list
is read before any of that — an entry for `git commit` refuses the commit
verb by the same match the command path uses, because a person who wrote that
line meant the act and not the spelling, and a tool that let the act through
under another name would be the way around the list.

Two of its rules are things a shell cannot do.

**It stages only this session's own work.** `git add -A` stages your
uncommitted morning beside the agent's afternoon, and the commit that results
cannot be reverted, cited or read as a unit. This tool has no field for `-A`
and no field for a glob: it takes paths, one by one, and refuses by name any
path the session's own record of what it changed does not hold. The rule that
work already in the tree is not the agent's stops being a sentence in the
prompt and becomes a fact about the arguments.

**A commit's checks run only on a checkout you trust.** A commit hook is a
program git runs as you, and a checkout can point git at one inside itself.
That is the line trust already draws around everything else a clone can make a session
run, so a commit on an untrusted checkout passes `--no-verify`, which skips
its pre-commit and commit-msg hooks and no others, and the receipt says
`pre-commit and commit-msg skipped · not trusted` rather than leaving you to
discover that the repository's own checks did not run. It names the two
because they are all `--no-verify` reaches: the post-commit and
reference-transaction hooks still run, as do the checkout's clean and smudge
filters on a stage and its post-checkout hook on a switch, so the card and the
receipt say which were held back rather than that nothing of the checkout's
ran.

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
([`hooks.md`](hooks.md#where-a-hook-is-written)), MCP servers to start,
settings that say which commands run without asking, and install lines that
prepare the place its commands run in
([`containment.md`](containment.md#a-checkout-declares-the-toolchain-its-work-needs))
— and every one of those runs as whoever cloned it. None of them load until you have said so.

It is one answer about the whole checkout, given once: `shhh trust`, `[a]`
on the doctor's trust row, or `/trust` in a session. It covers
`.shhh/skills`, `.agents/skills`, `.claude/skills`, `.shhh/agents`,
`.shhh/quality.json`, `.shhh/hooks.json`, `.shhh/mcp.json`, `.mcp.json`,
`.shhh/config.toml`, `.shhh/prompts`, `.shhh/todo/profile` and
`.shhh/toolchain.toml`. The answer
is keyed on the checkout and held until you withdraw it — `shhh trust off`,
or `/trust off` — so the files it covers are yours to edit, and editing them
does not ask again. The answer is kept outside the checkout, in the local
store, because a file in the checkout is the thing being decided about.

A change is told once, never withheld. What a trusted checkout's files say
can still move under you without your hand on them — a pull that rewrites a
suite's command line is the case worth guarding — so beside the answer shhh
keeps what each kind of file said when a session last read it. The first
session after one of them moves names the kinds that changed — on the start
screen, in `/status`, and in the line before a headless run begins — loads
them as they are now, and records what it read, so the session after says
nothing. That notice is the reading a person acts on, and `shhh trust off`
is the act. The doctor's row reports the same standing and writes nothing,
so running it does not use the notice up.

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
something the model does not know it has. The toolchain declaration is the
exception, read again at once, because what it says — which tools are
missing — loads nothing into the session; the reply to `/trust` names both
halves.

### The model is told what was held back

A person is told on the start screen; the model has to be told too, or the
withholding costs rounds. An instruction file that says to run the project's
suite, use its skills or ask its servers is read whether or not the checkout
is trusted, and a model that finds none of them goes looking for them or
rebuilds them by hand. So the prompt names the kinds this checkout declared
and this session did not load, says the instructions that mention them
describe things the session does not have, and — where the suites are among
them and the session can run commands — says to check the work with ordinary
commands, which are approved like any other.

It does not tell the model how to undo it, and it tells it not to ask. Trust
is a person's answer about files that run as them, and the person has already
been told what was held back. The checkout the withholding exists for is one
whose own `AGENTS.md` would like the model to press for it.

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

### A gate chooses one execution boundary

The fuller account of what each testing tier proves is in
[`testing.md`](testing.md). This section is the permission boundary that
chooses the hermetic one.

A quality suite that runs from a session is a contained check or it is not a
quality suite at all. Set `require_containment` on every suite a session may
run automatically or at its close. When this host cannot establish the
boundary, the result is **blocked** before any check starts; it never falls
back to the host and never turns an unavailable boundary into a passing
verdict.

The checks in such a suite must be hermetic: their temporary files, home,
configuration and toolchain caches are under the session's grants, and they
do not require a listener, a host service such as a clipboard, a container
engine, or the outside network. Those requirements belong to a separately
named integration target on a prepared CI runner. Keeping that target out of
an on-close suite makes a test result repeatable inside a session while still
letting CI certify the operating-system boundary itself.

Go quality checks use the session's own build cache, inside its private
scratch directory and shared with the builds its sub-agents run
([what they share](subagents.md#what-they-share)). The module cache remains
the granted dependency cache, so an ordinary test run neither writes its
compilation products to the host's cache nor needs to download its
dependencies again.

An integration target is explicit about its prerequisites and fails its
selected runner when they are absent. A skip is useful on another platform;
it is not evidence that the platform-specific contract held.

A failing check is run once more, alone, over the same tree, and a pass on
that second run is reported as a flake rather than hidden
([why one run, and why it never lowers the verdict](testing.md#how-do-quality-gates-stay-repeatable)).
A suite sets this with `rerun_failed`: `1`, the default when the key is
absent, runs a failed check once more, and `0` lets every failure stand. No
other number is accepted.

The suite can also be told to run on its own, as a turn closes over work it
changed. That changes nothing about what a check may do — the commands are
still only the ones in your file, still run read-only and contained where a
mechanism is available — and it changes nothing about the session's
permission mode either: a session that checks its own work may do exactly
what one that does not may do. What it changes is who asked. The name is one
key in the same trusted file, so turning it on is an edit to a file you own,
and a name that matches no suite is refused when the file is read rather than
at the close of the turn that was counting on it.

## One reading of the boundary

Everything a session may do without asking, and everything it is fenced off
from, is decided in several places and was reported in as many: the mode and
the grants under `/permissions`, the working scope under `/add-dir`,
containment under `/sandbox`, the checkout's standing under `/trust`, the
servers under `/mcp`, the vault under `/secret`, and a granted host only on
the card that granted it. Each of those is the right owner for its fact. None
of them answers the question a person asks before trusting a session with
more, which is the whole boundary at once.

So there is one reading of it, `/safety`, and it owns nothing. Each section is
the answer its owning command already gives, asked of the same function, and
ends by naming that command. It computes no answer of its own, because a
second computation of one fact is a second answer to one question, and the
day the two disagree the reading is the one that is wrong. It has no key that
changes anything, for the same reason: a place to read a fact and a place to
change it that are not the same place would be two places to change it.

What a session does not have is stated rather than omitted. A boundary read
by what is listed is read wrong when something is missing from the list —
containment that is unavailable, a checkout whose trust was withheld, no
servers — so each of those is a section saying so. It is the session's
boundary and not the machine's: which mechanisms this host could offer is
`shhh doctor`'s question, and a command-line `shhh safety` would have no
session to read. The layout is in
[the interface](../interface/surfaces.md#the-safety-reading).

## Related

- [`hooks.md`](hooks.md) — your own commands at the same seams, and what one
  may and may not decide
- [`containment.md`](containment.md) — what stops a command that was approved
- [`../architecture.md`](../architecture.md) — why the tiers are structural
- [`../interface/surfaces.md`](../interface/surfaces.md) — the approval card
