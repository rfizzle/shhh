# shhh

Natural language to shell commands. Type what you want, get a command you can run.

```
$ shhh find all go files changed in the last week
▸ find . -name '*.go' -mtime -7
  [Run] [Edit] [Revise] [Explain] [Copy] [Save] [Cancel]
```

## Install

### Homebrew

```bash
brew install rfizzle/tap/shhh
```

### Go

```bash
go install github.com/rfizzle/shhh/cmd/shhh@latest
```

### Build from source

```bash
git clone https://github.com/rfizzle/shhh.git
cd shhh
make build
```

## Quick Start

1. Set your API key:

```bash
export SHHH_API_KEY="sk-..."
```

2. Generate a command:

```bash
shhh list open ports on this machine
```

3. Or start a chat session:

```bash
shhh chat
```

## Configuration

Run the interactive config wizard:

```bash
shhh config
```

Or set values directly:

```bash
shhh config set provider.default openrouter
shhh config set provider.api_key sk-or-...
```

Config file location:
- macOS: `~/Library/Application Support/shhh/config.toml`
- Linux: `$XDG_CONFIG_HOME/shhh/config.toml` or `~/.config/shhh/config.toml`

### Example config

```toml
[provider]
default = "openai"
api_key = "sk-..."
```

A more complete example:

```toml
[provider]
default = "openrouter"
model = "anthropic/claude-sonnet-4-6"
api_key = "sk-or-..."

[behavior]
safety_warnings = true
system_prompt_extra = "Prefer ripgrep over grep. Use docker compose for services."
command_allowlist = ["git status", "go test"]

[agents]
model = "inherit"                     # sub-agents follow the session model

[agents.profiles.researcher]
model = "anthropic/claude-haiku-4-5"  # cheap and fast for parallel search

[appearance]
accent_color = "cyan"
```

### Config keys

| Key | Description |
|---|---|
| `provider.default` | Provider name |
| `provider.model` | Model to use |
| `provider.api_key` | API key |
| `provider.base_url` | Base URL override (each provider has its own default) |
| `provider.name` | Custom display name |
| `behavior.silent_mode` | Suppress explanation output |
| `behavior.shell` | Override detected shell |
| `behavior.safety_warnings` | Warn before destructive commands (default: true) |
| `behavior.context_max_tokens` | Max tokens for stdin context (default: 8000) |
| `behavior.max_tool_rounds` | Max consecutive tool-call rounds per chat turn (default: 25) |
| `behavior.command_allowlist` | Command prefixes auto-approved in chat/code sessions (e.g. `["git status", "go test"]`); safety-flagged commands always prompt |
| `behavior.read_only_commands` | Extra command prefixes treated as read-only inspection (they run without prompting in every mode, alongside the built-in list) |
| `behavior.read_only_auto` | Whether built-in inspection commands run without prompting (default: true); `false` makes reads prompt like anything else |
| `behavior.default_mode` | Permission mode sessions start in: `manual` (default), `accept-edits`, `auto`, or `plan` |
| `behavior.mode_cycle` | Shift+Tab mode order (default: `["manual", "accept-edits", "auto", "plan"]`) |
| `behavior.classifier_model` | Model auto mode's permission classifier uses (default: the session model) |
| `behavior.classifier_timeout_seconds` | Timeout per classifier request (default: 30) |
| `behavior.classifier_max_tokens` | Max tokens for the classifier's response (default: 1024) |
| `behavior.classifier_retries` | Extra attempts before a failed classifier check falls back to prompting (default: 1) |
| `behavior.memory_disabled` | Turn off durable memory: no recall injection, no `remember` tool (default: false) |
| `behavior.memory_max_entries` | Max memories injected into the system prompt per session (default: 20) |
| `behavior.memory_max_tokens` | Hard token budget for the injected memory block (default: 1200) |
| `behavior.system_prompt_extra` | Extra text appended to the system prompt |
| `agents.model` | Model sub-agents run on (default: the session model); `"inherit"` follows the session model explicitly |
| `agents.profiles.<role>.model` | Per-role override, `<role>` being `researcher` or `writer` (also settable as `agents.researcher_model` / `agents.writer_model`) |
| `agents.max_concurrent` | Sub-agents running at once; further spawns queue (default: 3) |
| `sandbox.profile` | Containment profile for assistant commands: `workspace` (network preserved, default) or `workspace-netless` |
| `sandbox.deny_extra` | Extra paths masked from contained commands (the built-in mask — `~/.ssh`, `~/.aws`, `~/.config/gh`, shhh's own config/state dirs — cannot be disabled) |
| `sandbox.write_extra` | Extra writable paths inside containment (beyond the workspace, scratch, and toolchain caches) |
| `sandbox.container_engine` | Force the container-sandbox engine (`podman` or `docker`); empty auto-detects, preferring a rootless engine |
| `sandbox.container_image` | Digest-pinned image (`name@sha256:…`) for container sandboxes; required before `--sandbox` works |
| `sandbox.image_allowlist` | When set, restricts sandbox images to these digest-pinned references |
| `sandbox.container_memory` | Sandbox memory ceiling (default: `2g`) |
| `sandbox.container_cpus` | Sandbox CPU ceiling (default: `2`) |
| `sandbox.container_pids` | Sandbox process-count ceiling (default: 256) |
| `sandbox.container_ttl_hours` | Hours before an owned sandbox container is reaped at startup (default: 24) |
| `sandbox.require_isolation` | Minimum verified isolation level for sandbox runs (`process`, `container`, or `vm`); an unverifiable requirement fails creation instead of downgrading |
| `web.allow_private` | Let `web_fetch` reach private/loopback/link-local/CGNAT addresses and any port (default: false — public addresses on ports 80/443 only); cloud metadata endpoints stay blocked regardless |
| `web.fetch_max_bytes` | Download ceiling per fetch (default: 2 MiB) |
| `web.fetch_timeout_seconds` | Time ceiling per fetch, redirects and body read included (default: 30) |
| `web.cache_ttl_minutes` | How long a cached response stays fresh (default: 60) |
| `web.search_provider` | `web_search` backend; `brave` is the default and only provider so far |
| `web.search_api_key` | Enables the `web_search` tool; without it the tool is not registered |
| `lsp.disabled` | Turn off the language-server integration: no servers started, no `definition`/`references` tools, no after-edit diagnostics (default: false) |
| `lsp.request_timeout_seconds` | Timeout per language-server request, initialize handshake included (default: 15) |
| `lsp.diagnostics_timeout_seconds` | How long an applied edit waits for fresh diagnostics before giving up quietly (default: 3) |
| `appearance.accent_color` | TUI accent color |

## Providers

| Provider | Name | Default Model | Default Base URL |
|---|---|---|---|
| OpenAI | `openai` | `gpt-4o` | `https://api.openai.com/v1` |
| Anthropic | `anthropic` | `claude-opus-5` | Anthropic API |
| Google Gemini | `gemini` | `gemini-2.5-flash` | Google AI API |
| OpenRouter | `openrouter` | `anthropic/claude-sonnet-4-6` | `https://openrouter.ai/api/v1` |
| OpenAI Responses | `openai-responses` | `gpt-4.1` | `https://api.openai.com/v1` |
| OpenAI-Compatible | `openai-compatible` | `llama3` | `http://localhost:11434/v1` |

Each provider picks a fast, capable default model. Override with `provider.model` in config or `SHHH_MODEL` env var.

`openai` and `openai-responses` are two dialects of the same API. Chat completions (`openai`) is the older, wider-supported shape; the Responses API (`openai-responses`) is what the reasoning families are served through, and what gateways route them to. The conversation goes up as a flat list of typed items rather than messages with attached tool calls, the system prompt travels as `instructions`, and `store` is off — shhh sends the whole conversation each turn, so there is nothing to gain from server-side retention. If a model 404s or complains about its input shape on one, try the other.

Reasoning models reject a sampling temperature and take their effort setting in a `reasoning` block that a vanilla request has no field for. Both are one rewrite rule each on a gateway profile:

```toml
[[rewrite]]
when  = { model = "gpt-5*" }
op    = "set"
path  = "reasoning.effort"
value = "high"

[[rewrite]]
when = { model = "gpt-5*" }
op   = "delete"
path = "temperature"
```

The OpenAI-shaped providers (`openai`, `openai-responses`, `openrouter`, `openai-compatible`) can enumerate their endpoint: the first bare `/model` of a session queries `GET {base_url}/models` and offers what actually answers there, filtered to the chat-capable ids. That is the only way to know the catalog of a local runtime or a private gateway — Ollama, vLLM, LiteLLM — where the curated list is necessarily empty. The query is lazy (nothing runs until you ask), bounded at 10 seconds, cached for the session, and cancellable with Esc; an endpoint that refuses falls back to the curated catalog and says why.

## Gateway profiles

A private or self-hosted gateway is OpenAI-compatible in shape but rarely in detail: one rejects a parameter the upstream forbids, another hands back an id that must not be echoed to it, a third publishes its catalog at a path of its own. Those are per-deployment facts that change without warning, and they have no business living in provider code. A **profile** puts them in your config, where fixing one is an edit instead of a release.

Drop a TOML file in `<config-dir>/providers/` — `~/.config/shhh/providers/gateway.toml`, or the `Application Support` equivalent on macOS. The filename is the provider name unless the file sets one, and the profile registers exactly like a built-in: `--provider gateway`, `provider.default = "gateway"`, `SHHH_PROVIDER=gateway`.

```toml
name        = "gateway"
api         = "openai-chat"            # or "openai-responses" / "anthropic-messages"
base_url    = "https://llm-gateway.internal/v1"
api_key_env = "GATEWAY_API_KEY"        # or api_key = "..." for a literal
models_path = "/v1/models/simple"      # optional: a non-standard catalog endpoint

[headers]
X-Title = "shhh"

[[models]]
id             = "gemini-3.1-pro"
context_window = 1048576
max_tokens     = 65536
cost           = { input = 2.0, output = 12.0, cache_read = 0.2 }

[[rewrite]]
when  = { model = "gemini-*" }
op    = "cut-at"
path  = "messages[].tool_calls[].id"
value = "__thought__"
note  = "The gateway appends __thought__<base64> to tool-call ids; the upstream rejects the fabricated ones when they come back."
```

`shhh providers` lists what resolves on this machine and checks each profile: where it points, whether its key is actually exported, what it declares, and what its rules do — including the `note` on each, because a profile outlives the memory of the incident that caused it.

### Model metadata

Declared models seed the `/model` picker before discovery runs, and supply what a catalog endpoint returning bare ids cannot: pricing for the spend meter, `context_window` for the context gauge. Costs are in dollars per million tokens, the unit model cards publish. Anything you leave out falls back to the public pricing table shhh already downloads (LiteLLM's `model_prices_and_context_window.json`, refreshed daily), so a profile only has to declare what that table gets wrong or has never heard of — `shhh providers` marks each model `profile`, `public table`, or `unpriced`. `cache_read` and `cache_write` are accepted and reported but not yet billed: shhh's usage accounting has no cached-token counters. `max_tokens` is metadata only — shhh does not add it to requests; a gateway that needs it set can get it from a `set-default` rule.

### Rewrite rules

Each rule names a place in the JSON on the wire and an edit to make there. Rules run in file order, so a later rule sees an earlier rule's edit, and they are written against the wire format rather than against shhh's own types — a field shhh doesn't model is still reachable.

| Field | Meaning |
|---|---|
| `when.model` | Glob narrowing the rule to some models (`"gemini-*"`); omit to match every request |
| `direction` | `request` (default) or `response` — response rules run on a JSON body and on each streamed `data:` event |
| `path` | Dotted keys, with `[]` for "every element of this array": `top_p`, `chat_template_kwargs.enable_thinking`, `messages[].tool_calls[].id` |
| `op` | The edit (below) |
| `value` | The operand: what to set, cut at, or trim |
| `to` | The second operand: the new key for `rename`, the replacement for `replace` |
| `note` | Why the quirk exists; printed back by `shhh providers` |

| Op | Effect |
|---|---|
| `delete` | Remove the field — a parameter the upstream rejects |
| `set` | Set it, replacing any value; builds the objects on the way to it, so a rule can add a parameter the request has no place for |
| `set-default` | Set it only when absent or null; also builds missing objects |
| `rename` | Move it to `to` within the same object |
| `cut-at` | Truncate a string at the first occurrence of `value` |
| `trim-prefix` / `trim-suffix` | Remove `value` from either end of a string |
| `replace` | Replace every `value` in a string with `to` |

A path that matches nothing is not an error — a rule with nothing to do does nothing, which is what lets one profile cover a conversation where only some messages carry tool calls. Only `set` and `set-default` create anything; the ops that edit an existing value never invent one, and none of them invent an array. A rule that can't work at all (an unknown op, a `set` with no value, a malformed glob) is refused at load, naming the file and the rule index, and that profile alone is skipped: one bad file never takes the session down.

## Environment Variables

### Universal

| Variable | Description |
|---|---|
| `SHHH_PROVIDER` | Default provider |
| `SHHH_MODEL` | Default model |
| `SHHH_API_KEY` | API key (works with any provider) |
| `SHHH_BASE_URL` | Base URL override |

### Provider-Specific Fallbacks

These are checked when `SHHH_API_KEY` is not set:

| Variable | Provider |
|---|---|
| `OPENAI_API_KEY` | OpenAI |
| `ANTHROPIC_API_KEY` | Anthropic |
| `GEMINI_API_KEY` | Gemini |
| `OPENROUTER_API_KEY` | OpenRouter |

### Precedence

```
CLI flag > SHHH_* env var > Provider-specific env var > Config file > Provider default
```

## Usage

### Generate a command

```bash
shhh compress this directory into a tar.gz
```

After generation you can **Run**, **Edit**, **Revise**, **Explain**, **Copy**, **Save**, or **Cancel**.

For multi-command output, **Run All** executes everything and **Run Step** prompts before each command.

### Chat mode

Multi-turn conversations with file and directory access:

```bash
shhh chat
shhh chat "help me debug this failing test"
shhh chat --continue     # resume the most recent session
shhh chat --resume       # pick a saved chat to resume
```

Every session is autosaved after each exchange (to the `(last session)` slot), so `--continue` always picks up where you left off. Use `/save <name>` inside a session to keep a conversation permanently.

You can also pipe context into a chat — it's attached to your first message:

```bash
cat error.log | shhh chat "why is this failing?"
```

Chat mode has read-only tools (`read_file`, `list_directory`, `search`) plus `execute_command`, which lets the assistant propose shell commands: each one is shown to you with safety warnings and only runs after you approve it with `y`.

How much gets approved automatically is governed by a permission mode, cycled with Shift+Tab or set with `/mode <name>`: **manual** prompts for every consequential tool call (the default), **accept-edits** auto-applies file edits but still prompts for commands, **auto** additionally runs allowlisted commands and sends everything else to an LLM permission classifier, and **plan** is read-only — edits and commands are refused. Read-only tools never prompt in any mode, and safety-flagged commands always ask. The status bar always shows the active mode; `behavior.default_mode` and `behavior.mode_cycle` configure the starting mode and cycle order.

Inspection commands never prompt either, in any mode. A built-in allowlist of commands that cannot change anything — `ls`, `cat`, `head`, `grep`, `rg`, `find`, `git status`/`log`/`diff`/`show`/`blame`, `go list`/`env`/`doc`, `whoami`, and similar — runs straight through, so reading the repository costs no approvals. The list is conservative by construction: anything that compiles or runs project code (`go build`, `go test`, `make`, `npm run`) is *not* on it, flags that turn a read into a write are excluded per command (`find -delete`, `find -exec`, `sort -o`, `git branch -D`, `env CMD…`), any redirection, pipe, chaining, or command substitution disqualifies the whole command, and a safety-flagged command is never matched against it. `behavior.read_only_commands` adds your own entries; `behavior.read_only_auto = false` turns the built-in list off entirely (plan mode still inspects).

In auto mode the classifier (the session model by default, `behavior.classifier_model` to override) judges each remaining tool call against your recent conversation and either runs it, refuses it with a reason the model sees, or falls back to asking you. Every classifier failure — timeout, invalid response, request error — fails closed to a prompt, never to an allow, and safety-flagged commands prompt you even when the classifier approves. The status bar shows `✦ checking` while a decision is in flight, classifier tokens count toward the session totals, and `/mode why` shows the latest denial's reason.

Assistant commands additionally run inside OS-level process containment when a mechanism is available — bubblewrap on Linux (unprivileged user namespaces are probed first), Seatbelt on macOS (deprecated by Apple but functional). Contained commands can write only to the workspace, scratch space, and toolchain caches, and a deny mask that cannot be disabled hides `~/.ssh`, `~/.aws`, `~/.config/gh`, and shhh's own config and state directories (masked paths read as empty and outrank any write grant). The exec confirm prompt shows the containment state, `shhh code doctor` (or `/sandbox` in a session) reports the mechanism and resolved policy, and a policy that can't be enforced faithfully fails the command rather than running it bare. `/run` — your own command — is never contained.

For long or unsupervised runs, `shhh code -p --sandbox` goes a step further and execs approved commands inside a disposable container: Podman or Docker is auto-detected (rootless preferred and reported), the image must be digest-pinned and pass the configured allowlist, and the container gets exactly one writable mount (the workspace), no host environment or credentials, all capabilities dropped, and memory/CPU/pid ceilings. Isolation reporting is honest — `process < container < vm`, each level verified or explained — and a required level that can't be verified (`sandbox.require_isolation`) fails creation rather than silently downgrading. Every container shhh creates is recorded durably; records are reconciled at session start, containers past their TTL are reaped, and `/sandbox list|status|destroy <id>|prune|doctor` manages them in-session.

`shhh code` sessions can also research the web through a guarded client. `web_fetch` retrieves a public http/https URL — HTML is reduced to bounded readable text (title, description, main content), JSON and plain text pass through bounded, and the result cites the final URL — while an SSRF guard blocks private, loopback, link-local, CGNAT, and cloud-metadata addresses (DNS answers are pinned and the connected address re-verified, so rebinding tricks don't help), redirects are re-validated per hop with credential headers stripped cross-origin, and byte/time ceilings bound every request. Fetching counts as an external action: manual and accept-edits modes prompt for it, auto mode sends it to the classifier. Responses are cached on disk (content-addressed, TTL-pruned, `web.cache_ttl_minutes`), and `web.allow_private = true` opts intranet/local-dev targets in (metadata endpoints stay blocked regardless). `web_search` is registered only when `web.search_api_key` is configured (Brave Search); without a key the model doesn't see the tool.

`shhh code` sessions can also verify their work with the repository's own checks through a quality gate. Named suites of checks live in the workspace's trusted `.shhh/quality.json` — each check is a resolved executable plus an argv array, run as-is with no shell — and the model's `quality_gate` tool can only ever name a suite, never supply command text (`/gate run [suite]` and `/gate result` expose the same path to you, with the run happening in the background). Checks run with time, output, and concurrency ceilings, contained by the same OS-level mechanism as assistant commands with a **read-only workspace** by default (a suite sets `"allow_write": true` to opt out; scratch and toolchain caches stay writable either way), and each check's bounded output lands in the evidence store. Every result is fingerprinted against git HEAD plus the porcelain status: a result over a tree that has since changed reports **stale** instead of silently passing, and the verdicts are exactly `pass`, `fail`, `blocked`, or `cancelled` — blocked and cancelled are never a pass. Example config:

```json
{
  "suites": {
    "default": {
      "checks": [
        {"name": "vet", "exe": "go", "args": ["vet", "./..."]},
        {"name": "test", "exe": "go", "args": ["test", "./..."]}
      ]
    }
  }
}
```

Bulky tool results are reduced before the model sees them: output over a size threshold is deterministically cut to a verbatim head and tail plus any flagged lines (errors, panics, test failures) from the elided middle, with terminal control sequences stripped. Small results pass through untouched. Each reduced result carries an opaque evidence id, and the full original is kept under shhh's state dir (user-only permissions, per-session, pruned after a week) where the model can retrieve it with the `evidence` tool — `info`, paged `read`, or literal `search`. The transcript shows exactly the reduced view the model got, and `/evidence` in a session shows store size and reduction stats (`/evidence purge` deletes the stored originals).

`shhh code` sessions also remember across sessions: durable memories — preferences, project conventions, corrections, lessons — live in shhh's local SQLite storage, scoped globally or to the current project (the repository root). A bounded selection (project entries first, hard entry and token caps, no model calls) is injected into the system prompt with each entry cited by id, so a wrong memory is easy to find and delete. The trust rule is absolute: you can add memories directly (`/memory add` or `shhh memory add` — your own words persist as-is), but when the *agent* proposes one through its `remember` tool, a confirm prompt always appears — pick the scope (project or global), optionally amend the entry with a note, or decline — in every permission mode, with no auto-approval and no classifier override, because memory an agent writes to itself is an injection surface. `behavior.memory_disabled`, `behavior.memory_max_entries`, and `behavior.memory_max_tokens` tune it.

A wrong turn costs one command, not the session: a checkpoint is recorded at the start of every user turn, and `/rewind` (interactive picker, or `/rewind <n>` directly) truncates the conversation back to just before a chosen turn. The abandoned tail is never lost — it's kept as a **branch** of the current session, and `/branches` lists the session's branch family and switches between them (the working conversation is saved before every switch, and `/save`/`/load` work on any branch). Rewind is honest about its scope: it restores conversation state only — files on disk are untouched, and the rewind message says so — and each checkpoint records the git HEAD and dirty status at the time, so the message can tell you when the working tree or HEAD has diverged since.

`shhh code` sessions also pick up best-in-class external code tools when they're installed: `fd` (fast, gitignore-aware file finding), `ast_grep` (language-aware structural search, and structural rewrites as **preview diffs**), `sd` (find-and-replace previews across files — always run with `--preview`), `tokei` (per-language codebase composition summary), and `jaq` (jq-style queries over JSON files). Each tool is registered only when its binary is on PATH — no binary, no tool. None of them can write a file: rewrites and replacements come back as previews the agent applies through the normal `edit_file` approval flow, argv construction is injection-safe (model-supplied values ride as `--flag=value` or behind a literal `--`, and jaq's file-reading/in-place flags aren't in the vocabulary at all), every search path is resolved against the workspace root and containment-checked before anything spawns, and every run has a timeout and output cap — a missing binary, timeout, or flood degrades to a clean tool error, with large results reduced through the evidence store like any other output.

`shhh code` sessions can also manage named long-running processes — dev servers, watchers, test runners — through the `process` tool: start one (approved like any command, with safety warnings, allowlist matching, and mode policy applying to the command text), probe it with `status`, page through its captured output with `read`, feed its stdin with `input`, and tear it down with `stop`. Each process runs in its own process group with its working directory contained to the workspace and an environment of exactly `PATH` and `HOME` plus whatever vars the agent passes explicitly (which can never shadow those two). Recent stdout/stderr live in bounded ring buffers for paged reads, the full log (bounded) lands in the evidence store when the process ends, and `/ps` lists everything the session owns. `stop`, session end, cancel, and quit all terminate the full process tree — no orphans.

`shhh code` can delegate scoped work to background **sub-agents**. The model spawns them with `spawn_agent` (you approve each spawn) in one of two roles: a **researcher** gets read-only tools plus the web against the real workspace, and a **writer** gets the full toolset against an *isolated git worktree* — its changes never touch your checkout directly, they come back as a single patch you review and apply. `/agents` (or Ctrl+A) is the agent manager: attach to a child's live session, steer it mid-run, cancel a turn, or kill it; `/attach <name>` jumps straight into one and `/detach` (or Esc) comes back; `agent_report` collects a child's final report.

All of that works **while the turn is in flight**, which is the only time sub-agents exist: commands run mid-turn, not just between turns. Type `/agents`, `/attach writer-1`, `/stats`, `/diff`, `/mode auto`, `/ps` — or open focus mode with Ctrl+E — while the agent works, and the turn keeps streaming underneath; plain text still queues as a steering message. The exceptions are the handful of commands that would rewrite or replace the conversation the agent is working in — `/clear`, `/compact`, `/rewind`, `/branches`, `/load`, `/chats`, `/model`, `/run` — which say what they'd disturb and wait for the turn to end (Ctrl+C ends it now). They drop out of the completion menu for the duration rather than failing when you pick them.

Sub-agents inherit the parent session's permission state rather than re-litigating it. A child is clamped to your mode — it can never be more permissive than you are — and it inherits your session grants (`[a]` on a prompt), your command allowlist, the read-only inspection list, and, in auto mode, the same permission classifier the parent uses. That last one matters in practice: without it, an auto-mode session still stopped to ask about every command its children ran. Safety-flagged commands still prompt, plan mode still refuses, and every child approval is routed to you labeled with the agent's name.

Which model a child runs on is configurable: `agents.model` sets the default for every sub-agent, `agents.profiles.<role>.model` overrides it per role (a cheap, fast model for wide research fan-out; the session model for writing code), and a `spawn_agent` call may name a `model` explicitly for one child. `"inherit"` at either level means the session model. `/model default <name>` persists the session default to your config file, and `/model agents <name>` persists the sub-agent model — both without leaving the session.

Concurrent writers can't overwrite each other (separate worktrees, reviewed patches), but two patches over the same file still conflict. A writer spawn may declare `paths` — the globs it intends to change — and the supervisor refuses a second writer whose claim overlaps a live one, telling the model to sequence the work or narrow its scope instead. A declared scope is passed into the writer's own prompt, and when a patch touches files another agent's applied patch already changed, the approval card says so before you apply it.

`shhh code` sessions also see their code the way an editor does, through the project's own language server. Common servers are auto-detected on PATH — `gopls`, `rust-analyzer`, `typescript-language-server`, `pyright` — and started lazily the first time a file they own is touched; no server on PATH is simply a no-op. After every applied `write_file`/`edit_file`, fresh diagnostics for the touched file are appended to the tool result (bounded, errors first), so the model sees the type error it just introduced and fixes it in the same round. The model also gets `definition` and `references` tools — point at a symbol occurrence by file, line, and identifier text and get bounded `file:line` answers — steering it away from grep when it needs actual semantics. Servers are owned by the session (shut down when it ends), every request is bounded by a timeout so a hung server can't wedge the agent loop, and `lsp.disabled = true` turns the whole thing off.

Reviewing the agent's edits is a first-class surface, not raw text. Every diff — the approval preview and the transcript row an applied edit leaves behind — renders with syntax highlighting (by file type, with add/remove coloring layered over it), line numbers, and background-tinted intraline emphasis on the changed span of a modified line. An applied edit lands in the transcript as one collapsed row (`✎ edit path  +12 −4 · 2 hunks`); in focus mode (Ctrl+E), Enter expands it in place to a bounded unified view, and Enter again opens it full screen — scroll with `j`/`k`, jump hunks with `n`/`p`, and toggle a side-by-side layout with `s` (automatic on terminals ≥ 120 columns). The approval card offers the same full view with `d`. `/diff` shows the cumulative session diff — every file this session changed, read from its own changeset record rather than from `git diff`, so it works in a directory that was never a repository — on the review surface below, read-only, since a cumulative diff has nothing to stage.

A turn ends with what it did rather than with the last thing it said. A finished turn closes with a summary row — `✓ Done · 4 steps · 18 tools · 1m 04s · $0.14`, with the round counter right-aligned — and, when it wrote anything, a second row carrying the mutation rail: `▎✎ 3 files changed +30 −4 · [v] review · [u] undo turn`, with what git knew about those files (`all tracked in git`, `2 tracked · 1 new`, `no git here`) on the right. If the turn ran the quality gate or a test command, a third row states the verdict and its tally (`✓ go test ./internal/agent/... passing · 41 packages · 12.8s`); several runs collapse into one `2 of 3 passing`. A turn that changed nothing gets the first row alone, and a turn you cancelled or one whose stream broke says `⊘ Cancelled` or `✗ Failed` and still reports what it changed before it stopped. The rows are ordinary transcript entries — they re-render on resize like everything else — and the keys they offer are handled by focus mode on the row, so the input keeps `v` and `u` for typing: Ctrl+E, then `v` opens that turn in review mode. (`u` names the offer the undo story fills in; until then it says so, and points at the records it will restore from — nothing about the turn has been discarded.) The counts come from the session's own per-turn record, so they are the same numbers `/diff` and the inspector rail quote.

Reviewing a turn is one surface rather than a scroll back through the feed. `/review` — or `[v]` on a turn's changeset row — takes over the screen with the files that turn touched down the left, each with a staging box and its own `+N −M`, and the focused file's hunks down the right. The turn's verdict is pinned under the file list, so the failing test sits beside the hunks that claim to fix it, and which sub-agent wrote which file is on the row it wrote. `space` stages the hunk under the cursor, `s` the whole file, `A` (or `a`) everything at once, `j`/`k` moves between files and `n`/`p` between hunks; `[enter]`'s label counts what is staged as you stage it. The hunks are the same renderer the approval card, the transcript row and `/diff` use — unified by default, paired side by side on terminals ≥ 120 columns or with `\`, and below 60 columns the list and the pane stack rather than truncating each other. Nothing in review is destructive: `⛨ nothing is committed` stays on screen the whole time, `esc` leaves having changed nothing, and for edits already on disk the staged selection is what an undo would restore.

Tool activity renders as a compact feed, not walls of output: every tool call and command lands on one column grid — a gutter, a glyph for the kind of act, an eight-column verb from a closed vocabulary (`read`, `search`, `glob`, `lsp`, `web`, `edit`, `write`, `patch`, `run`, `memory`, `spawn`, `agent`), the target, then a right-aligned outcome and a six-column duration (`⚙ read    main.go:10–20    81 lines  0.6s`, `▎$ run     go test ./...    exit 0   12s`) — so a long turn is scanned down a column instead of read row by row. Anything that changed your machine — a write, a command, a refusal — carries a mutation rail (`▎`) in the gutter, and a row that broke keeps a red one, so scrolling back finds the moments that mattered without hunting. Calls under half a second omit their duration rather than spending a column on `0.0s`. A refusal is not a failure: a call you declined reads `⊘ … denied · you`, one a rule refused reads `⊘ … denied · auto · plan mode · /mode why`, and neither is confused with `✗`. Raw output is never shown by default; focus mode (Ctrl+E, then Enter) expands a row in place, failed rows auto-expand to a bounded view with the error first, and a running command shows a live tail of its last output line right in the row while it executes. A turn's calls fold under numbered step headers, and inside a step a run of three or more consecutive read-only calls collapses into one counted row — `▸ ⚙ 6 reads · 2 searches   [enter] expand  3.9s` — that states exactly what it swallowed and what it cost; Enter in focus mode restores those rows in place and folds them back again. Mutations, failures, refusals and sub-agent rows are never folded into a group, so a fold only ever hides chrome. `/ui verbosity <low|normal|high>` picks the density for the session: `low` shows step headers only, `normal` folds the read-only runs, `high` expands every row with its bounded detail body. Colour never carries meaning on its own here — every state pairs its colour with a glyph or a word, so `/ui mono on` (and `NO_COLOR`, which turns it on for you) strips every surface to two greys and nothing becomes ambiguous: the mode segment keeps `⏵⏵`/`⏸`, a refusal keeps `⊘ denied · auto`, diff lines keep `+`/`−`, staged files keep `[x]`, and an agent waiting on you keeps its `⚠`. Syntax highlighting and coloured markdown are declined rather than recoloured, so assistant prose marks emphasis with `**` instead.

The status bar is a cockpit rail of session vitals: the active permission mode, the tool-round counter mid-turn (`round 7/25`), a context occupancy meter (`ctx ▰▰▰▰▰▱▱▱ 62%`) that changes color at the same thresholds that trigger automatic trimming, token usage and estimated cost, the running sub-agent count with a blocked badge, and the active model (dropped first when the terminal narrows). Past the trim threshold, the oldest tool results are automatically elided from the conversation before the next request.

On a wide terminal the transcript stops being the whole screen. Past 130 content columns the surface splits into a transcript pane and a 46-column inspector rail, divided by a single `│` column, so the session stops being interrogated with `/stats` and `/diff` for what it already knows: **THIS TURN** (steps so far, tool count, elapsed), **CHANGES** (`+N −M` for the turn, one row per changed file carrying the same mutation rail its edit row did, and the command that came back broken), **AGENTS** (each running child with its current target, tool count and spend), **CONTEXT** (occupancy of the model's window at the same thresholds that trigger trimming, the token counts, and a per-round burn sparkline), and **SPEND** (this turn, split between the orchestrator and its children, plus the session total). A block with nothing to say is omitted rather than drawn empty, and the rail never scrolls — when it runs out of room it truncates its longest block and says how many rows it hid. The split is horizontal only, so the rail costs no rows: the transcript keeps its height and wraps to the narrower pane. Takeover surfaces — an approval card, a picker, the full-screen diff, the agent manager — span the full width and hide the rail, restoring it when they are dismissed, and below 130 columns the single-pane layout is exactly what it was. `/ui` reports the layout in force.

Both rails read one accounting. Every turn's usage — tokens in and out, the part the provider served from its prompt cache, cost and wall time — is kept as a bounded per-turn history with running totals, which is what feeds the rail's burn sparkline and the session totals; evicting old turns costs history, never the total. Context occupancy is accounted by category rather than as a single percentage: the system prompt, the project context injected into it, the tool definitions, the transcript, and tool output. `/stats` prints that breakdown, the session's tokens (with the cached share) and cost, and the last turn's spend and duration — the same numbers the rails show, from the same source. Where the provider reports the size of the request it carried, the categories are scaled onto it; where it does not — before the first response, after a trim, after `/compact` or a rewind — the meter falls back to shhh's own estimate and says `estimated` rather than passing a guess off as a measurement.

Typing `/` in the input opens a completion menu over the commands this session actually has wired — ↑↓ moves, Tab completes, Enter runs the highlighted command, Esc dismisses. Completion continues past the command name: subcommands (`/memory add`, `/sandbox prune`, `/ui verbosity high`) and known values (saved chat names for `/load`, branch names for `/branches`, the model catalog for `/model`, turn numbers for `/rewind`) complete the same way, filtered on the token under the cursor.

Where a command's argument is really a choice from a known set, the bare command opens a select list instead of printing usage text: `/model` and `/mode` over the model catalog (live from the provider's endpoint where it has one) and the mode cycle, `/rewind` over the session's checkpoints, and `/load` / `/chats` over the saved chats (turn count and last-written time on each row) and `/branches` over the session's branch family (current branch marked and focused, each row naming its parent). `/run` joins them when the last response holds several code blocks: each row shows the block's first line, its language, and how many lines it holds, with a flattened preview underneath, and picking one drops into the usual confirmation with its safety warnings intact. ↑↓ moves, Enter applies, Esc cancels. With nothing to pick — no saved chats, no branches yet, a single code block — the command keeps its plain text answer or its direct path rather than opening a one-row list.

Slash commands inside a chat session:

| Command | Description |
|---|---|
| `/help` | Show commands and keybindings |
| `/clear` | Start a new conversation (also `/new`) |
| `/copy [code]` | Copy the last response (or just its code blocks) |
| `/run [n]` | Run a code block from the last response (asks for confirmation, shows safety warnings; output goes back into the conversation). Bare `/run` opens a picker when the response holds several blocks |
| `/model [name]` | Show or switch the model mid-session (same provider) |
| `/model default [name]` | Show or persist the default model for new sessions (`provider.model`) |
| `/model agents [name]` | Show or persist the model sub-agents run on (`agents.model`; `inherit` follows the session) |
| `/compact` | Summarize the conversation via the model and continue from the summary (frees context) |
| `/stats` | This session's context occupancy by category, token and cost totals (cached share included), and the last turn's spend and duration |
| `/evidence [purge]` | Tool-output evidence store: reduction stats and size; `purge` deletes the stored originals |
| `/gate run [suite]`, `/gate result` | Quality gate (`shhh code`): run a named suite of the project's own checks in the background, then show the verdict (marked stale if the tree changed) |
| `/diff` | Cumulative session diff, full screen: every file this session changed, from its own per-turn record (works outside git) |
| `/review [turn]` | Review what a turn changed: file list with per-file `+N −M` and staging, hunk pane beside it, the turn's verdict pinned under the files (bare reviews the last turn that changed anything; also `[v]` on its changeset row). Nothing is applied |
| `/agents` | Agent manager (also Ctrl+A): attach, steer, cancel a turn, kill — live while the parent turn runs |
| `/attach [name]` | Attach to a sub-agent's session and steer it (bare `/attach` opens the manager); `/detach` returns |
| `/ui verbosity <v>` | Activity feed density: `low` shows step headers only, `normal` folds read-only runs into a counted row, `high` expands every row (bare `/ui` also reports the pane layout) |
| `/ui mono <on\|off>` | Strip every surface to two greys; glyphs, words and layout carry the states. `NO_COLOR` turns it on for the session |
| `/memory` | Durable memories (`shhh code`): `list` (default), `add [global] [kind] <text>`, `forget <id>` |
| `/ps` | List the long-running processes this session owns (`shhh code`): state, pid, uptime, command |
| `/rewind [n]` | Rewind to before a user turn (bare `/rewind` opens a picker); the abandoned tail is kept as a branch. Conversation only — files are not restored |
| `/branches [n]` | Switch this session's branches (bare `/branches` opens a picker; current work is saved first) |
| `/save [name]` | Save this chat |
| `/load [name]` | Load a saved chat (bare `/load` opens a picker) |
| `/chats` | Saved chats — the same picker; Enter loads |
| `/exit` | Quit (also `/quit`, `/q`, Ctrl+D) |

Press Up/Down in an empty input to recall previous messages, Esc to clear the input, and Ctrl+C to cancel a streaming response (or clear the input / quit when idle). Commands run while the agent is working, except the ones that rewrite the running conversation. Type `/help` in a session for the full list.

### Pipe mode

Use in scripts or pipelines — activated when stdin is not a TTY or with `--raw`:

```bash
echo "list all docker containers" | shhh
shhh --raw "find large files" | sh
```

When both stdin content and arguments are provided, the stdin is injected as context:

```bash
cat error.log | shhh "explain this error"
```

### Explain a command

```bash
shhh -e find files larger than 100mb
```

### Shell integration

Add inline command generation to your shell with Ctrl+K:

```bash
# zsh
eval "$(shhh init zsh)"

# bash
eval "$(shhh init bash)"

# fish
shhh init fish | source
```

Type a description on your command line, press Ctrl+K, and it's replaced with the generated command.

### Project context

Create a `.shhh` file in your project root to give shhh context about your project's tooling and conventions:

```bash
shhh init --project
```

The contents of `.shhh` are appended to the system prompt when running shhh from that directory (or any subdirectory).

## Commands

| Command | Description |
|---|---|
| `shhh [prompt]` | Generate a shell command |
| `shhh chat [prompt]` | Start an interactive chat session |
| `shhh chat --continue` | Resume the most recent chat session |
| `shhh chat --resume` | Pick a saved chat to resume |
| `shhh config` | Interactive configuration wizard |
| `shhh config set <key> <value>` | Set a config value |
| `shhh init <shell>` | Output shell integration snippet |
| `shhh init --project` | Create a `.shhh` project context file |
| `shhh history` | Browse past prompts and commands |
| `shhh metrics` | Show provider usage statistics |
| `shhh rate` | Rate recent commands (feeds accuracy metrics) |
| `shhh snippets` | List saved command snippets |
| `shhh snippets run <name>` | Execute a saved snippet |
| `shhh snippets copy <name>` | Copy a snippet to clipboard |
| `shhh snippets show <name>` | Display a snippet |
| `shhh snippets delete <name>` | Delete a snippet |
| `shhh memory list` | List durable memories (current project + global) |
| `shhh memory add [--global] [--kind k] <text>` | Add a memory (preference, convention, correction, lesson) |
| `shhh memory forget <id>` | Delete a memory by id |
| `shhh providers` | List providers and check gateway profiles |
| `shhh completion <shell>` | Generate shell completion script |

### Flags

| Flag | Description |
|---|---|
| `--provider <name>` | LLM provider |
| `--model <name>` | Model name |
| `--api-key <key>` | API key |
| `--raw` | Force pipe mode (no TUI) |
| `-e, --explain` | Automatically explain the generated command |
| `-s, --silent` | Suppress explanation output |
| `--version` | Print version |

## Safety Warnings

shhh detects potentially destructive commands (recursive deletes, force pushes, disk writes, etc.) and prompts for confirmation before execution. Disable with:

```bash
shhh config set behavior.safety_warnings false
```

## Data Storage

History, snippets, metrics, and chat logs are stored in a local SQLite database:

- macOS: `~/Library/Application Support/shhh/shhh.db`
- Linux: `$XDG_DATA_HOME/shhh/shhh.db` or `~/.local/share/shhh/shhh.db`

## Development

```bash
make test       # Run tests
make race       # Run tests with race detector
make lint       # Run vet and golangci-lint
make fmt        # Format code
make build-all  # Cross-compile for darwin/linux (amd64/arm64)
```

The TUI surfaces are pinned by golden-file renders under
`internal/ui/components/testdata/golden` and `internal/ui/chat/testdata/golden`:
each activity row kind and state, the approval card's variants, the diff
viewer's three modes, the agent list, the inspector rail, the transcript's step
outline, and the prompt frame in all four layout modes — captured at 60, 80,
110 and 130 columns, in colour and in monochrome. Each file holds the render
twice: once with ANSI stripped, so the columns are readable in a diff, and once
with the escapes kept (`ESC` written as `␛`), so a changed colour assignment
shows up too. When a layout change is intended, regenerate them:

```bash
go test ./internal/ui/components ./internal/ui/chat -update-golden
```

## License

MIT
