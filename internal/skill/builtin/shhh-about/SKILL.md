---
name: shhh-about
description: Use when asked what shhh is, where it keeps something, or why something inside shhh failed.
---

# What shhh is and where it keeps things

shhh turns what you meant into something your machine can run. It comes in
four sizes: `shhh cmd` writes one shell command; inline, a hotkey set up by
`shhh init <shell>` finishes the line in your own shell; `shhh chat` is a
conversation; and `shhh code` is a coding agent that reads and edits a
checkout with tools.

## How a coding turn runs

The model calls tools in rounds. Every so often (a setting) the turn stops to
take stock of its plan. A tool that changes something asks the person on an
approval card; in auto mode a classifier decides most of those instead and
asks only when unsure. A checkout may declare a quality gate (the
`shhh-quality-gate` skill), which the model runs before it says it is done.
Larger jobs go to child agents, each working in a git worktree of its own.

## Where it writes

None of these paths is fixed; each command prints the one on this machine.
Do not state a path or a default from memory.

- The local store, logs and state directories: `shhh doctor --table` names
  each.
- Running sessions, then saved chats: `shhh sessions`, `shhh chats`.
- Durable memories: `shhh memory`.
- Usage and what a session did: `shhh metrics`, `shhh observe`.
- A request a provider refused: `shhh logs`.
- Report pages sessions built: `shhh reports`.
- Child agents' worktrees: under `agents.worktree_dir`; see
  `shhh config get agents.worktree_dir`.
- The sandbox, when one is configured: `shhh doctor --table` shows what is
  in force. Settings are in the `shhh-global-config` skill.

## When something went wrong

Work down this ladder and stop at the first answer:

1. `shhh doctor --table`: checks the binary, the settings, the provider and
   its key, the store and the sandbox, with a fix on each row that failed.
2. `shhh logs`: the tail of the diagnostic log, for a failed request.
3. `shhh observe session <id>`: a timeline of what one session did. `shhh
   sessions` and `shhh chats` give the id.
4. `shhh update`: a newer shhh may already have the fix.
5. A bug that remains goes to https://github.com/rfizzle/shhh/issues. Give
   the output of `shhh --version` and `shhh doctor --table`.
