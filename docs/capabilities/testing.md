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

The same answer has to hold on a busy machine, because three worktree gates
run side by side as a matter of course. So a test waits on a fact, never on a
clock: a channel the code under test closes, a state it polls with a bound,
the line a fixture prints once it is ready, or a fake clock it advances
itself. A sleep is a guess about how fast the machine is, and every guess
loses to a head start. A bound is a ceiling and never a pace: a fact that
holds ends the wait at once, so the bound is spent only by a test that is
failing, and it is set long enough that load alone never spends it. The few
pauses that remain are where time passing is itself the thing tested — a
quiet keyboard, a fixture standing in for a provider's latency — and each
says so beside it. A scene obeys the same rule: a snap waits for text only
the surface draws, and a step that has nothing on screen to wait for lets the
binary's own window run out rather than racing it.

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

A verdict is about the tree the checks ran over, so a gate run that saw the
tree change while it ran, or whose tree has moved since, is stale: it says so,
and a stale pass is not a pass. Every place that acts on the verdict reads it
the same way. The closing check's row marks it stale, and a backlog run's
verify stage counts it as a failure, reports it in the same words, and does
not close the item on it, whichever surface is working the backlog.

A check that fails is run once more, on its own, before the verdict is
reached: after the whole suite has finished, each check that exited with a
failure is run again with nothing else of the suite beside it, so a test that
failed only because its siblings were loading the machine is given the run it
did not get. That second run happens only over the tree the suite started on.
When the tree moved while the checks ran, nothing is run again and the result
is stale as before, and a tree that moves during the second run makes the
whole result stale too. A check that timed out or never started is not run
again, because neither is a verdict about the code.

A check that failed and then passed is a **flake**, and a flake is never
silent. Its line in the result says it flaked, with both runs' durations; the
result's first line counts it beside the tally; the closing check's row says
it beside the count of checks; a backlog run's verify report carries the same
text; and the model is told that a flaked check counts as passed, so it does
not spend a round fixing a failure that did not recur. The failure stays on
the record as the check's evidence, because why it flaked is what a person
looking at it next wants to read.

A flake never lowers the bar. The verdict is a pass only because the second
run passed, over the same tree, and a check that fails twice is a failure
exactly as it was before. There is one second run and not more: a check
allowed three tries passes when it fails two times in three, and the gate
would then be vouching for the odds rather than the code. One run is enough
to tell a check that failed under load from one that fails, and a check that
keeps needing it is a check to distrust, which the count beside the tally is
there to make visible. A suite that wants every failure to stand turns the
second run off.

The separately named contract and integration targets complete the evidence:
they run on a prepared host and certify capabilities deliberately absent from
the session boundary. Together the tiers make a result both repeatable where
the agent works and meaningful where the operating system must participate.
Continuous integration selects those targets in separate jobs, so preparing a
listener or an operating-system mechanism cannot change the result of the
ordinary contained gate.

## A flake is counted where it happened

One flake is a machine that was busy; the ninth is a check that needs fixing,
and the word on the row cannot tell the two apart. So every flake is counted,
per checkout, suite and check, with when it first and last happened, the
session it last happened in and the exit code the failing run gave — the one
place that code is kept, since the result reports the check as passed. The
count before this run rides the flaked line itself, `, 3 times before`, so the
model reads it where the screen does, and the closing check's row says
`flaked 3 times before · /gate flakes` in its note column where there is room.
`/gate flakes` lists the whole ledger, the most recent first, and `shhh
observe` carries a line for the checks that flaked in its window. Every runner
that can rerun a check writes to the same ledger — a session's, an unattended
run's, a backlog run's and each of its lanes — and a lane's flake is counted
against the checkout the lane was copied from, because the copy is gone once
it lands.

The ledger lives in shhh's own data directory, keyed on the checkout's root,
and not in the checkout. It is a reading of how this machine ran the checks
rather than a fact about the code, so a file in the checkout would travel with
a clone to a machine that never flaked, and writing it would move the very
tree the gate fingerprints, making every flaked run stale the moment it was
counted. The count is bookkeeping about the verdict and never part of it: a
ledger that cannot be opened or written costs the count, and the check still
says it flaked and still counts as passed.

## A skipped test is counted

A test that skips did not run, and a green suite with skips in it is a suite
that proved less than it appears to. The tests that put containment to the
operating system cannot nest inside a contained session, so the gate a session
runs is exactly where they skip — and a pass that says nothing about them
reads as though the boundary had been checked.

The test run therefore ends on one line per distinct skip reason with its
count, and the gate carries those lines under the test check's row whatever
its verdict: `skipped 9: no Seatbelt containment here`. The check's own line carries the
total as well, `· 9 skipped` after its outcome and before its evidence, and
says nothing where nothing skipped, so a host that cannot run a mechanism's
tests shows a number on the row. A reason is the skip
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
