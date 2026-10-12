---
name: shhh-checkout-setup
description: Use when setting shhh up in a checkout or deciding what belongs in its .shhh/ directory (context, settings, quality gate, skills, agent profiles, wordings, backlog).
---

# A checkout's .shhh/ directory

What shhh reads from a checkout lives in `.shhh/` at the checkout root (or
the nearest directory above that already has one). Commit it: it travels with
the code. Its settings, wordings, skills, agent profiles, quality gate,
hooks, MCP servers, toolchain and backlog profile are not used until the
person trusts the checkout with `shhh trust` (or `/trust` in a session);
that answer is the person's, so ask them rather than running it.

Write only what the project needs. Every file is optional.

## What lives there

- `project.md`: context every session here reads first: what the project
  is, how to build and test it, conventions worth knowing. `shhh init
  --project` writes a commented starter. One context file per directory is
  read and this one wins over `AGENTS.md` and `CLAUDE.md`, so a checkout
  that already has one of those does not need it.
- `config.toml`: the project's settings. `shhh config init`, run in the
  checkout, writes every key a checkout may set, commented out at its
  default with what it decides; `shhh config list` shows what is in effect
  and where each value came from; `shhh config set <key> <value>` writes
  one.
- `quality.json`: the quality gate, named suites of checks. Nothing
  scaffolds it: load the `shhh-quality-gate` skill for the shape and a
  starter.
- `skills/<name>/SKILL.md`: the project's own skills, in the Agent Skills
  format (frontmatter with `name` matching the directory and a
  `description` that says when to use it, then the instructions). `shhh
  skills` lists what a session here would load and why any did not; `shhh
  skills show <name>` shows one.
- `agents/<name>.toml`: a sub-agent role this project adds or replaces.
  `shhh agents` lists the roles a session here could spawn, built-in and
  the project's.
- `prompts/<key>.md`: a wording that replaces one of shhh's built-in texts
  whole. Overrides only: keep a file here only when its text differs from
  the built-in. `shhh config init --wording` lists the keys, `shhh config
  init --wording <key>` prints a built-in to start from, and `shhh config
  init --update` deletes a file that equals its built-in.
- `todo/`: the backlog, one Markdown file per item, finished items under
  `todo/done/`, and the profile the backlog is worked under in
  `todo/profile/`. `shhh todo` lists the items in the order a run takes
  them, `shhh todo next` shows the next one, `shhh todo show <slug>` one
  item.
- `mcp.json`: MCP servers for sessions here. `shhh mcp` lists every server
  a session here would connect and whether each answered.
- `hooks.json` and `toolchain.toml`: commands a session runs at its own
  seams, and tools the project's checks need that the sandbox image lacks.

## Checking the result

A file that will not load is named by the listing for its kind: `shhh
skills`, `shhh agents`, `shhh todo`, `shhh mcp`, and the `quality_gate`
tool for `quality.json`. `shhh doctor` checks the machine's own setup.
