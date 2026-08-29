# AGENTS.md

## Overview

`shhh` is a Go CLI tool that turns natural language into executable shell commands. It has four interaction modes: prefix (`shhh <prompt>`), inline/hotkey (`Ctrl+K` in shell), chat (`shhh chat`), and a coding agent (`shhh code`). The TUI is built with Bubble Tea v2 (charm.land/bubbletea/v2) and the LLM backend supports Anthropic, OpenAI, Gemini, and OpenRouter via a pluggable provider registry.

## Commands

| Task | Command |
|------|---------|
| Build | `make build` |
| Test all | `go test ./...` |
| Test single package | `go test ./internal/<pkg>` |
| Test with race detector | `make race` |
| Format | `make fmt` (runs gofmt + goimports) |
| Lint | `make lint` (go vet + golangci-lint) |
| Tidy modules | `make tidy` |
| CI suite | `make ci` |
| Update golden files | `go test ./internal/ui/components ./internal/ui/chat -update-golden` or `SHHH_UPDATE_GOLDEN=1 go test ./...` |

Build produces a `shhh` binary with version injected via `-ldflags`.

## Architecture

```
cmd/shhh/main.go          Entry point (cobra root command, executed through fang)
internal/
  cli/                     All cobra commands (root, chat, code, init, doctor, etc.)
  agent/                   Front-end-agnostic agentic loop (conversation, tool dispatch, approval queue, round cap)
  provider/                LLM provider interface + implementations (anthropic, openai, gemini, openrouter)
  ui/chat/                 Bubble Tea chat TUI model (the main interactive surface)
  ui/components/           Reusable TUI components (cards, lists, diffs, selectors)
  ui/golden/               Golden-file test framework for layout regression
  subagent/                Sub-agent orchestration (spawn_agent tool, worktrees, fan-out)
  tools/                   Tool definitions and execution (read-only, mutating, execute_command)
  config/                  TOML config loading (~/.config/shhh/config.toml)
  storage/                 SQLite persistence (sessions, memories, observations, snippets)
  sandbox/                 OS-level process containment (bubblewrap on Linux, Seatbelt on macOS)
  lsp/                     Language server integration (auto-detected, lazy-started)
  quality/                 Quality-gate runner (configurable check suites)
  changeset/               Per-turn edit tracking (before/after content, undo support)
  scope/                   Working-scope management (directory grants, deny mask)
  diff/                    Unified diff generation and patch application
  web/                     Web tools (fetch, search) with policy guards
  profile/                 Provider profile loading (gateway endpoints)
  prompt/                  System prompt construction
  safety/                  Command safety analysis
  memory/                  Durable memory (cross-session remembered facts)
  evidence/                Evidence store for quality-gate output
  plan/                    Plan mode state (step tracking)
  resolve/                 Provider resolution from flags/config/env
  project/                 Project context detection (language, framework, recent files)
  shell/                   Shell detection
  process/                 Background process management (the process tool)
  structural/              Optional external tools integration (ast-grep, fd, jaq, sd, tokei)
  radius/                  Blast-radius analysis for edits
  preflight/               Startup checks
  update/                  Version update checking
```

## Key Design Patterns

### Agent Loop

The `internal/agent` package is a **passive state machine** — front-ends (the chat TUI or headless runner) drive it step-by-step. The same `Agent` backs both the interactive TUI and the headless `shhh code -p` runner. The `Headless` type in `headless.go` drives the agent synchronously for scripted/sub-agent use.

### Tool Security Tiers

Tools are split into three permission tiers that must never be mixed:

1. **Read-only** (`ReadOnly()`): `read_file`, `list_directory`, `search`, `glob_files` — auto-execute without approval
2. **Execute** (`ExecCommandTool()`): `execute_command` — requires user approval or policy match
3. **Mutating** (`Mutating()`): `write_file`, `edit_file` — require approval in manual mode, auto-apply in accept-edits/auto

The `Execute()` function in `tools/tools.go` deliberately only dispatches read-only tools. Mutating calls route through `ExecuteMutating()`. This separation is a security invariant.

### Permission Modes

Four modes (S-059) control approval flow: `manual`, `accept-edits`, `auto`, `plan`. The auto mode uses an LLM classifier (S-060) that **always fails closed** — classifier errors never approve, they fall back to asking the human.

### Provider Interface

All providers implement `StreamCompletion(ctx, messages, opts) (<-chan StreamEvent, error)`. Providers register via `provider.Register(name, factory)` with a `Factory func(ResolveOpts) (Provider, error)`. Provider names are normalized (underscores become hyphens).

### TUI State Machine

The chat TUI (`internal/ui/chat/model.go`) distinguishes between **turn states** (what the session's work is doing) and **surface states** (what borrows the screen). A surface can overlay while a turn keeps running underneath. This split is why `turnState()` / `setTurnState()` exist separately from `Model.state`.

### Sub-agents

`shhh code` can spawn child agents via `spawn_agent`. Children are either `researcher` (read-only tools + web) or `writer` (full toolset against an isolated git worktree). Hard limits: max 3 concurrent, 16 total per session. Writer children produce patches that the parent approves.

## Testing

### Golden Files

TUI layout tests use golden files in `testdata/golden/` directories. Golden files capture renders at multiple terminal widths (60, 80, 110, 130 columns) in both color and mono palettes. Each golden has two blocks: ANSI-stripped layout and escaped-ANSI for color assertions.

**To update goldens after an intentional layout change:**
```
go test ./internal/ui/components ./internal/ui/chat -update-golden
```

A `TestMain` in each golden-using package calls `golden.Run(m)` which **deletes stale golden files** that no test touched. Adding/removing a test case therefore requires running with `-update-golden` to reconcile files.

### Test Conventions

- Tests live alongside their source (`foo_test.go` beside `foo.go`)
- Table-driven tests are the norm
- No external test dependencies (no testify); tests use stdlib `testing`
- SQLite storage tests use `OpenPath` with a temp file or `:memory:`
- The LSP package has integration tests that spawn real language servers

## Configuration

Config is TOML at `~/.config/shhh/config.toml` (XDG on Linux, `~/Library/Application Support/shhh/` on macOS). Key sections: `[provider]`, `[behavior]`, `[sandbox]`, `[web]`, `[lsp]`, `[appearance]`, `[history]`, `[agents]`.

## Gotchas

- **CGO_ENABLED=0**: The build is pure Go (uses `modernc.org/sqlite`, not cgo sqlite3). Never add cgo dependencies.
- **S-numbers in comments** (e.g. `S-060`, `S-142`): These are internal spec/story identifiers. They cross-reference DESIGN.md and DESIGN-TUI.md sections. Preserve them when editing nearby code.
- **Provider name normalization**: Underscores become hyphens in the registry (`open_ai` → `open-ai`). Use the normalized form when registering or resolving.
- **The deny mask is not configurable**: The sandbox's built-in deny mask (credential stores, shhh's own state) cannot be disabled. Only `deny_extra` can add to it.
- **Bubble Tea message routing**: shhh's own messages are typed structs (not interfaces with methods). When adding new async operations, add a corresponding `type fooMsg struct{}` and handle it in the `Update` switch. Note that some of Bubble Tea v2's *own* messages are interfaces — `tea.KeyMsg` covers presses and releases, `tea.MouseMsg` covers click/motion/release/wheel — so match `tea.KeyPressMsg` and the specific mouse types rather than the interface.
- **`View()` returns a `tea.View`, not a string**: the screen's content plus the terminal states the surface asks for (`AltScreen`, `MouseMode`). Tests that want the painted screen read `.View().Content`. Programs start through `internal/cli/program.go` so they all get the colour profile `components.Profile()` resolved the palette against.
- **Colours are resolved when styles are built, not when they are drawn**: a `lipgloss.Style` holds one `color.Color`, so a `components.Token` picks its truecolor/256/16 rung through `Token.Color()` at `newStyles` time. Changing the palette *or* the profile means rebuilding every derived style — both go through `applyPalette`.
- **Golden file deletion**: `golden.Run(m)` removes any `.txt` file in `testdata/golden/` that wasn't asserted during the run. Don't manually create golden files; let the test framework generate them.
- **Tool-round cap is a checkpoint, not a limit**: The default 150-round cap pauses for user input rather than terminating. Sub-agents default to uncapped (`UnlimitedToolRounds = -1`) because they have no one to ask.
- **Storage is single-connection SQLite**: `SetMaxOpenConns(1)` is intentional. The WAL journal mode and busy timeout handle concurrency; don't open multiple `*DB` instances to the same file.
- **Version injection**: The `version` var in `internal/cli` is set via `-ldflags` at build time. It defaults to `"dev"` when built without flags.
- **Releases**: Handled by GoReleaser v2 triggered on `v*` tags via GitHub Actions.
