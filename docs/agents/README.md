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
| [`reviewer.toml`](reviewer.toml) | overriding the built-in reviewer: read-only permissions and mode, a cheap model at low reasoning — the built-in uses the session's model |
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
| `model` | Model to run on. Empty or `"inherit"` defers to `[agents]` in `config.toml` — the level of delegation's own `[agents.depth.<n>] model` first, then `[agents] model` — and last to the session model. A `spawn_agent` call naming a model outranks all of them, and a profile that names one takes it at every depth: the role is above the depth, so a role somebody chose a model for keeps it wherever in the tree it runs. |
| `reasoning` | `"off"`, `"low"`, `"medium"`, `"high"`, or `"inherit"` (default) for the session's live level. |
| `permissions` | Tiers granted: `"read"` (always on: the file tools, and `quality_gate` where the checkout declares suites — running the project's own checks is a read of its health, not a command), `"write"` (`write_file`, `edit_file`), `"execute"` (`execute_command`), `"web"` (`web_fetch`, `web_search` — only when the session has them). Write or execute puts the agent in an isolated worktree; its changes come back as a patch. |
| `tools` | Allowlist of tool names within the granted tiers. Empty means every tool the tiers allow. Naming a tool whose tier is not granted is an error. Valid names: `read_file`, `list_directory`, `search`, `glob`, `quality_gate`, `spawn_agent`, `agent_report`, `write_file`, `edit_file`, `execute_command`, `web_fetch`, `web_search`. |
| `max_depth` | Not a profile field — it is `[agents] max_depth` in `config.toml`, and it is how deep delegation goes, counting the session itself as 1. The default is 3: this session, its children, and theirs. An agent at the deepest level is handed no delegation tools, and a spawn that would open a level past it is refused naming the depth and the key. |
| `mode` | Permission mode the agent starts in: `"manual"`, `"accept-edits"`, `"auto"`, `"read-only"`, `"plan"`. Empty inherits the parent's. Always clamped to the parent's mode — a profile can be stricter, never looser. |
| `prompt` | The agent's instructions. Appended to a base prompt built from the permissions (environment, tools, working style, final-report contract), or from `reviews` where that is set. |
| `prompt_file` | Path to a file whose contents are the prompt; relative paths resolve against the profile's directory. Not with `prompt`. |
| `prompt_mode` | `"append"` (default) or `"replace"`. Replace sends your prompt alone — you then own the environment and tool description too. |
| `reviews` | `true` makes the agent's declared paths the change it judges rather than files it may change: it is handed those paths and their diff ahead of its task, claims nothing against other agents, and is stopped at its round cap with a request for its report. It also selects the base prompt: the reviewer's, which reads the declared evidence first, ranks findings by severity and ends on a verdict, instead of the read-only agent's, which gathers facts and reports them. Your `prompt` still follows it. Only for a profile granting neither `write` nor `execute`. |
| `max_tokens` | Default token budget when the spawn names none, counted in new tokens — what the provider did not serve from its cache, plus the completion. A profile default is at least 300000; an explicit spawn may be as low as 200000 only when prompt admission still leaves its 200000-token working reserve. |
| `max_rounds` | Default check-in interval in tool rounds when the spawn names none. Zero, the default, never pauses. Under `reviews` it is not a pause but the end of the inspection: the agent is asked for its report there. |

## What a profile cannot do

- Grant a child more than its parent. Modes are clamped, the working scope is
  inherited, and the sandbox deny mask still applies. The same holds one level
  further down: an agent that changes nothing may delegate an agent that
  changes nothing, and a descendant starts in its spawner's mode — see
  [`../capabilities/subagents.md`](../capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth).
- Skip approval. Gated tools — commands, file edits, `web_fetch`, the spawn
  itself — ask the way they always do, subject to the mode.
- Add tools shhh does not have. The web tools appear only when the session
  registered them; a profile granting `web` without a configured search key
  gets what is there. `quality_gate` is the same: a checkout nobody has
  trusted registers none, for the session or for a child.
- Run anything but the project's own checks without `execute`. `quality_gate`
  picks a suite by name out of the trusted config and can never supply command
  text, which is why `read` is enough for it — see
  [`../capabilities/subagents.md`](../capabilities/subagents.md#a-profile-that-changes-nothing-can-still-run-the-checks).
  A profile granting `write` or `execute` may not name it at all: that agent
  works in a copy of the checkout, and the gate would report on the tree the
  copy was made from.
- Bypass admission. A profile’s `max_tokens` is a 300000-token-or-higher
  default, and the inherited prompt plus task must leave the 200000-token
  working reserve before the child starts.
- Name an MCP server's tool. A child is handed the tools of every server the
  session connected and you marked read-only, whatever its tier, and no
  other server's — see [`../capabilities/mcp.md`](../capabilities/mcp.md#what-a-conversation-may-reach).

## When a file is wrong

A profile that fails to parse or validate stops `shhh code` at startup with
the file's path and the field at fault. Fix or remove the file. Unknown keys
are errors too, so a typo cannot silently become a default.
