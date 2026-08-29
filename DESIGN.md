# shhh — Design Document

> A natural-language-to-shell tool written in Go.
> Turn plain English into executable commands, inline or via prefix.

---

## 1. Interaction Modes

### 1a. Prefix Mode (`shhh <prompt>`)

The primary interface. User types a natural language instruction; shhh streams back a command.

```
$ shhh find all go files modified in the last week
  find . -name "*.go" -mtime -7

  [Run]  [Edit]  [Revise]  [Copy]  [Cancel]
```

### 1b. Inline / Hotkey Mode

A shell-level keybinding (e.g. `Ctrl+K`) intercepts the current line buffer, sends it to the LLM, and replaces/appends the result directly on the command line — no secondary UI, no sub-shell.

Implementation per shell:

| Shell | Mechanism |
|-------|-----------|
| **zsh** | Custom ZLE widget bound via `bindkey` |
| **bash** | `bind -x` with `READLINE_LINE` / `READLINE_POINT` manipulation |
| **fish** | `bind` + `commandline` builtin |

`shhh init <shell>` prints the appropriate shell snippet to stdout, intended for `eval "$(shhh init zsh)"` in the user's rc file.

### 1c. Chat Mode (`shhh chat`)

Multi-turn conversational session. Useful for exploratory tasks ("how do I set up a cron job that...").

The assistant has read-only filesystem tools (`read_file`, `list_directory`, `search`) that run automatically, and an `execute_command` tool that requires per-command user approval: the command is shown with safety warnings and runs only after the user confirms. Users can also run code blocks from a response themselves with `/run`, and command output is fed back into the conversation either way.

### 1d. Pipe / Non-Interactive Mode

When stdin is not a TTY, shhh reads the prompt from stdin and writes only the raw command to stdout — no chrome, no interactivity. Enables composition:

```
echo "list open ports" | shhh | sh
```

---

## 2. Core Features

### Command Generation
- Detect user's shell (`$SHELL`) and OS at runtime
- System prompt includes shell type, OS, and cwd for context-aware generation
- Strip markdown fences / backticks from LLM output

### Command Explanation
- On-demand (`-e` / `--explain` flag, or select "Explain" in the action menu)
- Streams a plain-English breakdown of each part of the command
- Skippable via config (`silent_mode = true`) or `-s` flag

### Revision Loop
- After generation, user can provide follow-up feedback ("make it recursive", "use fd instead of find")
- Sends conversation history to the LLM for context-aware refinement
- No limit on revision rounds

### Edit Before Run
- Opens the generated command in a single-line readline editor (not $EDITOR)
- Pre-populated with the generated command; user can tweak and confirm

### Copy to Clipboard
- Cross-platform clipboard write (pbcopy / xclip / xsel / wl-copy)
- Fallback: print command and warn if no clipboard tool found

### Shell History Append
- After execution, append the *actual command run* (not the prompt) to the shell's history file
- Support: `~/.bash_history`, `~/.zsh_history`, `~/.local/share/fish/fish_history`

### Command Execution
- Execute via `$SHELL -c "<command>"` with inherited stdio
- Preserve exit codes — shhh exits with the child's code

---

## 3. AI Provider Architecture

### Multi-Provider Support

shhh should support multiple LLM backends from day one. All providers implement a common interface:

```go
type Provider interface {
    StreamCompletion(ctx context.Context, messages []Message, opts CompletionOpts) (<-chan StreamEvent, error)
    Name() string
}
```

### Providers

1. **OpenAI** (GPT-4o, o3, etc.) — default
2. **Gemini** (Google's Gemini API)
3. **OpenRouter** (unified gateway to many models)
4. **OpenAI-compatible** — any endpoint that speaks the OpenAI chat completions API (Ollama, vLLM, LM Studio, Groq, Together, etc.)

### Provider Resolution

```
CLI flag (--provider, --model)
  → Environment variable (SHHH_PROVIDER, SHHH_MODEL)
    → Config file
      → Default (openai / gpt-4o)
```

### API Key Resolution

```
CLI flag (--api-key)
  → Provider-specific env var (OPENAI_API_KEY, GEMINI_API_KEY, OPENROUTER_API_KEY)
    → Config file
      → Keychain / secret store (stretch goal)
```

---

## 4. Terminal Rendering

### Library Choice: Bubbletea + Lipgloss (Charmbracelet)

The [Charm](https://charm.sh) stack is the Go ecosystem's best answer for terminal UI:

| Library | Role |
|---------|------|
| **bubbletea** | Elm-architecture TUI framework — manages state, input, rendering. `View()` returns a `tea.View`: the screen plus the terminal states the surface asks for (alt screen, mouse mode) |
| **lipgloss** | Styled string rendering (colors, borders, padding). A `Style` holds a resolved colour and renders at full fidelity; the profile is decided once, in `components` (DESIGN-TUI.md §10a) |
| **bubbles** | Pre-built components (spinner, text input, list). The transcript pane is shhh's own: it windows a cached line list rather than taking its content as a string (DESIGN-TUI.md §10m) |

### Rendering Strategy

**Streaming output** is the primary UX. The LLM response streams token-by-token into the transcript pane. The user sees text appear in real time.

```
┌─────────────────────────────────────┐
│  ⣾ Thinking...                      │   ← spinner (during initial latency)
│                                     │
│  find . -name "*.go" -mtime -7      │   ← streamed output (replaces spinner)
│                                     │
│  ▸ Run   Edit   Revise   Copy   ✕   │   ← action bar (appears when stream ends)
└─────────────────────────────────────┘
```

**Key rendering details:**

- **Spinner**: Shown during the initial network round-trip before first token. Use `bubbles/spinner` with a dot-style animation.
- **Streaming text**: Written into the transcript pane, which holds the rendered history as lines and draws only the window the scroll offset names (DESIGN-TUI.md §10m). Each `StreamEvent` token appends to the buffer and triggers a re-render.
- **Action bar**: Horizontal selection menu rendered with lipgloss. Arrow keys or single-letter shortcuts (`r`, `e`, `c`) to select.
- **Colors**: Minimal palette — one accent color (configurable), dim for secondary text, bold for commands. Respect `NO_COLOR` env var.
- **Inline mode**: No bubbletea — direct `fmt.Fprint` to stderr to avoid corrupting the shell's line buffer. Spinner via simple `\r`-overwrite loop.

### Graceful Degradation

- Dumb terminals / non-TTY: plain text, no ANSI
- `NO_COLOR=1`: strip all color codes
- Narrow terminals (< 40 cols): drop borders, compact action bar
- Inline images: drawn by the terminal where it says it can, as half-blocks
  where there is colour, and as a density ramp where there is none — a staged
  screenshot is still a picture on a monochrome terminal (DESIGN-TUI.md §12h)

---

## 5. Configuration

### File Location

Follow XDG on Linux, `~/Library/Application Support` on macOS:

```
$XDG_CONFIG_HOME/shhh/config.toml   (Linux)
~/Library/Application Support/shhh/config.toml  (macOS)
~/.config/shhh/config.toml          (fallback)
```

### Format: TOML

```toml
[provider]
default = "openai"
model = "gpt-4o"

[provider.openai]
api_key = "sk-..."

[provider.gemini]
api_key = "AI..."
model = "gemini-2.5-flash"

[provider.openrouter]
api_key = "sk-or-..."
model = "anthropic/claude-sonnet-4-6"

[provider.openai_compatible]
api_key = "..."
base_url = "http://localhost:11434/v1"
model = "llama3"

[behavior]
silent_mode = false     # skip explanations
shell = ""              # auto-detect if empty

[appearance]
accent_color = "cyan"
```

### Interactive Config (`shhh config`)

Bubbletea-driven config wizard for setting API keys, selecting default provider/model, toggling options.

---

## 6. Shell Integration (`shhh init`)

`shhh init <shell>` emits a shell snippet that the user evals in their rc file.

What it sets up:
- **Inline keybinding** (`Ctrl+K` default) — captures current line, calls `shhh --inline`, replaces line buffer with result
- **Alias** (optional) — `alias ai="shhh"` for familiarity

Example output for zsh:

```zsh
_shhh_inline() {
  local result
  result=$(shhh --inline "$BUFFER" 2>/dev/null)
  if [[ -n "$result" ]]; then
    BUFFER="$result"
    CURSOR=${#BUFFER}
  fi
  zle redisplay
}
zle -N _shhh_inline
bindkey '^K' _shhh_inline
```

---

## 7. Project Structure

```
shhh/
├── cmd/
│   └── shhh/
│       └── main.go              # entrypoint
├── internal/
│   ├── cli/                     # command definitions (cobra)
│   │   ├── root.go
│   │   ├── chat.go
│   │   ├── config.go
│   │   └── init.go
│   ├── provider/                # LLM provider interface + implementations
│   │   ├── provider.go          # interface definition
│   │   ├── openai.go
│   │   ├── gemini.go
│   │   ├── openrouter.go
│   │   └── openai_compat.go
│   ├── prompt/                  # system prompts, message construction
│   │   └── prompt.go
│   ├── ui/                      # bubbletea models
│   │   ├── generate.go          # main generation flow
│   │   ├── action.go            # run/edit/revise/copy menu
│   │   ├── chat.go              # chat mode UI
│   │   └── config.go            # config wizard UI
│   ├── shell/                   # shell detection, history, init snippets
│   │   ├── detect.go
│   │   ├── history.go
│   │   └── init.go
│   ├── config/                  # TOML config read/write
│   │   └── config.go
│   └── clipboard/               # cross-platform clipboard
│       └── clipboard.go
├── go.mod
├── go.sum
├── DESIGN.md
└── README.md
```

---

## 8. Key Dependencies

| Dependency | Purpose |
|------------|---------|
| `github.com/spf13/cobra` | CLI command/flag parsing |
| `charm.land/fang/v2` | Styled `--help`, errors and man pages around cobra |
| `charm.land/bubbletea/v2` | TUI framework |
| `charm.land/lipgloss/v2` | Terminal styling |
| `charm.land/bubbles/v2` | Spinner, text input |
| `charm.land/glamour/v2` | Markdown rendering in the transcript |
| `github.com/charmbracelet/ultraviolet` | The screen the chat surface draws into and the layout engine that splits it (DESIGN-TUI.md §10n); the terminal event vocabulary the capability probe reads (§10k) |
| `github.com/BurntSushi/toml` | Config file parsing |
| `github.com/sashabaranov/go-openai` | OpenAI / OpenAI-compatible API client |
| `google.golang.org/genai` | Gemini API client |

---

## 9. Build & Distribution

- **Single static binary** — `CGO_ENABLED=0 go build`
- Cross-compile for: `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`
- **Install methods**:
  - `go install github.com/<user>/shhh/cmd/shhh@latest`
  - Homebrew tap
  - GitHub Releases (goreleaser)
  - AUR (stretch)

---

## 10. Phased Roadmap

### Phase 1 — MVP
- [ ] Prefix mode (`shhh <prompt>`) with streaming output
- [ ] OpenAI provider
- [ ] Action menu: Run, Copy, Cancel
- [ ] Config file with API key
- [ ] Shell detection + history append
- [ ] `shhh init` for zsh

### Phase 2 — Multi-Provider & Polish
- [ ] Gemini provider
- [ ] OpenRouter provider
- [ ] OpenAI-compatible provider (Ollama, vLLM, LM Studio, etc.)
- [ ] Revision loop
- [ ] Edit before run
- [ ] Explanation mode
- [ ] `shhh init` for bash and fish
- [ ] Interactive config wizard (`shhh config`)
- [ ] Pipe/non-interactive mode

### Phase 3 — Advanced
- [ ] Inline/hotkey mode (Ctrl+K)
- [ ] Chat mode
- [ ] Context injection (pipe file contents as context: `cat error.log | shhh fix this`)
- [ ] Command safety classification (warn before destructive commands like `rm -rf`)
- [ ] Custom system prompt overrides in config
- [ ] Shell completions (`shhh completion zsh/bash/fish`)

---

## 11. Open Questions

- **Name collision**: Is `shhh` available on Homebrew / pkg.go.dev? Fallback names: `shh`, `nsh`, `ask`.
- **Inline mode UX**: Should Ctrl+K replace the buffer silently, or show a brief spinner on the line? The silent approach is cleaner but gives no feedback during latency.
- **Multi-command output**: If the LLM returns multiple commands (e.g. a pipeline), should we split them into individually runnable steps or treat the whole thing as one unit?
- **Safety net**: How aggressive should destructive-command warnings be? Allowlist of "safe" commands vs. blocklist of dangerous patterns (`rm -rf /`, `dd`, `mkfs`)?
