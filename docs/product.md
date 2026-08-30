# What shhh is

shhh turns what you meant into something your machine can run, and it does
that at four different sizes.

The smallest is a command you could have written yourself if you remembered
the flags. The largest is an agent that edits the repository, runs the tests,
and tells you what it changed. They are the same product because they are the
same bargain: you say what you want in your own words, and shhh puts the
result in front of you *before* anything happens, in enough detail to say no.

## Who it is for

Someone who lives in a terminal and does not want to leave it. That single
assumption decides more than it looks like it does:

- **The terminal is the whole interface.** Not a companion to an editor, not a
  thin client for a web app. If a capability cannot be expressed in a
  terminal, it is not a capability shhh has.
- **The user already knows shell.** shhh is not a shell tutor and does not
  simplify what it produces. It writes the command an expert would write, and
  explains it on request rather than by default.
- **Their machine is theirs.** shhh asks before it changes anything, states
  what it is about to touch, and can put most of it back. See
  [`capabilities/approvals-and-safety.md`](capabilities/approvals-and-safety.md).
- **They will run it over work that matters.** A tool that is careless once in
  a hundred sessions is a tool nobody points at a real repository.

## The four sizes

Each mode is a different answer to "how much do you want to hand over".

### Prefix — `shhh <prompt>`

One prompt, one command, one decision. You get the command, a line of what it
does, and a row of keys: run it, edit it, ask for a different one, copy it,
save it. Nothing runs until you say so, and the safety assessment happens on
that screen rather than as an afterthought prompt.

This is the mode the product is named for and the one most sessions are.

### Inline — a hotkey in your own shell

`Ctrl+K` takes the half-written line already in your buffer and replaces it
with the finished command. There is no shhh screen at all: no sub-shell, no
takeover, no new prompt. You keep the shell you were in and the line you were
writing, and the line gets better.

The value is the absence of a context switch. Anything that put a UI in front
of the user here would cost more than it returned, which is why this mode
draws almost nothing.

### Chat — `shhh chat`

A conversation. The assistant answers, and reads whatever it needs to answer
— files in the working scope, the web — without asking, because reads change
nothing. It cannot run or edit anything, and nothing you press will let it.
It can hand a question to a named colleague: a read-only sub-agent with a
persona you wrote, and a shared notebook the team keeps across the
conversation.

Chat is where thinking-out-loud goes — "how would I set this up", "what does
this actually do", "find out whether this is still true" — and it does not
open by describing your checkout, because it is not about your checkout
unless you make it so: [`capabilities/chat.md`](capabilities/chat.md).

### Code — `shhh code`

The agent. It reads, edits, runs, checks its work, and can hand parts of the
job to child agents working in their own isolated copies of the repository. It
tells you what it changed at the end of every turn, and any turn can be put
back.

This is the mode with the most delegated to it, so it is the mode with the
most said about it: [`capabilities/coding-agent.md`](capabilities/coding-agent.md).

### And when nothing is watching

With no terminal on the other end — piped, scripted, in CI — shhh drops every
piece of chrome and writes the bare command to stdout. `echo "list open
ports" | shhh | sh` is the whole contract. A tool that only works when a human
is looking at it is not composable, and this is a shell tool.

## What shhh will not do

Stated as commitments, because each one is a thing we have chosen to be worse
at:

- **It does not run something you have not seen.** Auto-approval modes exist
  and the classifier behind them fails closed — an error asks the human. It
  never silently guesses yes.
- **It does not pretend to know what it cannot.** Where the blast radius of a
  command cannot be resolved, it says so rather than reporting a confident
  zero. Where a session cannot price its own tokens, it reports the tokens.
- **It does not lose your work to its own failures.** A provider outage, a
  broken stream, an exhausted quota — each is a recoverable row with a way
  forward, not a stack trace and a dead session.
- **It does not require colour.** Every state that has a colour also has a
  glyph or a word. See
  [`interface/principles.md`](interface/principles.md).
- **It does not need a network to be honest.** What containment is actually in
  force is reported from what is in force, never from what was configured.
