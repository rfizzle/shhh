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

## Sessions are conversations you can come back to

Chat and agent sessions persist and can be resumed — the most recent one, or
one you pick.

What comes back is the conversation, not a transcript that looks like it. The
work that was in flight, what was staged, where the session had got to: a
resume that restored only the visible text would look right and behave like a
fresh session, which is worse than not resuming at all.

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
- **You can see and delete all of it.** A tool that accumulates opinions about
  your project without showing you the list is a tool you cannot correct.

## Metrics are what it cost

Usage per model: requests, tokens, spend, latency and its tail.

Two rules make the numbers trustworthy. **A reading with nothing behind it is
left out** rather than drawn as an empty row — a fabricated zero is worse than
a gap, because a gap is legible as a gap. And **requests that never answered
are their own category**, because that is a cost you did not ask for and
folding it into the successes hides exactly the thing worth seeing.

Where no model can be priced, the split is over tokens, and it says so.

## Where it all lives

One local embedded database file, on your machine, in the platform's
conventional data directory. Nothing here is sent anywhere.

## Related

- [`generation.md`](generation.md) — what produces history entries
- [`configuration.md`](configuration.md) — where settings live instead
