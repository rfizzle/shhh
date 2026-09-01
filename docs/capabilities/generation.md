# Command generation

The original capability and still the most-used one: a sentence in, a command
out, and a decision in between. It is `shhh cmd`, one of the four sizes in
[`../product.md`](../product.md), and the hotkey in your own shell is the same
generation with the screen taken away.

## It writes for your machine, not for a manual

Shell, operating system and working directory are resolved at runtime and go
into the request. The answer is a command that works here — the right flag
spelling for this platform, the right utility for what is actually installed.

A tool that emits a generic command and leaves the user to port it has moved
the work rather than done it.

## The prompt is told which shell it is writing for

There is one resolution of "which shell", and both the prompt and the runner
read it. That is the whole point of it being one: a prompt that describes bash
while commands go to PowerShell is worse than either alone, because the model
writes something correct for a shell that is never going to see it.

Which shell that is differs by more than its name. On Unix it is `$SHELL`, or
the POSIX floor beneath it, run with `-c`. On Windows there is no `$SHELL` and
no `-c`: PowerShell is preferred, because that is where Windows development
happens and a command with a pipeline or a quoted path is ordinary there and a
fight in cmd — and cmd is the floor, the one shell certainly present, taking
`/C` instead.

The flags travel with the shell rather than being spelled at each call site,
because getting them wrong is silent. cmd reads an unknown leading flag as a
filename.

Neither platform loads the user's profile. On Unix that is what `$SHELL -c`
already does; on Windows it has to be asked for. A profile prints banners and
sets aliases, and both end up in captured output that the model reads back as
the command's own.

## The command is the output, and only the command

Whatever the model wraps around it — fences, backticks, prose — is stripped
before anything is shown. What appears on screen is what would run.

This matters most in the modes with no screen at all. Piped or scripted,
`shhh cmd` writes the bare command to stdout and nothing else, because the
consumer is another program. A tool that only works when a human is looking at it is not
composable, and this is a shell tool.

## Explanation is on request, not by default

One line of what the command does is shown; the full breakdown takes a key or
a flag. Explaining at length by default trains the user to skip the region of
the screen where the explanation lives — which is also where the warnings are.

## Revision is a conversation, not a retry

"Make it recursive", "use fd instead" — the follow-up carries the history, so
the second answer is a refinement rather than a fresh guess at a new prompt.
There is no limit on rounds.

## Editing is a line, not an editor

The generated command opens in a single-line editor, pre-filled, right where
it already is. It does not open `$EDITOR`.

Launching a full editor to change one flag costs a context switch far larger
than the edit, and lands the user in a program with its own modes and its own
quit key. The edit is small, so the surface is small.

## What happens after it runs

The command that *actually ran* is appended to your shell history — not the
prompt that produced it. The prompt is not a command and would be useless
there; the command is the thing you will want to recall, re-run and edit.

The child's exit code becomes shhh's exit code, so a generated command
composes in a script exactly as a hand-written one would.

## Inline mode draws nothing

The hotkey path replaces the line already in your buffer and shows no shhh
interface at all — no sub-shell, no takeover, no new prompt.

The value here is the absence of a context switch, so anything drawn would
subtract from it. This is also why inline mode does not use the TUI framework:
writing into a shell's line buffer means not corrupting it.

## Related

- [`../product.md`](../product.md) — how the modes differ
- [`approvals-and-safety.md`](approvals-and-safety.md) — the safety assessment
- [`sessions-and-memory.md`](sessions-and-memory.md) — history and snippets
