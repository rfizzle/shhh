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
  lsp/                     Language server integration (auto-detected, lazy-started): definitions, references, symbol search, file outlines, hover, and the capability gate on the last three
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
  mcp/                     MCP clients: server definitions and their catalog, the connect over the official SDK, the toolset on the executor chain
  todo/                    The project backlog: one Markdown item per file under .shhh/todo, the ready set and its order, the archive
  secret/                  Session secrets: the vault, the scrub every text passes through — by declared value, then by credential shape — and the prompt block naming them
  migrate/                 Layout migrations, detected and offered by `shhh doctor` (never at startup)
  observe/                 The session record's contract: the observer every surface reports through, and the closed sets its codes come from
  eval/                    The eval suite: a workspace and the verdict its own check gives, or a labelled table put to a call that leaves no workspace behind
  evidence/                Evidence store for quality-gate output
  plan/                    Plan mode state (step tracking)
  resolve/                 Provider resolution from flags/config/env
  project/                 The checkout as the session finds it: the survey, and the instruction files it reads root-down
  shell/                   Which shell this platform runs a command line with, and how — the one resolution the prompt and every runner read
  process/                 Background process management (the process tool)
  structural/              Optional external tools integration (ast-grep, fd, jaq, sd, tokei, yq) and the read-only git verbs
  radius/                  Blast-radius analysis for edits
  preflight/               Startup checks
  update/                  Release check behind `shhh update` and the startup nudge
```

## Key Design Patterns

This section is the map from the shapes described in [`docs/architecture.md`](docs/architecture.md) to where they live. The *why* is in the docs; the *where* is here.

### Agent Loop

The `internal/agent` package is a **passive state machine** — front-ends (the chat TUI or headless runner) drive it step-by-step. The same `Agent` backs both the interactive TUI and the headless `shhh code -p` runner. The `Headless` type in `headless.go` drives the agent synchronously for scripted/sub-agent use. Why it is passive: [`docs/architecture.md#one-agent-several-front-ends`](docs/architecture.md#one-agent-several-front-ends).

`Headless.Run` is the session's loop and not a simpler one ([`docs/capabilities/coding-agent.md#an-unattended-run-runs-the-same-loop`](docs/capabilities/coding-agent.md#an-unattended-run-runs-the-same-loop)): a round's auto calls go through `Agent.ExecuteCalls`, the same `MaxParallelToolCalls` semaphore returning results in call order, and gated ones stay one at a time through the approval queue. Because a round's calls overlap, `OnToolResult` carries the whole `ToolResult` — a front-end matches a result to the row it opened by `Call.ID` (`pendingEntry` in `subagent.go` is a map for exactly this) and takes the duration off the result rather than timing around the hook. **The retry is a state the driver enters, never a sleep inside the loop.** `retry.go` owns the decision and nothing else: `Backoff.Next` reports whether a failure earns another attempt and how long to wait (`Failure.RetryAfter` when the provider named one, doubling off a second otherwise, floored at a second and capped at a minute, `MaxRetryAttempts` across the whole stall, `Reset` on any answered request). Each driver waits its own way — the TUI's `retryWait` meter in `ui/chat/resume.go` ticks it down and offers `[m]`/`[esc]`, `Headless.waitToRetry` sleeps and registers its cancel where the stream's goes so `Interrupt` wakes it. Stream resume after a *broken wire* (`streamResume`, `continueStream`) stays TUI-only: continuing half a reply the transport lost is a judgement, and an unattended run asks again from the top — `RetryNotice.Partial` hands back what the broken stream had written so the surface that showed it can take it back (`print.go` closes the stdout line, the child clears `c.streaming`), because nothing else can and the replacement answer would otherwise run on from a severed sentence. **A reply cut off at the model's output ceiling is the other half of that and is not TUI-only** ([`docs/capabilities/providers.md#a-reply-says-why-it-stopped`](docs/capabilities/providers.md#a-reply-says-why-it-stopped)): `StreamEvent.Stop` carries a closed `provider.StopReason`, each dialect maps its own field to it, and on `StopLength` the session draws the same `[c]` row (the reply is already in the conversation, so continuing appends only `agent.ContinueAfterCeiling`) while `Headless.Run` appends it itself, once per round, and reports both that and a round that lost an unfinished call through `OnContinue`. `Run` returns the halves joined, since the second was asked to carry on from the first, and drops the half in hand wherever the next reply replaces the answer instead of finishing it (a tool round, a close hand-back). A ceiling reached again with the round's continuation already spent is `Headless.TruncatedReply` — the answer stands but says it is half of one, which `--output json` carries as `truncated` for the callers that grade an answer rather than print it. Anthropic needs `anthropicWholeCalls` rather than `CompletedToolCalls`: the SDK rewrites a cut-off argument string to `{}`, which parses, so the argument fragments are judged as they arrived. `observe.SignalRetry` is reported per attempt at each surface's own site — `headlessObserver.retry` in `cli/print.go`, the child's `OnRetry` in `subagent.go` — with `RetryNotice.Signal()` as the reason.

### Tool Security Tiers

Tools are split into three permission tiers that must never be mixed:

1. **Read-only** (`ReadOnly()`): `read_file`, `list_directory`, `search`, `glob_files` — auto-execute without approval
2. **Execute** (`ExecCommandTool()`): `execute_command` — requires user approval or policy match
3. **Mutating** (`Mutating()`): `write_file`, `edit_file` — require approval in manual mode, auto-apply in accept-edits/auto

The `Execute()` function in `tools/tools.go` deliberately only dispatches read-only tools. Mutating calls route through `ExecuteMutating()`. This separation is a security invariant — different functions rather than one function with a branch, for the reason in [`docs/architecture.md#tiers-not-permissions`](docs/architecture.md#tiers-not-permissions). Merging them always looks like a simplification; it isn't.

### The edits array

`edit_file` takes either the inline `old_text`/`new_text` pair or an `edits`
array, and refuses a call carrying both. `parseEditFileArgs` folds the pair
into a one-element list, so everything past it sees a list and there is one
code path to be right about. `applyEdits` in `tools/mutate.go` is the single
validator both `PreviewMutation` and `executeEditFile` go through: it matches
every quote against the file as read, collects the byte ranges each one
claims, sorts them, refuses an intersection naming both edits, and only then
splices the result. Moving either caller off it puts a card in front of a
person for a change the write will refuse.

What will bite you: **the ranges are offsets into the content as it was
read.** Applying each edit in turn with a plain string replacement looks like
a simplification, passes most of the tests, and changes the meaning of every
edit after the first — the offsets it matched against no longer exist. The
staleness check runs once per call, before any of this, and covers every
element for the same reason: they are all matched against that one content.
Why the batch exists and what it deliberately does not cover:
[`docs/capabilities/coding-agent.md#several-places-in-one-file-are-one-call`](docs/capabilities/coding-agent.md#several-places-in-one-file-are-one-call).

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

**The two command lists are matched in `internal/agent/policy.go` and read in `ModePolicy.Decide`, deny first.** `AllowlistMatches` refuses to match a line carrying shell punctuation; `DenylistMatches` asks `safety.Commands` what the line will actually run and matches each of them, because the two fail in opposite directions and only one of them may fail open. `Decide` answers the deny list before the mode, so a sub-agent's call and a headless run's get the same refusal through the same function. The interactive session raises it a second time in `advanceApprovalQueue`, beside the containment refusal and for the same reason: a card exists to put a decision to a person, and a standing refusal is not a decision — which is also what keeps an earlier batch approval from reaching one. `internal/cli/print.go` and `internal/cli/cmd.go` raise it at their own seams. Every one of them answers with the same tool result, and none of them names the key it came from — a refusal carrying the instructions for editing the list is the way around the list. Both lists union when a checkout layers its own settings (`internal/config/project.go`): a repository may add a refusal and may never take one away.

**What counts as dangerous is `internal/safety`'s table of verb plus flag set**, not a list of strings. A short flag is read out of a bundle, from any position, and a long spelling counts as the short one. Adding a danger is a row, and the corpus test beside it is where its spellings go — a rule with one spelling in the test is a rule that will be walked past. The regular expressions that remain are the dangers that are genuinely text: a redirection, a pipe into an interpreter, a statement in SQL.

**`safety.Commands` is the one reading of a shell line, and both gates use it.** It yields each command in a chain, the command an interpreter or a `-exec` was handed, and — behind an escalation, whose own options shhh cannot tell from the command behind them — every word the real command could start at. It over-reads deliberately, quoting included, because everything that reads it is a gate: a stop somebody can see is wrong costs a keystroke, and one that never happened is what the gate exists for. Teach it a carrier and both the deny list and the danger table learn it at once; teach one of them separately and the other has a hole.

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
turns it off.

Porcelain names paths and not their content, so the other half of the reading
comes from the record of what the model has been shown: `tools.SeenChanged`,
reached through the `ReadChanged` hook and wired once in `treeCheck`
(`internal/cli/session.go`) because the record is one per process rather than
one per front-end. The record itself is `internal/tools/seen.go` — a read
writes to it, a mutation is checked against it, `NoteUnknown` fills it from a
restored transcript that says what was read and not what it held, and
`ForgetAll` empties it whenever one conversation gives way to another.
`treeState.reported` is what keeps a stale reading from being named at every
round until the model goes back to it. Why it exists and what it does not see:
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

**The hold is a state the driver enters, never a flag the loop reads.** It
generalises the round-limit pause: `holdTurn` in `internal/ui/chat/hold.go`
parks the session's turn at the boundary `resumeToolLoop` checks the ceiling
at, with `Model.turnOpen` and the vitals ring left open — `setTurnState`
skips the whole working-to-idle close for a held turn, because a close row
and a turn record would both say a turn ended that is about to carry on.
`releaseHold` goes back through `resumeToolLoop` rather than straight to
`requestStream`, so the steering typed while parked, the tree notice and the
ceiling are all owed again. `agent.Headless.Hold` is the same park for a
child: a hook returning nil to run on or a channel to wait on, selected on at
the round tail beside the retry's wait and registering its cancel in the same
place so `Interrupt` wakes it. `Supervisor.Hold`/`Release` back it with a
channel that is replaced rather than reopened (a closed one lets every later
fan-out through), and **the child records the channel it is parked on, not a
flag**: `holdFor` reads the hold and parks the child under one lock, and
`unpark` clears the mark only for the hold being released, so a hold taken
again mid-release cannot have its freshly parked child un-marked by the
release before it. A held child is still `StateRunning` — it keeps its slot
and its worktree — so `Status.Held` rides beside the state, and everything
that reads the state for "is anything moving" has to read `Held` too:
`childProgress` for the rail's glyph, `frameWorking` for the suspend refusal,
`childrenRunning` for the spinner. The mid-turn marker is `storage.ChatHold`,
written inside the autosave's own transaction (`saveChatMarked`) rather than
beside it, because the conversation and what the slot says about it are one
fact; `SaveChat` leaves it alone, and `--continue` reads it.

`internal/cli/prompts.go` is the door: `loadPrompts` reads whatever
`[prompts]` named, refuses a file it cannot read or one naming a substitution
that wording does not take, and `steering` assembles the set from the config's
numbers and those files. It is called from `buildSessionEnv`, so a chat
session, a headless run and every child of either read one set;
`sessionPrompts.fingerprintOf` folds it into the `prompt_hash` at every
`stamp` call site. The reasons are in
[`docs/capabilities/configuration.md#the-mechanism-is-code-its-wording-is-configuration`](docs/capabilities/configuration.md#the-mechanism-is-code-its-wording-is-configuration).

### Context management

Two mechanisms in one place. The message surgery is
`agent.TrimOldToolResults` (`internal/agent/context.go`): it elides the oldest
tool results between index 0 and the last user message, and never the current
turn, user or assistant text, or a result the `KeepResults` predicate claims.
The figures are there too — `agent.TrimThresholdPercent` (80) is where
recovery starts, `agent.TrimLowWaterPercent` (60, the same line the ctx
indicator warns at) is where a trim stops — and `internal/ui/chat/context.go`
aliases them for the surface that colours by them. They are not the screen's,
because a session that acted at one share of its window and an unattended run
that acted at another would be one promise with two meanings.

The gap between those two numbers is the design, not slack. Rewriting any
message invalidates the provider's cached prefix from that message on
([`docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once`](docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once)),
so a trim costs a full recompute of the conversation however few bytes it
recovered. Trimming only to the threshold clears the line by a handful of
tokens and pays that price again a round later; closing the gap would put the
defect back. What a request actually read from cache is on `/context`, which
is where the effect is visible.

**An elided result is recoverable.** `Agent.StoreElided` takes the archive a
result is put into just before the trim replaces it, and the placeholder then
names the id — worded like the reduction notice `evidence.Reducer.Process`
writes, so the toolbox's one instruction about ids covers both
([`docs/capabilities/evidence.md#a-trim-makes-the-same-promise`](docs/capabilities/evidence.md#a-trim-makes-the-same-promise)).
`evidence.Reducer.Keep` is what the host wires in (`chat.Evidence.Keep`, set
in `internal/cli/session.go`); it scrubs before the store writes, like every
other door onto that store. An archive that answers false, or a result under
`minEvidenceBytes`, gets the bare `agent.ElidedResult`, and the loop tells an
already-elided message from a fresh one by prefix rather than by equality —
the placeholder is a different string every time now.

**The estimate that triggers the trim is corrected against the reports.**
`agent.Calibration` is a per-model running ratio of what the provider charged
to what `EstimateTokens` made of the same messages
([`docs/capabilities/providers.md#how-full-the-window-is-corrected-by-what-it-cost`](docs/capabilities/providers.md#how-full-the-window-is-corrected-by-what-it-cost)).
The chat model owns one, folds a response into it in `accumulateUsage` — before
the response joins the conversation, which is what makes the two figures
describe the same messages — and `contextAccounting` applies it to the
estimate and never to a report. `TrimOldToolResults` takes it too, because the
caller trims against a corrected figure and shrinking that by raw estimates
would stop the loop late. What a figure is — a report, an estimate, a
corrected estimate — is one phrasing in `contextBreakdown.source`, on
`/context` and `/stats` as the source line and on the rail beside the count.

**Compaction is a step a driver calls, not something the loop asks for**
([`docs/capabilities/coding-agent.md#the-window-recovers-where-nobody-is-watching`](docs/capabilities/coding-agent.md#the-window-recovers-where-nobody-is-watching)).
`internal/agent/compact.go` holds all of it: the instruction, the summary
request (`Agent.CompactRequest`, sent under `provider.ToolChoiceNone`), the
verbatim tail (`Agent.CompactKeep`, cut at a user message so the rebuilt list
is well-formed on every dialect), the rebuild (`Agent.Compact`), and
`agent.Compactor`, which is the policy — the window, the toolset's cost, where
the summary is asked, and the calibration. `Compactor.Recover` trims first and
asks for a summary only where the trim could not clear the line, at most once
per crossing. `Agent.Compact` deliberately does **not** reset the round
counter; `finishCompact` does, because there the request was the user's own.

`Headless.Compact` is where an unattended run installs one, and the step runs
at the head of each round — ahead of the request, so the first request of a
turn resumed onto a full conversation is covered too. `Headless.askSummary`
registers its cancel where the stream's goes, so an interrupt aborts a
compaction the way it aborts a round. `headlessCompactor`
(`internal/cli/print.go`) resolves the window from the pricing table then the
model family and returns **nil** when neither can say; `childCompactor`
(`internal/subagent/subagent.go`) does the same from the child's model name
alone, since a child holds one stream and never sees its own tool
definitions. A configured `summary.model` takes the request only when its
window is at least the conversation's. Both surfaces report through
`Headless.OnCompact`: stderr and the record for a `-p` run, a transcript row
and a lane update for a child.

### Provider Interface

All providers implement `StreamCompletion(ctx, messages, opts) (<-chan StreamEvent, error)`. Providers register via `provider.Register(name, factory)` with a `Factory func(ResolveOpts) (Provider, error)`. Provider names are normalized (underscores become hyphens). What the interface deliberately does not abstract over: [`docs/capabilities/providers.md`](docs/capabilities/providers.md).

### TUI State Machine

The chat TUI (`internal/ui/chat/model.go`) distinguishes between **turn states** (what the session's work is doing) and **surface states** (what borrows the screen). A surface can overlay while a turn keeps running underneath. This split is why `turnState()` / `setTurnState()` exist separately from `Model.state`.

**A surface is one row of the register in `internal/ui/chat/overlay.go`, not an entry in six lists.** The row says where the mode draws (the bottom panel, the transcript pane, or floating above the frame until the handover), whether it borrows the screen from the turn, how tall it may grow, what it leaves where the draft box was, and what it does with a key — and `isSurface`, the key route in `keyroute.go`, `resolvePanel`, `paneView`, `draftPanel` and `clickableTranscript` all read it. Adding a mode is that row plus the mode's own file; a mode missing from one of those readers is not a compile error, it is a surface that draws and cannot be typed into, so `overlay_test.go` holds the rows to each other. Two modes ride over whatever the state is (`coverOverlay`: the agent manager and a child's routed ask) and one rides inside the approval card (`askOverlay`: the memory prompt), because none of the three is a state the session can be in.

The register is built on first use rather than at initialisation, and so are the slash-command tables in `complete.go` and `command.go`: a row names the session's own methods, and reading the session eventually asks which mode has the screen, which the compiler reads as an initialisation cycle in a package-level table.

### The take-over screens

Seven surfaces take the whole terminal — doctor, metrics, config, history, rate, the context reading and the profile drafter ([`docs/interface/surfaces.md#the-supporting-screens`](docs/interface/surfaces.md#the-supporting-screens)). **They share one chrome, in `internal/ui/components/chrome.go`, and a screen supplies its parts rather than drawing its own skeleton.** `ScreenChrome` is the header, the rule, the body's row budget and the footer; `ScreenHeader` is the row itself; `KeyFooter` is the key row and what annotates it. What a screen still owns is what is a fact about that screen: its title, what it is counting, which keys it offers, and what its body draws in the rows it is left.

What will bite you: **the header's two halves are fitted in the opposite order from the one that reads naturally.** The keys are laid out first and the left-hand rail into what is left of the row, so the reading a screen is counting is dropped before its stated way out is. Fitting the left first is the bug this replaced — three of the seven did it, each self-consistent, and no single screen's test could see it. The family's drop order is asserted once in `chrome_test.go` and captured once in the `screen-family` goldens at 60 and 130 columns, which is the only place all seven are side by side.

A left-hand field carries the ` · ` that joins it to the field in front of it. That is why a dropped field cannot leave a dangling separator behind — and why the fields can be coloured separately, which a spinner, a percentage and a warning about unwritten changes all need.

### Three primitives every surface that scrolls or lists is built on

**`List[T]` (`internal/ui/components/list.go`) is the pointer and the window**: where the focus may land, where it goes next, which run of items a body budget shows, and — through `Matches` and `Filter` — what the query left showing. The selector, the multi-select, the note selector, the agent manager and the saved-chat browser all move on it, and the four case-folded substring filters are one function now. A list keeps its own `Options`/`Rows` and `Focus` as exported fields because hosts read and write both; the `List` is aimed at them where the movement happens and the focus it leaves is read back. The window itself is the one piece of state — `listwindow.go` is the arithmetic under it, and `ListOverflowRow` is the counted marker its edges take. `noteselect.go` has no copy of any of this and never did: it holds a `Select` and moves through it, so it inherited the move.

**`Pager` (`pager.go`) is the offset a body longer than its pane is read through**: `Held` holds it inside the body, `Window` takes the run and writes the held offset back, `Reveal` brings a row in with the least movement, `Above`/`Below` are what a counted marker states, and `Screen` is the header/body/footer shape with the pad that keeps the footer on the bottom row. The full-screen diff, the output view, review mode's hunk pane and the approval card's body are all it.

The transcript pane has a search of its own (`internal/ui/chat/viewport.go`): `Search` finds every occurrence, `NextMatch`/`PrevMatch` walk them and bring the pane to them, and the marks are painted on the copy `visibleLines` already makes rather than on the line cache's own rows. `internal/ui/chat/navigate.go` is the session's half — it routes to the pane the reader is looking at, leaves a surface that owns the pane alone, and puts the position on the reading rail. **What it does not have yet is a way in**: reading mode's key ladder is in `focus.go` and the binding would be a row in `internal/ui/keys`, so a query box is still to be built on top of this. The reason the pane has its own search rather than the bubbles viewport's is written out at the top of `viewport.go` and is about ownership, not speed: that viewport retains and rewrites the caller's slice, and clears its highlights on every content change.

**`SectionFitter` (`sectionfitter.go`) drops whole blocks until a body fits**, reserving the marker's own row from the moment anything is dropped. What a screen supplies is the order — the diagnostic drops what has least to say, the metrics screen drops from the bottom — because the order is the only part that is a fact about the screen.

What will bite you: **a golden is the test for all three.** Every adoption of them was required to leave the rendered bytes identical, so a change to the arithmetic that looks harmless shows up as a golden diff on a surface you were not editing.

A screen's height reaches it through `Sized.SetSize`, and its keys answer with a typed result rather than `any` (`Keyed[R]`). `internal/cli/screen.go` hosts all five commands' screens with one `screenModel[R]`: the command's own state stays in its own type, behind a pointer the `answer` function closes over, because a Bubble Tea model is a value and every one of these commands has something to say after the screen has closed.

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

Every writer is a seam that is the *only* place its event is named, and that is the rule for adding one — a formatted line sprayed at a call site is how a log stops being worth tailing. There are seven. `record` in `internal/provider/failure.go` sits on the classifier every dialect's error already passes through, and `Backoff.Next` in `internal/agent/retry.go` writes each wait from the one place the decision is made rather than from each driver. The other five are the mechanisms that fail without stopping the session, so the only symptom is something else entirely: `Classifier.Judge` when the attempts are used up and it falls back to asking, `Summarizer.Summarize` on a reading that did not happen, `startServer` in `internal/lsp/server.go` at either half of the handshake, `Dial` in `internal/mcp/client.go` when the transport will not connect, and `logUnconfined` in `internal/cli/sandbox.go` when nothing on the host contains commands. **A cancellation is never logged** — at any of them; the commonest line in the file would otherwise be somebody pressing escape, and a log like that is one nobody reads. It bites hardest at `Dial`, which runs on the session's own context because the SSE transport keeps its stream on it, so a caller that gave up waiting leaves the dial running: without the guard, quitting with a slow server still handshaking accuses it of a failure that was the session ending. **Nor is the failure's own text**, at the five: a provider's words are already a line here through the taxonomy, and a language server's, an MCP transport's or a containment probe's are built from a path or a command line, which the file two sessions share does not accumulate. What each line carries is a fixed identifier — a model, a server name, a profile — and a failure code from a closed set, which is also what makes them cheap to grep.

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
diff view. Lines are aligned by Myers' difference algorithm in its
linear-space form, with the common head and tail trimmed off first, so the
cost follows the size of the change and not the size of the file. One trap
survives that: a region whose edit distance passes the `maxEdits` bound in
`diff.go` still degrades to deleting every line and adding every line, which
on screen is a whole-file replacement with no hunks and no intraline
emphasis. If a diff looks like that, the bound is the first thing to check.

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
Attaching switches the focused agent. It does not hide the inspector rail:
`inspectorHidden` lists the takeovers and an attached child is not one, since
the rail's blocks answer for the session either way. The rail's `AGENTS` block
is the map of the run — `Model.inspectorAgents` puts `orchestratorAgent` first
and then `Snapshot()` in spawn order, and `components.InspectorAgent.Focused`
marks whichever row `attachedTo` names. `Model.sessionMap` is that same order
as names and `cycleAgent` (`keys.Draft.NextAgent` / `PrevAgent`) steps through
it via `attach`, so the per-session scroll is kept.

### Project instructions

`internal/project` is what the model is told about the checkout before the first keystroke, and what the checkout is allowed to make a session load ([`docs/capabilities/configuration.md#project-context-is-opt-in-and-lives-with-the-project`](docs/capabilities/configuration.md#project-context-is-opt-in-and-lives-with-the-project)). `project.Instructions(dir, user)` collects the set: the user's own file first (`userInstructionsPath` in `internal/cli/session.go` — `instructions.md` beside the config file), then one file per directory from `project.Root(dir)` down to `dir`, the first of `contextFilenames` (`.shhh/project.md`, `AGENTS.md`, `CLAUDE.md`) that exists and is not blank in each. `project.InstructionBlock(files, prompt.InstructionBudget)` renders them under `## <path>` headings and applies the cap, cutting the outermost file first and saying in its heading that it did. `project.FindFrom` still exists and still answers a different question — the nearest single file — which is what `NeedsScaffold` and the fallback for a directory with no root above it use.

**Trust is the other half of the package, and it is the one to read before touching any loader** ([`docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs`](docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs)). `trust.go` holds the list of paths a checkout can use to make a session run something — the three skills directories, `.shhh/agents`, `.shhh/quality.json`, `.shhh/hooks.json`, `.shhh/mcp.json`, `.mcp.json` — and it is the only such list: `project.Fingerprint` walks it to a content digest and reports which `project.Kind`s the checkout actually holds, and `project.ResourceNames` is the same list as a surface prints it, so a kind that loads without appearing in the withheld list cannot happen quietly. `project.ReadTrust(root, store)` compares that digest against `storage.ProjectTrusted` and returns a `project.Trust`; its zero value withholds, so a missing store, an unnamed root and a surface that forgot to ask all land on the same answer. A missing file is fingerprinted as absent and a symlink as the link rather than as what it points at — following one would hash a tree outside the checkout and re-pointing it would not ask again. Instruction files are deliberately not in the set: prose can only ask.

The CLI side is `internal/cli/trust.go`. `projectTrust` reads once and holds the answer because four loaders ask — `loadSkills`, `loadAgentProfiles`, `openQualityGate`, `mcpOptions` — and a session that loaded skills under one answer and withheld suites under another would be reporting a state that never existed; it is a variable so a test can state the answer instead of writing a checkout and a store to imply one. `setProjectTrust` is the only writer of the row, so the doctor's `[a]`, `shhh doctor trust|distrust` and `/trust` cannot drift apart — and it calls `forgetProjectTrust`, **which is the part to keep**: the doctor re-runs every check when an offer is taken and `shhh mcp` dials again, so a held reading would put "untrusted" directly under the answer the reader just gave. A session already under way is unaffected, which is what the hold is for. `trustStartupNote` is the stderr line every session prints when something was held back, emitted from `buildSessionEnv` because that is the one point the interactive and the headless session share. The reading is a `⊘` and never a failure: withholding is a diagnostic, and trust is a person's act — no mode reaches it and the classifier is never asked.

**The workspace block does not freeze** ([`docs/capabilities/coding-agent.md#the-agent-knows-where-and-when-it-is-standing`](docs/capabilities/coding-agent.md#the-agent-knows-where-and-when-it-is-standing)). `project.PromptBlock` renders the survey as the `# Workspace` section and `project.ReplaceBlock` swaps that section inside an assembled system prompt — it matches the **last** line equal to the heading and ends at the blank line between sections, because a project instruction file is injected ahead of it and is free to contain the same words. `project.RereadGit` is the cheap half of a survey asked again (branch, detached, dirty, head) with the walk's answers kept, and it stamps `Info.Reread`, which is what turns the dirty sentence from "already there before this session started" into a dated count that admits the session's own edits. One closure builds the block for every reader — `sessionEnv.workspace` in `internal/cli/session.go`, reached through `workspaceBlock` — so a conversation and a child cannot come to two answers about one tree. Readers: `chat.Model.WithWorkspaceBlock`, whose `regenerateWorkspace` (`internal/ui/chat/context.go`) is called from `finishCompact` and from `loadChatByName`, the two places a conversation is rebuilt out of a stored message; and `childExtra` (`internal/cli/subagents.go`), which appends `worktreeNote` when `subagent.Spec.Worktree` says the child is standing in a seeded copy rather than in the parent's own directory. **The unattended run does not have it yet**: `agent.Compactor` takes no such hook, so a headless run that compacts keeps the block it launched with.

Three traps. **The walk is `project.Root`'s, not a second one**: the set stops at the repository root, or with no `.git` at the nearest ancestor holding a `.shhh` directory, and only where there is neither does it fall back to the nearest single file above. **Display paths are stated from the root, never from cwd** — otherwise the same file is named `AGENTS.md` in one session and `../../AGENTS.md` in another opened two directories deeper, and the survey's `ContextFiles`, the start screen's context note and `shhh doctor`'s project row all print that. **The set is read once, at session start**, in `buildSessionEnv`; the system prompt is built once per session and `env.projectTokens` is the estimate of this block, so a per-turn re-read would both cost a syscall and invalidate a cached prefix for a file nobody edited. `@path` imports are not followed: such a line is text.

### Skills

`internal/skill` follows the Agent Skills specification ([agentskills.io](https://agentskills.io/specification)) and its client guide. `skill.Roots(cwd, native, projectTrusted)` is the search order (project `.shhh/skills`, `.agents/skills`, `.claude/skills` from cwd up to the git root, then the user-scope ones) — with the project half dropped entirely when the checkout has not been trusted, which is a parameter rather than something this package reads because nothing inside a checkout may decide it. `skill.Discover` reads them into a `Catalog` with lenient validation — a skill that cannot load is a `Diagnostics` entry, never an error, for the reason in [`docs/capabilities/skills.md#where-skills-live`](docs/capabilities/skills.md#where-skills-live). The frontmatter reader in `frontmatter.go` is deliberately not a YAML parser: it takes everything after the first colon as the value, because skills written for other harnesses do.

Three tiers, three places: `skill.PromptBlock` is the catalog in the system prompt (appended as prompt extra, like the toolbox, only when something loaded); the `skill` tool (`skill.ToolDefinition`, read-only, its `name` an enum of the catalog) returns `skill.Content` — body, directory, bundled files listed not read; the file tools do the rest. `/skill <name> [task]` and the `/<skill-name>` shortcut (`internal/ui/chat/skills.go`) send the same content as a user message. `agent.KeepResults(skill.IsContent)` exempts activated content from `TrimOldToolResults`. `allowed-tools` is parsed and displayed and grants nothing — [`docs/capabilities/skills.md#a-skill-cannot-grant-itself-anything`](docs/capabilities/skills.md#a-skill-cannot-grant-itself-anything). `shhh skills` and `/skills` print the catalog with its diagnostics.

### MCP servers

`internal/mcp` is the client side of the Model Context Protocol ([`docs/capabilities/mcp.md`](docs/capabilities/mcp.md)) over `github.com/modelcontextprotocol/go-sdk`. A `mcp.Definition` is one server as written — `Transport` stdio/http/sse, argv or URL, `ReadOnly`, `Scope` user or project — and `mcp.Discover(cwd, userDefs, userDirs)` reads the catalog: the config file's `[mcp.servers]` (converted in `internal/cli/mcp.go`'s `mcpDefinitions`), `mcp.json` beside it, and the project's `.shhh/mcp.json` / `.mcp.json` under `mcp.ProjectRoot`, project shadowing user by name, every unreadable definition a `Diagnostics` entry. `mcp.Connect(ctx, catalog, Options)` dials every admitted definition concurrently and returns a `Toolset` with a `Report` per definition (`Status` connected / failed / disabled / untrusted / changed / missing-env / excluded); `admit` is where a project server in an untrusted checkout, a `${VAR}` that is unset, or a non-read-only server in a conversation is left out *before* anything is spawned; `mcp.Options.Project` is the person's answer about the checkout as a value, because a definition file is not the only thing a clone can make run and one answer covers all of them. `Definition.Expand` resolves `${VAR}` and the unexpanded definition is what reports show, so a token never reaches a listing. Tool names are `mcp.ToolName`: `<server>__<remote>` made provider-safe and capped at 64; `mcp.SplitName` is how the UI recognises one without a registry. `Server.Call` flattens a result with `mcp.Flatten` (text as is, binary as a one-line notice, structured content as the fallback) and returns `IsError` as a Go error so the agent reports it like any failed tool.

The CLI (`internal/cli/mcp.go`) owns every door: `openMCP` in `runChatSession` and `runPrintSession` (after the store opens, and before the toolbox block is built), `Toolset.WrapExecutor` beside web/lsp/structural, `Toolset.Gated()` registered as `chat.GatedPreviewFunc`s (so a non-read-only server's call goes through the ordinary approval queue as `ActionOther` — Ask in every mode, classifier in auto, Deny in plan), `chat.MCP{Has, ReadOnly, Manage, Sources}` via `Model.WithMCP` (`Sources` is `mcpToolSources`, what the rail's `toolsBlock` and `/status` name), the headless gate and approver in `print.go`, and `ReadOnlyDefinitions` for children in `subagents.go`'s `newEnv`. Trust is not this package's any more: `mcpOptions` reads `projectTrust()` (`internal/cli/trust.go`) and the row behind it is `storage.ProjectTrusted/TrustProject/DistrustProject` on `project_trust`, keyed by repository root alone. `shhh mcp` is `doctorCommand`'s screen with `Title`/nouns (`runDoctorScreenTitled`) over `mcpProbes` — one probe per server whose verb is the transport — and `mcpFinding` is the reading, including the `[a] trust` offer, which trusts the checkout and says so; `mcpListing` is `/mcp` and the no-TTY text. The transcript maps a server tool to verb `mcp`, kind `components.ActivityRemote` (⇄, rail) unless `MCP.ReadOnly` says it is a read (`activity.go`). `prompt.Toolbox` knows nothing about these tools: `mcp.PromptBlock` is a section of its own with the server's `Instructions`, and children get `ReadOnlyPromptBlock`.

### Backlog

`internal/todo` is the project backlog ([`docs/capabilities/todo.md`](docs/capabilities/todo.md)): `todo.Root(cwd)` keys it on the repository root — or, with no `.git` anywhere up the walk, on the nearest ancestor holding a `.shhh` directory, and on the working directory only when there is neither (`project.Root`, whose `project.InRepo` is the same walk asked only whether a repository was found), `todo.Load(root)` reads `.shhh/todo/*.md` and `.shhh/todo/done/*.md` into a `Store` whose `Items` are in `todo.Less` order (priority, created, slug) and whose `Ready()` is open items with every `depends_on` in the archive. The header reader in `header.go` keeps every line's text so `SetStatus`/`SetSize` rewrite one line and nothing else; `Render` is only for a new file. Loading is lenient the way skills are: a file that cannot be read at all — no header, no title, an unknown status — is a `Diagnostics` entry, and a value merely off its scale (priority, size, kind) is a warning on the loaded `Item`. Slugs are validated by `todo.ValidSlug` and must never look like a planning identifier, for the docs-check reason above. `Create` writes `.shhh/todo/.gitignore` ignoring `.run/`; whether the backlog itself is committed is the user's call and nothing in shhh stages it. `sprint.go` is the set being worked — `.shhh/todo/sprint.md`, the item header grammar over a name/status/created/session plus a goal paragraph and a `## Items` list of slugs, read by `LoadSprint` into `Store.Sprint` and skipped by `readDir` so it is never loaded as an item. An open sprint is what `Ready`/`Next` answer from, in the file's order (`readyAll` is the unscoped list, and what a proposal is drawn from); a slug that is not ready is skipped rather than offered, and `SprintEntries` is where every surface reads each slug's state from. Writes are line edits like an item's: `SprintAdd`/`SprintDrop` move one bullet, `SprintSetGoal` replaces the block above `## Items`, `SprintSetStatus` goes through `editHeader`. `CreateSprint` refuses a name `done/sprints/` already holds and the chat picks a free one with `freeSprintName`, because a sprint that cannot be filed under its own name never closes and goes on scoping `Ready` to slugs that are all finished. `CloseSprintIfDone` is called after every archive — `todoManager`'s `done` case and `todoRunDone` — and moves the file to `done/sprints/<name>.md` with each item's `## Report` copied under its slug; `SprintProgress` leaves a slug the backlog no longer holds out of both halves of n of m while `SprintFinished` still counts it as accounted for. The planning card is `internal/ui/chat/todosprint.go` reusing `stateTodoPropose` and `todoPropose` with `todoSprintPlan` as the discriminator in `updateTodoPropose`; `run.Options.Sprint` carries the goal into `State.Sprint`, which only `researchPrompt` reads. `shhh todo` (`internal/cli/todo.go`) prints the store, and `todoVerb` there is the one implementation of the textual subcommands: `todoManager` wraps it for the session's `/todo` (a `todoNotFound` becomes the mistyped-name row, a `todoUsage` the usage line, anything else an `Error:` line), and the cobra verbs registered beside `newTodoCmd` — `ready`, `next`, `block`, `open`, `done`, `drop` over `newTodoStateCmds`, plus `sprint`, `run` and `show` — hand the same error to fang so a script has the refusal in its exit status. `todoHeld` is the one refusal only the command surface could have needed and both get: `run.HeldBy` reads the item's checkpoint and then the sprint's, because a sprint records the item it has taken before that item has a checkpoint of its own. `todo_json.go` is `--json` on the listing verbs (`todoDoc`, the store as the screen has it — state, ready, waiting, the sprint, diagnostics and the per-item warnings), and `todoShow` splits on the destination: `report.Mono`/`report.Width` and `internal/ui/markdown` for a terminal, the item file byte for byte for a pipe. The chat side is `internal/ui/chat/todo.go`: `Todos`/`WithTodos` wiring from `internal/cli/chat.go`, a cached `Model.todoStore` reloaded by `reloadTodos` on the events that can change a file (a `/todo` command, the editor returning, a turn ending in `turn.go`) and never per frame, `openTodoPick` over the generic picker, `openTodoEditor` reusing the draft editor's `editorArgv`/`editorRefusal` with its own `todoEditorDoneMsg` so the item file is never removed, and `inspectorTodo` feeding the rail's `todoBlock` (`internal/ui/components/inspector.go`, between PLAN and CHANGES). A bare `/todo add` is `internal/ui/chat/todoadd.go`: `todo.Extractor` (`internal/todo/extract.go`, the summarizer's shape — tool schema, text fallback, untrusted digest, no tool output) runs as a background command and lands as `todoProposalsMsg`; the card is a `components.MultiSelect` on `stateTodoPropose` with everything checked, and `writeProposals` resolves `depends_on` titles to slugs over the accepted set, dropping and naming what matches nothing. The reading is billed as `meter.SourceBacklog` and uses the session model.

The runner is `internal/todo/run` (a pure state machine: `State`, `Step`, `First`/`Observe`/`VerifyResult`/`Committed`/`Block`, the stage prompts in `prompt.go`, marker-line parsers, and the `.shhh/todo/.run/<slug>.json` checkpoint) driven by `internal/ui/chat/todorun.go`: `startTodoRun` sets the item in progress and sends the research prompt in plan mode; `todoRunAfter` is the `Update` tail hook (beside the summary's close) that reads the stage's answer when the turn is truly over — not during a round-limit pause or a decision card — and `todoRunStep` carries out the step handed back; `todoVerifyCmd` runs `State.Tests` (the `## Tests` bullets snapshotted by `run.Start`, never re-read from the file the model may have edited) and `Gate.Run`, `todoCommitCmd` stages `todoRunPaths` (changeset records since the run's first turn, under the root, never `.shhh/todo/`) and commits, `todoRunDone` archives with the report, `todoRunBlocked` writes the evidence. What will bite you in `todoCommitCmd`: **`git diff --cached --quiet` has four exits and only one of them is about the index.** 1 is a staged difference; 127 is what `git` here reports for a binary that could not be started at all (an `*exec.Error` rather than an `*exec.ExitError`); and outside a repository the code moved between git versions — 128 for the refusal, 129 on 2.51, where `--cached` becomes a usage error against the `--no-index` fallback — so the repository is read from the filesystem with `project.InRepo` instead, which does not move. `run.Options` carries the two facts a run is started with: `NoCommit` (`--no-commit` on the command, or `todo.commit = false` reaching `chat.Todos.NoCommit` from `internal/cli/session.go`) ends the run at `State.archive` after a clean review, with a code-written report naming `State.Paths`; `Repo` is what every stage prompt that names a git command reads (`readTheChange`, `commitStyle`, the reviewer's task note), so a run outside a repository never tells the model to read a history that is not there. The run is drawn as one transcript row (`entryTodoRun`, `todoRunRow` in `todorun.go`): appended by `openTodoRunRow` when the run starts, told about every transition by `observeTodoRunRow`, and rendered by `todoRunRowView` from the machine's own `*run.State` rather than from a copy, so it moves as the run moves. What the row keeps of its own is `marks` — which strip stages this row watched go by — because a run continued from a checkpoint saw none of the ones below it and draws them as restored. `run.Strip`/`run.Place` are the stages and their order; `run.Step.Name` is the one word the record (`observe.SignalRun`) and the row both use for a transition, which is the stage for a model turn and the action otherwise. `/todo status` opens that row in reading mode rather than printing a summary, `[o]` (`keys.Row.Reopen`, `todoRunReopen`) reopens a blocked run's item from it, and a blocked run's accepted follow-up is named on the row by `nameFollowUpOnRun`. The plan-approval card is suppressed while a run owns the plan-mode turn (`doneMsg` in `model.go`). `/clear`, `/todo stop` and the cancel chord on a stage turn end a run and put the item back to open (esc stops nothing — `cancelStreaming` is the one path that sets `todoRunCancelled`); plain text is refused while a run is going (`todoRunHoldsInput` in `command.go`), and a turn that ends without being the stage's (`todoRunTurn`) blocks the run. Review and commit turns run in plan mode. Phase-two gates: `afterResearch` pauses (`ActionPause`, `State.Paused`) for L always, for M on questions or a size upgrade, and blocks an S with questions; the chat's `openTodoPause` is a `components.NoteSelect` on `stateTodoPause` whose answers are `Resume`, `Replan(note)` (the note is appended to the item under `## Answers` and research runs again) or stop. For M/L the review is `ActionReview`: `startTodoReview` spawns `subagent.RoleReviewer` through `Supervisor.Spawn` with the diff in the task, `todoReviewDone` (hooked in `handleSubagentEvent`'s `EventDone`) reads `Supervisor.Report` into `ReviewResult`; no supervisor or a refused spawn falls back to `SelfReview`. A blocked run offers `todoFollowUp` on the proposals card (`openTodoProposals`). Resilience: `/todo run <slug>` on an in-progress item with a checkpoint calls `State.Continue` (the checkpointed stage restarts; `Session`, `PrevMode`, `Turn` are re-stamped), and a stage turn displaced by another turn (`turnCount != todoRunTurn`) goes through `stopTodoRunKeeping` — checkpoint kept, item left in progress — rather than blocking. **A stage turn that ended on a recovery row is not observed at all** (`todoStageStopped`, reading the `entryStreamDrop` the session drew): a `streamResume.truncated` row is continued with `continueStream` — the same `agent.ContinueAfterCeiling` the row's `[c]` sends — with the half kept in `todoRunState.carried` so `todoStageAnswer` returns both halves as the one answer, and a second ceiling in the stage blocks on `run.CutAtCeiling`; a wire drop takes `stopTodoRunKeeping` instead, because continuing half a sentence is the judgement the row offers a reader. The changeset store is per session, so `State.Paths` (snapshotted on every save) is what lets a continued run stage and diff what an earlier session changed; `todoRunPaths`/`todoRunDiff` union it with this session's records. Reviewer children are named from `State.Reviews`, never reused, because a killed child keeps its name in the supervisor. A large item goes through `StageSplit` (plan mode; `ParseLanes` in `run/lanes.go` checks 2–`MaxLanes` lanes with disjoint paths, none under the backlog, and `lanes: none` or a refused division falls back to `implementWhole`) and `StageFanOut` (`ActionFanOut`: `startTodoFanOut` spawns one `subagent.RoleWriter` per `Lane` in one supervisor batch, named `tw<Fanouts>-<lane>`, with `LaneTask` as its task and the lane's paths as its claim); `todoLaneAsk` in `handleSubagentEvent` auto-approves a lane's `AskPatch` and refuses one carrying an overlap warning (`LaneFailed`), `EventPatch` marks the lane landed (`LanePatched`), and the writer's `EventDone` is `todoWriterDone` → `LaneDone`, which blocks on an unfinished writer or one whose patch never landed and, on the last lane, sends the `integratePrompt` as the `StageImplement` turn. `State.LiveAgents` is what `endTodoRun`/`stopTodoRunKeeping` kill. `internal/todo/run/sprint.go` is the loop above a run: `run.Sprint` is a checkpoint of its own at `.shhh/todo/.run/sprint.json` — current item, done, attempts, cap, and one of four closed endings (`SprintEmpty`, `SprintCapped`, `SprintBlocked`, `SprintStopped`) — and `run.Live(root)` is how every surface asks whether a sprint is going, so a corrupt or ended file reads as no sprint rather than as one nobody can end. It takes items from `Store.Ready()` and nothing of its own, which is what scopes it to the sprint file when the backlog holds one. `run.State.InSprint` marks a run the loop started: it arms the on-close gate for every stage (`closeGateArmed`), names the item in the turn-close notification (`sprintCloseWords`), and is what the rail's `on <slug>` row is drawn from. The chat side is `startTodoSprint`/`sprintNext`/`advanceSprint`/`endTodoSprint` in `todorun.go`; `advanceSprint` crosses the session boundary through the same `startNewSession` `/new` uses, so one definition of a session ending serves both. **The sprint's state is on disk and not on the model**, read back through `run.Live` at each decision point rather than cached — which is why nothing in the rail's per-frame path touches it. The per-item wall-clock cap is `run.Sprint.Expired`/`run.TimedOut`, read at a stage boundary rather than by a timer — a stage is the smallest thing the machine can judge — and each driver supplies the duration itself: the headless one from `config.Config.TodoItemTimeout` (`todo.item_timeout_minutes`), the chat one from `chat.Todos.ItemTimeout`. `internal/cli/todorun.go` is the same loop with no TUI: `todoDriver` runs the machine and spends each `ActionPrompt` as one `shhh code --print --output json` in the checkout (`todoDriver.turn`, a field so the loop is testable without a provider), reads `.final` whatever the exit status and blocks on the child's `truncated` (`Headless.TruncatedReply` through `jsonTranscript`) with the same `run.CutAtCeiling` the session uses, blocks on `ActionPause` and falls back to `SelfReview`/`NoLanes` because there is no supervisor, and reads what it may stage out of `git status --porcelain` against the set the tree already held — `todoGitLines` is untrimmed on purpose, because porcelain's first two columns are marks and a trimmed line moves the path. Its `exitBlocked` (7) joins the closed exit-code set in `internal/cli/print.go`. The `.shhh` path is a directory now: the context file is `.shhh/project.md` (`project.ContextFile`), `shhh init --project` writes it there, and a checkout still holding the old single `.shhh` file is a `shhh doctor` migration (`internal/migrate/project.go`), not something the context reader falls back to.

### Saved chats

A conversation autosaves to a slot of its own, and the slot is the store's to give ([`docs/capabilities/sessions-and-memory.md#a-slot-belongs-to-one-session`](docs/capabilities/sessions-and-memory.md#a-slot-belongs-to-one-session)): `newSessionName` (`internal/ui/chat/model.go`) is only the timestamp, and `ClaimChatSlot` is the insert that settles a collision, appending `(2)`, `(3)` when the name is taken — a look before the insert would hand two processes started in the same second the same name, which is the defect this replaced. `Model.WithDB` claims, `adoptSlot` moves the session between slots and gives an unwritten claim back through `ReleaseChatSlot`, and `quitCmd` does the same for a session that never saved. **`ListChats` inner-joins `chat_messages`, so a claimed slot is invisible until its first save** — without that, `--continue` would offer the empty slot the session had just minted. The store remembers `chatSeq`, the highest seq this process wrote to or read from each slot, under a `chatMu` held across the whole save, record included: `saveChatTx` returns `ChatSlotConflictError` when the slot no longer holds what this process left there, `AutosaveChat` answers that by claiming a fresh slot and writing the conversation there, and `chatMoved` keeps a second refusal on the same slot from making a second copy. The chat model follows with `autosaveMovedMsg`/`noteSlotMove`, which re-points the observer's session link and resets the title reading, because the slot it was read for is somebody else's row now. **A new conversation is a new session** ([`docs/capabilities/sessions-and-memory.md#a-new-conversation-is-a-new-session`](docs/capabilities/sessions-and-memory.md#a-new-conversation-is-a-new-session)): `startNewSession` (`internal/ui/chat/model.go`) is the boundary and the only definition of what one resets — the autosave first in `quitCmd`'s own sequence, children cancelled, a backlog run let go of through `keepTodoRun` with its checkpoint kept, then the conversation, the changeset (`changeset.Store.Reset`), the seen map (`tools.ForgetAll`), the tree baseline (`agent.RestartTreeCheck`), the counters, the odometers, the summary and the slot — `mintSlotKeeping` rather than `mintSlot` whenever a save is queued, because giving the old claim back deletes the row that save is about to write into. The half it cannot do itself is the `chat.NewSession` hook the host is handed at start (`WithNewSession`, wired in `internal/cli/session.go`): `observeRecorder.restart` ends the row and opens another under the same kind, provider, model and settings stamp, and `chatSession.systemPrompt` builds the prompt again from the checkout as it stands — the same function `buildSessionEnv` uses at launch, so the two cannot drift. `/clear` and `/new` are one command; over a turn that is not over (`turnInFlight`, so a held turn too) both draw the confirm quitting draws, through the one surface `openEndConfirm` builds with the act a yes carries out. The pressure card's `[n]` crosses the same boundary. `storage/chat.go` is the rest of the store: `SaveChat`/`SaveChatBranch`/`LoadChat`, `ListChats` with the generated `Title`, `RenameChat` (branches follow ids; a collision is `ChatExistsError`), `DeleteChat` (takes the descendants with it), `CountChatBranches` for the confirm, `SetChatTitle`/`ChatTitle`. Three views draw it ([`docs/capabilities/sessions-and-memory.md#housekeeping`](docs/capabilities/sessions-and-memory.md#housekeeping)): the `/chats` picker is `internal/ui/chat/chats.go` — the generic picker with `Select.Actions` offering `keys.Select.Delete`/`Rename`, a `components.Confirm` or a `textinput` row drawn under the card, the session's own slot as the `⊘` row (`protectedPhrase`), state in the `chatOps` value on the model; the browser is `internal/ui/browse` with `browse.Ops` wired by `pickSavedChat` in `internal/cli/session.go`; `shhh chats` (`internal/cli/chats.go`) is the browser bare and `list|show|delete|rename [--json]` with a verb. Titles ([`docs/capabilities/sessions-and-memory.md#a-title-you-did-not-write`](docs/capabilities/sessions-and-memory.md#a-title-you-did-not-write)) are `agent.Titler` (`internal/agent/title.go`, the summarizer's shape, `CleanTitle` bounds the answer) driven by `internal/ui/chat/title.go`: `titleCloseCmd` in the `Update` tail beside the summary's close, at most `titleAttempts` readings, only on an `isAutosaveSlot` name, written by `finishTitle` and carried by every autosave and `/save`; `/ui title` and `summary.title` (`Config.TitlesEnabled`, on iff `summary.model` is set) switch it. **A conversation is told what the checkout looks like now on its way back** ([`docs/capabilities/sessions-and-memory.md#a-resumed-session-sees-the-tree-as-it-is`](docs/capabilities/sessions-and-memory.md#a-resumed-session-sees-the-tree-as-it-is)): `internal/ui/chat/reopen.go` is the whole of it — `ResumeContext` reads the slot's `storage.ChatResume` (the `summary` and `head` columns, written by every autosave beside the title through `SetChatResume`) and surveys the workspace, `resumeNotice` builds the user-role messages and the folded row from a `project.Info` and that state, and `resumeConversation` is the one path `WithResumedMessages` and `loadChatByName` both take. `project.Head` is the cheap half of the survey, asked for at every save so the slot records the commit the conversation was written down on; the moved-head line is the difference between it and `Info.Head`. `ResumeContext` is a package function rather than a method because a front-end with no model — the unattended run handed a conversation to carry on — needs the same reading. **The reading is never stored with the conversation**: `stripResumeContext` takes it off at every save and at every load, so a slot cannot come back carrying a reading of a checkout that has since moved, rendered as something the person said; `injectResumeContext` shifts the rewind checkpoints by what it inserted, since a checkpoint is a conversation index. The `/compact` summary is carried on the model as `compactSummary`, set by `finishCompact` and cleared at the session boundary — nothing else writes one, and nothing summarizes at quit.

### Session observability

`shhh observe` ([`docs/capabilities/sessions-and-memory.md#observations-are-what-the-session-did`](docs/capabilities/sessions-and-memory.md#observations-are-what-the-session-did)) is four layers. `internal/observe` is the contract itself — the `Observer` callbacks, the `Pos` an event happened at, and every code as a constant: `ClassFromResult` maps a failed result to its class by shape, `ReasonCode` a policy reason to its code, `ToolOutcome` is the pair a surface reports a result as, and all three are where free text is stopped from reaching the table. It lives below every front-end because a vocabulary each runner keeps its own copy of is a vocabulary that drifts, and nothing fails when it does. `storage/observe.go` is the tables (`agent_sessions` with provenance and the chat-session link, `agent_events` with `turn`/`round`) and the aggregate queries the dashboard draws; the event kinds are `tool`, `decision`, `turn` and `signal`. `internal/ui/chat/observe.go` is the chat model's adaptation to the contract — where its turn, round, ledger and close state are read off — and declares none of the vocabulary itself; `headlessObserver` in `internal/cli/print.go` is the same thing for a headless run, whose whole position is turn 1 and the round the loop has reached. `internal/cli/observe.go` is the `observeRecorder` that writes what the observer reports, the `stamp` of provenance (fingerprints, never the prompt or the path), and the `observe`, `observe compare --split <key>`, `observe session <id>`, `observe export [--transcript]` and `observe purge` commands. The chat model's hook sites: tool results and the turn close (`close.go`, the one place a turn ends), `trimContext`, `finishSummary`, `injectSteering`, `applyUndo`, `applyMode`, the plan card, the round-pause offers, `handleSubagentEvent`, `todoRunStep` and the run's stop paths, `activateSkill`, and `autosaveCmd` with `noteSlotMove` for the session link. `agent.IsRepeatNotice` is how a surface counts repeat notices without knowing their wording. Adding a signal means a new constant in `internal/observe` with its reason set in the comment, and a call at the site — never a formatted string.

**The record has a window and a switch, and they are different things** ([`docs/capabilities/sessions-and-memory.md#the-record-is-kept-for-a-window`](docs/capabilities/sessions-and-memory.md#the-record-is-kept-for-a-window)). `PruneAgentObservability` is the window: it rides `pruneStoreOnce` in `internal/cli/store.go` — one guard, one goroutine, the first store a command opens — off `observe.retention_days`, whose default is twice history's because a cohort comparison reads back across a change made a quarter ago. `PurgeAgentObservability` is still everything, on purpose. Two traps in the prune. **`agent_sessions.parent_id` declares no cascade**, so a parent deleted while one of its children survives leaves a row pointing at nothing; the delete set is therefore a recursive family and not a `WHERE` on the timestamp, and a child goes with its parent whether or not it ever ended. And the events are deleted explicitly beside the sessions in one transaction rather than left to the schema's `ON DELETE CASCADE`: a prune that quietly did nothing because a pragma was off is exactly the failure a window cannot survive, since nobody looks at a table that is supposed to shrink by itself. Only an ended row seeds the family — an open one is either running or waiting for the next session's start to close it, which brings it into reach the ordinary way.

**The record can leave this machine** ([`docs/capabilities/sessions-and-memory.md#the-record-can-leave-this-machine`](docs/capabilities/sessions-and-memory.md#the-record-can-leave-this-machine)). `internal/observe/otel.go` is the exporter: `NewExporter` builds an OTLP-over-HTTP trace client from `otel.endpoint`, `Exporter.Session` opens the span a session hangs off, and `SessionSpan` has one method per `Observer` callback so the recorder reports the same arguments to the store and to the wire from one line each. The attribute keys are the `Attr*` constants and `exportAttrs` is the closed set; `otel_test.go` reads the file with `go/ast` and fails on an attribute built from anything but one of them, which is what keeps the set closed rather than merely documented. `ParseEndpoint` refuses a scheme-less endpoint on purpose — http and https are different promises about the network. **The wiring seam is `setObserveExport` in `internal/cli/observe.go`**, called from the root beside the two retention windows, because a recorder is opened by four surfaces and none of them has the config: `observeExport.Session(...)` is nil-safe, so `startObserveRecorder` fills `observeRecorder.span` unconditionally and every callback is one extra line. Traps: `usage` is span attributes and not an event, because the record keeps it on the session row; `link` exports nothing, because the saved conversation's name is the join to content; retry is switched **off** at the exporter, since the default would wait out a dead collector on the goroutine closing the session; and a failed export sets `spanSink.off` and writes exactly one log record, for the whole process. **`restart` is the one caller that sends its closing span from a goroutine**, because a session boundary is answered inside the update loop (`/new`, the pressure card, a run starting a fresh conversation) and a slow-but-alive collector would otherwise freeze the screen for `exportTimeout`; the exit keeps the synchronous send, since a span nobody waits for is one the process outlives. A child session gets a span of its own with no parent link — `startChildObserveRecorder` is handed the parent's row id and not the parent recorder.

**A session knows it is not alone** ([`docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone`](docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone)). `agent_sessions` carries a `pid` and a `heartbeat` beside the `project` fingerprint it already had, and those three answer "is another session in this checkout right now". `storage/observe.go` holds the reading: `StartAgentSession` stamps the id and the first beat at the insert, `BeatAgentSession` is called from `observeRecorder.turn` — the one callback every surface already reports a finished turn through, so the beat follows the row a session boundary opened rather than the one it closed — `LiveSibling` is the query, and `CloseCrashedAgentSessions` runs at every `startObserveRecorder` to end the rows of processes that are gone. **The liveness check is `pidRunning`, a variable over the build-tagged `pidAlive` in `pid_unix.go`/`pid_windows.go`**: signal 0 on Unix with `EPERM` read as alive, and on Windows `os.FindProcess`, which is `OpenProcess` there and fails outright for an id nothing answers to — `Process.Signal` refuses anything but a kill on Windows, so there is no signal 0 to send. It is a variable because a test cannot write down an id that is dead on every machine. The window beside `agentHeartbeatWindow` is deliberately long; the trap is the opposite one, an idle second session that beats nothing and would disappear from a short window. Wiring: `readSibling` in `internal/cli/start.go` points the question at the store as soon as one is open and `chatSession.sibling` carries it — a question rather than a cached answer, since a second session usually arrives after the first and the prompt is built again at a session boundary. `sessionSibling.since` puts the start time on `project.Info.Sibling`, which the screen (`startFacts`) and the prompt (`project.PromptBlock`) are both written from; `withSibling` hands `sessionSibling.live` to `agent.TreeCheck.Sibling`, which `diffTree` asks only once it has something to report. Slots: `liveChatSlots` marks `ChatListEntry.Live`, which `chatPickOptions` folds with `livePhrase` and `chatsReport` warns on; the picker's apply refuses it the way it refuses the session's own row. **One mark, and every picker over it refuses**: `chatBrowseItems` in `internal/cli/session.go` puts the same sentence on `browse.Item.Refused`, and `browse.take` answers it with the list's own notice rather than quitting, so `--resume` cannot mark a row and open it anyway. Both spellings of the phrase are a `livePhrase` const — one in `internal/ui/chat/chats.go`, one in `internal/cli/session.go`, which also writes `--continue`'s refusal from it.

**Every composition writes the same rows** ([`docs/capabilities/sessions-and-memory.md#every-composition-is-one-population`](docs/capabilities/sessions-and-memory.md#every-composition-is-one-population)). A session and a headless run open their row in `runChatSession` and `runPrintSession`; `newCmdCmd` opens one of kind `cmd` above its pipe branch, so a piped one-shot is recorded too, and closes it with an explicit call before each `os.Exit`, because a defer does not survive one — `raw.SystemPrompt` is exported so the piped run's stamp fingerprints the prompt that actually went out rather than a second construction of it. **The one-shot's store is opened on a goroutine (`startRecord`), so the row, its stamp and the `requests` row all sit behind `pendingRecord.wait`, and that is the trap:** anything added to `newCmdCmd` that touches `db` or the recorder without going through `wait` first reads a nil that is only nil because the store had not answered yet, and it will be nil on a fast machine and not on a slow one. The prompt both branches stamp with is therefore built above the branch rather than inside it, because the goroutine is started with it. `oneShotOutcome` and `headlessTurnOutcome` map each surface's ending onto the same closed set the chat's `turnOutcomeCode` uses; a headless round cap is `cap-paused`, not `failed`, because it is the same event a session's cap is and only the way out differs. A child is `subagent.Recorder` — the contract plus the `End` no caller outside the supervisor could hold — handed to `Options.Record` with the child's own system prompt, so its provenance is its own rather than its parent's; `child.pos()` places its events, `endTurn` in `Supervisor.run` closes each of its turns, and `resolveGated` reports the policy's verdict, the classifier's and the user's answer as the two events a session reports. The decision reason codes are `observe.Reason*` — one spelling per verdict across four deciders. `agent.Headless.OnSummary` is what puts a reading in the record from every unattended surface at once, and `observe.SummaryCode` is the one spelling of its states.

**Whether it worked** ([`docs/capabilities/sessions-and-memory.md#whether-it-worked`](docs/capabilities/sessions-and-memory.md#whether-it-worked)) is two things on top of that. The gate verdict is `Observer.Gate`, the one callback that is not a `Signal` — a signal carries one qualifier and a gate run carries a suite as well, and it takes no `Pos` because `/gate run` starts one between turns. `quality.Runner.Observe` hangs off `finish`, the single point both `Run` and `Start` land on; `recordGateVerdicts` is the one function that points a gate at a session's record, so a third surface cannot wire the gate and forget the record. It is handed the suite only when `Result.Trusted` says the name resolved in `.shhh/quality.json`: the tool takes that name from the model, so an unresolved one is the model's own text and `observe.GateHook` replaces it with `GateSuiteUnknown`. Verdicts map through `observe.GateVerdict`, and the event is a signal row whose `tool` is the suite. The session outcome is the `outcome` column, written optimistically by `observeRecorder.turn` through `observe.SessionOutcome` and corrected in `end` (nothing finished ⇒ abandoned) or `endWith` (the TUI failed and `os.Exit` will skip the close). A cap-paused turn maps to nothing at all — a sub-agent's supervisor grants itself more rounds and runs on, so a pause read as an abandonment would libel every child that took a check-in. An empty column reads as `unknown` and is never written. `AgentGateVerdicts` and `AgentSessionOutcomes` are the two aggregates, drawn as the `GATE` and `OUTCOMES` sections; `observeSignalRows` drops the gate because it has a section of its own, and the pass rate's denominator is pass plus fail only, since a blocked run is no reading of the code rather than a bad one.

**The `rating` column is the human check on that outcome** ([`docs/capabilities/sessions-and-memory.md#a-rating-is-how-you-check-the-inference`](docs/capabilities/sessions-and-memory.md#a-rating-is-how-you-check-the-inference)), written by `shhh rate` through `RateAgentSession` and nullable, so unrated and disliked stay apart. **`ListUnratedSessions` is the one query in `storage/observe.go` that reads content, and it is the trap this section exists to name:** it joins `agent_sessions` to `chat_sessions` and pulls the title and the first user message, because a content-free row is not something anyone can judge. The crossing is read-only — the answer that comes back is one integer — and the join is also what excludes children and headless runs, since `Observer.Session` is wired from the chat model and nowhere else — its autosave, and the session boundary, which links the new row to the slot it mints. Resuming a conversation opens a second session row against the same name, so both are reminded by the first sitting's opening line. `internal/cli/rate.go` holds the walk: `rateItem` is one entry of either kind, `rateHandle` is the `c11`/`s7` string the card carries back so the screen never learns there are two tables, and `components.RateRow`'s `Kind`/`Verb`/`Target` are what let one card draw a shell command and an agent run.

**What a session ran under** ([`docs/capabilities/sessions-and-memory.md#what-a-session-ran-under`](docs/capabilities/sessions-and-memory.md#what-a-session-ran-under)) is `storage.AgentSettings`, carried on `AgentProvenance` and written by `StampAgentSession` only when its `ConfigHash` is set; the nine columns are nullable so a row older than them scans to a nil `AgentSessionSummary.Settings` rather than to zero values, and `observeSettingsPairs` prints nothing for that row. `sessionSettings` in `internal/cli/observe.go` is the allowlist — the one function that reads a config value into the stamp, except the sandbox profile, which each surface parses through `sandbox.ParseProfile`'s closed set before filling `runSettings` — over a `runSettings` each surface fills with what it resolved for itself (mode, effort, the cap through `roundCapFor`, the containment profile in force, and whether it has a summariser and a classifier at all); `configHash` is the JSON of the whole `config.Config` fingerprinted. `settings_test.go` marks every config field by reflection and fails if any marker off `settingsAllowlist` reaches the stamp, so a new config key is excluded by default. A child's mode and cap ride on `subagent.Spec` (`Mode`, `MaxRounds`, the latter from `roundCap`) because the supervisor is the only thing that knows them after the profile and the parent clamp; the headless run stamps no mode and no classifier, and `cmd` stamps only its reasoning level and the hash.

**Two cohorts, as rates** ([`docs/capabilities/sessions-and-memory.md#a-comparison-is-two-cohorts-as-rates`](docs/capabilities/sessions-and-memory.md#a-comparison-is-two-cohorts-as-rates)). `shhh observe compare --split <key>` is `AgentCohorts` — one `GROUP BY` over a stamped column, largest cohort first — plus `ReadAgentCohort`, which is every event aggregate the dashboard draws taken over one cohort's sessions. The CLI half folds the two readings into one `[]observeChange`, and the report and `--json` are both rendered from that list and nothing else: a second builder for the export is exactly how the screen and the file come to hold different numbers. **The aggregates are one piece of query text each, not two copies.** Every event reading and the outcome mix is a `const` with a `%s` where its `WHERE` clause goes, filled by `observeEventWindow`/`observeSessionWindow` for the dashboard and `observeEventCohort`/`observeSessionCohort` for a cohort — so the comparison cannot drift into measuring something the dashboard does not draw. Those two write the column name into the SQL rather than binding it, which a placeholder cannot do, and that is why the name comes from `agentSplitColumn`'s allowlist and from nowhere else. Traps: `compareMinSessions` refuses the whole comparison rather than caveating a row, and `observeCompared` is the pure half so the refusal and the full screen are both held against fixtures; `observeShareColumns` settles a share's decimal per column *after* every change is built, since a block whose rows disagree about their decimal reads as a rendering fault; a share's direction is in points and everything else's in percent, because a pass rate moving 75% → 92% is seventeen of one and twenty-three of the other; and no row carries a tick or a cross, because which direction is the good one is the reader's.

### Quality gate

`internal/quality` is the gate: `LoadConfig` reads the workspace's trusted `.shhh/quality.json` with `DisallowUnknownFields`, `Runner.Run` and `Runner.Start` are the blocking and background halves over the one `execute`, `Result.Format` writes the verdict and `Summarize` reads it back — beside `Format`, so the one place that writes the string is the one place that parses it. The CLI owns the wiring (`internal/cli/quality.go`): `openQualityGate` returns nil in a checkout the person has not trusted — no runner, so no `quality_gate` tool is registered at all rather than one that would refuse — and otherwise builds the runner with the containment wrap and the evidence hook, `gateManager` backs `/gate run|result`, and `recordGateVerdicts` points it at the record.

**A turn can run the suite itself as it closes** ([`docs/capabilities/coding-agent.md#it-can-check-itself`](docs/capabilities/coding-agent.md#it-can-check-itself)). The suite is `Config.OnClose` and the hand-back budget `Config.CloseRetries()` — a pointer field, because zero is an answer here and an absent key is not. A name that resolves to no suite is refused by `validate`, at the read rather than at the close. Two surfaces run it and they share nothing but the text: `agent.Headless.OnClose` is asked at the one point the loop reaches a final message, and what it returns is appended as a user message and the loop continues (`internal/cli/print.go`'s `headlessCloseGate`, whose `err` is the run's exit code); the chat's is `internal/ui/chat/gate.go`, where `setTurnState` sends a closing turn to `stateCloseGate` instead of to the input, the Update tail starts the run, and `finishCloseGate` either hands the verdict back and re-enters the stream or settles and lets the close row be drawn. **The verdict is fed back as a user message and not as a synthetic tool result**: a fabricated assistant tool call would put a request in the model's mouth that it never made, and every checkpoint, save and rewind index would carry it. The text is the runner's own either way, which is the part that matters.

What will bite you: **the close row reads its verdict off the transcript, not off the runner.** `turnChecksRow` scans the turn's entries for a `quality.ToolName` row, so `appendCloseGateRow` is what makes the run visible at all — and it is why `cancelStreaming` calls `cancelCloseGate` first, since a turn abandoned mid-suite would otherwise close showing whatever the turn had checked itself. `closeGateOwed` is the one question, asked in `setTurnState` and again by the tail hook, and `changeset.Turn.Checkable` (`changeset.AnyCheckable` for the surfaces with no changeset) is what keeps a turn that only wrote under `project.StateDir` from paying for a build. A backlog run arms itself through `run.State.ClosesWithGate`, which is the implement stage and no other, and `run.State.Checks` carries a pass to the verify stage so the suite is not run twice over a tree that did not move between them.

### Secrets

`internal/secret` is the vault of values the model never sees ([`docs/capabilities/secrets.md`](docs/capabilities/secrets.md)). A `secret.Vault` holds name→value; `Environ()` is what a command gets, `Scrub()` is what everything read gets, and `PromptBlock` names the secrets to the model. The CLI owns the vault (`internal/cli/secrets.go`) and every door: `chatSession.openSecrets` runs before the first command and puts the values in `runner.SetSessionEnv` (every captured runner sets `cmd.Env` from `runner.Environ()`), `process.Supervisor.SetEnv` (the process tool builds a bare environment and would otherwise miss them), and `sandbox.Container.ExecArgv`'s `--env NAME` pass-through (a container has no host environment). The scrub sits on the executor chain (`Vault.WrapExecutor`, outside the reducer), on the runners (`scrubRunner`, `scrubTailRunner`, `scrubContainment` — the tail is what the screen shows), on the agent (`Agent.SetScrub`, applied in `Append`, `SetMessages` and `Stream`) and on the stream closure in `buildSessionEnv` and the sub-agent `newEnv`. Children get the same through `subagent.Env.Scrub`. `/secret` (`internal/ui/chat/secrets.go`) calls the CLI's `secretsManager`, which returns the note and an announcement the chat sends the model as a user message — the system prompt cannot be rewritten mid-session. `Scrub` also replaces base64/hex/URL-escaped forms and any run of the value ≥ 8 bytes, and then runs a second pass (`patterns.go`) over what is left: a small table of credential shapes — AWS access keys, GitHub and Slack tokens, PEM private-key blocks, JWTs — each replaced with `[redacted:<kind>]`. The order is load-bearing: a declared value is already `[secret:NAME]` by the time a shape could match it, so the name survives, and a session with nothing declared still gets the shapes. Both are text matching and the doc says so. The by-name half is `secrets.env_mask` (on unless turned off): `secret.MaskedEnvName` is handed to `runner.SetEnvMask` by `openSecrets`, so `runner.Environ()` drops every inherited `*_KEY`/`*_SECRET`/`*_TOKEN` before the vault's own pairs are appended — which is what exempts a declared secret. `process.buildEnv` needs no mask because it names `PATH` and `HOME` and inherits nothing else; `secret.PromptBlock` tells the model the mask and both placeholders exist. **The wrap order is not where the disk is protected.** `Vault.WrapExecutor` sits outside `Reducer.WrapExecutor`, so it sees a result only after the store has already written the original — which is why the scrub is also handed *into* the three things that write copies that outlive the turn: `evidence.Reducer.SetScrub` runs it before `Store.Put`, and `process.Supervisor.SetScrub` runs it on every stream write, so a process's spool and the ring the model pages are the same clean bytes — plus one whole-text pass over the spool at `spoolCopy`, because a value split across two pipe writes is otherwise only caught by the fragment rule, and the spool is the copy that becomes a file. **The quality gate is the third and is wired differently**, because it is built with the toolset and the toolset is complete before `openSecrets` runs: `openQualityGate` hands `quality.Runner.SetScrub` the reducer's own `Scrub` method, so the runner reads whatever scrub was installed at the moment a check's output is kept rather than copying a vault that does not exist yet. `runCheck` runs it once over the whole capture, before the evidence hook and before `tailExcerpt`, so the file in the store and what `/gate result` prints are the same text; checks run with `cmd.Env` nil, which is shhh's own environment and where the values were loaded from. All three take a `func(string) string` rather than a vault, so none of the packages imports `internal/secret`; nil is a session with no secrets and writes raw, and a scrub can never be why a write fails — `streamBuf.Write` reports the caller's own count, because `os/exec` reads a short write as a broken pipe. The outer wrap stays as the second door: it is what scrubs a tool's error, a self-bounding result the reducer exempts, and the `evidence` tool's own paged output.

### Sub-agents

`shhh code` can spawn child agents via `spawn_agent`. Children are `researcher` (read-only tools + web), `writer` (full toolset against an isolated git worktree) or `reviewer` (read-only, plan mode, handed a diff; `prompt.BuildReviewer`), or any custom profile the user wrote to `~/.config/shhh/agents/<name>.toml`. Hard limits: max 3 concurrent, 16 total per session — a limit on attention, not on resources ([`docs/capabilities/subagents.md`](docs/capabilities/subagents.md)). Children that can write or execute produce patches that the parent approves.

**A writer's worktree is not the last commit** ([`docs/capabilities/subagents.md#a-writer-starts-from-your-tree`](docs/capabilities/subagents.md#a-writer-starts-from-your-tree)). `addWorktree` (`internal/subagent/worktree.go`) seeds each fresh worktree with `git diff HEAD --binary` from the parent plus the untracked paths `Options.Untracked` names — `sessionUntracked` in `internal/cli/subagents.go`, reading the session changeset, because git cannot tell a file this session wrote from a scratch file the person left lying about — and then **commits it in the worktree**, which is the part that will bite you: HEAD in a child is a dangling seed commit, not the parent's HEAD, and that is exactly what makes `worktreePatch`'s `git diff --cached` return the child's own work rather than the parent's changes as well. `git apply` stays plain in both directions: `--3way` implies `--index` and refuses any file whose working copy differs from the index, which is every file the parent has edited and not staged — the case this whole mechanism exists for. A seed that will not apply fails the spawn; a writer that silently started from HEAD would write a patch against text nobody has. `Status.Seeded` carries the count to the lane note.

Profiles are three layers, and the split matters: `config.AgentDefinition` (`internal/config/agents.go`) is the file as written and validates what needs no runtime — names, tiers, tool names against tiers; `subagent.Profile` (`internal/subagent/profile.go`) is what the supervisor decides by — worktree or not, starting mode, default budgets — and knows nothing about tools or prompts; `internal/cli/subagents.go` translates a definition into a child's toolset and prompt (`profileEnv`) and checks the mode and reasoning names, because those vocabularies belong to `agent` and `provider`, which `config` must not import. The three built-in roles are `subagent.BuiltinProfiles()` and keep their hand-written prompts; a file named `researcher.toml`, `writer.toml` or `reviewer.toml` replaces one — so the `reviewer.toml` example in `docs/agents/` is now an override, and copying it changes the backlog runner's reviewer to what the file says. The file format and examples are in [`docs/agents/README.md`](docs/agents/README.md).

### The unattended contract

`shhh code -p` is a contract a script is written against ([`docs/capabilities/headless.md`](docs/capabilities/headless.md)), and its two halves are the exit code and the output shape.

**The exit code is a projection of the record's turn outcome, never a second closed set** ([`docs/capabilities/headless.md#the-exit-code-is-the-contract`](docs/capabilities/headless.md#the-exit-code-is-the-contract)). `headlessTurnOutcome` maps the loop's ending onto `observe.Turn*` — the same value `observeRecorder.turn` writes to the table — and `headlessExitCode` (`internal/cli/print.go`) reads the code off *that*: `cap-paused` → 2, `cancelled` → 3, `failed` → 4, and for a turn that finished the two second opinions, a failing on-close suite → 5 and a standing policy refusal → 6, in that order. 1 is deliberately outside the set and belongs to every command that could not run at all. The code leaves the tree in an `exitError`, which fang returns unwrapped, `cli.ExitCode` reads with `errors.As` and `cmd/shhh/main.go` exits with; anything else is a 1. **A signal is an interrupt for the turn, and only the first one is** ([`docs/capabilities/headless.md#what-a-signal-does-to-a-run`](docs/capabilities/headless.md#what-a-signal-does-to-a-run)). `interruptOnSignal` (`internal/cli/print.go`) is up only around `Headless.Run`: the first SIGINT or SIGTERM calls `Interrupt`, which the retry wait, the hold and the stream checkpoints already answer to, and the watcher stops its own channel before telling the loop anything — so the second signal reaches the default disposition and kills the process, and so does one that arrives once the loop has returned. Take the handler away and a signalled run dies with no record, no slot to continue from and a signal status where a script expects the code. The trap is that **exit 6 is the *last* verdict and not any verdict** — `lastVerdict` wraps the reporter the approver is handed, so every decision still reaches the record on its way past, and a denial the run went on from is not the ending.

**`--output text|json|jsonl` is the shape; `--json` is an alias for `json` and stays one.** `resolveOutput` settles the two spellings in `newCodeCmd` before a provider is resolved, and refuses a disagreement rather than picking a winner. `jsonl` is `jsonlStream` in `print.go`: one `jsonEvent` per line, kinds from `observe.Event*`, every code field a constant from `internal/observe` and never text the run composed. **Events leave from the observer and not from the hooks** — `headlessObserver` carries the stream beside the recorder and `signal`/`decision`/`toolResult`/`usage` write to both, which is what keeps an event from reaching the table and not the stream. The stream is nil for every other shape and each method is nil-safe. `usageOf` is the one reading of a run's totals, so the transcript and the stream state the same three figures, cached tokens included.

**One registration, not one per surface.** `buildToolset` (`internal/cli/toolset.go`) opens and registers the reducer, the web tools, the language server, the structural tools, the quality gate, the process supervisor, the report publisher and the vault, and `toolset.executor` builds the dispatch chain over them; `runChatSession` and `runPrintSession` both call it, and `registerSkills` is the last-mile pair. It stops short of MCP because the servers have not answered yet — the chain is built after `attachMCP`, which appends its own definitions. Each surface adds only what the other genuinely does not have: sub-agent roles, the memory tool and the notebook on the session, the repeat detector and the sub-agent supervisor outside the shared chain. `kind` is how the record names the surface and the origin a published report is filed under (`"print"` for a headless run), and `toolsetOpts.browser` is the one behavioural difference: a run with nobody at a desktop pops none.

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
- `internal/cli` builds the binary once in its `TestMain` (into a `bin` directory under the temp home, since the temp home itself is the config directory) and drives `shhh code -p` against a fake provider over `httptest`; the fake must speak the openai-compatible dialect the built binary is configured for. The suite stays cacheable, so `make ci` must not pass `-count=1`

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
- **Every setting is one row of `settings` in `internal/config/settings.go`; the words it may say are judged in `internal/cli`**: the table holds each key's kind, default, description, and the environment variable or flag that outranks the file, and `config.Set` parses a value by the Go type of the field the key names — so a key of a type already in the table is one row and no new parse. `config.Value` reads one back as text, which is what the config screen, `shhh config list` and `shhh config get` all draw from, and `config.Reference` prints the table into the generated region of [`docs/capabilities/configuration.md#every-setting`](docs/capabilities/configuration.md#every-setting) (`make docs` writes it; `make docs-check` and the suite fail when it is stale). A field with no row and a row with no field both fail a test. But a value that is one of a few *names* (a permission mode, a reasoning level, a containment profile or engine or isolation level) is checked by `checkConfigValue` in `internal/cli/config.go`, because those vocabularies belong to `agent`, `provider` and `sandbox`, and `config` imports none of them — the same split `profileFromDefinition` makes for a profile file. A word key whose vocabulary no other package owns is judged against the table's own `Values`; a key that carries `Values` and is not judged fails a test. Every writer in `internal/cli` goes through `writeConfigEdits`, so a new one gets the judge for free — and the note it answers with, which is what the checkout's own file has to say about a key just written to the user's. See [`docs/capabilities/configuration.md#a-value-is-refused-before-it-is-written`](docs/capabilities/configuration.md#a-value-is-refused-before-it-is-written).
- **Both config files are read in one place, `loadLayeredConfig` (`internal/cli/config.go`), and the result rides on the command's context**: `ConfigFrom` is the merged settings and `ProjectConfigFrom` is what the checkout's `.shhh/config.toml` contributed — the file and the keys it set, which is what every surface that says `project` in a source column reads. `config.LayerProject` (`internal/config/project.go`) does the merge by the keys the file wrote rather than by which of them are non-zero, so a checkout can turn something off; `config.RefusedInProject` is the set a checkout may not decide and is asked by the load and by `config set --project` alike. Don't call `config.Load` from a command — it is the user's file alone, and a surface built on it disagrees with the session beside it. The checkout's file is a trust resource like its skills and its suites (`internal/project/trust.go`), so it loads only where the checkout is trusted. See [`docs/capabilities/configuration.md#two-files-one-resolution-order`](docs/capabilities/configuration.md#two-files-one-resolution-order).
- **The deny mask is not configurable**: The sandbox's built-in deny mask (credential stores, shhh's own state) cannot be disabled. Only `deny_extra` can add to it.
- **The namespaces and the environment are unconditional, and the environment is an allowlist**: `envAllowlist` in `internal/sandbox/sandbox.go` is every variable name that crosses into containment, plus `Policy.SecretNames` — the vault's own list, asked for rather than guessed at, which is why nothing here reads the shape of a name. Both halves are filled in one place, `sandboxPolicy` (`internal/cli/sandbox.go`), from `runner.Environ` and `runner.SessionEnvNames`, because every command path resolves its policy through it: a sub-agent's command, the quality gate's check and a process start would each otherwise decide separately what a contained command may carry. `containedEnv` applies the allowlist to `Policy.Env` (the session's pairs; empty falls back to this process's environment) and each mechanism rebuilds from the result: bubblewrap with `--clearenv` and a `--setenv` per pair beside `--unshare-pid --unshare-ipc --unshare-uts`, Seatbelt with `env -i`, since a Seatbelt policy says what a process may reach and nothing about what it is told. Both also mask `SSH_AUTH_SOCK`'s socket, after the write grants, because dropping the address does not stop a command that guesses the path — and an agent signs for anything that reaches it, so the deny mask over the private key is worth nothing without this. `agentSocketPath` answers `""` for a path nothing is listening on, and it has to: a mount needs somewhere to land, bubblewrap cannot make one on the read-only root, and an address left over from a dead agent would otherwise fail every wrap in the session (`--ro-bind-try` does not help — it forgives a missing *source*, and the source is `/dev/null`). Don't turn the allowlist into a mask of credential-shaped names: the leak it closed came from variables shhh had never heard of. See [`docs/capabilities/containment.md#a-contained-command-carries-almost-no-environment`](docs/capabilities/containment.md#a-contained-command-carries-almost-no-environment).
- **A required session refuses before the card, not at the runner**: `sandbox.require` (and `--require-sandbox`, which can only turn it on) makes `buildContainment` fill `chat.Containment.Refusal` with the doctor's own wording — `doctorSandbox` in `internal/cli/doctor.go`, so the instruction for installing a mechanism has one spelling. `advanceApprovalQueue` answers it in front of everything the queue does, the headless approver in front of policy, and `childCommandRunnerUnbounded` (`internal/cli/subagents.go`) in place of the plain runner it would otherwise fall back to — a child is the one path with no card to draw and nobody to draw it for, so a requirement that stopped at the session is one a fan-out walks around. The model reads the refusal as the call's result and no card is drawn for a decision that has no sides. The flag is folded into the config in `buildSessionEnv` rather than beside the containment build, because those three read it from three different builders. The knob only adds the refusal; the environment allowlist above is in force either way. See [`docs/capabilities/containment.md#containment-can-be-required`](docs/capabilities/containment.md#containment-can-be-required).
- **There are two command paths and both take the wrap**: `execute_command` runs through `chat.Containment.Run`, and a `process` start runs through `process.Supervisor`, which `buildContainment` (`internal/cli/sandbox.go`) hands the same `sandbox.Availability` and the same policy via `SetContainment` — resolved per start, so a directory added to the working scope mid-session is writable to a process too, and with the start's own directory as the policy's cwd, because the mechanism chdirs into it. A start the wrap refuses is an error result naming the mechanism, never a bare process; a `--sandbox` run refuses one outright, since a process cannot follow the commands into the container. The approval card asks `Processes.Contained` — the supervisor — rather than the runner for the mechanism it names, which is the difference between reporting this path and reporting the one beside it. A third way to spawn would need the wrap as well. See [`docs/capabilities/containment.md#a-started-process-is-contained-too`](docs/capabilities/containment.md#a-started-process-is-contained-too).
- **The command ceiling is decided where the command is held, not where the limit is set**: the deadline is put on the context by `boundedRunner` (`internal/cli/timeout.go`) for the surfaces with no reader, and by `executeRun` (`internal/ui/chat/run.go`) for an assistant command in a session; what happens when it arrives belongs to `capture` in `internal/runner/capture.go`, the one funnel every captured form goes through. It builds the child on a context with the deadline stripped — `os/exec` would otherwise kill it before there was anything to decide — watches the caller's context itself, and tells a deadline from a cancellation by `ctx.Err()`. A command that has printed something is offered to `runner.SetAdopter`, installed by `openProcessSupervisor` (`internal/cli/process.go`) and taken by `process.Supervisor.Adopt`, which registers it under a generated name and hands back the writer the rest of its output goes to; a silent one is stopped. Both endings are said in words in the output, in that one place, because an exit code cannot tell either from a command that broke — don't put a second notice back at a caller. **The adopter is package state on purpose**, like the session environment and the env mask beside it: a session runs commands through half a dozen paths and a ceiling that backgrounded on some and killed on the others would be a dev server that lives or dies by whether containment was available. A handed-over command reports exit code 0, because it did not fail and a code that says it did sends the model debugging. A `--sandbox` run withdraws the adopter (`internal/cli/print.go`) beside the start refusal and for the same reason: the local process there is the exec client, not the thing running in the container. `process.reap` takes a `func() error` rather than an `*exec.Cmd` since the run's wait is already in flight and `os/exec` allows only one. See [`docs/capabilities/containment.md#a-command-that-will-not-finish-is-not-waited-on-forever`](docs/capabilities/containment.md#a-command-that-will-not-finish-is-not-waited-on-forever).
- **A process with a terminal takes a different spawn**: `pty:true` on a start goes through `startPTY` (`internal/process/pty_unix.go`, refused in a sentence by `pty_windows.go` so the Windows build still links), which opens the terminal, wires the command's three streams to it and starts it — so that path must not call `cmd.Start` again. `sysProcAttr(tty bool)` drops `Setpgid` for it: opening a terminal makes the process a session leader, and `setpgid` on a session leader is refused by the kernel. It is still in a group of its own, so the signal path is unchanged. The master is both the process's input and the whole of its output, which is why `stderr` stays empty, and `proc.drained` is what keeps the reaper's close of the master from cutting off output the terminal still held.
- **The quit chord is answered once, above the surfaces**: every takeover surface leaves the session on it rather than leaving the surface, so `surfaceKey` (`internal/ui/chat/cancel.go`) answers it in front of whichever handler the key ladder picked, and `quitNow` is the one place that cancels what was still running. Don't put the chord back at the top of a surface's own key handler — the copies that were there each cancelled a different half of the live work. Two branches of the ladder do not route through it: the quit confirm, because that surface is the question the chord asks, and the context screen, which has never answered it.
- **Bubble Tea message routing**: shhh's own messages are typed structs (not interfaces with methods). When adding new async operations, add a corresponding `type fooMsg struct{}` and handle it in the `Update` switch. Note that some of Bubble Tea v2's *own* messages are interfaces — `tea.KeyMsg` covers presses and releases, `tea.MouseMsg` covers click/motion/release/wheel — so match `tea.KeyPressMsg` and the specific mouse types rather than the interface.
- **`View()` returns a `tea.View`, not a string**: the screen's content plus the terminal states the surface asks for (`AltScreen`, `MouseMode`, and the window's own `WindowTitle` and `ProgressBar`, which `internal/ui/chat/terminal.go` derives beside the suspend guard and the redraw key). Tests that want the painted screen read `.View().Content`. Programs start through `internal/cli/program.go` so they all get the colour profile `components.Profile()` resolved the palette against.
- **The chat surface's geometry lives in `internal/ui/chat/layout.go`** ([`docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it`](docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it)): `columns()` and `surface()` split the terminal into rectangles with `ultraviolet/layout`, and `contentWidth`, `paneWidth`, `transcriptWidth` and `viewportHeight` read them. Don't add a new `width - something` in a renderer — add a rectangle to the split and read it. Blocks are placed with `drawIn`, which clips to the rectangle, so a renderer never has to measure what it is about to overflow. One paint resolves that geometry once: `paint` puts a `frame` on its own copy of the model, and `columns`, `surface`, `panel`, `liveTail`, `framePreRails`, `interruptLines` and `frameLayout` all read it rather than resolving again. The bottom panel is the reason — its rows are what the vertical split takes off the transcript, so the split has to render it to learn them, and the draw then rendered it a second time to paint it. Add a new block whose own size decides the split and give it a slot on the frame; `testHookRenderPanel` holds the property that a frame renders the panel once. The frame also memoises two things a paint asks for more than once: the step tiling of a run of transcript entries, keyed on the run itself, which reading mode's paint wanted eight times over the whole session; and the inspector rail, keyed on the spinner's frame, the transcript's length and the turn count. Both are per-paint — `paint` runs on its own copy of the model, so nothing written during one survives it — and outside a paint every caller resolves its own answer as before. A block fitted to the panel's rows goes through `padPanel` and not its own pad loop. The inspector rail's own column count is `railWidth` there and nowhere else: `components.InspectorWidthFor` is the ladder (`InspectorWidth`, `InspectorMaxWidth`, `InspectorMinContentWidth` in `internal/ui/components/inspector.go`), `Model.railCols` is what `appearance.rail_width` and `/ui rail` set, and the rail's blocks size their meter and sparkline runs off the width they are handed (`railCells` in `meters.go`) rather than off the constant.
- **Colours are resolved when styles are built, not when they are drawn**: a `lipgloss.Style` holds one `color.Color`, so a `components.Token` picks its truecolor/256/16 rung through `Token.Color()` at `newStyles` time. Changing the palette *or* the profile means rebuilding every derived style — both go through `applyPalette`.
- **Golden file deletion**: `golden.Run(m)` removes any `.txt` file in `testdata/golden/` that wasn't asserted during the run. Don't manually create golden files; let the test framework generate them.
- **The investigation rules in `BuildAgent` are load-bearing**: the "Finding things" section — batch independent calls, make one search answer the question, never repeat a call you already made — is there because a real session spent all 150 rounds re-running the same searches. It reads like padding and is not; see the comment on `BuildAgent` and [`docs/capabilities/coding-agent.md#finding-things`](docs/capabilities/coding-agent.md#finding-things).
- **Never name a tool in a base system prompt**: the optional toolset is assembled from what the machine turned out to have (a language server was detected, a binary is on PATH, a key is configured), so a prompt that names one promises a tool the session may not have. `prompt.Toolbox` describes the tools actually registered and is appended as prompt extra after the last one joins.
- **Gemini pairs tool results by function *name*, not by id**: `FunctionResponse.Name` must be the name of the function called, and the Gemini API sends no `functionCall.id` at all — the ids in `provider.ToolCall` are ours. Don't "simplify" `toGeminiContents` back to putting `ToolCallID` in that field; it addresses every result to a function the model never called, and the model just calls again. Gemini 3 thought signatures ride the same parts and must go back on the part they arrived on.
- **The output ceiling is `max_completion_tokens` everywhere but one branch**: chat completions deprecated `max_tokens`, and a reasoning model answers a request naming it with a 400, so `openai` and `openrouter` send the new field for every model. The `openai-compatible` provider is the exception: it points at whatever the user is running — the default is a local Ollama — and those runtimes do not agree on whether they know the new field, so it sends the new one only for a model something describes as reasoning and the old one otherwise. That branch is judged by the same `CapabilitiesFor` answer that decides whether `reasoning_effort` goes out at all; don't collapse it to one field, and don't give the two decisions separate judges.
- **Cache breakpoints are decided in one place and applied in two**: `internal/provider/cache.go` chooses the positions — the head, then the last two messages — and both the Messages API request and the gateway's chat-completions body take them from there. The gateway half is annotated on the encoded body, in the transport in `openrouter.go`, because the OpenAI Go client's content part is a closed struct with nowhere to put `cache_control`; it fires only for a model id the gateway routes to that API, and every other id is sent the bytes it was already sent. Don't add a third copy of the position rule for a new dialect, and don't put a marker on the tools as well as the system prompt — the API hashes the tools in front of it, so the second marker would cache a prefix the first already covers. See [`docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once`](docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once).
- **Tool-round cap is a checkpoint, not a limit**: The default 150-round cap pauses for user input rather than terminating. Sub-agents default to uncapped (`UnlimitedToolRounds = -1`) because they have no one to ask.
- **Three directories, one layout, no per-platform branch**: config (`config.Paths`), data (`storage.Dir`) and cache (`pricing.CacheDir`) each follow XDG on every platform including macOS. Don't reintroduce a `runtime.GOOS == "darwin"` branch in any of them — the retired `~/Library` layout is a migration in `internal/migrate`, not a fallback, and the reasons are in [`docs/capabilities/configuration.md#one-layout-everywhere`](docs/capabilities/configuration.md#one-layout-everywhere).
- **A migration is a `shhh doctor` check, never a startup step and never a command of its own**: add a detector to `migrate.detectors` returning a `Pending`; leave `Pending.Apply` nil when the change needs a person's judgement, and the doctor row will report it without offering a key. See [`docs/capabilities/configuration.md#a-migration-is-a-doctor-check`](docs/capabilities/configuration.md#a-migration-is-a-doctor-check).
- **Storage is single-connection SQLite**: `SetMaxOpenConns(1)` is intentional. The WAL journal mode and busy timeout handle concurrency; don't open multiple `*DB` instances to the same file from one command. In `internal/cli` open the store through `openStore()` (`store.go`), never `storage.Open()` directly: the history purge rides the first connection a command opens, once per process, so no command opens a second one for it. `migrate` still applies every step under `BEGIN IMMEDIATE` with the version re-read inside the lock, so two processes opening a store that is behind — two shells starting `shhh` after an upgrade — cannot apply the same `ALTER TABLE` twice.
- **Version injection**: The `version` var in `internal/cli` is set via `-ldflags` at build time. It defaults to `"dev"` when built without flags.
- **Releases**: Handled by GoReleaser v2 triggered on `v*` tags via GitHub Actions.
