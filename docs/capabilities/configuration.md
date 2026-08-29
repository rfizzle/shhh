# Configuration

## One file, one format, one resolution order

Settings live in a TOML file in the platform's conventional configuration
directory. Every value resolves most-specific-first: an explicit flag, then
the environment, then the file, then a default.

No setting reverses this order and none can be set in only one place. That
uniformity is worth more than the flexibility of special-casing, because it is
what makes a wrong value *findable*: a user who can predict where a value came
from can fix it.

## Editing it asks first

The interactive editor is a surface that changes your machine, so it is one
that confirms before it writes.

Diagnostics deliberately do not write. When a check fails it *names* the fix
and shows it to you; applying it is a separate act on a screen built to ask.
A diagnostic tool that silently repairs things is one you cannot use to find
out what is wrong.

## A failed check says what it will cost you

Diagnostics report in the words of the surface where the consequence will
appear — what an approval will look like, what will run as you — rather than
naming a missing component.

"Bubblewrap not found" tells a user who already knows what bubblewrap is
something they could have guessed. What they need is what changes about their
session, and that is stated instead.

## Project context is opt-in and lives with the project

A project can carry its own context file, created deliberately. Repository
settings layer over user settings, and where a value is overridden the surface
says so rather than showing the winner alone — otherwise a user reads their
own configuration and cannot see why it is not what they set.

## Related

- [`providers.md`](providers.md) — provider and gateway settings
- [`../architecture.md`](../architecture.md) — why resolution is uniform
