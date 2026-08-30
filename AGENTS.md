# AGENTS.md

## Overview

`shhh` is a Go CLI tool that turns natural language into executable shell commands. It has four interaction modes: prefix (`shhh <prompt>`), inline/hotkey (`Ctrl+K` in shell), chat (`shhh chat`), and a coding agent (`shhh code`). The TUI is built with Bubble Tea v2 (charm.land/bubbletea/v2) and the LLM backend supports Anthropic, OpenAI, Gemini, and OpenRouter via a pluggable provider registry.

## Documentation

Two skills in [`.agents/skills/`](.agents/skills/) carry the working guidance.
`sound-patterns` is how to settle a question about style or convention —
default to what the Go standard library does, and measure rather than assert.
`documentation` is the [working
guide](.agents/skills/documentation/SKILL.md) — where a fact belongs, the
citation convention, and the test a comment has to pass to earn its place,
with worked before/after examples.
[`docs/README.md`](docs/README.md) is the architecture it follows. The short
version:

**Each document answers one question, and that decides where a fact belongs.**
`docs/product.md` is what shhh is; `docs/architecture.md` is the big shapes and
why; `docs/capabilities/` is what it does and why that exists;
`docs/interface/` is what every surface obeys; **this file** is where the code
is and what will bite you. A fact filed under the wrong question rots there.

**Documents name no Go symbol. Code cites documents.** The dependency points
one way. A document that can name a function drifts silently when the function
is renamed, so the map from intent to code lives here in AGENTS.md, and the map
from code to intent is the citation in the comment:

```go
// Commands always carry the mutation rail: shhh cannot know whether a command
// wrote something, so it assumes it did.
// See docs/interface/principles.md#weight-tracks-risk.
```

The reason goes in the comment as prose. **The citation is a pointer to the
long form, never a substitute for the reason** — a reader who does not open the
document must still understand why the line is the way it is.

**Cite only for product and design decisions.** Local mechanics do not get a
citation: `SetMaxOpenConns(1)` is explained by the sentence next to it, and
pointing at a document for it would be noise.

**Headings are anchors — treat one like an exported symbol.** Renaming a
heading breaks every citation to it, silently. Rename deliberately and fix the
citations in the same commit. `make docs-check` verifies every citation
resolves and lists documents nothing cites; it runs as part of `make ci`.

**Exact visual specification is not in this repository.** Column widths, colour
rungs, glyph assignments and the artboards are normative in the `shhh Design
System` project in Claude Design, read with the DesignSync tool. Don't re-draw
an artboard in Markdown — it becomes a second source of truth that disagrees
with the first.

### Never reference a story or a plan

**No comment, document or test name may refer to a story, a sprint, a backlog
item or anything under `.plan/`.** That directory is not part of the
repository, so such a reference points at something the reader cannot open —
and even where it can be opened, it answers "when was this built", which is
not a question the code should be asking.

Say what the code does and why. Where the reason is a product or design
decision, cite the document that holds it. If the reason is not captured in
`docs/` yet, add the section — that is the direction the dependency runs.
Planning cites the capabilities in `docs/`; `docs/` never cites planning.

`make docs-check` fails on a story identifier anywhere in the code or a
golden fixture, so this cannot drift back.

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
| Check doc citations | `make docs-check` |
| Update golden files | `go test ./internal/ui/components ./internal/ui/chat -update-golden` or `SHHH_UPDATE_GOLDEN=1 go test ./...` |

Build produces a `shhh` binary with version injected via `-ldflags`.

## Version control

**Never stage with `git add -A`, `git add .`, or `git commit -a`.** Name the
paths. A blanket add commits whatever else is in the tree — a half-finished
experiment, a scratch file, a golden nobody meant to update — and the mistake
is invisible in the diff you reviewed, because you never reviewed those files.

**Scope every commit to the work of the session that produced it.** If you did
not change a file for the reason you are committing, it does not belong in the
commit, even when it is already dirty and even when it is a one-line fix. A
commit that carries a stranger cannot be reverted, cited, or read as a unit,
which are the only three things a commit is for.

**Commit on the branch that is checked out.** `master` included — do not
create a branch first. Where the work should live is the author's call and
they have usually already made it by checking something out; branching on
their behalf moves the commit somewhere they did not ask for and have to go
looking for.

All three rules yield to an explicit instruction to do otherwise.

## Architecture

```
cmd/shhh/main.go          Entry point (cobra root command, executed through fang)
internal/
  cli/                     All cobra commands (root, chat, code, init, doctor, etc.)
  agent/                   Front-end-agnostic agentic loop (conversation, tool dispatch, approval queue, round cap, repeat detection)
  provider/                LLM provider interface + implementations (anthropic, openai, gemini, openrouter)
  pricing/                 Model data: prices, context windows, reasoning flags — downloaded table over the built-in snapshot (`make model-data`)
  meter/                   Session spend ledger + the provider gate every request is billed at
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
  prompt/                  System prompt construction (per-command prompts + the registered-toolset section)
  safety/                  Command safety analysis
  memory/                  Durable memory (cross-session remembered facts)
  skill/                   Agent Skills: discovery, SKILL.md frontmatter, the catalog prompt block and the activation tool
  todo/                    The project backlog: one Markdown item per file under .shhh/todo, the ready set and its order, the archive
  secret/                  Session secrets: the vault, the scrub every text passes through, and the prompt block naming them
  migrate/                 Layout migrations, detected and offered by `shhh doctor` (never at startup)
  evidence/                Evidence store for quality-gate output
  plan/                    Plan mode state (step tracking)
  resolve/                 Provider resolution from flags/config/env
  project/                 Project context detection (language, framework, recent files)
  shell/                   Shell detection
  process/                 Background process management (the process tool)
  structural/              Optional external tools integration (ast-grep, fd, jaq, sd, tokei)
  radius/                  Blast-radius analysis for edits
  preflight/               Startup checks
  update/                  Release check behind `shhh update` and the startup nudge
```

## Key Design Patterns

This section is the map from the shapes described in [`docs/architecture.md`](docs/architecture.md) to where they live. The *why* is in the docs; the *where* is here.

### Agent Loop

The `internal/agent` package is a **passive state machine** — front-ends (the chat TUI or headless runner) drive it step-by-step. The same `Agent` backs both the interactive TUI and the headless `shhh code -p` runner. The `Headless` type in `headless.go` drives the agent synchronously for scripted/sub-agent use. Why it is passive: [`docs/architecture.md#one-agent-several-front-ends`](docs/architecture.md#one-agent-several-front-ends).

### Tool Security Tiers

Tools are split into three permission tiers that must never be mixed:

1. **Read-only** (`ReadOnly()`): `read_file`, `list_directory`, `search`, `glob_files` — auto-execute without approval
2. **Execute** (`ExecCommandTool()`): `execute_command` — requires user approval or policy match
3. **Mutating** (`Mutating()`): `write_file`, `edit_file` — require approval in manual mode, auto-apply in accept-edits/auto

The `Execute()` function in `tools/tools.go` deliberately only dispatches read-only tools. Mutating calls route through `ExecuteMutating()`. This separation is a security invariant — different functions rather than one function with a branch, for the reason in [`docs/architecture.md#tiers-not-permissions`](docs/architecture.md#tiers-not-permissions). Merging them always looks like a simplification; it isn't.

### Permission Modes

Four modes control approval flow: `manual`, `accept-edits`, `auto`, `plan`. The auto mode uses an LLM classifier that **always fails closed** — classifier errors never approve, they fall back to asking the human. There is no path where "could not decide" becomes yes, and the zero value must stay the one that costs nothing. See [`docs/capabilities/approvals-and-safety.md`](docs/capabilities/approvals-and-safety.md).

### Provider Interface

All providers implement `StreamCompletion(ctx, messages, opts) (<-chan StreamEvent, error)`. Providers register via `provider.Register(name, factory)` with a `Factory func(ResolveOpts) (Provider, error)`. Provider names are normalized (underscores become hyphens). What the interface deliberately does not abstract over: [`docs/capabilities/providers.md`](docs/capabilities/providers.md).

### TUI State Machine

The chat TUI (`internal/ui/chat/model.go`) distinguishes between **turn states** (what the session's work is doing) and **surface states** (what borrows the screen). A surface can overlay while a turn keeps running underneath. This split is why `turnState()` / `setTurnState()` exist separately from `Model.state`.

### TUI Components

Components are **plain state plus two methods** — an update that takes a key
press and reports whether it is done, and a view that takes an explicit width
— not nested Bubble Tea models. The chat `Model` owns them via its states. No
sub-programs, no goroutines inside a component.

Every component is handed a width and must handle a narrow terminal by
stacking rather than truncating its hints. The column-grid field widths are
constants in `activityrow.go`, and every surface drawing a transcript row uses
them, so a grid change is a one-line change.

Diffs are computed in `internal/diff` from the old and new content the edit
tools already hold — no shelling out to `git diff` except for the session-wide
diff view.

The step outline is a layer over the entry list in `internal/ui/chat/steps.go`
rather than a component: it groups history instead of rendering a widget.

Reasoning is a row like any other act: `internal/ui/chat/think.go` owns the
`think` row — where the round's thinking is collected as it streams, its three
fold depths, and the verbosity that drops it. The text it shows is not the
reasoning the next request replays; that stays with the agent as the
provider's own signed blocks, and dropping them turns the second round of
every thinking turn into a 400 (see the Gemini and Anthropic notes below).

The attached sub-agent view is not a separate surface — the chat `Model`
renders whichever agent is focused, and every agent including the orchestrator
is an `internal/agent` instance with its own transcript, queue and mode.
Attaching switches the focused agent.

### Skills

`internal/skill` follows the Agent Skills specification ([agentskills.io](https://agentskills.io/specification)) and its client guide. `skill.Roots` is the search order (project `.shhh/skills`, `.agents/skills`, `.claude/skills` from cwd up to the git root, then the user-scope ones), `skill.Discover` reads them into a `Catalog` with lenient validation — a skill that cannot load is a `Diagnostics` entry, never an error, for the reason in [`docs/capabilities/skills.md#where-skills-live`](docs/capabilities/skills.md#where-skills-live). The frontmatter reader in `frontmatter.go` is deliberately not a YAML parser: it takes everything after the first colon as the value, because skills written for other harnesses do.

Three tiers, three places: `skill.PromptBlock` is the catalog in the system prompt (appended as prompt extra, like the toolbox, only when something loaded); the `skill` tool (`skill.ToolDefinition`, read-only, its `name` an enum of the catalog) returns `skill.Content` — body, directory, bundled files listed not read; the file tools do the rest. `/skill <name> [task]` and the `/<skill-name>` shortcut (`internal/ui/chat/skills.go`) send the same content as a user message. `agent.KeepResults(skill.IsContent)` exempts activated content from `TrimOldToolResults`. `allowed-tools` is parsed and displayed and grants nothing — [`docs/capabilities/skills.md#a-skill-cannot-grant-itself-anything`](docs/capabilities/skills.md#a-skill-cannot-grant-itself-anything). `shhh skills` and `/skills` print the catalog with its diagnostics.

### Backlog

`internal/todo` is the project backlog ([`docs/capabilities/todo.md`](docs/capabilities/todo.md)): `todo.Root(cwd)` keys it on the repository root, `todo.Load(root)` reads `.shhh/todo/*.md` and `.shhh/todo/done/*.md` into a `Store` whose `Items` are in `todo.Less` order (priority, created, slug) and whose `Ready()` is open items with every `depends_on` in the archive. The header reader in `header.go` keeps every line's text so `SetStatus`/`SetSize` rewrite one line and nothing else; `Render` is only for a new file. Loading is lenient the way skills are: a file that cannot be read at all — no header, no title, an unknown status — is a `Diagnostics` entry, and a value merely off its scale (priority, size, kind) is a warning on the loaded `Item`. Slugs are validated by `todo.ValidSlug` and must never look like a planning identifier, for the docs-check reason above. `Create` writes `.shhh/todo/.gitignore` ignoring `.run/`; whether the backlog itself is committed is the user's call and nothing in shhh stages it. `shhh todo` (`internal/cli/todo.go`) prints the store, and `todoManager` there backs the textual `/todo` subcommands. The chat side is `internal/ui/chat/todo.go`: `Todos`/`WithTodos` wiring from `internal/cli/chat.go`, a cached `Model.todoStore` reloaded by `reloadTodos` on the events that can change a file (a `/todo` command, the editor returning, a turn ending in `turn.go`) and never per frame, `openTodoPick` over the generic picker, `openTodoEditor` reusing the draft editor's `editorArgv`/`editorRefusal` with its own `todoEditorDoneMsg` so the item file is never removed, and `inspectorTodo` feeding the rail's `todoBlock` (`internal/ui/components/inspector.go`, between PLAN and CHANGES). The `.shhh` path is a directory now: the context file is `.shhh/project.md` (`project.ContextFile`), `shhh init --project` writes it there, and a checkout still holding the old single `.shhh` file is a `shhh doctor` migration (`internal/migrate/project.go`), not something the context reader falls back to.

### Secrets

`internal/secret` is the vault of values the model never sees ([`docs/capabilities/secrets.md`](docs/capabilities/secrets.md)). A `secret.Vault` holds name→value; `Environ()` is what a command gets, `Scrub()` is what everything read gets, and `PromptBlock` names the secrets to the model. The CLI owns the vault (`internal/cli/secrets.go`) and every door: `chatSession.openSecrets` runs before the first command and puts the values in `runner.SetSessionEnv` (every captured runner sets `cmd.Env` from `runner.Environ()`), `process.Supervisor.SetEnv` (the process tool builds a bare environment and would otherwise miss them), and `sandbox.Container.ExecArgv`'s `--env NAME` pass-through (a container has no host environment). The scrub sits on the executor chain (`Vault.WrapExecutor`, after evidence reduction), on the runners (`scrubRunner`, `scrubTailRunner`, `scrubContainment` — the tail is what the screen shows), on the agent (`Agent.SetScrub`, applied in `Append`, `SetMessages` and `Stream`) and on the stream closure in `buildSessionEnv` and the sub-agent `newEnv`. Children get the same through `subagent.Env.Scrub`. `/secret` (`internal/ui/chat/secrets.go`) calls the CLI's `secretsManager`, which returns the note and an announcement the chat sends the model as a user message — the system prompt cannot be rewritten mid-session. `Scrub` also replaces base64/hex/URL-escaped forms and any run of the value ≥ 8 bytes; it is text matching and the doc says so.

### Sub-agents

`shhh code` can spawn child agents via `spawn_agent`. Children are either `researcher` (read-only tools + web) or `writer` (full toolset against an isolated git worktree), or any custom profile the user wrote to `~/.config/shhh/agents/<name>.toml`. Hard limits: max 3 concurrent, 16 total per session — a limit on attention, not on resources ([`docs/capabilities/subagents.md`](docs/capabilities/subagents.md)). Children that can write or execute produce patches that the parent approves.

Profiles are three layers, and the split matters: `config.AgentDefinition` (`internal/config/agents.go`) is the file as written and validates what needs no runtime — names, tiers, tool names against tiers; `subagent.Profile` (`internal/subagent/profile.go`) is what the supervisor decides by — worktree or not, starting mode, default budgets — and knows nothing about tools or prompts; `internal/cli/subagents.go` translates a definition into a child's toolset and prompt (`profileEnv`) and checks the mode and reasoning names, because those vocabularies belong to `agent` and `provider`, which `config` must not import. The two built-in roles are `subagent.BuiltinProfiles()` and keep their hand-written prompts; a file named `researcher.toml` or `writer.toml` replaces one. The file format and examples are in [`docs/agents/README.md`](docs/agents/README.md).

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

Config is TOML at `~/.config/shhh/config.toml` (or `$XDG_CONFIG_HOME/shhh/`), the same on every platform — see [`docs/capabilities/configuration.md#one-layout-everywhere`](docs/capabilities/configuration.md#one-layout-everywhere). Key sections: `[provider]`, `[behavior]`, `[sandbox]`, `[web]`, `[lsp]`, `[appearance]`, `[history]`, `[agents]`, `[secrets]` (names only; values come from the environment). Agent profiles are one TOML file each in the `agents/` directory beside it (`config.AgentDirs`), loaded by `config.LoadAgents`. User-scope skills are directories under `skills/` beside it (`config.SkillDirs`); project-scope ones come from the checkout (`skill.ProjectRoots`).

## Gotchas

- **CGO_ENABLED=0**: The build is pure Go (uses `modernc.org/sqlite`, not cgo sqlite3). Never add cgo dependencies.
- **Never add a story identifier** (`S-060`, `E-018`) to a comment, a document or a test name. They are gone from the code and `make docs-check` fails on one. Say what the code does and cite `docs/` — see [Never reference a story or a plan](#never-reference-a-story-or-a-plan).
- **Provider name normalization**: Underscores become hyphens in the registry (`open_ai` → `open-ai`). Use the normalized form when registering or resolving.
- **The deny mask is not configurable**: The sandbox's built-in deny mask (credential stores, shhh's own state) cannot be disabled. Only `deny_extra` can add to it.
- **Bubble Tea message routing**: shhh's own messages are typed structs (not interfaces with methods). When adding new async operations, add a corresponding `type fooMsg struct{}` and handle it in the `Update` switch. Note that some of Bubble Tea v2's *own* messages are interfaces — `tea.KeyMsg` covers presses and releases, `tea.MouseMsg` covers click/motion/release/wheel — so match `tea.KeyPressMsg` and the specific mouse types rather than the interface.
- **`View()` returns a `tea.View`, not a string**: the screen's content plus the terminal states the surface asks for (`AltScreen`, `MouseMode`). Tests that want the painted screen read `.View().Content`. Programs start through `internal/cli/program.go` so they all get the colour profile `components.Profile()` resolved the palette against.
- **The chat surface's geometry lives in `internal/ui/chat/layout.go`** ([`docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it`](docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it)): `columns()` and `surface()` split the terminal into rectangles with `ultraviolet/layout`, and `contentWidth`, `paneWidth`, `transcriptWidth` and `viewportHeight` read them. Don't add a new `width - something` in a renderer — add a rectangle to the split and read it. Blocks are placed with `drawIn`, which clips to the rectangle, so a renderer never has to measure what it is about to overflow.
- **Colours are resolved when styles are built, not when they are drawn**: a `lipgloss.Style` holds one `color.Color`, so a `components.Token` picks its truecolor/256/16 rung through `Token.Color()` at `newStyles` time. Changing the palette *or* the profile means rebuilding every derived style — both go through `applyPalette`.
- **Golden file deletion**: `golden.Run(m)` removes any `.txt` file in `testdata/golden/` that wasn't asserted during the run. Don't manually create golden files; let the test framework generate them.
- **The investigation rules in `BuildAgent` are load-bearing**: the "Finding things" section — batch independent calls, make one search answer the question, never repeat a call you already made — is there because a real session spent all 150 rounds re-running the same searches. It reads like padding and is not; see the comment on `BuildAgent` and [`docs/capabilities/coding-agent.md#finding-things`](docs/capabilities/coding-agent.md#finding-things).
- **Never name a tool in a base system prompt**: the optional toolset is assembled from what the machine turned out to have (a language server was detected, a binary is on PATH, a key is configured), so a prompt that names one promises a tool the session may not have. `prompt.Toolbox` describes the tools actually registered and is appended as prompt extra after the last one joins.
- **Gemini pairs tool results by function *name*, not by id**: `FunctionResponse.Name` must be the name of the function called, and the Gemini API sends no `functionCall.id` at all — the ids in `provider.ToolCall` are ours. Don't "simplify" `toGeminiContents` back to putting `ToolCallID` in that field; it addresses every result to a function the model never called, and the model just calls again. Gemini 3 thought signatures ride the same parts and must go back on the part they arrived on.
- **Tool-round cap is a checkpoint, not a limit**: The default 150-round cap pauses for user input rather than terminating. Sub-agents default to uncapped (`UnlimitedToolRounds = -1`) because they have no one to ask.
- **Three directories, one layout, no per-platform branch**: config (`config.Paths`), data (`storage.Dir`) and cache (`pricing.CacheDir`) each follow XDG on every platform including macOS. Don't reintroduce a `runtime.GOOS == "darwin"` branch in any of them — the retired `~/Library` layout is a migration in `internal/migrate`, not a fallback, and the reasons are in [`docs/capabilities/configuration.md#one-layout-everywhere`](docs/capabilities/configuration.md#one-layout-everywhere).
- **A migration is a `shhh doctor` check, never a startup step and never a command of its own**: add a detector to `migrate.detectors` returning a `Pending`; leave `Pending.Apply` nil when the change needs a person's judgement, and the doctor row will report it without offering a key. See [`docs/capabilities/configuration.md#a-migration-is-a-doctor-check`](docs/capabilities/configuration.md#a-migration-is-a-doctor-check).
- **Storage is single-connection SQLite**: `SetMaxOpenConns(1)` is intentional. The WAL journal mode and busy timeout handle concurrency; don't open multiple `*DB` instances to the same file.
- **Version injection**: The `version` var in `internal/cli` is set via `-ldflags` at build time. It defaults to `"dev"` when built without flags.
- **Releases**: Handled by GoReleaser v2 triggered on `v*` tags via GitHub Actions.
