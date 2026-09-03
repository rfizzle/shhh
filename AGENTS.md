# AGENTS.md

## Overview

`shhh` is a Go CLI tool that turns natural language into executable shell commands. It has four interaction modes: one-shot generation (`shhh cmd <prompt>`), inline/hotkey (`Ctrl+K` in shell), a read-only conversation with persona sub-agents and a shared notebook (`shhh chat`), and a coding agent (`shhh code`). The TUI is built with Bubble Tea v2 (charm.land/bubbletea/v2) and the LLM backend supports Anthropic, OpenAI, Gemini, and OpenRouter via a pluggable provider registry.

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
| Check every released platform compiles | `make cross` (part of `make ci`) |
| Check doc citations | `make docs-check` |
| Run the eval suite | `make eval` (costs real requests; not part of `make ci`) |
| Verify prompt caching against a live endpoint | `SHHH_CACHE_IT_URL=… SHHH_CACHE_IT_KEY=… go test ./internal/provider -run CacheIntegration -v` |
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

**Write the subject as a Conventional Commit.** `type(scope): summary`, where
the type is one of `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `build`,
`ci`, `chore` or `revert`, the scope is optional and names the surface the
change lives in (`ui/chat`, `cli`, `todo`, `pricing`), and the summary is
imperative, lower case, has no trailing period and keeps the whole line under
72 characters. A change that breaks a command's flags, a config key or an
on-disk format takes a `!` before the colon and a `BREAKING CHANGE:` footer
saying what the reader has to do about it. A subject that needs an `and` is
two commits.

The prefix is the only thing the convention decides. The body is what it
always was: prose after a blank line, saying what the diff cannot — the
failure the change is for, the decision behind it, what was deliberately left
alone. History from before this rule stays as it is; don't rewrite it to match.
`shhh todo run` writes its own commit message by reading `git log -10` and
matching the shape it finds there, so it picks the convention up on its own as
recent history turns over.

All four rules yield to an explicit instruction to do otherwise.

## Architecture

```
cmd/shhh/main.go          Entry point (cobra root command, executed through fang)
internal/
  cli/                     All cobra commands (root, cmd, chat, code, init, doctor, etc.)
  cli/report/              The shape every non-interactive listing prints in (rows, sections, tallies)
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
  logs/                    The diagnostic log: the file a refused request is written to, and the tail `shhh logs` reads
  reports/                 Report pages: typed blocks and validated freehand rendered to HTML, stored globally, served on loopback
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
  mcp/                     MCP clients: server definitions and their catalog, the connect over the official SDK, the toolset on the executor chain, project trust
  todo/                    The project backlog: one Markdown item per file under .shhh/todo, the ready set and its order, the archive
  secret/                  Session secrets: the vault, the scrub every text passes through, and the prompt block naming them
  migrate/                 Layout migrations, detected and offered by `shhh doctor` (never at startup)
  observe/                 The session record's contract: the observer every surface reports through, and the closed sets its codes come from
  eval/                    The eval suite: a case, the run that measures it, and the verdict its own check gives
  evidence/                Evidence store for quality-gate output
  plan/                    Plan mode state (step tracking)
  resolve/                 Provider resolution from flags/config/env
  project/                 Project context detection (language, framework, recent files)
  shell/                   Which shell this platform runs a command line with, and how — the one resolution the prompt and every runner read
  process/                 Background process management (the process tool)
  structural/              Optional external tools integration (ast-grep, fd, jaq, sd, tokei) and the read-only git verbs
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

### The read-only git verbs

`internal/structural/git.go` is the `git` tool: five reading verbs (`status`,
`log`, `show`, `diff`, `blame`) built by `buildGitArgv`, registered by
`NewToolset` only when the binary is on PATH **and** `insideRepo` says the
workspace is inside a working tree. It is not gated anywhere, which is the
whole point — it auto-runs like `search`, in every mode. Why the verb set is
the security boundary:
[`docs/capabilities/approvals-and-safety.md#a-closed-verb-set-is-what-makes-a-read-a-read`](docs/capabilities/approvals-and-safety.md#a-closed-verb-set-is-what-makes-a-read-a-read).

What will bite you: **git's colour flags are not uniform across the five
verbs.** `status` has no `--no-color` and takes `--porcelain=v1` instead;
`blame` rejects `--no-color` as ambiguous and needs `--no-color-lines
--no-color-by-age`; the other three take `--no-color`. A flag added to the
common prefix rather than the per-verb branch will fail at runtime on one verb
and pass every builder test that does not spawn. `git diff --staged` likewise
takes at most one commit, so the builder refuses the pair rather than earning
a usage dump.

**One git configuration key is shut off from the environment, not by a flag.**
`core.fsmonitor` names a program git execs on `status`, `diff` and `blame`,
and there is no flag for it — `spawnEnv` blanks it with the `GIT_CONFIG_*`
triple, which is why `run` sets `cmd.Env` at all. Doing it with `-c` on the
command line would put the one flag the closed vocabulary most needs to
exclude back into the argv. `--no-pager`, `--no-ext-diff`, `--no-textconv` and
`--no-show-signature` cover the other four keys that name a program.

Pathspecs use `resolveGitPaths`, not the package's `resolvePath`: history
names files that no longer exist, so containment is lexical and the symlink
check runs only when the path is on disk. **Containment is a fact about the
arguments, not about the output** — a call that names no path answers for the
whole repository, so a session rooted in a subdirectory sees history from
outside its root. That is what `git status` means and it is not worth
breaking; know that it is true.

### Permission Modes

Four modes control approval flow: `manual`, `accept-edits`, `auto`, `plan`. The auto mode uses an LLM classifier that **always fails closed** — classifier errors never approve, they fall back to asking the human. There is no path where "could not decide" becomes yes, and the zero value must stay the one that costs nothing. See [`docs/capabilities/approvals-and-safety.md`](docs/capabilities/approvals-and-safety.md).

### The tree reading

Whether the working tree moved under a turn is decided in
`internal/agent/tree.go` (`SetTreeCheck`, `NextTreeNotice`) and delivered by
whichever front-end holds the turn: `internal/ui/chat/tree.go` for a session,
`Headless.deliverTree` for a headless run. The snapshot is one
`git status --porcelain=v2 --branch -z` at the repository root; the
subtrahend is the front-end's — a session hands in its changeset, a headless
run the paths its mutating calls wrote (`writtenByCalls` in
`internal/cli/print.go`). `BeginToolRound` counts the command calls of a round
so the next notice can say a command ran rather than claim the changes are
somebody else's. A sub-agent is not handed one: a writer stands in its own
worktree, and a reader's fan-out would multiply the cost. `behavior.tree_check`
turns it off. Why it exists and what it deliberately does not see:
[`docs/capabilities/coding-agent.md#the-tree-can-move-under-a-session`](docs/capabilities/coding-agent.md#the-tree-can-move-under-a-session).

### The interruption machinery

The steer and the check-in are decided in `internal/agent` and delivered by
whichever front-end holds the turn. `agent.Steering` (`steering.go`) is the
tuning every surface carries: the check-in interval, how far it widens, the
bound on what a steer quotes back, and the two wordings. A zero value is the
built-in set, so a test and an unconfigured session run the same words.
`SetCheckInInterval` writes only the interval, because that one is
per-surface — `newChildAgent` applies the configured set and then puts a
child's own shorter interval back over it. The summariser's and the
classifier's own instructions are `SummaryConfig.Prompt` and
`ClassifierConfig.Prompt`, beside the rest of what each costs.

**Which model the bounded calls answer on is `auxiliaryModel`**
(`internal/cli/summarizer.go`): the provider's `CheapModel` where it names
one, the session's own where it does not, with `modelOr` putting
`behavior.classifier_model` or `summary.model` ahead of both. Every surface
fills the record's `runSettings.model` from the same call, so the stamp names
the model that was actually asked rather than the one the session runs on.
Each of these calls sends `EffortLow` outright and carries a ceiling with
room for the thought and the answer together — off is the model's own depth,
and the four ceilings are spent by the reasoning first.

`internal/cli/prompts.go` is the door: `loadPrompts` reads whatever
`[prompts]` named, refuses a file it cannot read or one naming a substitution
that wording does not take, and `steering` assembles the set from the config's
numbers and those files. It is called from `buildSessionEnv`, so a chat
session, a headless run and every child of either read one set;
`sessionPrompts.fingerprintOf` folds it into the `prompt_hash` at every
`stamp` call site. The reasons are in
[`docs/capabilities/configuration.md#the-mechanism-is-code-its-wording-is-configuration`](docs/capabilities/configuration.md#the-mechanism-is-code-its-wording-is-configuration).

### Context management

Two mechanisms in two places. The message surgery is
`agent.TrimOldToolResults` (`internal/agent/context.go`): it elides the oldest
tool results between index 0 and the last user message, and never the current
turn, user or assistant text, or a result the `KeepResults` predicate claims.
The figures are the chat model's, because the window belongs to whichever
model the session is on — `trimThresholdPercent` (80) is where a trim starts
and `trimLowWaterPercent` (60, the same line the ctx indicator warns at) is
where it stops, both in `internal/ui/chat/context.go` beside the `/compact`
flow.

The gap between those two numbers is the design, not slack. Rewriting any
message invalidates the provider's cached prefix from that message on
([`docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once`](docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once)),
so a trim costs a full recompute of the conversation however few bytes it
recovered. Trimming only to the threshold clears the line by a handful of
tokens and pays that price again a round later; closing the gap would put the
defect back. What a request actually read from cache is on `/context`, which
is where the effect is visible.

### Provider Interface

All providers implement `StreamCompletion(ctx, messages, opts) (<-chan StreamEvent, error)`. Providers register via `provider.Register(name, factory)` with a `Factory func(ResolveOpts) (Provider, error)`. Provider names are normalized (underscores become hyphens). What the interface deliberately does not abstract over: [`docs/capabilities/providers.md`](docs/capabilities/providers.md).

### TUI State Machine

The chat TUI (`internal/ui/chat/model.go`) distinguishes between **turn states** (what the session's work is doing) and **surface states** (what borrows the screen). A surface can overlay while a turn keeps running underneath. This split is why `turnState()` / `setTurnState()` exist separately from `Model.state`.

### CLI reports

Every non-interactive listing is one shape, built in `internal/cli/report`
([`docs/interface/surfaces.md#outside-the-tui`](docs/interface/surfaces.md#outside-the-tui)): a
`Report` is a title, `Section`s of `Row`s (`glyph name  subject · detail
[outcome]`, with consequence, body and fix lines beneath), `Pair`s aligned on
the colon, `Note`s for warnings and diagnostics, and a tally. `report.Empty`
and `report.Done` are the two one-row shapes — every empty state and every
write confirmation in the CLI is one of them, which is what `voice_test.go`
asserts as a pattern.

`Render(width)` produces plain bytes and nothing else; `Fprint(w, r)` measures
the stream with `term.GetSize` (falling back to 80, the exit banner's rule) and
paints through the palette only when `components.DetectProfile` says the
destination is above ASCII — so a pipe, `TERM=dumb` and `NO_COLOR` are
byte-identical and escape-free, and every width calculation happens before any
styling. A row clips its target and never its outcome. The name column sizes to
the section's longest name capped at `NameCap`; `Section.NameWidth` pins it,
which is what the doctor and `shhh mcp` do at eight so their closed
vocabularies keep the drift signal. `Report.String()` renders at the fallback width, for the
slash commands whose answers land in the transcript with no stream to measure:
a row rendered wider than it is displayed soft-wraps instead of clipping, which
puts the outcome on a line of its own and loses the rule.

`doctorReportOf` in `internal/cli/doctor.go` is the seam from
`components.DoctorCheck` to a report; `report.StateOf` maps the screen's state
enum to the report's. `--json` never emits a report: each command has its own
domain structs (`providers_json.go`, `metrics_json.go`, `memoryJSON`,
`jsonMessages` for both transcript emitters) through the shared `writeJSON`.
The fixtures are `internal/cli/testdata/report`, written by
`go test ./internal/cli -update-golden`.

### The diagnostic log

`internal/logs` is the file behind `shhh logs` ([`docs/capabilities/configuration.md#a-failure-is-written-down`](docs/capabilities/configuration.md#a-failure-is-written-down)). It is a leaf package — it takes the path rather than asking `storage.Dir()`, because `internal/storage` is in `internal/provider`'s import graph and the provider is what writes to it. `logs.Logger()` is an `*slog.Logger` over a sink that discards until `logs.To(path)` names a file; `To` also calls `slog.SetDefault`, which re-points the `log` package, so a dependency's stray line lands in the file instead of on top of a session.

The sink opens the file per record and closes it again — don't "optimise" that into a held handle. Three things depend on it: `MaxBytes` is a bound rather than a size read once at startup; two sessions share one file, and a held handle goes on writing into the generation the other one renamed, which the next rotation then unlinks; and a directory that was unwritable a minute ago is retried instead of costing the session its whole log. The file is `O_APPEND` at 0600, one generation is set aside at `MaxBytes`, and a record that cannot be written is dropped silently — the store's own doctor row is what fails when that directory is unusable, and duplicating it in the log's row would name one fault twice.

There is exactly one writer: `record` in `internal/provider/failure.go`, on the classifier every dialect's error already passes through — a cancellation is not logged. Adding a second means a call at a seam that is the *only* place its event is named, the way the classifier is; a formatted line sprayed at a call site is how a log stops being worth tailing.

`internal/cli/logs.go` owns the path (`logPath`, beside `doctorStorePath`'s own join), `openLog` in `root.go`'s `PersistentPreRunE`, and `runLogs` — the tail's offset is what the follow resumes from. `probeLogs`/`doctorLogs` in `doctor.go` is the row that names the file, and it asks `logPath` so a check cannot report a path the reader cannot open.

### Reports

`internal/reports` is the page behind the `report` tool ([`docs/capabilities/reports.md`](docs/capabilities/reports.md)): typed blocks re-rendered from data on every serve, freehand sections validated once and frozen — the stored markup is the validator's own re-serialization, never the model's raw string, so what was checked is exactly what replays. Every colour is a `var(--token)` from the embedded stylesheet, whose normative home is `tokens/report.css` in the design system; the validator parses its token names from that file at init, so the two cannot disagree. The freehand grammar is an allowlist (no scripts, no event handlers, no `href`/`src`, no literal colours), and the server sends the CSP that makes self-containment the browser's problem too.

The store mirrors `internal/evidence`: opaque `rp-` ids resolved only through the index (an id can never name a path — 64 random bits is also the URL's unguessability), 0700/0600, prune-on-open against `reports.retention_days`. The server is the repo's first and only HTTP listener: loopback, port 0, one route, lazy — a session that makes no report opens no port. `internal/cli/reports.go` owns the path (`reportsDir`, the `logPath` pattern) for the command, the publisher and the doctor row; registration is in `session.go` and `print.go` (headless never pops a browser) and deliberately **not** in `subagents.go` — a child answers its parent, not the user. The activity row lifts the result's first line — the URL, by the tool's contract — into the outcome field, the one that never clips.

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

A tool or command row's output has the same three depths an edit's diff has:
the bounded body with its counted tail (`components/activityrow.go`), the
wider in-place window, and the full screen — `internal/ui/chat/outputview.go`
hosts the last one (`components/outputview.go` renders it), and the cycle
lives in `toggleRow` (`click.go`) so the key and the pointer open a row
through one act. Reading mode's copy key is `internal/ui/chat/copyrow.go`;
the approval card's scroll is the card's own windowing in
`components/approval.go`, with its offsets on the chat model because the card
is rebuilt every frame.

Reasoning is a row like any other act: `internal/ui/chat/think.go` owns the
`think` row — where the round's thinking is collected as it streams, its three
fold depths, and the verbosity that drops it. The text it shows is not the
reasoning the next request replays; that stays with the agent as the
provider's own signed blocks, and dropping them turns the second round of
every thinking turn into a 400 (see the Gemini and Anthropic notes below).

So is a session reading. `internal/ui/chat/summary.go` owns both halves: the
rail's bounded `SUMMARY` block (`inspectorSummary`) and the `summary` row every
landed reading appends to the transcript (`appendSummaryRow`, `summaryRowFor`,
kind `components.ActivitySummary`), which is where a reading too long for
three rail lines can be read whole. `finishSummary` reports whether it wrote a
row, because the reading arrives with no stream behind it owing a repaint. The
row stores its own `summaryReading` — the verdict plus `summaryTarget` as it
stood — rather than reading the target back at render time, since the target is
anchored per turn and the next instruction moves it.

The attached sub-agent view is not a separate surface — the chat `Model`
renders whichever agent is focused, and every agent including the orchestrator
is an `internal/agent` instance with its own transcript, queue and mode.
Attaching switches the focused agent.

### Skills

`internal/skill` follows the Agent Skills specification ([agentskills.io](https://agentskills.io/specification)) and its client guide. `skill.Roots` is the search order (project `.shhh/skills`, `.agents/skills`, `.claude/skills` from cwd up to the git root, then the user-scope ones), `skill.Discover` reads them into a `Catalog` with lenient validation — a skill that cannot load is a `Diagnostics` entry, never an error, for the reason in [`docs/capabilities/skills.md#where-skills-live`](docs/capabilities/skills.md#where-skills-live). The frontmatter reader in `frontmatter.go` is deliberately not a YAML parser: it takes everything after the first colon as the value, because skills written for other harnesses do.

Three tiers, three places: `skill.PromptBlock` is the catalog in the system prompt (appended as prompt extra, like the toolbox, only when something loaded); the `skill` tool (`skill.ToolDefinition`, read-only, its `name` an enum of the catalog) returns `skill.Content` — body, directory, bundled files listed not read; the file tools do the rest. `/skill <name> [task]` and the `/<skill-name>` shortcut (`internal/ui/chat/skills.go`) send the same content as a user message. `agent.KeepResults(skill.IsContent)` exempts activated content from `TrimOldToolResults`. `allowed-tools` is parsed and displayed and grants nothing — [`docs/capabilities/skills.md#a-skill-cannot-grant-itself-anything`](docs/capabilities/skills.md#a-skill-cannot-grant-itself-anything). `shhh skills` and `/skills` print the catalog with its diagnostics.

### MCP servers

`internal/mcp` is the client side of the Model Context Protocol ([`docs/capabilities/mcp.md`](docs/capabilities/mcp.md)) over `github.com/modelcontextprotocol/go-sdk`. A `mcp.Definition` is one server as written — `Transport` stdio/http/sse, argv or URL, `ReadOnly`, `Scope` user or project — and `mcp.Discover(cwd, userDefs, userDirs)` reads the catalog: the config file's `[mcp.servers]` (converted in `internal/cli/mcp.go`'s `mcpDefinitions`), `mcp.json` beside it, and the project's `.shhh/mcp.json` / `.mcp.json` under `mcp.ProjectRoot`, project shadowing user by name, every unreadable definition a `Diagnostics` entry. `mcp.Connect(ctx, catalog, Options)` dials every admitted definition concurrently and returns a `Toolset` with a `Report` per definition (`Status` connected / failed / disabled / untrusted / changed / missing-env / excluded); `admit` is where a project server without trust, a `${VAR}` that is unset, or a non-read-only server in a conversation is left out *before* anything is spawned. `Definition.Expand` resolves `${VAR}` and the unexpanded definition is what reports show, so a token never reaches a listing. Tool names are `mcp.ToolName`: `<server>__<remote>` made provider-safe and capped at 64; `mcp.SplitName` is how the UI recognises one without a registry. `Server.Call` flattens a result with `mcp.Flatten` (text as is, binary as a one-line notice, structured content as the fallback) and returns `IsError` as a Go error so the agent reports it like any failed tool.

The CLI (`internal/cli/mcp.go`) owns every door: `openMCP` in `runChatSession` and `runPrintSession` (after the store opens, because trust is read from it; before the toolbox block is built), `Toolset.WrapExecutor` beside web/lsp/structural, `Toolset.Gated()` registered as `chat.GatedPreviewFunc`s (so a non-read-only server's call goes through the ordinary approval queue as `ActionOther` — Ask in every mode, classifier in auto, Deny in plan), `chat.MCP{Has, ReadOnly, Manage, Sources}` via `Model.WithMCP` (`Sources` is `mcpToolSources`, what the rail's `toolsBlock` and `/status` name), the headless gate and approver in `print.go`, and `ReadOnlyDefinitions` for children in `subagents.go`'s `newEnv`. Trust is `storage.MCPTrusted/TrustMCP/DistrustMCP` on the `mcp_trust` table keyed by repository root and name, at `Definition.Fingerprint()`. `shhh mcp` is `doctorCommand`'s screen with `Title`/nouns (`runDoctorScreenTitled`) over `mcpProbes` — one probe per server whose verb is the transport — and `mcpFinding` is the reading, including the `[a] trust` offer; `mcpListing` is `/mcp` and the no-TTY text. The transcript maps a server tool to verb `mcp`, kind `components.ActivityRemote` (⇄, rail) unless `MCP.ReadOnly` says it is a read (`activity.go`). `prompt.Toolbox` knows nothing about these tools: `mcp.PromptBlock` is a section of its own with the server's `Instructions`, and children get `ReadOnlyPromptBlock`.

### Backlog

`internal/todo` is the project backlog ([`docs/capabilities/todo.md`](docs/capabilities/todo.md)): `todo.Root(cwd)` keys it on the repository root, `todo.Load(root)` reads `.shhh/todo/*.md` and `.shhh/todo/done/*.md` into a `Store` whose `Items` are in `todo.Less` order (priority, created, slug) and whose `Ready()` is open items with every `depends_on` in the archive. The header reader in `header.go` keeps every line's text so `SetStatus`/`SetSize` rewrite one line and nothing else; `Render` is only for a new file. Loading is lenient the way skills are: a file that cannot be read at all — no header, no title, an unknown status — is a `Diagnostics` entry, and a value merely off its scale (priority, size, kind) is a warning on the loaded `Item`. Slugs are validated by `todo.ValidSlug` and must never look like a planning identifier, for the docs-check reason above. `Create` writes `.shhh/todo/.gitignore` ignoring `.run/`; whether the backlog itself is committed is the user's call and nothing in shhh stages it. `shhh todo` (`internal/cli/todo.go`) prints the store, and `todoManager` there backs the textual `/todo` subcommands. The chat side is `internal/ui/chat/todo.go`: `Todos`/`WithTodos` wiring from `internal/cli/chat.go`, a cached `Model.todoStore` reloaded by `reloadTodos` on the events that can change a file (a `/todo` command, the editor returning, a turn ending in `turn.go`) and never per frame, `openTodoPick` over the generic picker, `openTodoEditor` reusing the draft editor's `editorArgv`/`editorRefusal` with its own `todoEditorDoneMsg` so the item file is never removed, and `inspectorTodo` feeding the rail's `todoBlock` (`internal/ui/components/inspector.go`, between PLAN and CHANGES). A bare `/todo add` is `internal/ui/chat/todoadd.go`: `todo.Extractor` (`internal/todo/extract.go`, the summarizer's shape — tool schema, text fallback, untrusted digest, no tool output) runs as a background command and lands as `todoProposalsMsg`; the card is a `components.MultiSelect` on `stateTodoPropose` with everything checked, and `writeProposals` resolves `depends_on` titles to slugs over the accepted set, dropping and naming what matches nothing. The reading is billed as `meter.SourceBacklog` and uses the session model.

The runner is `internal/todo/run` (a pure state machine: `State`, `Step`, `First`/`Observe`/`VerifyResult`/`Committed`/`Block`, the stage prompts in `prompt.go`, marker-line parsers, and the `.shhh/todo/.run/<slug>.json` checkpoint) driven by `internal/ui/chat/todorun.go`: `startTodoRun` sets the item in progress and sends the research prompt in plan mode; `todoRunAfter` is the `Update` tail hook (beside the summary's close) that reads the stage's answer when the turn is truly over — not during a round-limit pause or a decision card — and `todoRunStep` carries out the step handed back; `todoVerifyCmd` runs `State.Tests` (the `## Tests` bullets snapshotted by `run.Start`, never re-read from the file the model may have edited) and `Gate.Run`, `todoCommitCmd` stages `todoRunPaths` (changeset records since the run's first turn, under the root, never `.shhh/todo/`) and commits, `todoRunDone` archives with the report, `todoRunBlocked` writes the evidence. The plan-approval card is suppressed while a run owns the plan-mode turn (`doneMsg` in `model.go`). `/clear`, `/todo stop` and esc on a stage turn end a run and put the item back to open; plain text is refused while a run is going (`todoRunHoldsInput` in `command.go`), and a turn that ends without being the stage's (`todoRunTurn`) blocks the run. Review and commit turns run in plan mode. Phase-two gates: `afterResearch` pauses (`ActionPause`, `State.Paused`) for L always, for M on questions or a size upgrade, and blocks an S with questions; the chat's `openTodoPause` is a `components.NoteSelect` on `stateTodoPause` whose answers are `Resume`, `Replan(note)` (the note is appended to the item under `## Answers` and research runs again) or stop. For M/L the review is `ActionReview`: `startTodoReview` spawns `subagent.RoleReviewer` through `Supervisor.Spawn` with the diff in the task, `todoReviewDone` (hooked in `handleSubagentEvent`'s `EventDone`) reads `Supervisor.Report` into `ReviewResult`; no supervisor or a refused spawn falls back to `SelfReview`. A blocked run offers `todoFollowUp` on the proposals card (`openTodoProposals`). Resilience: `/todo run <slug>` on an in-progress item with a checkpoint calls `State.Continue` (the checkpointed stage restarts; `Session`, `PrevMode`, `Turn` are re-stamped), and a stage turn displaced by another turn (`turnCount != todoRunTurn`) goes through `stopTodoRunKeeping` — checkpoint kept, item left in progress — rather than blocking. The changeset store is per session, so `State.Paths` (snapshotted on every save) is what lets a continued run stage and diff what an earlier session changed; `todoRunPaths`/`todoRunDiff` union it with this session's records. Reviewer children are named from `State.Reviews`, never reused, because a killed child keeps its name in the supervisor. A large item goes through `StageSplit` (plan mode; `ParseLanes` in `run/lanes.go` checks 2–`MaxLanes` lanes with disjoint paths, none under the backlog, and `lanes: none` or a refused division falls back to `implementWhole`) and `StageFanOut` (`ActionFanOut`: `startTodoFanOut` spawns one `subagent.RoleWriter` per `Lane` in one supervisor batch, named `tw<Fanouts>-<lane>`, with `LaneTask` as its task and the lane's paths as its claim); `todoLaneAsk` in `handleSubagentEvent` auto-approves a lane's `AskPatch` and refuses one carrying an overlap warning (`LaneFailed`), `EventPatch` marks the lane landed (`LanePatched`), and the writer's `EventDone` is `todoWriterDone` → `LaneDone`, which blocks on an unfinished writer or one whose patch never landed and, on the last lane, sends the `integratePrompt` as the `StageImplement` turn. `State.LiveAgents` is what `endTodoRun`/`stopTodoRunKeeping` kill. The `.shhh` path is a directory now: the context file is `.shhh/project.md` (`project.ContextFile`), `shhh init --project` writes it there, and a checkout still holding the old single `.shhh` file is a `shhh doctor` migration (`internal/migrate/project.go`), not something the context reader falls back to.

### Saved chats

A conversation autosaves to a slot of its own, and the slot is the store's to give ([`docs/capabilities/sessions-and-memory.md#a-slot-belongs-to-one-session`](docs/capabilities/sessions-and-memory.md#a-slot-belongs-to-one-session)): `newSessionName` (`internal/ui/chat/model.go`) is only the timestamp, and `ClaimChatSlot` is the insert that settles a collision, appending `(2)`, `(3)` when the name is taken — a look before the insert would hand two processes started in the same second the same name, which is the defect this replaced. `Model.WithDB` claims, `adoptSlot` moves the session between slots and gives an unwritten claim back through `ReleaseChatSlot`, and `quitCmd` does the same for a session that never saved. **`ListChats` inner-joins `chat_messages`, so a claimed slot is invisible until its first save** — without that, `--continue` would offer the empty slot the session had just minted. The store remembers `chatSeq`, the highest seq this process wrote to or read from each slot, under a `chatMu` held across the whole save, record included: `saveChatTx` returns `ChatSlotConflictError` when the slot no longer holds what this process left there, `AutosaveChat` answers that by claiming a fresh slot and writing the conversation there, and `chatMoved` keeps a second refusal on the same slot from making a second copy. The chat model follows with `autosaveMovedMsg`/`noteSlotMove`, which re-points the observer's session link and resets the title reading, because the slot it was read for is somebody else's row now. `storage/chat.go` is the rest of the store: `SaveChat`/`SaveChatBranch`/`LoadChat`, `ListChats` with the generated `Title`, `RenameChat` (branches follow ids; a collision is `ChatExistsError`), `DeleteChat` (takes the descendants with it), `CountChatBranches` for the confirm, `SetChatTitle`/`ChatTitle`. Three views draw it ([`docs/capabilities/sessions-and-memory.md#housekeeping`](docs/capabilities/sessions-and-memory.md#housekeeping)): the `/chats` picker is `internal/ui/chat/chats.go` — the generic picker with `Select.Actions` offering `keys.Select.Delete`/`Rename`, a `components.Confirm` or a `textinput` row drawn under the card, the session's own slot as the `⊘` row (`protectedPhrase`), state in the `chatOps` value on the model; the browser is `internal/ui/browse` with `browse.Ops` wired by `pickSavedChat` in `internal/cli/session.go`; `shhh chats` (`internal/cli/chats.go`) is the browser bare and `list|show|delete|rename [--json]` with a verb. Titles ([`docs/capabilities/sessions-and-memory.md#a-title-you-did-not-write`](docs/capabilities/sessions-and-memory.md#a-title-you-did-not-write)) are `agent.Titler` (`internal/agent/title.go`, the summarizer's shape, `CleanTitle` bounds the answer) driven by `internal/ui/chat/title.go`: `titleCloseCmd` in the `Update` tail beside the summary's close, at most `titleAttempts` readings, only on an `isAutosaveSlot` name, written by `finishTitle` and carried by every autosave and `/save`; `/ui title` and `summary.title` (`Config.TitlesEnabled`, on iff `summary.model` is set) switch it.

### Session observability

`shhh observe` ([`docs/capabilities/sessions-and-memory.md#observations-are-what-the-session-did`](docs/capabilities/sessions-and-memory.md#observations-are-what-the-session-did)) is four layers. `internal/observe` is the contract itself — the `Observer` callbacks, the `Pos` an event happened at, and every code as a constant: `ClassFromResult` maps a failed result to its class by shape, `ReasonCode` a policy reason to its code, `ToolOutcome` is the pair a surface reports a result as, and all three are where free text is stopped from reaching the table. It lives below every front-end because a vocabulary each runner keeps its own copy of is a vocabulary that drifts, and nothing fails when it does. `storage/observe.go` is the tables (`agent_sessions` with provenance and the chat-session link, `agent_events` with `turn`/`round`) and the aggregate queries the dashboard draws; the event kinds are `tool`, `decision`, `turn` and `signal`. `internal/ui/chat/observe.go` is the chat model's adaptation to the contract — where its turn, round, ledger and close state are read off — and declares none of the vocabulary itself; `headlessObserver` in `internal/cli/print.go` is the same thing for a headless run, whose whole position is turn 1 and the round the loop has reached. `internal/cli/observe.go` is the `observeRecorder` that writes what the observer reports, the `stamp` of provenance (fingerprints, never the prompt or the path), and the `observe`, `observe session <id>`, `observe export [--transcript]` and `observe purge` commands. The chat model's hook sites: tool results and the turn close (`close.go`, the one place a turn ends), `trimContext`, `finishSummary`, `injectSteering`, `applyUndo`, `applyMode`, the plan card, the round-pause offers, `handleSubagentEvent`, `todoRunStep` and the run's stop paths, `activateSkill`, and `autosaveCmd` with `noteSlotMove` for the session link. `agent.IsRepeatNotice` is how a surface counts repeat notices without knowing their wording. Adding a signal means a new constant in `internal/observe` with its reason set in the comment, and a call at the site — never a formatted string.

**Every composition writes the same rows** ([`docs/capabilities/sessions-and-memory.md#every-composition-is-one-population`](docs/capabilities/sessions-and-memory.md#every-composition-is-one-population)). A session and a headless run open their row in `runChatSession` and `runPrintSession`; `newCmdCmd` opens one of kind `cmd` above its pipe branch, so a piped one-shot is recorded too, and closes it with an explicit call before each `os.Exit`, because a defer does not survive one — `raw.SystemPrompt` is exported so the piped run's stamp fingerprints the prompt that actually went out rather than a second construction of it. `oneShotOutcome` and `headlessTurnOutcome` map each surface's ending onto the same closed set the chat's `turnOutcomeCode` uses; a headless round cap is `cap-paused`, not `failed`, because it is the same event a session's cap is and only the way out differs. A child is `subagent.Recorder` — the contract plus the `End` no caller outside the supervisor could hold — handed to `Options.Record` with the child's own system prompt, so its provenance is its own rather than its parent's; `child.pos()` places its events, `endTurn` in `Supervisor.run` closes each of its turns, and `resolveGated` reports the policy's verdict, the classifier's and the user's answer as the two events a session reports. The decision reason codes are `observe.Reason*` — one spelling per verdict across four deciders. `agent.Headless.OnSummary` is what puts a reading in the record from every unattended surface at once, and `observe.SummaryCode` is the one spelling of its states.

**Whether it worked** ([`docs/capabilities/sessions-and-memory.md#whether-it-worked`](docs/capabilities/sessions-and-memory.md#whether-it-worked)) is two things on top of that. The gate verdict is `Observer.Gate`, the one callback that is not a `Signal` — a signal carries one qualifier and a gate run carries a suite as well, and it takes no `Pos` because `/gate run` starts one between turns. `quality.Runner.Observe` hangs off `finish`, the single point both `Run` and `Start` land on; `recordGateVerdicts` is the one function that points a gate at a session's record, so a third surface cannot wire the gate and forget the record. It is handed the suite only when `Result.Trusted` says the name resolved in `.shhh/quality.json`: the tool takes that name from the model, so an unresolved one is the model's own text and `observe.GateHook` replaces it with `GateSuiteUnknown`. Verdicts map through `observe.GateVerdict`, and the event is a signal row whose `tool` is the suite. The session outcome is the `outcome` column, written optimistically by `observeRecorder.turn` through `observe.SessionOutcome` and corrected in `end` (nothing finished ⇒ abandoned) or `endWith` (the TUI failed and `os.Exit` will skip the close). A cap-paused turn maps to nothing at all — a sub-agent's supervisor grants itself more rounds and runs on, so a pause read as an abandonment would libel every child that took a check-in. An empty column reads as `unknown` and is never written. `AgentGateVerdicts` and `AgentSessionOutcomes` are the two aggregates, drawn as the `GATE` and `OUTCOMES` sections; `observeSignalRows` drops the gate because it has a section of its own, and the pass rate's denominator is pass plus fail only, since a blocked run is no reading of the code rather than a bad one.

**The `rating` column is the human check on that outcome** ([`docs/capabilities/sessions-and-memory.md#a-rating-is-how-you-check-the-inference`](docs/capabilities/sessions-and-memory.md#a-rating-is-how-you-check-the-inference)), written by `shhh rate` through `RateAgentSession` and nullable, so unrated and disliked stay apart. **`ListUnratedSessions` is the one query in `storage/observe.go` that reads content, and it is the trap this section exists to name:** it joins `agent_sessions` to `chat_sessions` and pulls the title and the first user message, because a content-free row is not something anyone can judge. The crossing is read-only — the answer that comes back is one integer — and the join is also what excludes children and headless runs, since `Observer.Session` is wired from exactly one place, the chat model's autosave. Resuming a conversation opens a second session row against the same name, so both are reminded by the first sitting's opening line. `internal/cli/rate.go` holds the walk: `rateItem` is one entry of either kind, `rateHandle` is the `c11`/`s7` string the card carries back so the screen never learns there are two tables, and `components.RateRow`'s `Kind`/`Verb`/`Target` are what let one card draw a shell command and an agent run.

**What a session ran under** ([`docs/capabilities/sessions-and-memory.md#what-a-session-ran-under`](docs/capabilities/sessions-and-memory.md#what-a-session-ran-under)) is `storage.AgentSettings`, carried on `AgentProvenance` and written by `StampAgentSession` only when its `ConfigHash` is set; the nine columns are nullable so a row older than them scans to a nil `AgentSessionSummary.Settings` rather than to zero values, and `observeSettingsPairs` prints nothing for that row. `sessionSettings` in `internal/cli/observe.go` is the allowlist — the one function that reads a config value into the stamp, except the sandbox profile, which each surface parses through `sandbox.ParseProfile`'s closed set before filling `runSettings` — over a `runSettings` each surface fills with what it resolved for itself (mode, effort, the cap through `roundCapFor`, the containment profile in force, and whether it has a summariser and a classifier at all); `configHash` is the JSON of the whole `config.Config` fingerprinted. `settings_test.go` marks every config field by reflection and fails if any marker off `settingsAllowlist` reaches the stamp, so a new config key is excluded by default. A child's mode and cap ride on `subagent.Spec` (`Mode`, `MaxRounds`, the latter from `roundCap`) because the supervisor is the only thing that knows them after the profile and the parent clamp; the headless run stamps no mode and no classifier, and `cmd` stamps only its reasoning level and the hash.

### Secrets

`internal/secret` is the vault of values the model never sees ([`docs/capabilities/secrets.md`](docs/capabilities/secrets.md)). A `secret.Vault` holds name→value; `Environ()` is what a command gets, `Scrub()` is what everything read gets, and `PromptBlock` names the secrets to the model. The CLI owns the vault (`internal/cli/secrets.go`) and every door: `chatSession.openSecrets` runs before the first command and puts the values in `runner.SetSessionEnv` (every captured runner sets `cmd.Env` from `runner.Environ()`), `process.Supervisor.SetEnv` (the process tool builds a bare environment and would otherwise miss them), and `sandbox.Container.ExecArgv`'s `--env NAME` pass-through (a container has no host environment). The scrub sits on the executor chain (`Vault.WrapExecutor`, after evidence reduction), on the runners (`scrubRunner`, `scrubTailRunner`, `scrubContainment` — the tail is what the screen shows), on the agent (`Agent.SetScrub`, applied in `Append`, `SetMessages` and `Stream`) and on the stream closure in `buildSessionEnv` and the sub-agent `newEnv`. Children get the same through `subagent.Env.Scrub`. `/secret` (`internal/ui/chat/secrets.go`) calls the CLI's `secretsManager`, which returns the note and an announcement the chat sends the model as a user message — the system prompt cannot be rewritten mid-session. `Scrub` also replaces base64/hex/URL-escaped forms and any run of the value ≥ 8 bytes; it is text matching and the doc says so.

### Sub-agents

`shhh code` can spawn child agents via `spawn_agent`. Children are `researcher` (read-only tools + web), `writer` (full toolset against an isolated git worktree) or `reviewer` (read-only, plan mode, handed a diff; `prompt.BuildReviewer`), or any custom profile the user wrote to `~/.config/shhh/agents/<name>.toml`. Hard limits: max 3 concurrent, 16 total per session — a limit on attention, not on resources ([`docs/capabilities/subagents.md`](docs/capabilities/subagents.md)). Children that can write or execute produce patches that the parent approves.

Profiles are three layers, and the split matters: `config.AgentDefinition` (`internal/config/agents.go`) is the file as written and validates what needs no runtime — names, tiers, tool names against tiers; `subagent.Profile` (`internal/subagent/profile.go`) is what the supervisor decides by — worktree or not, starting mode, default budgets — and knows nothing about tools or prompts; `internal/cli/subagents.go` translates a definition into a child's toolset and prompt (`profileEnv`) and checks the mode and reasoning names, because those vocabularies belong to `agent` and `provider`, which `config` must not import. The three built-in roles are `subagent.BuiltinProfiles()` and keep their hand-written prompts; a file named `researcher.toml`, `writer.toml` or `reviewer.toml` replaces one — so the `reviewer.toml` example in `docs/agents/` is now an override, and copying it changes the backlog runner's reviewer to what the file says. The file format and examples are in [`docs/agents/README.md`](docs/agents/README.md).

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

**Never change the working directory in a test.** `cmd/go` records every
chdir target as one of the test's inputs, and a `t.TempDir()` path is new on
every run, so a single `os.Chdir` or `t.Chdir` makes its whole package
uncacheable — `go test ./...` re-runs it in full against a tree nothing has
touched, and the packages around it report `(cached)` so it reads as a slow
suite rather than a broken one. Give the code under test the directory
instead: `chat.Model.WithWorkspace`, `radius.Resolve`, `project.FindFrom`,
`migrate.Plan` and `buildScaffold` all take it as an argument for this reason.
`make docs-check` fails on a `Chdir` in a `_test.go`, so this cannot drift
back.

The same reasoning bans a test that reads the machine rather than its own
scratch directory. A fixture command of `rm -rf /` asks `internal/radius` to
walk the real filesystem — twenty thousand entries, a different set of
`/proc` paths each run, and the package is uncacheable again. Point a
destructive fixture at a `t.TempDir()`.

## Configuration

Config is TOML at `~/.config/shhh/config.toml` (or `$XDG_CONFIG_HOME/shhh/`), the same on every platform — see [`docs/capabilities/configuration.md#one-layout-everywhere`](docs/capabilities/configuration.md#one-layout-everywhere). Key sections: `[provider]`, `[behavior]`, `[sandbox]`, `[web]`, `[lsp]`, `[appearance]`, `[history]`, `[agents]`, `[secrets]` (names only; values come from the environment), `[prompts]` (paths to wordings that replace the built-in ones, read at session start), `[mcp]` with one `[mcp.servers.<name>]` table per server (plus `mcp.json` beside the file and the project's `.mcp.json`, read by `mcp.Discover`). Agent profiles are one TOML file each in the `agents/` directory beside it (`config.AgentDirs`), loaded by `config.LoadAgents`. User-scope skills are directories under `skills/` beside it (`config.SkillDirs`); project-scope ones come from the checkout (`skill.ProjectRoots`).

## Gotchas

- **CGO_ENABLED=0**: The build is pure Go (uses `modernc.org/sqlite`, not cgo sqlite3). Never add cgo dependencies.
- **Never add a story identifier** (`S-060`, `E-018`) to a comment, a document or a test name. They are gone from the code and `make docs-check` fails on one. Say what the code does and cite `docs/` — see [Never reference a story or a plan](#never-reference-a-story-or-a-plan).
- **Provider name normalization**: Underscores become hyphens in the registry (`open_ai` → `open-ai`). Use the normalized form when registering or resolving.
- **An MCP server's annotations grant nothing**: `Tool.ReadOnlyHint` is displayed in `shhh mcp show` and never consulted by `Toolset.ReadOnly`, which reads only `Definition.ReadOnly` — the user's word in *their* config; `ReadJSON` drops `readOnly` from a project file with a diagnostic. Don't wire the hint into the gate, however sensible it looks for a particular server: [`docs/capabilities/mcp.md#a-server-cannot-vouch-for-itself`](docs/capabilities/mcp.md#a-server-cannot-vouch-for-itself).
- **A config key's shape is parsed in `internal/config`; the words it may say are judged in `internal/cli`**: `config.Set` is the one parse — integers, booleans and which keys give a negative a meaning — and every write door reaches it, `config set` through `config.Write` included. But a value that is one of a few *names* (a permission mode, a reasoning level, a containment profile or engine or isolation level) is checked by `checkConfigValue` in `internal/cli/config.go` instead, because those vocabularies belong to `agent`, `provider` and `sandbox`, and `config` imports none of them — the same split `profileFromDefinition` makes for a profile file. Add a name-valued key and it needs a case there, or the value saves cleanly and the session refuses it later; add a numeric one and it needs an entry in `intField`, whose second return is whether a negative is a decision or a typo. Every writer in `internal/cli` goes through `writeConfigEdits`, so a new one gets the judge for free. See [`docs/capabilities/configuration.md#a-value-is-refused-before-it-is-written`](docs/capabilities/configuration.md#a-value-is-refused-before-it-is-written).
- **The deny mask is not configurable**: The sandbox's built-in deny mask (credential stores, shhh's own state) cannot be disabled. Only `deny_extra` can add to it.
- **Bubble Tea message routing**: shhh's own messages are typed structs (not interfaces with methods). When adding new async operations, add a corresponding `type fooMsg struct{}` and handle it in the `Update` switch. Note that some of Bubble Tea v2's *own* messages are interfaces — `tea.KeyMsg` covers presses and releases, `tea.MouseMsg` covers click/motion/release/wheel — so match `tea.KeyPressMsg` and the specific mouse types rather than the interface.
- **`View()` returns a `tea.View`, not a string**: the screen's content plus the terminal states the surface asks for (`AltScreen`, `MouseMode`, and the window's own `WindowTitle` and `ProgressBar`, which `internal/ui/chat/terminal.go` derives beside the suspend guard and the redraw key). Tests that want the painted screen read `.View().Content`. Programs start through `internal/cli/program.go` so they all get the colour profile `components.Profile()` resolved the palette against.
- **The chat surface's geometry lives in `internal/ui/chat/layout.go`** ([`docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it`](docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it)): `columns()` and `surface()` split the terminal into rectangles with `ultraviolet/layout`, and `contentWidth`, `paneWidth`, `transcriptWidth` and `viewportHeight` read them. Don't add a new `width - something` in a renderer — add a rectangle to the split and read it. Blocks are placed with `drawIn`, which clips to the rectangle, so a renderer never has to measure what it is about to overflow.
- **Colours are resolved when styles are built, not when they are drawn**: a `lipgloss.Style` holds one `color.Color`, so a `components.Token` picks its truecolor/256/16 rung through `Token.Color()` at `newStyles` time. Changing the palette *or* the profile means rebuilding every derived style — both go through `applyPalette`.
- **Golden file deletion**: `golden.Run(m)` removes any `.txt` file in `testdata/golden/` that wasn't asserted during the run. Don't manually create golden files; let the test framework generate them.
- **The investigation rules in `BuildAgent` are load-bearing**: the "Finding things" section — batch independent calls, make one search answer the question, never repeat a call you already made — is there because a real session spent all 150 rounds re-running the same searches. It reads like padding and is not; see the comment on `BuildAgent` and [`docs/capabilities/coding-agent.md#finding-things`](docs/capabilities/coding-agent.md#finding-things).
- **Never name a tool in a base system prompt**: the optional toolset is assembled from what the machine turned out to have (a language server was detected, a binary is on PATH, a key is configured), so a prompt that names one promises a tool the session may not have. `prompt.Toolbox` describes the tools actually registered and is appended as prompt extra after the last one joins.
- **Gemini pairs tool results by function *name*, not by id**: `FunctionResponse.Name` must be the name of the function called, and the Gemini API sends no `functionCall.id` at all — the ids in `provider.ToolCall` are ours. Don't "simplify" `toGeminiContents` back to putting `ToolCallID` in that field; it addresses every result to a function the model never called, and the model just calls again. Gemini 3 thought signatures ride the same parts and must go back on the part they arrived on.
- **The output ceiling is `max_completion_tokens` everywhere but one branch**: chat completions deprecated `max_tokens`, and a reasoning model answers a request naming it with a 400, so `openai` and `openrouter` send the new field for every model. The `openai-compatible` provider is the exception: it points at whatever the user is running — the default is a local Ollama — and those runtimes do not agree on whether they know the new field, so it sends the new one only for a model something describes as reasoning and the old one otherwise. That branch is judged by the same `CapabilitiesFor` answer that decides whether `reasoning_effort` goes out at all; don't collapse it to one field, and don't give the two decisions separate judges.
- **Tool-round cap is a checkpoint, not a limit**: The default 150-round cap pauses for user input rather than terminating. Sub-agents default to uncapped (`UnlimitedToolRounds = -1`) because they have no one to ask.
- **Three directories, one layout, no per-platform branch**: config (`config.Paths`), data (`storage.Dir`) and cache (`pricing.CacheDir`) each follow XDG on every platform including macOS. Don't reintroduce a `runtime.GOOS == "darwin"` branch in any of them — the retired `~/Library` layout is a migration in `internal/migrate`, not a fallback, and the reasons are in [`docs/capabilities/configuration.md#one-layout-everywhere`](docs/capabilities/configuration.md#one-layout-everywhere).
- **A migration is a `shhh doctor` check, never a startup step and never a command of its own**: add a detector to `migrate.detectors` returning a `Pending`; leave `Pending.Apply` nil when the change needs a person's judgement, and the doctor row will report it without offering a key. See [`docs/capabilities/configuration.md#a-migration-is-a-doctor-check`](docs/capabilities/configuration.md#a-migration-is-a-doctor-check).
- **Storage is single-connection SQLite**: `SetMaxOpenConns(1)` is intentional. The WAL journal mode and busy timeout handle concurrency; don't open multiple `*DB` instances to the same file from one command. In `internal/cli` open the store through `openStore()` (`store.go`), never `storage.Open()` directly: the history purge rides the first connection a command opens, once per process, so no command opens a second one for it. `migrate` still applies every step under `BEGIN IMMEDIATE` with the version re-read inside the lock, so two processes opening a store that is behind — two shells starting `shhh` after an upgrade — cannot apply the same `ALTER TABLE` twice.
- **Version injection**: The `version` var in `internal/cli` is set via `-ldflags` at build time. It defaults to `"dev"` when built without flags.
- **Releases**: Handled by GoReleaser v2 triggered on `v*` tags via GitHub Actions.
