# Containment

Approval decides whether something runs. Containment decides what it can reach
once it does. They are independent on purpose: a command you approved is still
a command that can be wrong.

## Scope is the set of directories the work may reach

A session starts scoped to the directory it was opened in. That is the right
default and the wrong one the moment the work spills over — a config directory
the project reads, a sibling checkout, a vendored dependency outside the tree.

Before this existed the only answers were to edit a config file and restart,
or to watch contained commands fail on paths that were plainly part of the
job. Neither is a decision; both are obstacles.

Adding a directory is a permission grant and goes through the same machinery
every other grant does: you ask for it, or you answer the card that appears
when an action reaches outside. What the scope holds is what OS-level
containment makes writable and what edits may touch without asking again — one
list, not several that agree by convention.

### Two classes of directory never come along

- **Refused.** A path behind the deny mask cannot be granted at all, by any
  key. The mask cannot be disabled, so neither can this.
- **Sensitive.** A home directory, a system root, another tool's credential
  store. It can be granted, but only by a person answering for it — never by a
  permissive mode and never by the classifier.

The second class is the interesting one. It exists because "can be granted"
and "can be granted without a human" are different questions, and a mode that
was turned on for convenience must not be able to answer the second one.

## The deny mask is not configurable

Credential stores and shhh's own state are unreachable, always. There is a
setting to add to the mask and none to subtract from it.

A configurable mask is a mask that gets configured away — by a user
troubleshooting something unrelated, by a script, by a session that argued
persuasively. The protection is only worth having if it cannot be turned off,
so it cannot be.

## A cancelled command takes its children with it

Every captured command is a shell, and the work is that shell's children.
Cancelling used to signal only the shell, so the build, the watcher or the
test runner underneath went on running with nothing left watching it. A
session interrupted a few times left a few of those behind, each still holding
the port or the lock the next attempt needed.

A command now runs in its own process group and cancelling signals the group.
Interrupt first — that is what the reader pressed, and it is the signal a
compiler or a test runner knows how to stop cleanly on — then kill, after a
short grace, for whatever ignored it.

Underneath both there is a bound on the wait itself. A surviving relative that
inherited the output pipe keeps it open after its parent is gone, and reading
that pipe is exactly what the runner is blocked on, so a command that is
already dead could still hold the turn open.

## A command that will not finish is not waited on forever

There is a ceiling on how long one command the assistant runs may take.
Reaching it is not the ordinary case and is not meant to be: the number is far
past a full test suite, a cold dependency install or a release build, so what
it actually catches is the command that was never going to finish — one
waiting on a prompt nobody will answer, a watcher started in the foreground, a
network read with no timeout of its own.

**A command the reader typed is never bounded by it.** They are in front of
the session and chose to run the thing; the key that cancels it is their
ceiling, and a limit that cut their build short would be the tool overruling
them about their own machine.

The ceiling matters most where there is nobody to do that. A headless run and
a sub-agent both have no reader, and a command that hangs there does not hang
one command — it holds the whole run until something outside kills it, and the
parent waits on a report that is never coming.

**Being stopped and having failed are different, and are said differently.**
What comes back from a killed command is whatever it printed and an exit code
that says only that it did not exit normally, which reads exactly like a
broken command — so the reason is appended in words: that it did not fail, it
did not finish, and what to do about it. Without that the model debugs a
command that was working.

## What is reported is what is in force

Every surface that mentions containment reports the mechanism actually
containing the process, not the one that was requested. Where nothing is
containing it, the surface says so in those words and does not soften it.

A tool that reports its intended security posture rather than its actual one
is worse than a tool with none, because it is believed. Where containment is
unavailable, the honest answer changes the user's behaviour; a reassuring one
does not.

## Related

- [`approvals-and-safety.md`](approvals-and-safety.md) — deciding whether it runs
- [`../interface/surfaces.md`](../interface/surfaces.md) — how it is reported
