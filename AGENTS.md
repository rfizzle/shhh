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
- **The goldens and `make design` are the record of exact visual
  specification**; a surface not yet built is drafted as a text panel in its
  story, in the golden's shape, and approved there before the code. Markdown
  here says what the rules are, not what they measure.

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
| Update goldens (rewrites every golden in those packages, deletes orphans) | `go test ./internal/ui ./internal/ui/components ./internal/ui/chat -update-golden` |
| Capture one new golden | `go test ./internal/ui/<pkg> -update-golden -run '<TestName>$'` |
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
- **Goldens**: `golden.Run(m)` deletes every golden no test touched, so add
  or remove a case with `-update-golden`; never hand-write one. Anything the
  screen draws reads the held `clock()`, never `time.Now`.

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
  receipt/         what one call did, in the words the chat screen draws
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
- **Every git shhh runs on the host goes through `hostgit.Command`**, never a
  bare `exec.Command("git", …)`.
- **`CGO_ENABLED=0`.** The build is pure Go (`modernc.org/sqlite`).

## Gotchas

- **Storage** is single-connection SQLite on purpose. In `internal/cli` open
  it with `openStore()`, never `storage.Open()`.
- **Config**: read the merged layers with `ConfigFrom`, never `config.Load`.
  Write config files through `config.ReplaceFile`.
- **Chat geometry is rectangles in `internal/ui/chat/layout.go`.** Add one to
  the split rather than a `width - n` in a renderer.
- **A new optional tool joins `registrableDefinitions`**
  (`internal/cli/registrable.go`) with its describer, in the change that
  registers it: how a call reads (kind, verb, subject, count) is declared
  beside the definition, in its package's `Describers`, and a package's
  first tool joins `sources` in `internal/receipt/build.go`. The screen
  names no tool.
