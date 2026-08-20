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
| `behavior.system_prompt_extra` | Extra text appended to the system prompt |
| `appearance.accent_color` | TUI accent color |

## Providers

| Provider | Name | Default Model | Default Base URL |
|---|---|---|---|
| OpenAI | `openai` | `gpt-4o` | `https://api.openai.com/v1` |
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
```

Chat mode has access to read-only tools: `read_file`, `list_directory`, and `search`.

The status bar shows token usage, estimated cost, the current context size, and the active model.

Slash commands inside a chat session:

| Command | Description |
|---|---|
| `/help` | Show commands and keybindings |
| `/clear` | Start a new conversation (also `/new`) |
| `/save [name]` | Save this chat |
| `/load <name>` | Load a saved chat |
| `/chats` | List saved chats |
| `/exit` | Quit (also `/quit`, `/q`, Ctrl+D) |

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
| `shhh config` | Interactive configuration wizard |
| `shhh config set <key> <value>` | Set a config value |
| `shhh init <shell>` | Output shell integration snippet |
| `shhh init --project` | Create a `.shhh` project context file |
| `shhh history` | Browse past prompts and commands |
| `shhh metrics` | Show provider usage statistics |
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
