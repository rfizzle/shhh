---
name: shhh-global-config
description: Use when asked about shhh's own settings, such as the default model or where a setting lives.
---

# shhh's global configuration

shhh reads settings from two files, called `global` and `project` (never
"user"). The global one is `config.toml` in shhh's config directory; the
project one is `.shhh/config.toml` in a checkout, and it counts only once the
person has trusted the checkout (`shhh trust`). Do not state where the config
directory is: it depends on the machine (`$XDG_CONFIG_HOME/shhh` when that is
set, else `~/.config/shhh`). `shhh doctor --table` names the files in use, and
`shhh config list` shows every setting with its value and which file it came
from.

## What lives in the global directory

Everything the person wrote for shhh, side by side:

- `config.toml`: the global settings.
- `providers.toml`: gateway profiles, their models, and declarations scoped
  to a flow. `shhh providers` lists them and checks each.
- `keybindings.toml`: key moves. `shhh keys` lists every key and marks the
  ones moved.
- `theme.toml`: the colour table. Global only; a checkout cannot set it.
- `prompts/<key>.md`: wordings that replace one of shhh's built-in texts.
  `shhh config init --wording` lists the keys.
- `agents/` and `skills/`: sub-agent roles and skills for every checkout
  (`shhh agents`, `shhh skills`).

## Which value wins

A value set in the running session beats the
project file, which beats the global file, which beats the built-in default.
`shhh config get <key>` prints the value in force and where it came from.

## Commands

- `shhh config list [section]`: every setting. This is the list of keys; do
  not reproduce it.
- `shhh config get <key>`: one setting.
- `shhh config set <key> <value>`: writes the project file inside a checkout;
  add `--global` for the global file.
- `shhh config init`: writes a commented settings file at its defaults
  (`shhh config init --global` for the global one); `--update` brings an
  existing one up to date and keeps every value.
- `shhh providers`, `shhh doctor`: the provider in use, and whether the
  machine is set up.

## Settings people ask about

Name the key and have the person (or yourself) read the value; never answer
from memory.

- Default provider and model: `provider.default`, `provider.model`
  (per size: `provider.chat_model`, `provider.code_model`). See
  `shhh config get provider.model`.
- Where child agents' worktrees go: `agents.worktree_dir`. See
  `shhh config get agents.worktree_dir`.
- How often a long turn stops to take stock: `behavior.check_in_interval_rounds`.
  See `shhh config get behavior.check_in_interval_rounds`.
- Whether commands must run in a sandbox: `sandbox.require` for the session,
  `agents.require_sandbox` for writers. See `shhh config get sandbox.require`.
