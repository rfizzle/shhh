# Testing and quality evidence

## What does a passing check mean?

A check is evidence only for the boundary it actually crossed. A test that
uses an invented peer proves the behaviour on this side of that peer; one that
talks to a real loopback listener additionally proves the two ends can meet;
one that starts inside the operating-system boundary proves that boundary can
be established. Calling all three the same kind of result hides what a green
run did and did not establish.

The ordinary suite is therefore hermetic. It holds its inputs and side effects
inside a temporary workspace, replaces an in-process HTTP peer with an
in-memory transport, and substitutes any host helper at its command seam. It
does not need an open port, a clipboard, a container engine, a running daemon,
the public network, or a shared compiler cache. That is the suite a session
can run again after an edit and obtain the same answer.

## When does a real boundary belong in a test?

Some claims cannot be made from an invented peer: that a built command reaches
a provider over loopback, that telemetry reaches a collector, that a fixture
site is actually served and fetched, that a report is reachable from its link,
that an operating-system service accepts its input, or that containment is
actually established. Those checks are contracts, not ordinary fixtures. They
live in separately named targets whose selected runner has the required
capability.

A contract target says what it needs before it begins. If that target was
selected and its listener or host service is unavailable, it fails with that
fact. On a platform where the contract cannot apply, a skip says only that the
test was inapplicable; it is not a passing result for the supported runner.
This keeps a restricted session from reporting a false failure while keeping a
CI runner from reporting a false green.

## How do quality gates stay repeatable?

An automatic or on-close gate chooses the hermetic suite and requires
containment. If the host cannot establish the requested boundary, the gate is
blocked before it runs a command. It never falls back to the host merely to
produce a verdict. A closing suite includes the ordinary tests as well as
static, formatting, and documentation checks; a shorter suite may provide an
early signal but does not stand in for that verdict. Its Go compilation
products and tool caches live in the session's private scratch space, it does
not update the module cache, and it clears provider, terminal-palette, and
executable Git-hook state inherited from the launching shell. Git runners
independently reject repository configuration that names an executable. The
gate therefore neither depends on permission to update a shared cache nor
takes a different branch because a developer happened to export a local
setting.

The separately named contract and integration targets complete the evidence:
they run on a prepared host and certify capabilities deliberately absent from
the session boundary. Together the tiers make a result both repeatable where
the agent works and meaningful where the operating system must participate.
Continuous integration selects those targets in separate jobs, so preparing a
listener or an operating-system mechanism cannot change the result of the
ordinary contained gate.

## Related

- [`approvals-and-safety.md`](approvals-and-safety.md#quality-gates-run-what-you-wrote)
  — why a quality suite is trusted command text
- [`containment.md`](containment.md) — what an approved command can reach
