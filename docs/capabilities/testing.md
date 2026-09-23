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
the public network, or a shared compiler cache. The model-data cache is one of
the things it owns: seeded from the snapshot inside its temporary home, so a
test never starts the download from the public price table or writes into the
developer's own cache. That is the suite a session can run again after an
edit and obtain the same answer.

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

## A skipped test is counted

A test that skips did not run, and a green suite with skips in it is a suite
that proved less than it appears to. The tests that put containment to the
operating system cannot nest inside a contained session, so the gate a session
runs is exactly where they skip — and a pass that says nothing about them
reads as though the boundary had been checked.

The test run therefore ends on one line per distinct skip reason with its
count, and the gate carries those lines under the test check's row whatever
its verdict: `skipped 9: no Seatbelt containment here`. A reason is the skip
message up to its first colon, so the error text after it does not split one
reason into many. Reasons naming a missing containment mechanism come first,
because those are the tests only the integration target can stand in for;
ordinary ones, such as a host without git, follow. Counting reads the run's
output and adds nothing to its inputs, so the suite stays as cacheable as it
was, and a failing package still prints its failures in full.

## A scene can run in the gate

A driven scene is the one check that sees the built program in a terminal,
and a person closing a surface from a session should be able to have the gate
run one rather than tick the box by hand. It cannot join the closing suite:
it needs tmux, a Python interpreter and a loopback listener for the scripted
model, and the closing verdict may depend on none of them. So it is a suite of
its own, `tui`, run by name and never on close. It drives the smoke scene
contained, with the workspace writable, because that is where the captures
are written.

Where a program is missing, the suite blocks. It names tmux and the
interpreter as checks of their own, and the runner finds every executable a
suite names before it runs any of them. So a host without tmux gets a blocked
verdict that names tmux. It does not get a failed scene, and it does not get
a skip that reads as a pass.

A contained run can write in two places: the workspace, and a temporary
directory of the session's own. On bubblewrap that directory is a private
`/tmp`; on Seatbelt it is a directory under shhh's data directory. The run
puts its captures in the workspace and everything else in the temporary
directory: the scene's own repository and home, and the tmux socket. The
socket cannot go in the checkout, because a Unix socket's path is capped near
104 bytes and a worktree's path has already used most of them. On Seatbelt a
data directory deep enough to push the socket past that cap stops the run
before anything starts, and the driver names the path. Setting the socket's
directory yourself does not help there, because containment passes a command
only an allowlist of variables and that one is not on it.

The first attempt to run a scene from a session, on 2026-09-08, was recorded
as a run that hung because it wrote outside the workspace. What was denied
was the tmux socket. With no socket directory set, tmux uses `/tmp`, and
containment gives a command a temporary directory of its own in place of
`/tmp`. tmux could not start, so no pane opened and the model was never
asked. The first snap then waited out its twenty seconds for a screen that
did not exist. Run again with that day's binary and driver, the failure is
the same (`couldn't read directory /private/tmp/tmux-502 (Operation not
permitted)`), but the run exits after that one wait. Nothing in it waits
forever, so the hang was most likely that silent wait: it drew nothing, and
it still printed a tick under the step it failed. The driver now keeps the
socket in the session's own temporary directory, and it marks a failed step
as failed.

## Related

- [`approvals-and-safety.md`](approvals-and-safety.md#quality-gates-run-what-you-wrote)
  — why a quality suite is trusted command text
- [`containment.md`](containment.md) — what an approved command can reach
