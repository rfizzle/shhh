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

## Model data is fetched, and a snapshot ships

One public table carries what shhh needs to know about a model: what it
costs, how much context it has, and how it spells its thinking level. One
download serves the spend meter, the context gauge, and the reasoning
ladder, and the three cannot disagree with each other because they are one
file.

The table is downloaded once a day, quietly, and `shhh update` downloads it
now — with a release check for the binary alongside, or without one under
`--data`. A snapshot of the table, trimmed to the providers shhh speaks and
the fields it reads, is built into the binary. It is the floor under the
download: a fresh install answers before its first fetch, an offline machine
answers after a failed one, and a download that does not parse is not
written over a good cache.

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

Most dialects need no marker at all, and get none: they cache on their own by
matching the prefix they were sent last time. A dialect is annotated here only
when annotating it is the only way to ask.

A saving that is real has to be visible, so what a request served from the
cache actually cost is what the session's ledger charges for it, at the
provider's own reduced rate rather than at the price of reading it fresh.

## Failures are classified before they are surfaced

Every provider error is mapped into a closed set — unauthorised, rate limited,
quota exhausted, overloaded, context too long, network, malformed, cancelled,
and one class for everything the table has no case for.

The classes belong to the provider layer; what to *offer* about each one
belongs to the interface. That split is why a new provider inherits every
recovery path already built rather than needing its own error handling.

The catch-all class is deliberate. A message that could not be named still
gets shown, so an unrecognised failure is a row with the vendor's own words on
it rather than a dead session.

## Related

- [`../architecture.md`](../architecture.md) — why the boundary is here
- [`configuration.md`](configuration.md) — where the settings live
- [`../interface/surfaces.md`](../interface/surfaces.md) — the recovery row
