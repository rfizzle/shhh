# Sessions, history and memory

Three different things shhh remembers, kept separate because they answer
different questions and have different lifetimes.

## History is what you asked and what happened

Every generated command is recorded with the prompt behind it, what became of
it, and what it cost. It is browsable, filterable and searchable.

Every row leads with what happened to the command — generated, failed,
dismissed, never run — because that is what you are scanning for. You are
looking for the thing that worked, or the thing that did not.

**Nothing re-runs without a deliberate key.** A history browser where the
obvious keystroke executes something is a trap, and the trap springs on the
person who opened it to *read*.

Snippets are the deliberate half of the same idea: a command you decided to
keep, under a name, runnable directly.

### Rating is what the accuracy figures are made of

An exit code says the shell was happy. It does not say the command did what
you asked for, and the difference between those two is exactly what an
accuracy figure claims to know. So the only place that answer can come from
is you, and the whole design problem is that answering has to cost nothing.

It is therefore a question rather than a listing: one command at a time, the
prompt and what came back on the same card, and an answer that is a single
key with no confirming keystroke after it. Stopping is a key too, and it is
the one every other surface stops on, because a screen you can only leave by
answering every question is a screen you answer carelessly.

What it says at the end is three numbers: what you answered, what you passed
over, and how much of the list you never reached. The last of those is the
one that decides whether running it again is worth it, and a tally that
reported only the answers would be hiding it.

Nothing about this is a terminal's privilege. Piped, scripted, or asked for
as a plain list, it is the same walk and the same words — a command whose
only interface is a drawn card is a command nothing can automate.

## Sessions are conversations you can come back to

Chat and agent sessions persist and can be resumed — the most recent one, or
one you pick.

What comes back is the conversation, not a transcript that looks like it. The
work that was in flight, what was staged, where the session had got to: a
resume that restored only the visible text would look right and behave like a
fresh session, which is worse than not resuming at all.

### A resumed session sees the tree as it is

What comes back is a conversation, and a conversation describes the
repository as it was. That is fine an hour later and dangerous the next
morning: a pull landed, a branch was switched, another session committed, and
the transcript now names paths, line numbers and states that were true and are
not. Nothing in it says so, so the first turn edits a file that moved or
re-fixes what is already fixed.

So a reopened conversation is handed the checkout as it stands now, in front
of everything it remembers: the branch, how much is changed, and the commit in
front of it. Where that commit is not the one the conversation was written
down on, one line says so and names both — the case where the transcript
actively misleads is the whole reason the reading exists. The same reading is
what `/load` gives a conversation opened mid-session, because a conversation
opened is a conversation opened however it got there.

The reading is not part of the conversation. It is built from the checkout
every time, the way the system prompt is, so opening the same conversation
three times tells it about the tree once rather than three times about three
commits. One folded row accounts for it — the branch and the count on the
line, what the conversation was actually told underneath.

Where a conversation was compacted, the summary that compaction wrote is kept
beside it and comes back with it, after the reading. Nothing is summarized to
fill that in: `/compact` already asks for the goals, the decisions, the work
done and what is open, and a summarizer run at quit would be a request nobody
made and nobody waited for. A conversation that never compacted comes back
with no summary and no placeholder saying there is none.

### An unattended run comes back too

A run started with `--print` is a conversation like any other. It can be told
to carry one on — the most recent, or the one it names — and what it does
with it is what a session does: the stored conversation goes in front of the
prompt, and the checkout as it stands now goes in front of that. A run is
where that reading earns the most, because a run acting on a path the
transcript describes has nobody to notice the path moved.

What the run leaves behind is a conversation in a slot of its own, written
when the turn ends and equally when it does not: a round cap reached, a
provider that stopped answering, a turn cut short. Reopening the most recent
conversation opens it, which is the only way to see what a run that failed
overnight was actually doing. The mark that says a turn was parked is never
written by one — parking is something a person does with a keyboard, and
there was none.

The saved-chat browser is the one thing a run cannot be asked for: it is a
full-screen program and a person choosing, and there is neither. Asking for
it is refused and says so, rather than accepting the flag and starting from
nothing under the word "resume".

### A held turn comes back held

A conversation can be quit with a turn parked at a round boundary rather than
finished, and the slot remembers it: the mark says the turn was held and where
it had got to. Reopening it opens it held, with the same words on the rail
that were there when it was left.

Without the mark the conversation would come back looking finished, with an
unanswered round at the end of it and an idle prompt underneath — which is the
shape a person reads as "it is done", and the round it is owed would never be
asked for. A slot with no mark is every conversation written before there was
one, and it opens exactly the way it always opened.

### A new conversation is a new session

Starting a new conversation is quitting and launching again without the exit.
The conversation so far is written to its own slot and left there whole, the
session's record is closed and another opened, and everything a launch reads
is read again: the system prompt is built from the checkout as it stands, the
tree reading takes its baseline, the record of what the model has been shown
is emptied, and the changed files, the turn count and the spend all start from
nothing. The row it opens on says which slot the last conversation is in and
what command reopens it, because there is no exit banner here to say it.

A conversation that kept any of that would be the same session wearing an
empty transcript, and the record is where that shows: one row would carry one
prompt fingerprint, one settings stamp and one outcome for work that was done
twice, and every rate computed over it would be an average of two sittings
that had nothing to do with each other.

It is never crossed in the middle of a turn. A turn that is still working, or
parked at a round boundary, is asked about first — the same question quitting
asks, because a yes costs the same thing either way: the turn is cancelled,
its children go with it, and what had already been said is in the conversation
that gets saved. A backlog run is the one thing that survives, because its
checkpoint was written to survive exactly this: the run is let go of with the
item still in progress, and the new conversation opens with the command that
continues it.

### A session knows it is not alone

Two sessions in one checkout is a decision somebody made, and nothing refuses
to start over it. What used to be missing is that neither of them knew. One
would find a change in its own diff that it could not remember making and set
about explaining or reverting it, and the other would offer to open the
conversation the first was still writing.

So both say so. Where another session already has this checkout open, the
start screen carries one more clause on its header line — *another session
open here since 10:32* — the model's workspace block says the same and adds
that changes it did not make are most likely that session's work, and the
block reporting that the tree moved ends by naming the likeliest author —
behind `--print` as much as in front of a person, since a run nobody is
watching is the one that would otherwise spend its rounds explaining or
reverting work it never did. None of them names the other session's
conversation: what it is doing is its own, and this one needs only to know
that somebody is there to ask.

The record is what answers the question. A session's row has always said when
it started and when it ended, and an unfinished row has always meant two
different things — still running, or killed with its ending never written.
The process id tells those apart, and a beat written at every turn boundary
guards the id, since ids are reused and a dead session's number can come to
belong to something else. A row whose process is gone is closed at the next
session's start, the way an abandoned sandbox container is reaped: the record
outlives the thing it describes, so something has to bring the two back in
line.

Nothing is written into the checkout to make this work. A lock or a lease
file would have to live in the project's own directory, which people commit,
and a lease in a committed directory is a merge conflict waiting for a bad
afternoon. The checkout is matched on the fingerprint the row already
carries, which is also what keeps the reading clear of the record's one rule:
it stores no paths.

The last place it shows is the saved conversations. A slot a running session
is still autosaving into is marked wherever conversations are listed, and no
picker will open it — the one inside a session and the one in front of one
alike, since loading a conversation somebody else is still adding to only
gets it taken back by their next save. The refusal happens where the row
stands rather than closing the list under it, and it is the only thing the
row refuses: reading it, renaming it and deleting it stay where they were,
and asking for the slot by name still opens it, which is the way in for
somebody who means to take it anyway. Offering to pick up where you left off
steps past such a slot to the newest one nobody else holds, and the row it
draws says in a word that it did — an offer that quietly changed which
conversation it was making would read as the last one having lost the turns
that are in fact still being added to it somewhere else. Asking for the last
session by flag does not: an instruction answered with a different
conversation is the worse failure of the two — worst where nobody is
watching — so the slot is named and refused instead, with the name the way
through as before. A slot whose session is gone is offered like any other,
since the record of that session was closed at this one's start.

### A slot belongs to one session

A session that was never named is called by the moment it began, and for a
while that name was also how the autosave found the place to write. Two
sessions started in the same second therefore shared one slot, and because a
save replaces what the slot holds, whichever of them saved last was the only
one left: the other conversation was gone, and nothing said so.

So the slot is taken from the store rather than read off the clock. A session
claims its slot when it starts, and the claim is the write that settles the
collision — the second session in the same second is given the same timestamp
with a counted suffix, which is a name a person can still read in a listing.
A claimed slot holds nothing until the first save and is not listed until
then, so a session sitting idle is never what reopening the last conversation
offers.

That leaves the other half: a slot can still fill up with somebody else's
messages, when two sessions resumed the same conversation. A save that finds
its slot grown past what this session put there is refused, because replacing
those messages would be the last anyone saw of them. The session takes a slot
of its own instead and carries on saving there, and says both halves in one
line — a refusal that left the conversation unsaved would protect one
transcript by losing another. Two sessions on one project are a thing people
do on purpose; neither of them may quietly cost the other its work.

### Housekeeping

A saved chat can be renamed and deleted where it is listed — the picker
inside a session, the browser `shhh chats` opens, and the same command with
a verb for a script. Deleting asks first, names the chat and the branches
that go with it, and defaults to No; a branch is a tail of the conversation
it forked from, so it goes with the conversation rather than lingering under
a name nobody typed. Renaming keeps every branch, and refuses a name that is
already in use rather than merging two conversations under one name.

The one chat that cannot be touched is the one the session is in. Its slot is
the one every autosave writes to, and a key that deleted it would be racing
the save; the row stays in the list and says why.

### A title you did not write

A session that was never named is called by the moment it began, and a list
of those is a list of timestamps. So after a session's first completed turn,
a cheap model reads the exchange and writes a title of a few words, shown
beside the slot's name wherever chats are listed.

Three rules keep it honest. A name you give a session — `/save name`, or a
rename — always wins, and such a session is never asked for a title. A
reading that fails leaves the row untitled and is retried once, after the
next turn, never in a loop. And the reading is off unless a summary model is
configured, because on the session model the cheapest question is still not
cheap; `summary.title` and `/ui title` say otherwise.

## Memory is what shhh knows about your project

Durable facts that outlive a session: a preference, a project convention, a
correction you made, a lesson learned. They can be scoped to one project or
kept globally.

Three constraints define the feature:

- **Every memory is proposed and never assumed.** The session may suggest one;
  you save it or decline it. A declined proposal is not raised again.
- **A memory is short, general and durable.** Not a file's contents, not a
  session-specific fact, and never a secret. Something that is only true today
  is noise by next week.
- **You can see, reword and delete all of it.** A tool that accumulates
  opinions about your project without showing you the list is a tool you
  cannot correct.

Recall is bounded, and the bound is visible. A session carries only so many
entries, and only so many tokens of them, into its prompt. An entry too long
for what is left is stepped over rather than ending the list. The entries are
ordered by scope and then by age, never by size, so one paragraph-length
project note would otherwise sit in front of ten short preferences and keep
every one of them out of a prompt with room for all ten. Whatever did not fit
is counted, and the inspector rail's tool block says how many: a memory the
session never saw is otherwise indistinguishable from one nobody ever wrote.

The way out of that count is to shorten an entry, which is why a memory can be
reworded rather than only dropped. `/memory edit <id>` opens the entry in your
own editor and saves what comes back; `shhh memory edit <id> <text>` does the
same from a shell. Both keep the scope, the kind and the provenance you
already confirmed — rewording a memory is not restating it — and both mark it
as freshly stated, so the entry you have just fixed sorts to the top of the
list rather than below the ones you left alone.

## Metrics are what it cost

Usage per model: requests, tokens, spend, latency and its tail.

Two rules make the numbers trustworthy. **A reading with nothing behind it is
left out** rather than drawn as an empty row — a fabricated zero is worse than
a gap, because a gap is legible as a gap. And **requests that never answered
are their own category**, because that is a cost you did not ask for and
folding it into the successes hides exactly the thing worth seeing.

Where no model can be priced, the split is over tokens, and it says so.

## Observations are what the session did

Every agent session leaves a record of what happened in it — not what was
said. Which tools were called, in which round of which turn, how long each
took and whether it failed and how; what the permission policy decided and
why; how every turn ended and how many rounds it took; and each time one of
the loop's own safeguards spoke — the repeat detector, the round cap, the
context trim, the summarizer's reading of whether the work was still on
target. Beside the events, the record says what the session ran under: the
build, a fingerprint of the system prompt, how many skills loaded, a
fingerprint of the checkout, and the settings it was configured with — and,
on the row itself, how the session came out.

The record exists so the prompts and the workflow can be improved against
evidence rather than anecdote. "The agent circles" is a feeling; "the repeat
detector fires in one turn in six, and those turns average forty rounds" is
a number that a prompt edit either moves or does not. The provenance is what
lets two weeks of sessions be split into before and after that edit.

**The record is content-free by construction.** Never a prompt, an output, a
path, or a command: every string stored is a fixed identifier — a provider,
a model, a tool, a skill's name — or a code from a closed set. This is what
makes recording every session unconditionally the right default: there is
nothing in it to leak, so there is nothing to opt out of. It is also what
makes the record safe to export and share.

**A base rate is recorded before a guard is built on it.** Every summarizer
reading is recorded, not only the ones that say the work drifted; every turn
end, not only the ones that hit the cap. A threshold chosen without the
denominator is a guess, and a guard that fires on a guess is one that
interrupts work that was fine.

**Joining the record to the conversation is a deliberate act.** A session's
record names the saved conversation it wrote, and the export can put the two
side by side — but only when asked, in so many words, because that is the
moment the export stops being content-free and the reader should know it.

Sessions can be recorded and never read; the record can be exported as JSON
and purged entirely.

### The record is kept for a window

The record is pruned at startup the way command history and generated report
pages are: sessions that ended longer ago than `observe.retention_days` go,
and their events go with them. A store that only grows is not a harmless
one: the record is written by every session unconditionally, and a table
nobody trims eventually makes the reading of it slow enough that nobody
takes the reading.

**The window is longer than history's, because the reader is a different
reader.** History's window is about a person remembering a command they ran
last month. This one is read by a comparison of two cohorts, and a comparison
of a change made in one quarter wants the quarter before it as well; the
default is a hundred and eighty days, twice history's, so that a question
asked about last quarter's edit still has both sides of it to answer from.

**Pruning a session takes everything hanging off it in one act.** Its events
go with it, and so do the sub-agent sessions it spawned, whose spend means
nothing without the session that spent it — an event or a child left behind
would be a row no reading could place, since every figure is drawn by joining
back to the session. Nothing is ever taken on account of an ending it never
wrote: a session with no end time is either still running, or waiting to be
closed by the next session's start, which brings it inside the prune's reach
the ordinary way, and it leaves early only when the session that spawned it
goes.

**The window and the switch stay apart.** `shhh observe purge` still means
everything, in one act, on purpose. Retention is the answer to "I do not want
a store that grows forever"; purge is the answer to "I want none of this",
and a window that quietly did the second thing would be the one setting in
the product nobody could trust.

### The record can leave this machine

Set `otel.endpoint` to a collector and every session is also sent to it over
OTLP: one span per session, one event on that span per row the record keeps,
and the totals the session spent as attributes on the span. It is off until
an endpoint is written down, and the endpoint is the only setting — what
gets exported is the record, and the record is fixed.

**It is the same record, not a second one.** Every event is reported to the
store and to the collector from the same arguments at the same moment, in
the same words: the outcome the row keeps is the outcome the span event
carries, and the codes are the closed sets the record already reasons in. A
dashboard built on the export and a reading taken with `shhh observe` are
therefore two views of one thing rather than two numbers that have to be
reconciled.

**The content-free posture is what makes this safe to switch on.** There is
no filter between the record and the wire, and there deliberately is not
one: a filter is a mechanism that can be wrong one day without anything
failing, and the reason none is needed is that there was never a path, a
prompt or a command in the record to remove. What crosses the network is
what the local table holds. The one thing that does not cross is the name of
the saved conversation a session is writing — that name is the join from the
record to what was actually said, and putting the two side by side stays a
deliberate act at the export command rather than something that happens to
every collector on the network.

**A collector that will not answer costs a session nothing.** The connection
is made when a session ends, not when it starts, so an endpoint that is down
is invisible until then; the attempt is bounded and never retried; and a
failure switches export off for the rest of the process and writes one line
to the diagnostic log. A session that ends because you started a new
conversation does not wait for the send at all — that boundary is crossed
while you are sitting in front of it, and a collector that is merely slow is
not allowed to be felt there. The record is a by-product of doing the work, so it
is never a reason for the work to wait or to stop. `shhh doctor` names the
endpoint when one is set, so where the record goes is visible without
opening the file.

### Every composition is one population

Every composition shhh runs — a session, a headless run, every sub-agent, and
the one-shot — writes the same rows into the same table. A number drawn from
one of them is a number about that surface and not about the product: a tool
error rate taken only where a person was watching says nothing about the runs
nobody was watching, and those are the ones a guard is built for.

So the one-shot is recorded as what it is — one request, so one turn, with no
rounds and no tools — rather than given a shape of its own, and it joins every
aggregate without any of them learning what a one-shot is. It is recorded when
piped into a script as much as when shown on a card, because the piped one is
the one nobody watched. A sub-agent is recorded with its own provenance rather
than its parent's, because a child routinely runs a different model under a
different prompt, and a row that borrowed its parent's would say it ran under
something it did not. Nothing that reads the record has to know which surface
wrote a row.

The words are the same everywhere too. A turn that stopped at its round cap is
`cap-paused` whether a person could grant it more rounds or not; a call the
safety prompt refused is `cancelled` on the same footing as one escaped off a
card. Where two surfaces would otherwise spell one event two ways, the record
takes the spelling that lets the two be added up, and leaves what actually
differs between them to the exit code and the screen.

### What a session ran under

A change to a setting is only worth making if the record can say whether it
helped, and it can only say so if it knows which sessions ran under which
value. So every session is stamped with the settings that were in force when
it started: the permission mode, the reasoning level, the round cap, whether
the summariser was taking readings and on which model and at what interval,
the classifier's model, and the containment profile. These are values, not
merely fingerprints, because the question a tuning loop asks is "sessions at
interval 10 against sessions at interval 20", and a hash has no order and no
meaning to group by.

They are an allowlist, and the allowlist is what keeps the record
content-free: every one of them is a mode name, a level, a model name, a
count or a profile name — a fixed identifier or a code from a closed set.
Much of the configuration is paths and several fields are secrets, and none
of that is stamped whole. It reaches the record only as one hash over the entire
effective configuration, which is enough to tell "before I changed
something" from "after" for a setting nobody thought to list, and never
enough to read the setting back. A field added to the configuration is
outside the allowlist until someone decides otherwise; it is inside the hash
from the day it exists, so a change to it is never silently invisible.

The mode is stamped at the start rather than only when it changes. A session
that runs from beginning to end in the configured default changes mode
never, and a record that only noted changes would say nothing about it —
which is also what a session that recorded nothing at all says. The change
is still recorded when it happens, because a session that went from manual
to auto halfway through is a different fact from either mode on its own.

A session recorded before the settings were kept reads as having none. It
does not read as having today's defaults, because a reader comparing two
sessions would take the fill for a fact.

### Whether it worked

Everything above describes what a session did. None of it says whether the
work was any good, and without that the record is a description rather than
an evaluation: a session that solved the problem and one its user gave up on
are the same row, and every rate beside them is a rate over both.

Two signals answer it, and they cost very different amounts.

**The quality gate's verdict is the one judgement nobody has to remember to
give.** The project's own checks run against a fingerprint of the tree, so a
verdict can never vouch for code it did not see, and a run that was blocked
by a broken setup or cancelled part-way is kept apart from one that failed
its checks — an infrastructure problem read as a failing test would move the
only objective rate the record has, so the pass rate is taken over the runs
that produced a verdict and the rest are named beside it. Every run that
finishes while the session is open is recorded with its verdict and the suite
that ran, whether the model asked for it or a person did.

The suite's name is recorded only when it matched one the project actually
defines. The model picks a suite by name and the gate runs without asking
anyone, so a name that matched nothing is text the model wrote, and it is
replaced with a fixed code rather than stored — otherwise there would be one
path where the model chooses what goes in a record that is content-free by
construction.

**A session's outcome is inferred, never asked for.** A card on the way out
of a session is answered by the people who were pleased and dismissed by the
people who were not, which is the wrong bias for the one field the whole
record is correlated against. So the outcome is read off how the session
actually left: the last turn to close finished its work (`completed`), was
cancelled (`interrupted`), or failed (`error`). A session that reached its
own exit with nothing finished is `abandoned` — the process survived to say
something, and what it says is that nothing came of it.

A turn that stopped at its round cap is not a close and reads as nothing at
all. A session waits there for a person to grant more rounds and a sub-agent
grants itself more and runs on, so a pause read as an abandonment would
describe every child that paused to take stock as one that gave up. If the
pause turns out to have been the end, the exit calls it abandoned then.

**The outcome is written optimistically and corrected, because the
interesting case is the one that cannot write.** A run the user gave up on
and killed is exactly the run whose exit path never executes, so an outcome
stamped only on the way out would record the sessions that ended well and
nothing about the rest — the same bias by another route. Instead every
closing turn stamps how the session has come out so far, and a killed process
leaves the last turn's reading behind.

That leaves `unknown` for a session that died before its first turn closed,
and it is a visible category rather than an abandonment. "The record cannot
say" and "nothing was finished" are different answers, and only one of them
is about the work; folding them together would quietly inflate whichever
figure a reader was about to trust.

### A rating is how you check the inference

An inferred outcome is worth what the checks on it are worth. `abandoned` is a
guess about what leaving without finishing means, and a few dozen sessions
somebody has actually judged is the only way to find out whether the guess is
any good. So the rating's job is to audit the outcome field rather than to be
it, and it is priced accordingly: it is one more card on a walk that already
exists rather than anything new to remember.

The walk asks about sessions beside commands, on the one card and the one set
of keys, newest first whichever kind each one is. Two lists asked one after
the other would spend the whole limit on commands whenever there were enough
of them, and the sessions would only ever be offered to somebody who had
caught up on everything else — which is nobody. `--commands` and `--sessions`
narrow it when only one of the two questions is wanted.

**A session is only offered when there is something to be reminded by.** The
record is content-free by construction: a session's row is token counts,
durations and codes, and nobody can say whether a fortnight-old row of those
was any good. What makes the question answerable is the conversation the
session left behind — its title, or failing that the first thing you said in
it — so a session with no saved conversation is not asked about at all. That
reminder is read for the walk and never written back; what the store keeps is
the answer, which is one bit.

**The accuracy figure stays what it was.** It is a figure about commands, and
folding a different judgement of a different thing into it would change what
it means without changing its name. The session answer lives on the session's
own row: it travels with the export, and the session's own page prints it
next to the outcome it is checking, which is the only place the two can be
read against each other one at a time.

There is deliberately no rate over the answers yet. A handful of ratings is
a sample to read one by one, and a percentage over six of them would lend
an authority the number has not got — the same reason the record refuses a
ratio over a cohort too small to divide.

### A comparison is two cohorts as rates

Everything above this is collection. The comparison is where the collection
becomes a decision: split a window's sessions on something the record
stamped — the prompt fingerprint, the config fingerprint, the build, the
model, or any of the settings a session ran under — and the two largest
groups are drawn side by side, with the direction and the size of every
change between them.

**Rates rather than counts, because two cohorts are never the same size.** A
week either side of an edit is not a controlled experiment: one may hold
twice the sessions, and every count would then read as a change that is only
a change in how much work went through. Everything is per turn, per call or
per session, and the session counts sit in the header, where they are the
denominators rather than findings.

**A cohort too small to read prints its count and no rate.** The threshold is
arithmetic rather than statistics: at ten sessions one unusual sitting is a
tenth of the answer, and at six, two of them going the same way produce a
forty-percent difference out of nothing at all. A row that prints such a
difference invites acting on it, and nothing about a percentage says it was
made of two sessions. Refusing the comparison is the whole treatment — a
caveat printed beside a number loses to the number.

**Rounds per turn is compared at equal outcome, and the qualification is the
metric.** A change that made turns shorter by making them fail is an
improvement on the unqualified figure and a regression on the real one, so
the rows are grouped by how the turn came out first, and each carries the
share of turns that came out that way beside its rounds.

**It does not attempt significance.** This is a local store of a few hundred
sessions. The rates, the denominators and the direction are the whole answer,
and a p-value over a sample this shape would lend the screen an authority it
has not got. No row draws a tick or a cross either: fewer rounds is an
improvement unless the turns got shorter by failing, and more interruptions
is a regression unless they are what stopped the person having to break in.

A window whose sessions all ran under one value is an empty state rather than
every rate having appeared or vanished at once, and the JSON carries the same
figures the screen draws, from the same list, so the two cannot come to
disagree.

## Where it all lives

One local embedded database file, on your machine, in the platform's
conventional data directory. Nothing here is sent anywhere unless you write
down a collector to send it to, and then it is the session record and only
the session record — the half that has no prompt, no path and no command in
it — that leaves.

## Related

- [`generation.md`](generation.md) — what produces history entries
- [`configuration.md`](configuration.md) — where settings live instead
