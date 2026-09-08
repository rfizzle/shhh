# Providers

shhh talks to several LLM vendors and is not loyal to any of them. Nothing
above the provider layer knows which one is answering.

## Interchangeable, not equivalent

Every backend exposes one operation — stream a completion — and registers
under a name the rest of the system resolves against.

What the interface does *not* do is pretend the vendors agree. Dialects differ
in ways that are not cosmetic: how a tool result is addressed back to the call
it answers, whether reasoning state must be handed back untouched, what a
failure looks like on the wire. Each implementation absorbs its own
differences, and where one has a rule that reads like a quirk, it is
load-bearing.

Those quirks are recorded in [`AGENTS.md`](../../AGENTS.md) rather than here,
with the symptom attached — because the symptom (the model silently repeats a
tool call) does not point at the cause, and the next person to "simplify" the
code will have the symptom, not the explanation.

## Resolution runs one way

Which provider and model answer is decided most-specific-first: an explicit
flag, then the environment, then configuration, then a default. Keys resolve
the same way, with the vendor's conventional variable in the environment slot.
Configuration's own answer for a key is the name of a variable to read it
from rather than the key itself, so the file names the credential and the
environment carries it; a key a file still holds directly is read after that
name and is warned about.

Uniformity is the feature. A user who can predict where a value came from can
fix it; one who must know which of four mechanisms wins for this particular
setting cannot.

## A gateway is a provider with addresses inside it

A hosted API is one name at one address. A gateway is not that shape: one
deployment serves different model families through different dialects at
different paths, under one key and one set of house rules.

Modelling that as several providers meant several copies of the key, the
headers and every rule, and it meant switching provider before switching
model — which is not a thing the user was trying to do.

So a profile holds endpoints. **An endpoint states only what differs** and
inherits everything else; a profile that repeated the key on every endpoint
would be the several copies again with more syntax. Headers merge with the
endpoint winning a collision, and rules concatenate with the profile's first,
so a quirk true of the whole gateway is written once and a quirk true of one
address sits with that address.

The profile's own fields are the default endpoint — where a model no endpoint
claims is sent. That is why the profile keeps a base address even when every
model is routed: it is the answer to a question every session can ask.

An explicitly declared model id beats a pattern match, always. Naming a model
and an address in one breath is the user being specific, and nothing overrides
being specific.

## A session never starts without thinking

A session that says nothing about reasoning starts on medium. Off is still a
level — it is what a `--reasoning off`, an `SHHH_REASONING=off`, or a
`reasoning = "off"` in the file asks for — but it is no longer what silence
asks for.

The previous default was off, on the argument that a session which never
touched the key should get exactly the requests it got before the key
existed. That argument protected old sessions from a new field and was right
while the field was new; it stopped being right the moment every current
model reasoned by default and only shhh's requests did not. A tool that
quietly asked less of a model than the model's own vendor did was not being
conservative; it was being worse, on every turn, for everyone who had not
found the setting.

Medium is the rung every provider has, the one that costs a working budget
rather than a ceiling, and the one the vendors themselves reach for as a
default. Anything more specific is a choice, and choices are the user's.

## A level is fitted to the model before it is sent

The session picks one of six levels — off, low, medium, high, xhigh, max —
and each provider translates. Translation has two halves and both are the
provider's problem, not the session's.

The first is spelling: a named effort on the Responses API and on chat
completions, a named effort under adaptive thinking on the current Anthropic
models, a token budget on the older ones and on Gemini. The second is fit: a
rung the model lacks becomes the highest one it has, and a model with no
reasoning knob at all is sent no field, whatever was asked, because a
`reasoning_effort` on a model without reasoning is a refused request.

Fit is why the default can be medium without breaking anything. gpt-4o gets
no field; a budget-only Claude gets a budget; a model that always thinks and
is asked for off is sent nothing, which is the most off it has. A session
never has to know which of these it is talking to, and a level set once
survives a `/model` switch to a family that spells it differently.

The rungs a model has are what the cycle key walks and what `/reasoning`
offers. A level that fit would only lower is not a level worth landing on.

Off is not the same as no thinking. It means no field goes out, and a model
whose own default is to think then thinks at whatever depth it likes. On a
turn that costs nothing, because a turn's output is not capped. On a bounded
call it is the whole failure: every dialect spends the thought and the answer
from one ceiling, so a call sized for its answer alone runs out mid-thought
and comes back with nothing in it. That is why the calls around the turn ask
for a level outright rather than leaving it unsaid, and why their ceilings are
sized for a thought and an answer together.

## Thinking goes back to the model that did it

The reasoning behind a tool call happens before the call, and the round that
reads the result is a different request. So the thinking has to travel, and
every dialect that produces any is handed its own back untouched — unread,
unedited, in whatever form it arrived in.

Two of them insist. Anthropic's dialect refuses a follow-up whose assistant
turn asked for a tool and dropped the signed thinking behind it, and Gemini's
thought signature rides the call itself and means the same thing. The third,
the Responses API, accepts the request either way, which is what makes it the
one worth stating: nothing fails, the model simply works out again what it had
already worked out, once per round, and the cost is paid in tokens and in the
quality of a long turn rather than in an error.

It is also the only one that has to be asked. That API can keep a response's
reasoning on its own server, and shhh keeps nothing there — the whole
conversation goes out every turn, so retention would buy nothing and would
leave a copy of the session on somebody else's disk. The request asks for the
thinking to come back sealed instead, and sends the sealed item out again
ahead of the calls it explains. A round that comes back without one is sent
none: an unsealed item is a pointer into a response nobody kept, and naming it
is a refused request in place of a round that would merely have thought twice.

Asking and sending back are one decision. A model that is not being asked to
think is sent no thinking either — a conversation that crossed a switch to
such a model still carries the thinking of the rounds before it, and handing
that over would describe an item the model never wrote and cannot take.

### Only the chain being worked on now goes back

Travelling is not the same as travelling forever. What has to go back is the
reasoning behind the calls this round is answering; the rounds before it are
finished, and their thinking is a copy of a plan that has already been
carried out.

It is not free to keep sending. Anthropic's published pricing counts
"thinking blocks from prior assistant turns that remain in context" as input
tokens, and its preservation table puts every model this product would put a
session on in the group that keeps all of them by default rather than the
older group whose API strips them. The Responses API says the opposite thing
and reaches the same place: outside its newest models it carries an earlier
turn's tokens forward without rendering that turn's reasoning into the next
sample, and its own guidance is to pass back the reasoning items since the
last user message and no more. So on one dialect the history's thinking is
paid for on every round for the life of the session, and on the other it is
sent and not used.

That is the category nothing else here can reach. A trim rewrites tool
results and never an assistant turn, so on a long thinking session the one
part of the conversation that grows every round is also the one part no
recovery touches.

So the cut is the last user turn, which is where the current chain starts,
with one turn of slack: a round boundary can append a message of its own — a
notice that the working tree moved, an interruption — after the assistant
turn that asked for a tool, and cutting at that message would send the call
with none of the thinking behind it, which is the refusal the blocks are
carried for in the first place.

The cut only ever moves forward, and that is what makes it safe on the
dialect that checks. A thinking block records which block came before it, so
removing them from the front of a history, oldest first, leaves every later
block valid, while removing one from the middle invalidates all of them —
a 400 in place of the round it was trying to make cheaper.

Moving it is not free either, and the price is the prefix cache: the round
that drops a turn's thinking sends different bytes at that position, so
everything from there on has to be written into the cache again instead of
read. That is why the cut is the user turn and not the round. Within a turn
it does not move at all — and a turn is where the rounds are, up to the whole
tool-round cap of them — so what a moved cut costs is one invalidation at the
moment a person has just finished typing, and what it buys is every round
after it carrying a smaller prompt.

**Recorded basis, and what is still owed.** The above is what the two vendors
publish, read on 2026-09-08; it is not a measurement. The measurement this
asks for — one response's reported input count with and without the history's
older reasoning, on each dialect — has not been taken, because it needs
credentials against both. It is worth taking twice over: it would say what
the saving actually is, and it would settle the trade against the
invalidation above, which is the one part of this that could come out
negative on a session of short turns. It is also the only thing that would
catch a vendor changing this without changing the sentence.

## A bounded call runs on the small model

The permission classifier, the session summary and the title it shares are
judgements over evidence the session has already assembled, and they run
often — the classifier on every gated call, carrying the recent conversation
with it. So they default to the small model the provider names beside its
default one, and only fall back to the session's own where the provider has
none to name: a local endpoint serves whatever weights were pulled, and
guessing a name there is a request that 404s.

Naming the model explicitly still wins. A session that puts a model in the
classifier or summary setting gets that model, which is what a person reaches
for when the small one is judging badly.

## A bounded call asks for the shape of its answer

The permission classifier and the reading that turns a session into backlog
items do not want prose. Each wants one object with named fields, and each
has always asked for it the same way: describe a tool, offer it, and read the
arguments the model wrote into the call. Where the model answered in text
instead, a parser looks for the object in what it said.

Describing a tool was a way of spelling a schema, not a request to do
anything. Every dialect now has a field that spells one outright, and the
answer is checked against it before it is sent — so a missing key, an
invented one or a truncated brace is not something the reader has to handle
at all. Where that field exists it is the better ask.

So a bounded call offers both, on the same request, and the provider chooses:
a model that takes a schema is sent the schema and no tools, and every other
model is sent the tools, exactly as before. The caller never has to know
which kind of model it drew. That is what makes the fallback free — nothing
is switched off where a schema cannot be used, because the path that was
there is still the path, down to the parser that reads the reply.

**A schema and tools are alternatives, never a pair.** One dialect refuses
the two together outright, and on the ones that accept them the model may
answer with a call the schema does not describe, which is the failure the
schema was asked for in order to prevent. So a request carries one or the
other, and the schema wins where it can be used, because it is the more
specific of the two.

What goes out is worth saying per dialect, since they agree on nothing but
the idea. Chat completions and the Responses API each take a named schema
with strict validation switched on, in differently-placed fields. Gemini
takes the schema beside a declaration that the answer is JSON, and ignores
the schema without it; it has nowhere to put a name, so the name it is given
is not sent. The Messages API takes it under the same output configuration
that carries the thinking effort, so the schema and the level have to be able
to arrive on one request without displacing each other.

Strict is why the schemas a bounded call declares close every object and
require every key: strict validation is refused on a schema that leaves
either open, and a section a reading has nothing to put in comes back as an
empty list rather than as an absent key. The tool path is just as happy with
a closed schema, so there is one schema per call and not two.

The providers that point at somebody else's endpoint — a local runtime, a
gateway speaking one of the OpenAI shapes — are sent no schema, for the
reason they are sent no reasoning effort either: what answers there is
whatever the operator installed, and a field it has never heard of is a
refused request rather than a looser answer. They keep the tool.

Which models take a schema is decided where the thinking level and the output
ceiling are decided, from the same description of the model. The one
difference is that the downloaded table has no column for it, so that answer
comes from the by-family floor even for a model the table otherwise
describes — a field nobody fills reads as "no", and "no" for every model the
table knows would be the feature switched off by silence.

What "takes a schema" means is narrower than it sounds: it is a claim about
the request that actually goes out, not about the vendor's feature list. A
generation that constrains an answer only through a field shhh does not
write is a generation that takes no schema here, and it gets the tools. So a
model nothing recognises gets the tools too, and that is the right way
round — the tools are what every model takes, while a schema sent to a model
that cannot take one is a refused request, and one of those two mistakes is
free.

## Model data is fetched, and a snapshot ships

One public table carries what shhh needs to know about a model: what it
costs, how much context it has, and how it spells its thinking level. One
download serves the spend meter, the context gauge, and the reasoning
ladder, and the three cannot disagree with each other because they are one
file.

The table is downloaded once a day, quietly, and nothing waits for it. What
is already on disk answers the process that asked, and the download runs
behind it for the next process to read — because every process pays for a
refresh and almost none of them need today's prices to do their work. Asked
for by hand, it does wait: `shhh update` downloads it now, with a release
check for the binary alongside or without one under `--data`, and somebody
who asked is owed the answer including the error.

A download that does not land is remembered for an hour. A failure changes
nothing else — the cache it would have replaced is deliberately left alone —
so without that memory the next process reads the same stale cache and asks
again, and a machine with no route to the table re-learns that fact in every
process it starts. An unattended run is a fresh process per stage.

A snapshot of the table, trimmed to the providers shhh speaks and the fields
it reads, is built into the binary. It is the floor under the download: a
fresh install answers before its first fetch, an offline machine answers
after a failed one, and a download that does not parse is not written over a
good cache.

A gateway profile can say it outright. Its declared models take a reasoning
shape beside their prices and context window, and a declaration outranks the
table the way a declared price does — including a declaration of none, which
is a statement about a model, not the absence of one. A private gateway's ids
are exactly the ones the public table will never learn.

An endpoint can answer for itself, and about the context window it does. A
runtime that serves the weights is asked what length it loaded them at, in
the same request the model picker already makes of it, and that answer
outranks the table. Not because the table is unreliable, but because for a
self-hosted model it is silent: the public table keys those under a gateway's
id, and a local runtime reports the name the weights are installed under,
which no public table has ever seen. Most runtimes report no length at all,
and they cost nothing for being asked — the question is asked once, in the
background, and everything reads the table as before until an answer lands.

Below the table there is a floor by family for the models the table has not
caught up with. A brand-new Claude is sent the shape the current Claudes
take, a brand-new GPT the shape the current GPTs take; the table overrides
the floor the day it learns the model. Wrong-by-family costs one refused
request; wrong-by-silence costs a model asked to think less than it can, on
every turn, until someone notices.

The floor carries the context window as well as the thinking shape, and it
names the self-hosted families beside the hosted ones for the same reason the
endpoint is asked first: a bare weight name is precisely what the table
cannot key. Here the two ways to be wrong are not symmetric either. A window
guessed low is a session throwing away findings it had the room to keep and
rediscovering them the next turn, quietly, for as long as the session lasts;
a window guessed high is one request the endpoint refuses or truncates, once,
visibly.

Which way each family leans follows from what an unrecognised name in it
means. A hosted model the table cannot describe was announced this morning
and is the largest thing its family has, so the family's figure is the
current generation's. A self-hosted one is whatever somebody chose to pull,
the tags an older and much smaller build answers to are still in every
library, and no endpoint is behind the floor to correct it — the runtime
most people run locally reports no window at all. So a self-hosted family
carries a row for an older generation wherever that generation is both still
widely served and much smaller than the current one, and a version written
the way weights are packaged is read as the version it is.

## How full the window is, corrected by what it cost

The window's size comes from the table. How much of it a conversation is
using does not: the only figure that answers that exactly is the provider's
own, and it arrives after the request, which is one request too late to
decide whether that request needed trimming first.

So the size is estimated from the bytes, at four of them to the token. That
is about right for prose and wrong in one direction for everything else a
coding session carries — source, JSON, diffs and stack traces all tokenize
nearer three bytes to the token, and they are most of what fills a long
session. Under-counting means the window is fuller than the meter says, the
trim fires later than it should, and the request that overflows is the one
nobody was warned about.

Counting for real would mean a token-counting round-trip before every
request: a request spent to find out what a request will cost, on the one
dialect of five that offers the call in a comparable shape. The correction is
free instead. Every response reports what the prompt it just read actually
came to, and the estimate for those same messages is already in hand, so the
ratio between them is a measurement nobody paid for. It is folded into a
running factor that scales later estimates, weighted so that three responses
land near a steady ratio and no single request — a cache-warming first round,
a turn carrying three screenshots — can own the figure.

The factor is a fact about a tokenizer, so it belongs to one model and starts
over when the session switches: a stale correction is worse than none,
because it is confident. It is bounded at two bytes to the token in one
direction and eight in the other, which is wider than any text a conversation
carries; a ratio outside that is a report describing something other than
what was counted, and against that the last good factor is the better guess.
A provider that reports no usage at all teaches it nothing, and such a session
runs on the plain estimate exactly as it always did.

None of this touches a reported figure. Where a report has arrived it is used
as it arrived — it is the measurement the factor is derived from, and scaling
it would be converting a number into itself.

A report also stops where its request stopped. It counts the messages that
request carried, and it arrives before the round's answer and the results of
the tools it called join the conversation. So it is held against the list it
described, and everything appended after it is estimated on top and corrected
like any other estimate. Letting it stand for the whole conversation is how a
round that returned 400 KB of tool output moved the occupancy figure by
nothing at all: the trim that exists for exactly that round declined to fire,
and the request after it went out oversize on a number the provider was quoted
for. What is anchored is the prompt the provider counted, not the prompt plus
what the model wrote back — the answer becomes a message a moment later, and
the estimate counts it then.

What is estimated includes the thinking. On a thinking model it is the
fastest-growing thing a round adds, and an estimate that left it out was
calibrated against a quantity the request does not contain — with the gap
absorbed into a single correction factor applied to everything, which papers
over the total while distorting every part of the breakdown. It is an
over-count on the two dialects that now replay only the current chain's
thinking, and exact on the one that replays all of it; of the two directions
that is the safe one, because an over-count trims early where an under-count
sends the request that overflows.

One consequence is worth stating plainly: a resumed session and a live one at
the same message count are not the same request. Nothing keeps the reasoning
across a save — a conversation read back off disk carries none — so it
estimates lower, and it is right to, because the request it will send is
smaller too.

Which kind of number a figure is — a report, a report with the rounds since
estimated on top of it, a plain estimate, a corrected one — is stated wherever
it is shown, because a number that quietly changed what it means is worse than
either a guess or a measurement.

## A request says whether a tool may be called

A request that offers tools also says what may be done with them, and the
answer is one of exactly two things: the model chooses, or the model may not
call one at all. Both are honoured by every provider, because a caller that
cannot rely on the second has to carry its own recovery for a model that
ignored it — and where the second was not honoured, that recovery is what a
reader saw instead of the answer they asked for.

Forcing a *particular* tool is not the third option, and deliberately so. The
newest models refuse a forced choice outright, so a harness leaning on one
would be built on something being withdrawn. Where a specific tool is wanted,
the prompt asks for it.

Where the difference is load-bearing is a request for prose from a session
whose whole shape says work: a summary of a conversation that ends in tool
results, a handoff from a child that has just run out of budget. The
instruction is the newest turn in a transcript of rounds, and a model reading
it as one more round answers with the call the round was about to make.
Saying so on the request costs nothing and removes the retry.

Saying so is also not the same as taking the tools away. The tools lead the
part of the request a provider caches, so a request without them shares
nothing with the session's other requests and is read from scratch — paying
for the whole opening again to avoid one retry. The tools stay; only the
permission changes.

## A reply says why it stopped

Every dialect reports why the model stopped writing, and for a long time
shhh read none of them. Four of those reasons are worth telling apart and
the rest are not, so the four are named — the reply is finished, it is owed
tool results, it was cut off at the model's output ceiling, or the model
declined to give it — and everything else a dialect names is one word for
"something the harness does not act on differently". Five values, closed, in
shhh's own vocabulary rather than four vendors' overlapping ones.

The ceiling is the one that changes what happens. A reply cut off at it is
not the model's answer, it is the first half of one, and nothing downstream
can tell the difference from a look at the words: the sentence simply ends.
So a session says so, and offers to have it finished. The reply is a real
answer as far as it goes, so it joins the conversation and the transcript
like any other, and the row under it is the same offer a dropped stream gets
— the same key, the same shape — with the one difference that a truncated
reply cannot be asked for again from the top, because the model's own half
answer is already standing under the question. Continuing sends the
instruction alone.

**Where nobody is watching, the run continues it by itself, once.** A
scripted run and a sub-agent have nobody to ask, and a half sentence returned
as the run's result is the worst way to end: it looks like an answer to
everything downstream of it. So the run appends the instruction and asks
again, once per round, and lets the second attempt stand whatever it is — a
turn free to ask for one more paragraph every time it filled a budget would
have no ceiling at all in the one place nobody is there to notice. A round
that ran tools starts fresh, because what the bound exists to stop is a turn
spent finishing one answer, not a long turn. What the turn hands back is both
halves: the second was written to carry on from where the first stopped, so
the rest of an answer returned on its own would be a sentence beginning in
the middle. Standing is not the same as
being whole: a second attempt the ceiling also cut says so alongside the
answer, because what reads it downstream — a backlog run grading a stage, a
lane reading its writer — cannot see it in the words, and everything that
reads it is somewhere nobody is watching.

**The silent case is the tool call**, and it is why this is worth doing at
all. A reply that stops in the middle of writing a call leaves half a JSON
object. Half an object is not a request — it reaches the tool as malformed
input and gets answered as though the model had asked for something — so the
unfinished call is dropped and the calls the model did finish still run. What
changed is that the drop is now said out loud on every surface: before, the
round simply had one fewer call in it, the model waited for a result that was
never coming, and the turn closed as if it had answered.

One dialect makes that drop harder than it sounds. Its SDK launders a cut-off
argument string as it accumulates — a call whose JSON never closed comes back
with an empty object for its input, which is valid JSON and indistinguishable
from a call that genuinely takes no arguments. Handed that, the filter the
other dialects use would pass a file write with no path as a request the
model made, so on that dialect the argument fragments are judged as they
arrived instead of as they were accumulated. Another dialect never sends a
call in pieces at all: what it delivers is whole by construction, and a
ceiling costs it only the part that was never sent.

None of this adds a ceiling. A session asks for no output limit and still
does; what it gained is knowing when it hit the one the provider applies
anyway.

## Tool arguments arrive as fragments

A round that ends in a tool call spends most of itself writing that call. The
arguments are JSON and they stream in like everything else, but until the last
brace closes there is no call — so a round rewriting two hundred lines reports
nothing for as long as it takes to write two hundred lines, and every surface
above it has the same nothing to draw.

The fragments are reported as they arrive. Each one names the call it belongs
to and carries the bytes, and nothing else: the tool's name, the finished
arguments and the order the calls were made in are all on the terminal event,
which is the only place any of them is complete. A fragment is never
dispatched, stored or replayed, and a stream that breaks in the middle of one
still hands back only the calls that are whole — the fragment is a reading of
progress, not a claim about what the model asked for.

The four dialects write them differently and the reading is the same in all
four. Two send a call in pieces and name it once, when it starts, so the
fragments after that are addressed from what has already been accumulated.
One sends the pieces under the id of the output item rather than the id a
result is answered with, so the two ids are paired when the item opens, and an
endpoint that never opens the item reports no progress rather than progress
under an id that would address the wrong call. The fourth never breaks a call
up at all: its arguments arrive whole, and the whole is reported as one
fragment, in its place in the stream, so a reader that follows fragments does
not have to know which dialect it is following.

## The prompt prefix is paid for once

A coding turn sends the same opening over and over. The system prompt, the
project's context file, the memory block, the skills catalog and every tool
schema are fixed for the life of the session, and the conversation under them
only ever grows at the end — a round appends the assistant's request and the
results that answer it, and touches nothing before that. By the fiftieth round
the unchanged head of the request is most of what is being sent, and it has
been read and billed fifty times.

Where a dialect can be told which part is stable, it is told. The marker goes
in three places: after the fixed head, so the tools and the system prompt are
one reusable block; and at the end of the last two turns, so each round reads
back what the round before it left behind and extends it. The two rolling
marks rather than one are for the round that appends more blocks than the
provider will search back through on its own — a round that ran eight tool
calls in parallel — where a single mark at the very end can miss the boundary
the round before it wrote.

This is a statement about billing, not about meaning. Nothing is added to the
request, nothing is removed from it, and a provider that ignores the marker
answers exactly what it would have answered. That is what makes it safe to
send unconditionally to anything speaking the dialect, including a gateway
that has never heard of it.

The dialects do not agree about this, and which is which is worth stating
rather than leaving to be discovered. OpenAI's two — chat completions and the
Responses API — cache the prefix they recognise from the last request without
being asked, and so does Gemini. They are annotated nowhere and need not be:
asking would change nothing. Anthropic's Messages API caches only what the
request asked it to, and a request that asked for nothing caches nothing, so
every request to it is annotated.

A gateway is neither, and it is the case that was wrong for longest. The
request leaves in the OpenAI shape and arrives wherever the model name routes
it, so what decides is the model and not the dialect the request was written
in: a gateway request naming a model on the Messages API carries the same
three markers, written in the shape that gateway takes them in, and one naming
any other model is sent exactly as it would have been. The model shhh's own
gateway provider defaults to is an Anthropic one, so for as long as this was
decided by dialect a session there re-read its entire opening at full price on
every round of every turn.

Every path that sends that shape gets it, not just the built-in one. A
configured gateway profile is the documented way to reach Anthropic models
through somebody else's endpoint, so a profile whose route speaks the OpenAI
dialect is annotated exactly as the built-in gateway is — the rule belongs to
the wire, and every path that writes to that wire shares it.

A marked prefix does not live forever, and every mark in one request is given
the same life. The two lifetimes on offer are five minutes and an hour, each
measured from the last read, and an hour is what a session takes unless it
says otherwise.

There is an argument for a shorter life on the rolling marks: they are
replaced every round, so the dearer write buys a prefix the next round
supersedes. It holds only while the rounds keep coming, and they do not. A
session pauses constantly — the person is reading a diff, answering an
approval card, away from the desk — and an unattended run's stop-and-wait
makes the pause routine rather than exceptional. One pause past five minutes
and the whole conversation under the head has expired, so the round after it
rewrites the body from nothing, where the longer life would have paid its
premium on that round's delta alone. The pause is what decides, and the pause
is systematic. One lifetime is also one rule instead of two: the API takes the
longer-lived breakpoints first, so a request carrying both had an ordering to
keep.

Writing for the hour still costs more than writing for the five minutes, which
is why the lifetime is a setting rather than a constant: a session that is
never idle, or one that is short, can turn it down and pay the lower write on
everything.

A saving that is real has to be visible, so what a request served from the
cache actually cost is what the session's ledger charges for it, at the
provider's own reduced rate rather than at the price of reading it fresh.

## Failures are classified before they are surfaced

Every provider error is mapped into a closed set — unauthorised, rate limited,
quota exhausted, overloaded, context too long, no such model, network,
malformed, cancelled, and one class for everything the table has no case for.

A model the provider does not serve earns a class of its own rather than
falling to the catch-all, because the catch-all offers "try again" and the
next request would carry the same id. It is also the failure a near-miss
spelling produces — a gateway writes a generation with a dot where the
vendor's own API writes a hyphen — so the row names the id and points at the
picker.

The classes belong to the provider layer; what to *offer* about each one
belongs to the interface. That split is why a new provider inherits every
recovery path already built rather than needing its own error handling.

The catch-all class is deliberate. A message that could not be named still
gets shown, so an unrecognised failure is a row with the vendor's own words on
it rather than a dead session.

## A stall is waited out on one schedule

Three of those classes describe a request that never reached the model at all
— rate limited, overloaded, a connection that died before a token. Nothing
was answered, nothing entered the conversation, and asking again is the same
question rather than a second one, so those three are waited out and the rest
are not. A rejected key and a request that did not fit the window are the same
failure however long you wait.

The schedule is one schedule. A wait doubles off a second, floored at a
second so an implausibly short one still gives the window time to roll over
and capped at a minute because a longer wait is a decision for a person
rather than a countdown; a provider that names its own wait is believed over
any of it, since it knows when its own window turns. Onto that goes a small
random spread. Without it a fan-out whose children were all refused in the
same second are handed the same wait and come back in the same second, which
re-creates the limit they just sat out. The spread is only ever added, never
taken off, because a wait the provider named must not be cut short — and it
is bounded by the same cap, so the number a countdown shows is a number it
keeps.

The bound is on the whole stall and not on each request: three attempts by
default, and it is a request that is actually answered that clears the count,
not the passing of time. `behavior.provider_retries` sets it. Unset is the
built-in three; a larger number suits an unattended machine on a flaky link;
zero is a run that would rather see the failure than sit out a wait, which is
a different answer from leaving the key alone and is stored as one. The
setting reaches every surface that drives the loop, children included.

There is one schedule and not one per surface because three copies of "how
long, how many times" is three answers, and nothing fails when they drift
apart — a session and the children it spawned would simply start behaving
differently under the same limit. What differs is only how each surface says
it is waiting: a countdown you can press out of, a line on stderr, a lane
that reads *waiting*. Every attempt is also written to the diagnostic log
with its class and its wait, so a run that went quiet can be told from a run
that hung after the fact, when the screen it was said on is gone.

## A stream that stops writing is a failure

A request that is accepted, answered with headers and then never written to
again looks, from inside the loop reading it, exactly like a model thinking
hard. Both are a read that has not returned. Nothing in the reply says which
one it is, and nothing ever will: silence has no content.

So every turn's stream carries a deadline on the *gap between events*, and any
event pushes it forward — a token, a fragment of a tool call's arguments, the
model's thinking as it is written, a keep-alive the reader had no other use
for. What is being watched for is silence on the wire, never progress toward
an answer, which is why the deadline can be generous without being useless: a
reply that takes ten minutes to write is a stream that wrote something every
few seconds for ten minutes.

The deadline reaches the request that opens the stream as well as the stream
itself. An endpoint that accepts a connection and never sends its headers is
the same failure one line earlier, and it is the one the plain HTTP paths had
no protection from at all.

An expired deadline ends the turn as a network failure — the class that
already means the connection died on the way back, and one of the three the
[schedule above](#a-stall-is-waited-out-on-one-schedule) waits out and asks
again. That is the whole point of naming it that: a gateway that went quiet
for thirty seconds is very often answering the next request, and the
alternative to a retry here is a person noticing.

Two minutes is the default, and `provider.stream_idle_seconds` moves it. A
negative removes the deadline entirely, for a machine that would rather wait
indefinitely than lose a turn.

This exists for the runs with nobody in front of them. In the TUI a stalled
stream is a person pressing Esc after twenty seconds of nothing; an unattended
run, a served loop, a queued stage and a delegated child each have no such
person, and the process holds its worktree lock and its budget until something
outside it decides to intervene.

## Related

- [`../architecture.md`](../architecture.md) — why the boundary is here
- [`configuration.md`](configuration.md) — where the settings live
- [`../interface/surfaces.md`](../interface/surfaces.md) — the recovery row
