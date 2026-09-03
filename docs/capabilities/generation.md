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

## But only the generator writes for the user's shell

The rule above is the generator's, and it stops there. `shhh cmd` exists to
produce a line the user runs and keeps in their own history, so writing that
line in anything but their own shell's syntax would make it worthless.

Everywhere else — the agent's `execute_command`, a background process, the
body of a sandbox wrapper — shhh composes the command and shhh reads the
output back. The user's shell is a liability there, so those go to bash, and
the prompt for those sessions is told bash (`shell.Execution`,
`shell.DetectExec`).

Three things say so. A model writes bash by default and the syntax rules only
move the odds; they cannot conjure a construct the user's shell has not got,
and fish has no heredoc. Every other coding agent runs bash, so a command that
works in one of them and fails here is a bug in shhh. And the user's shell is
not quiet, which is the next section.

## `$SHELL -c` is not a quiet shell

This page used to claim that no profile is loaded on Unix because `$SHELL -c`
is non-interactive. That is true of `sh` and of `bash`, and false of the two
shells people actually set: `fish` sources `config.fish` on every invocation
including this one, and `zsh` sources `.zshenv`.

So on a fish or zsh machine every single agent command paid for the user's
prompt setup, version-manager hooks and tool inits, and anything they printed
landed in the captured output the model reads back as the command's own —
which is the exact failure `-NoProfile` is passed to PowerShell to prevent.
Picking bash for execution fixes it on Unix for the same reason the flag fixes
it on Windows.

## Platform rules are stated, not left to be inferred

The request carries what this shell and this operating system actually do,
because a model asked for "a command" writes the most common one, and the most
common one is Linux with GNU coreutils. On macOS that is a BSD tool given GNU
flags; on Windows it is a POSIX command the machine has never had.

Windows needs the most saying, and the sharpest of it is not about what is
missing. PowerShell aliases several POSIX names — `ls`, `cat`, `rm`, `ps` — to
its own cmdlets, which do not take POSIX flags, so `ls -la` is an error rather
than a listing. A name that is absent produces a command-not-found the reader
understands immediately; a name that is present and behaves differently
produces a failure they have to think about.

The two PowerShells are told apart rather than given the intersection of what
they can do. PowerShell 7 has `&&` and `||`; the 5.1 that Windows ships treats
them as a syntax error. Writing for the older one everywhere would make every
command on the newer one longer than it needs to be, and writing for the newer
one everywhere would break on the shell most machines have.

Elevation is a platform difference too, and the one most likely to be got
wrong silently: "not root" and "no such concept" are the same boolean and
opposite instructions. A Windows session is told there is no sudo, rather than
merely not being told there is one.

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

That line comes back with the command, in the same answer. Asked for on its
own it was a second request that could not start until the first had
finished, so a screen that is one sentence and a row of keys waited out two
round trips to fill in. The generator has already decided what the command
does by the time it writes it, and saying so costs a few tokens there and no
time at all. Where an answer arrives without one — a model that ignored the
format, a reply cut short — it is asked for separately, which is the request
the full breakdown makes anyway.

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
