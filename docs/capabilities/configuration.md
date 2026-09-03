# Configuration

## One file, one format, one resolution order

Settings live in a TOML file in the platform's conventional configuration
directory. Every value resolves most-specific-first: an explicit flag, then
the environment, then the file, then a default.

Four keys have all four ranks, and they are the ones a single run is most
often started with a different answer to: which provider, which model, its
key and its reasoning level, all under `[provider]`. The provider's base URL
has the environment rank and no flag. Every other key is the file or the
default — there is no flag and no environment variable for it.

No setting reverses this order. That uniformity is worth more than the
flexibility of special-casing, because it is what makes a wrong value
*findable*: a user who can predict where a value came from can fix it.

The same reason decides what happens to a key the file names that no setting
reads. It is refused, with the file, the key and the nearest key it might have
been, and nothing starts until it is fixed. A file that loaded past it would
leave the default in force while the person reads their own file and cannot
see why — the one failure this arrangement exists to prevent — and a warning
is not enough, because a warning on the alternate screen is painted over and
one on stderr before a headless run lands in a log nobody tails. The doctor's
config row carries the same refusal, so that is where to read it.

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

## A write changes one line

A write from any surface — `config set`, the editor's `[w]`, a slash command
that saves its answer, `shhh mcp add` — changes the key it was asked to
change and leaves every other byte of the file as the person wrote it: the
order of the sections, the blank lines, and above all the comments. The file
is theirs. A three-line file with a comment saying why is a record of a
decision, and a write that hands back eighty lines with the comment gone has
destroyed the record to save one value.

A key set to its zero value is taken out of the file rather than written as
zero. Unset means the default, and for a few keys zero and unset are not the
same fact — a negative round limit is a limit removed, and a written zero
beside a comment saying "unset means the default" is a value nobody chose.
The file never holds a zero the person did not type.

A file that does not exist is created holding only what was written, under
its section header. A file that does not parse is refused untouched: the
person is told, and the write is theirs to make once the file reads.

## A value is refused before it is written

A setting takes a shape — a number, a boolean, one of a few words — and a
value that is not that shape is refused, naming the key and what it wanted.
Nothing is written and the file is left exactly as it was.

The alternative is worse than it sounds, because the natural failure of a
loose parser is not an error but a *plausible* value. A retention measured in
days, given a word, becomes zero; zero is not "no bound" here but "keep
nothing", and the next startup prunes the history the setting was meant to
protect. A flag given `yes` becomes false, which is the opposite of what was
asked for and looks in the file like a decision. Both report success. Neither
is visible until the thing they turned off is missed.

The words a value may be are judged by the same code the running session
judges them with — one list of permission modes, one of reasoning levels, one
of containment profiles — so `config set`, the editor and the slash commands
that save their answer all refuse the same value for the same reason. A
second list, written beside the writer for convenience, is a list that
eventually disagrees with the first, and the disagreement shows up as a value
that saved cleanly and does nothing.

A negative is a value where a key says what a negative means — no round
limit, no command timeout, an interval that never widens — and a refusal
everywhere else. Elsewhere a minus sign is a slipped finger, and what it
writes is a ceiling nothing can satisfy: a setting that reads as present and
turns its feature off.

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

What a checkout carries layers over what the user has: its agent profiles, its
MCP servers and its skills, each shadowing the user's by name, and that
context file. Settings are not yet among them — the settings file is the
user's alone, and a checkout's copy is not read. When they are, a value the
checkout overrides will be said so by the surface rather than shown as the
winner alone — otherwise a user reads their own configuration and cannot see
why it is not what they set.

## The mechanism is code, its wording is configuration

A session interrupts its own turns. It asks a long one what it has got, and
it tells one that has wandered which instruction it was judged against. What
those interruptions are for, when each fires, and that a steer asks rather
than accuses are decisions about the product, and they stay in the program.
How many rounds pass, how far the interval widens, how much of the
instruction is quoted back, and the sentences themselves are none of those
things, and they are exactly what someone tuning a session has to be able to
change. So they come out: four numbers as keys, and four wordings as files
the configuration names.

The line is drawn there because of what it costs to be wrong on either side.
A wording in the program can only be changed by a build, and a change nobody
can afford to make is a change nobody makes. A mechanism in the file can be
half-configured into a shape the program was never written for, and the
failure arrives in a session, in front of a user, rather than in a review.

**A wording that cannot be read stops the session.** Not a warning and not a
fallback: the path, the reason, and no session. The failure this guards
against is a session running the built-in steer while the person who wrote
the path believes it is running theirs — and then a fortnight of comparison
across two groups of sessions that are in fact one group. Nothing read from
the record afterwards recovers from that, so it fails at the start, where the
person who wrote the path is still watching.

An empty file is that failure wearing a disguise, and is refused the same
way. Nothing has an empty wording to send, so a file a truncated write left
with nothing in it would silently mean "not configured" — the built-in words
back in place, and a record that says the session overrode nothing.

**A wording is part of what a session was sent, so it is part of the
fingerprint.** The record already fingerprints the system prompt as it went
out, which is what puts sessions on either side of an edit to it. A steer file
is sent to the same model in the same session by the same mechanism, and an
override that did not divide the record the same way would be a dial with no
instrument on it. A session that overrode nothing fingerprints exactly as it
did before, so turning the ability on divides nothing by itself.

**The substitutions are checked before anything runs on them.** A wording is
prose with a few values dropped into it — the rounds that have gone, the
instruction to quote back — and a misspelled one is invisible: it reaches the
model as literal punctuation, the value it stood for never arrives, and the
wording still reads like a wording. So a file naming a substitution that does
not exist is refused the way an unreadable one is, and so is one naming a
substitution that belongs to a different wording. Two of the four are sent
exactly as written and take none at all, which makes any of them a mistake
there.

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
