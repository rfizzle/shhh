# Configuration

## One file, one format, one resolution order

Settings live in a TOML file in the platform's conventional configuration
directory. Every value resolves most-specific-first: an explicit flag, then
the environment, then the file, then a default.

No setting reverses this order and none can be set in only one place. That
uniformity is worth more than the flexibility of special-casing, because it is
what makes a wrong value *findable*: a user who can predict where a value came
from can fix it.

## One layout everywhere

Three directories, and the same three on every platform: settings where a
person edits them, state where the program keeps what it recorded, cache for
whatever can be fetched again. The XDG environment variables decide where each
one is when they are set, and there is a conventional default under the home
directory when they are not.

There used to be a second layout — macOS read a single `Library` directory for
all three — and it cost more than it bought. "Where are my settings" became a
question with two answers and no way to tell from outside which one a given
machine would give; a Mac with an XDG directory *and* the platform one had two
config files, only one of which was ever read; and it was the one place where
the settings a person edits and the database they never open sat in the same
directory, which is exactly the distinction the other platforms were keeping.

Conventional per-platform placement is a real principle and this is a real
departure from it. It is made once, in favour of the property that a user can
predict where their own settings are — the same property the resolution order
exists for.

Windows is included in "everywhere", and is the same departure rather than a
second one: settings sit under the home directory there too, not in AppData.
It is what git, ssh and half the tools on the machine already do, the XDG
variables still decide it when they are set, and one answer to "where are my
settings" is worth more than each platform's own.

## A migration is a doctor check

Old layouts are not read. A version that changes where something lives stops
looking in the old place entirely, and what would have been a permanent
fallback becomes a check in `shhh doctor` instead.

The check states that this machine is still shaped the old way, what that
costs in the words of what the reader will find missing, and exactly what
would move. Where the change is one the program can make correctly on its own
it also offers to make it, after asking. Where it is not — two files both
claiming to be the config, and only the person who wrote them knowing which —
it says so and leaves the decision alone.

The two alternatives are worse in opposite directions. Migrating silently at
startup makes a change the reader did not ask for and cannot watch, on the one
run where they are least able to reason about it. Reading both layouts forever
means a decision that was supposed to be over is paid for on every startup,
indefinitely. Detecting costs a stat in a command nobody runs in a loop, and
the reader finds out at the moment they went looking for why something was not
where they left it.

## Editing it asks first

The interactive editor is a surface that changes your machine, so it is one
that confirms before it writes.

Diagnostics do not repair. When a check fails it *names* the fix and shows it
to you; applying it is a separate act on a screen built to ask. A diagnostic
tool that silently repairs things is one you cannot use to find out what is
wrong.

A pending migration is the single exception, and it is one because it is not a
repair: nothing is broken, the machine is merely shaped an older way, and the
change is mechanical rather than a judgement about what you meant. It is still
offered rather than made — the same confirm the editor uses, on the row that
found it — so the rule that holds is the one about asking, not the one about
diagnostics being read-only.

## A failed check says what it will cost you

Diagnostics report in the words of the surface where the consequence will
appear — what an approval will look like, what will run as you — rather than
naming a missing component.

"Bubblewrap not found" tells a user who already knows what bubblewrap is
something they could have guessed. What they need is what changes about their
session, and that is stated instead.

## Project context is opt-in and lives with the project

A project can carry its own context file, created deliberately — by the
command that scaffolds one, or by accepting the offer a session makes on
first contact in a checkout that has none. Neither writes it on the way past.
Repository settings layer over user settings, and where a value is overridden
the surface says so rather than showing the winner alone — otherwise a user
reads their own configuration and cannot see why it is not what they set.

## A failure is written down

A refused request is a row on the screen for as long as the screen lasts.
The log is where it is still legible in the morning, and in another pane
while the request is failing — which is the case it exists for: you start a
tail, you run the thing that breaks, and you watch it break.

It holds what has no other surface. Every request a provider refused, in the
words the failure taxonomy already reads it in, and whatever a library writes
to the standard logger while the alternate screen is up — which would
otherwise be painted over a running session. It does not hold the
conversation: that is in the state directory too, and a log that repeated it
would be a second transcript with none of a transcript's structure.

Nothing is written until something goes wrong. A machine where nothing has
gone wrong therefore has no log file at all, and the reader is told that
rather than shown an empty file shhh made for itself on the way past. Once
there is one it is bounded, and the older generation is dropped when it fills:
a diagnostic that fills a disk is a fault of its own.

Two sessions write to one file, which is what settles how the writing works.
The bound is checked for each line rather than once when a session starts, or
an afternoon of being refused by one provider would have no bound at all; and
a session does not hold the file between lines, or it would go on writing
into a generation another session had already set aside — the failure you
opened the tail for being exactly the one that went missing. A line that
cannot be written is dropped rather than announced, because the only place to
announce it is the screen the session is drawing on, and the next line tries
again: a directory that could not be reached a minute ago can be reached
now.

It is state, so it lives where state lives, and it inherits what that costs
and what it buys — one layout on every platform, and the containment deny
mask, which is why an approved command cannot read the log any more than it
can read the database beside it.

## Related

- [`providers.md`](providers.md) — provider and gateway settings
- [`../architecture.md`](../architecture.md) — why resolution is uniform
