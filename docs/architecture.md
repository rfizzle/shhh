# Architecture

The structural decisions, and what each one bought. Where these shapes live in
the tree is [`AGENTS.md`](../AGENTS.md) — this page is the reasoning, that page
is the map.

## One agent, several front-ends

The agentic loop is a **passive state machine**. It does not own a screen, a
goroutine, or a main loop; something else advances it one step at a time and
decides what to do with what comes back.

That inversion is what lets the interactive session and the scripted runner be
the same agent rather than two implementations that drift. A child agent
spawned by a parent is the same object again, driven synchronously. Every
behaviour that matters — the approval queue, the round accounting, the
repeat-call detection — is therefore written once and observed identically
everywhere, including in tests, which drive it the same way a front-end does.

The cost is that the loop cannot decide anything for itself. It cannot show a
prompt, cannot block on an answer, cannot retry on a timer. Each of those
becomes a state the caller has to handle. That is the trade, and it is worth
it: the alternative is an agent that behaves differently depending on who is
watching.

## Tiers, not permissions

Tools are separated by what they can do to your machine, and the separation is
structural rather than a flag checked at call time:

- **Read-only** tools change nothing and run without asking.
- **Execute** runs a command, and needs an answer — from the user, or from a
  policy that has already decided.
- **Mutating** tools write to disk, and need an answer unless the session's
  mode has granted it in advance.

The dispatch paths for these are *different functions*, not one function with
a branch. A read-only dispatcher that has no case for a mutating tool cannot
be talked into running one — not by a bug, not by a malformed tool name, not
by a model that has learned to ask nicely. A boolean can be wrong; a function
that does not contain the code cannot be.

This is the invariant most worth protecting in the codebase, and the easiest
to erode by accident, because merging the two paths always looks like a
simplification.

See [`capabilities/approvals-and-safety.md`](capabilities/approvals-and-safety.md).

## Providers are interchangeable and are not equivalent

Every backend exposes one operation — stream a completion — and registers
itself under a name the rest of the system resolves against. Nothing outside
that layer knows which vendor is answering.

What the interface deliberately does *not* do is pretend the vendors agree.
Dialects differ in ways that are not cosmetic: how a tool result is addressed
back to the call it answers, whether reasoning state has to be handed back
untouched, what a failure looks like on the wire. Those differences are
absorbed inside each implementation, and where one has a rule that reads like
a quirk, it is load-bearing — the ones that have bitten us are recorded in
[`AGENTS.md`](../AGENTS.md) with the symptom, because the symptom (the model
silently calls the same tool again) does not point at the cause.

A failure is classified into a closed set before it reaches any surface. The
classes belong to the provider layer; what to *offer* the user about each one
belongs to the interface. Splitting it there is why a new provider inherits
every recovery path already built.

See [`capabilities/providers.md`](capabilities/providers.md).

## Configuration resolves in one direction

Everything configurable resolves the same way, most specific first: an
explicit flag, then the environment, then the config file, then a default.
There is no setting that reverses this and none that can only be set in one
place.

The reason is debuggability. A user who can predict where a value came from
can fix it; one who has to know which of four mechanisms wins for *this*
particular setting cannot. Uniformity is worth more here than the flexibility
of special-casing.

See [`capabilities/configuration.md`](capabilities/configuration.md).

## State is local, single-connection, and boring

Sessions, history, memories and metrics live in one embedded SQLite file with
exactly one connection open to it. Concurrency is handled by the journal mode
and a busy timeout rather than by a pool.

Nothing here needs a pool — the workload is one interactive process — and a
second connection to the same file buys nothing while introducing a class of
lock contention that is miserable to reproduce. The constraint is deliberate
and should be left alone.

The binary is pure Go with no cgo, which is what makes a single static
cross-compiled binary possible. That rules out the conventional SQLite
bindings, and that trade has already been made.

## The screen is a rectangle, and so is everything in it

Terminal layout resolves once, into rectangles, and each renderer is handed
the rectangle it may draw into. A renderer that needs to know how wide it is
reads its rectangle; it does not subtract a width from another width.

Before this, geometry was a scatter of arithmetic across every surface, and
every new pane meant finding all of it. The failure mode of the old approach
is a surface that is correct at 100 columns and one character wrong at 132 —
which is exactly the bug nobody catches, because nobody has that terminal.

Clipping is a property of the rectangle rather than a thing each renderer
remembers to do, so a block that is too big is cut off instead of corrupting
the frame around it.

## Colour is resolved once, at the top

The terminal's actual capability is decided in one place, and every style is
built against that decision. Changing the palette or the profile rebuilds
every derived style rather than patching some of them.

The bug this prevents is the one where a surface was constructed before the
profile was known, keeps its original colours, and looks subtly wrong on a
16-colour terminal that nobody on the team is using.

See [`interface/principles.md`](interface/principles.md).

## Only one place speaks to the terminal

Asking a terminal what it can do means writing escape sequences and reading
replies. Exactly one component does that; everything else holds the answer as
a value.

Terminal capability probing is the kind of code that spreads — one more
question asked from one more place, each with its own timeout and its own idea
of what a non-answer means — and the result is a program that hangs on one
emulator in a way no one can reproduce. Keeping the wire in one component
means there is one thing to reason about and one thing to fix.

## Design lives outside the repository

The visual specification — tokens, components, artboards, and the guidelines
that constrain them — is a design-system project in Claude Design, and it is
normative. This repository implements it.

That direction is deliberate. Markdown re-drawings of an artboard become a
second source of truth that disagrees with the first, and the disagreement is
discovered by a reader who cannot tell which one is stale. What lives here is
what the rules *are* and why they hold; what the design system holds is what
they measure.

See [`interface/`](interface/).
