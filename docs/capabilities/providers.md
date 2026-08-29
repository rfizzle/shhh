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
