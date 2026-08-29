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
