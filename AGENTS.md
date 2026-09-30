# AGENTS.md

`shhh` is a Go CLI that turns natural language into shell commands. It has
four modes: one-shot (`shhh cmd <prompt>`), inline (`Ctrl+K` in the shell), a
read-only conversation with persona sub-agents (`shhh chat`), and a coding
agent (`shhh code`). The TUI is Bubble Tea v2 (`charm.land/bubbletea/v2`).

## Scope and restraint

Make the smallest change that is correct, safe and verifiable, following the
local pattern. Don't refactor, rename, reformat, update dependencies, rewrite
tests or expand documentation unless the requested behaviour needs it. A
review reports concrete, evidenced defects, not style preferences or
speculative refactors. A broadly applicable, low-risk fix that prevents
serious harm may be called out separately as a windfall; don't fold it in.

## Documentation

`.agents/skills/documentation/SKILL.md` is the working guide;
`sound-patterns` settles style (default to what the Go stdlib does, measure).

- **Each document answers one question.** `docs/product.md` is what shhh is,
  `docs/architecture.md` the big shapes and why, `docs/capabilities/` what it
  does and why, `docs/interface/` what every surface obeys, this file where
  the code is and what will bite you.
- **Documents name no Go symbol; code cites documents.** The reason goes in
  the comment as prose, then the pointer:
  `// See docs/interface/principles.md#weight-tracks-risk.` Cite only product
  and design decisions, never local mechanics.
- **Headings are anchors.** Renaming one breaks its citations; fix them in the
  same commit. `make docs-check` verifies every citation resolves.
- **Never reference a story, sprint, backlog item or `.plan/`** in a comment,
  document or test name. `make docs-check` fails on a story identifier.
- **Exact visual specification lives in the `shhh Design System` project in
  Claude Design** (read with DesignSync), not in Markdown here.

### Where the model reads it

The model learns about a tool only from its definition's schema and
description, its `prompt.Toolbox` line, or a system-prompt paragraph. **The
base prompt names no tool**: the toolset depends on the machine, and a model
promised a tool it lacks tries to use it. The line says what to do; the reason
is prose in `docs/capabilities/`, cited from the comment beside it
([`docs/capabilities/coding-agent.md#the-agent-knows-what-this-machine-has`](docs/capabilities/coding-agent.md#the-agent-knows-what-this-machine-has)).

## Commands

| Task | Command |
|------|---------|
| Build / test one package | `make build` / `go test ./internal/<pkg>` |
| **Quality gate** (the done rule) | `make test vet lint fmt-check docs-check` |
| Format | `make fmt` |
| Loopback contracts / containment | `make test-contract` / `make test-integration` |
| CI pipeline | `make ci` (the gate plus `cross` and `tui-check`) |
| Update goldens | `go test ./internal/ui ./internal/ui/components ./internal/ui/chat -update-golden` |
| Capture a TUI scene | `make tui-shot SCENE=<name> COLS=110 ROWS=40` |
| Eval suite (costs real requests) | `make eval` |

## Testing

- `make test` is hermetic: no TCP listener, clipboard, container, daemon or
  network. Tests that cross a real boundary carry a build tag (`contract`,
  `integration`) so they never compile into it.
- **`httptest` opens a real listener.** Use `internal/testhttp` through the
  package's transport seam; replace host executables at their test seam. In a
  restricted shell use a fresh writable `GOCACHE`. See
  `.agents/skills/testing/SKILL.md`.
- **Never `os.Chdir` or `t.Chdir` in a test**: it makes the package
  uncacheable. Pass the directory in, and point destructive fixtures at a
  `t.TempDir()`, never the real filesystem.
- Stdlib `testing` only (`teatest` is the one harness allowed), table-driven,
  beside the source. Don't add `-count=1` to `make test`.
- **Goldens** are in `testdata/golden/` at 60/80/110/130 columns, colour and
  mono. `golden.Run(m)` deletes every golden no test touched, so add or remove
  a case with `-update-golden`; never hand-write one. Anything the screen
  draws reads the held `clock()`, never `time.Now`.

### Driving the binary

A golden proves the render, `internal/ui/chat/program_test.go` the route, and
a driven scene (`scripts/tui/`, tmux plus `fakeprovider.py`) the terminal.
**A change to a surface is accepted with both a golden and a driven capture**,
and the capture has to be read. Compare its `.txt` with the golden's layout
block, never the `.ansi`. Wait on text only the surface draws. Guide:
`.agents/skills/tui-drive/SKILL.md`.

## Version control

- **Never `git add -A`, `git add .` or `git commit -a`.** Name the paths.
- Scope each commit to this session's work, even when other files are dirty.
- Commit on the checked-out branch, `master` included; don't branch first.
- Subject: `type(scope): summary` with type `feat`, `fix`, `docs`, `refactor`,
  `perf`, `test`, `build`, `ci`, `chore` or `revert`; imperative, lower case,
  no trailing period, under 72 characters. A breaking change to flags, config
  keys or an on-disk format takes `!` and a `BREAKING CHANGE:` footer. A
  subject that needs "and" is two commits. The body says what the diff cannot.

These yield to an explicit instruction to do otherwise.

## Architecture

Package doc comments and the comments at each trap are the detailed map.
Packages not listed are named for what they do.

```
cmd/shhh/main.go   entry point (cobra root, executed through fang)
internal/
  cli/             every cobra command, and each surface's assembly
  agent/           front-end-agnostic agent loop; Headless drives it unattended
  provider/        provider interface and the four dialects
  ui/chat/         the chat TUI model (the main interactive surface)
  ui/components/   reusable TUI components
  ui/keys/         the key register and keymap file
  subagent/        sub-agents: spawn, worktrees, fan-out, patches
  tools/           built-in tools, split by security tier
  runner/          captured command execution and the command ceiling
  config/          TOML config and the settings table
  storage/         SQLite persistence
  sandbox/         process containment (bubblewrap, Seatbelt, containers)
  scope/           working-scope grants and the deny mask
  safety/          command danger analysis
  radius/          blast-radius analysis
  web/             fetch and search, host policy, the sources ledger
  project/         checkout survey, instruction files, trust
  structural/      external tools, and the read-only and write git verbs
  hostgit/         how shhh runs git on the host
  secret/          the vault and the scrub
  observe/         the session record's contract and closed vocabularies
  evidence/        store for reduced and elided output
  todo/, todo/run/ the project backlog and its runner
  rpc/             the JSON-RPC surface behind `shhh serve`
```

## Invariants

- **Three tool tiers, never merged.** `Execute` in `tools/tools.go` dispatches
  read-only tools only, mutating tools go through `ExecuteMutating`, and
  `execute_command` is its own tier requiring approval. Merging them removes a
  security boundary
  ([`docs/architecture.md#tiers-not-permissions`](docs/architecture.md#tiers-not-permissions)).
- **Nothing fails open.** Auto mode's classifier falls back to asking on any
  error. Deny lists are answered before the mode; a checkout's settings may
  add a refusal, never remove one.
- **Every way of starting a program takes the containment wrap**
  (`chat.Containment.Run`, `process.Supervisor`), including a new one.
- **Anything that persists tool output takes the secret scrub before it
  writes.** The executor wrap only sees a result after it is on disk.
- **An MCP server's annotations grant nothing.** Read-only is the user's
  `Definition.ReadOnly`, never `Tool.ReadOnlyHint`.
- **Every git shhh runs on the host goes through `hostgit.Command`**, never a
  bare `exec.Command("git", …)`.
- **`CGO_ENABLED=0`.** The build is pure Go (`modernc.org/sqlite`).

## Gotchas

- **Storage** is single-connection SQLite on purpose. In `internal/cli` open
  it with `openStore()`, never `storage.Open()`.
- **Config**: read the merged layers with `ConfigFrom`, never `config.Load`.
  Every setting is one row of `settings` in `internal/config/settings.go`.
  Write config files through `config.ReplaceFile`.
- **Provider names are normalised**: underscores become hyphens.
- **Bubble Tea**: shhh's messages are typed structs handled in `Update`. Match
  `tea.KeyPressMsg` and the concrete mouse types, not v2's interfaces.
  `View()` returns a `tea.View`; tests read `.View().Content`.
- **Chat geometry is rectangles in `internal/ui/chat/layout.go`.** Add one to
  the split rather than a `width - n` in a renderer. A chat surface is one row
  of the register in `overlay.go`.
- **`edit_file` ranges are offsets into the file as read.** Every edit goes
  through `applyEdits`; applying them one at a time by string replacement
  changes the meaning of each edit after the first.
- **A new optional tool joins `registrableDefinitions`**
  (`internal/cli/registrable.go`) in the change that registers it.
- **The tool-round cap is a checkpoint**: at 150 rounds a session pauses for
  the user. The "Finding things" rules in `BuildAgent` are load-bearing;
  without them a session spent all 150 re-running the same searches.
- **Migrations are `shhh doctor` checks**, never a startup step. Config, data
  and cache follow XDG on every platform; don't add a `darwin` branch.

### Provider quirks

Each looks like something to simplify, and the symptom doesn't point at the
cause.

- **Gemini pairs tool results by function name, not id.** Put the id in
  `FunctionResponse.Name` and the model silently calls the tool again.
- **Messages and Responses replay only the current chain's thinking**
  (`replayFrom`; Gemini replays all on purpose), and the cut only moves forward. Replay everything and every round bills the
  session's thinking again; cut in the middle and the request is a 400.
- **Chat-completions tool calls are keyed by id, never `index`.** Gateways
  renumber, skip or omit it, and keying on it folds two calls into one.
- **The output ceiling is `max_completion_tokens`**, except on
  `openai-compatible` for a non-reasoning model. Never read that field's name
  as a context-length failure.
- **Every stream loop calls `idleWatch.alive()` on every raw event** and ends
  through `idleWatch.err`, or the dialect has no idle deadline.
