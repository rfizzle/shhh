# Agent profiles

A profile is one TOML file at `~/.config/shhh/agents/<name>.toml` (or under
`$XDG_CONFIG_HOME/shhh/agents/`), or, for `shhh code` only, at `.shhh/agents/<name>.toml`
under the repository root for a profile that belongs to one project; the
project's shadows a global one of the same name. `/agents new` in a session drafts one
from a sentence. Its file name is the role a `shhh code`
session spawns it by, so once `reviewer.toml` exists the orchestrator can call
`spawn_agent` with `role = "reviewer"` — the model is told the profile exists
and what it is for. Why profiles are files, and what they can and cannot
change, is in [`../capabilities/subagents.md`](../capabilities/subagents.md#a-profile-is-a-file).

The files beside this one are working examples. Copy one into the directory
and edit it.

| Example | What it shows |
|---------|---------------|
| [`reviewer.toml`](reviewer.toml) | overriding the built-in reviewer: read-only, plan mode, a cheap model at low reasoning — the built-in uses the session's model |
| [`test-writer.toml`](test-writer.toml) | write + execute in an isolated worktree, narrowed to the editing tools it needs, high reasoning |
| [`web-researcher.toml`](web-researcher.toml) | read + web with a tool allowlist, budgets, and a prompt in a separate file |
| [`researcher.toml`](researcher.toml) | overriding a built-in role: same name, different model |
| [`web-researcher-prompt.txt`](web-researcher-prompt.txt) | the `prompt_file` the web researcher points at |

## Fields

Every field is optional. A file containing nothing but a comment defines a
read-only researcher named after the file.

| Field | Meaning |
|-------|---------|
| `name` | The role name. Defaults to the file's stem; a value that differs from it is an error. Lowercase letters, digits, dashes, up to 24 characters. |
| `description` | One line on what the agent is for. The orchestrating model reads this when choosing a role, so write it for the model. |
| `model` | Model to run on. Empty or `"inherit"` defers to `[agents]` in `config.toml`, then the session model. A `spawn_agent` call naming a model outranks all of them. |
| `reasoning` | `"off"`, `"low"`, `"medium"`, `"high"`, or `"inherit"` (default) for the session's live level. |
| `permissions` | Tiers granted: `"read"` (always on), `"write"` (`write_file`, `edit_file`), `"execute"` (`execute_command`), `"web"` (`web_fetch`, `web_search` — only when the session has them). Write or execute puts the agent in an isolated worktree; its changes come back as a patch. |
| `tools` | Allowlist of tool names within the granted tiers. Empty means every tool the tiers allow. Naming a tool whose tier is not granted is an error. Valid names: `read_file`, `list_directory`, `search`, `glob`, `write_file`, `edit_file`, `execute_command`, `web_fetch`, `web_search`. |
| `mode` | Permission mode the agent starts in: `"manual"`, `"accept-edits"`, `"auto"`, `"plan"`. Empty inherits the parent's. Always clamped to the parent's mode — a profile can be stricter, never looser. |
| `prompt` | The agent's instructions. Appended to a base prompt built from the permissions (environment, tools, working style, final-report contract). |
| `prompt_file` | Path to a file whose contents are the prompt; relative paths resolve against the profile's directory. Not with `prompt`. |
| `prompt_mode` | `"append"` (default) or `"replace"`. Replace sends your prompt alone — you then own the environment and tool description too. |
| `max_tokens` | Default token budget (prompt + completion) when the spawn names none. Clamped to the same floor and ceiling a spawn's own value is. |
| `max_rounds` | Default check-in interval in tool rounds when the spawn names none. Zero, the default, never pauses. |

## What a profile cannot do

- Grant a child more than its parent. Modes are clamped, the working scope is
  inherited, and the sandbox deny mask still applies.
- Skip approval. Gated tools — commands, file edits, `web_fetch`, the spawn
  itself — ask the way they always do, subject to the mode.
- Add tools shhh does not have. The web tools appear only when the session
  registered them; a profile granting `web` without a configured search key
  gets what is there.

## When a file is wrong

A profile that fails to parse or validate stops `shhh code` at startup with
the file's path and the field at fault. Fix or remove the file. Unknown keys
are errors too, so a typo cannot silently become a default.
