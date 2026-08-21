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
| `behavior.default_mode` | Permission mode sessions start in: `manual` (default), `accept-edits`, `auto`, or `plan` |
| `behavior.mode_cycle` | Shift+Tab mode order (default: `["manual", "accept-edits", "auto", "plan"]`) |
| `behavior.classifier_model` | Model auto mode's permission classifier uses (default: the session model) |
| `behavior.classifier_timeout_seconds` | Timeout per classifier request (default: 30) |
| `behavior.classifier_max_tokens` | Max tokens for the classifier's response (default: 1024) |
| `behavior.classifier_retries` | Extra attempts before a failed classifier check falls back to prompting (default: 1) |
| `behavior.system_prompt_extra` | Extra text appended to the system prompt |
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
| `appearance.accent_color` | TUI accent color |

## Providers

| Provider | Name | Default Model | Default Base URL |
|---|---|---|---|
| OpenAI | `openai` | `gpt-4o` | `https://api.openai.com/v1` |
| Anthropic | `anthropic` | `claude-opus-5` | Anthropic API |
| Google Gemini | `gemini` | `gemini-2.5-flash` | Google AI API |
| OpenRouter | `openrouter` | `anthropic/claude-sonnet-4-6` | `https://openrouter.ai/api/v1` |
| OpenAI-Compatible | `openai-compatible` | `llama3` | `http://localhost:11434/v1` |

Each provider picks a fast, capable default model. Override with `provider.model` in config or `SHHH_MODEL` env var.

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

In auto mode the classifier (the session model by default, `behavior.classifier_model` to override) judges each remaining tool call against your recent conversation and either runs it, refuses it with a reason the model sees, or falls back to asking you. Every classifier failure — timeout, invalid response, request error — fails closed to a prompt, never to an allow, and safety-flagged commands prompt you even when the classifier approves. The status bar shows `✦ checking` while a decision is in flight, classifier tokens count toward the session totals, and `/mode why` shows the latest denial's reason.

Assistant commands additionally run inside OS-level process containment when a mechanism is available — bubblewrap on Linux (unprivileged user namespaces are probed first), Seatbelt on macOS (deprecated by Apple but functional). Contained commands can write only to the workspace, scratch space, and toolchain caches, and a deny mask that cannot be disabled hides `~/.ssh`, `~/.aws`, `~/.config/gh`, and shhh's own config and state directories (masked paths read as empty and outrank any write grant). The exec confirm prompt shows the containment state, `shhh code doctor` (or `/sandbox` in a session) reports the mechanism and resolved policy, and a policy that can't be enforced faithfully fails the command rather than running it bare. `/run` — your own command — is never contained.

For long or unsupervised runs, `shhh code -p --sandbox` goes a step further and execs approved commands inside a disposable container: Podman or Docker is auto-detected (rootless preferred and reported), the image must be digest-pinned and pass the configured allowlist, and the container gets exactly one writable mount (the workspace), no host environment or credentials, all capabilities dropped, and memory/CPU/pid ceilings. Isolation reporting is honest — `process < container < vm`, each level verified or explained — and a required level that can't be verified (`sandbox.require_isolation`) fails creation rather than silently downgrading. Every container shhh creates is recorded durably; records are reconciled at session start, containers past their TTL are reaped, and `/sandbox list|status|destroy <id>|prune|doctor` manages them in-session.

Bulky tool results are reduced before the model sees them: output over a size threshold is deterministically cut to a verbatim head and tail plus any flagged lines (errors, panics, test failures) from the elided middle, with terminal control sequences stripped. Small results pass through untouched. Each reduced result carries an opaque evidence id, and the full original is kept under shhh's state dir (user-only permissions, per-session, pruned after a week) where the model can retrieve it with the `evidence` tool — `info`, paged `read`, or literal `search`. The transcript shows exactly the reduced view the model got, and `/evidence` in a session shows store size and reduction stats (`/evidence purge` deletes the stored originals).

The status bar shows token usage, estimated cost, the current context size, and the active model. The context indicator changes color as the conversation approaches the model's context window (from the pricing table when known); past that threshold, the oldest tool results are automatically elided from the conversation before the next request.

Slash commands inside a chat session:

| Command | Description |
|---|---|
| `/help` | Show commands and keybindings |
| `/clear` | Start a new conversation (also `/new`) |
| `/copy [code]` | Copy the last response (or just its code blocks) |
| `/run [n]` | Run a code block from the last response (asks for confirmation, shows safety warnings; output goes back into the conversation) |
| `/model [name]` | Show or switch the model mid-session (same provider) |
| `/compact` | Summarize the conversation via the model and continue from the summary (frees context) |
| `/evidence [purge]` | Tool-output evidence store: reduction stats and size; `purge` deletes the stored originals |
| `/save [name]` | Save this chat |
| `/load <name>` | Load a saved chat |
| `/chats` | List saved chats |
| `/exit` | Quit (also `/quit`, `/q`, Ctrl+D) |

Press Up/Down in an empty input to recall previous messages, Esc to clear the input, and Ctrl+C to cancel a streaming response (or clear the input / quit when idle). Type `/help` in a session for the full list.

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

## License

MIT
