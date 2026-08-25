# shhh — TUI Component Catalog

> Visual components for the coding-agent TUI (`shhh chat` / `shhh code`).
> Companion to DESIGN.md. Implemented per backlog story S-076; consumed by
> S-048 (approvals), S-061 (plan mode), S-070 (memory), S-074 (diffs),
> S-075 (activity feed & cockpit), S-082 (input frame).
>
> **v2 — S-088.** §6, §8 and §10 are rewritten and §13–§17 added, so the file
> describes one grammar rather than a record of how it grew. The source is the
> `shhh Design System` project in Claude Design (projectId
> `8bd9b60d-8d86-403e-a591-c15a9ebccfd9`, readable with the DesignSync tool):
> `tokens/terminal.css` for the column grid, `tokens/colors.css` for the
> palette, `guidelines/` for the rules, `ui_kits/cockpit/` for the artboards
> (`Main`, `Steps`, `Changeset`, `Edges`, `Sheet`). E-013 through E-018
> implement it; nothing below exists until a story builds it.

---

## Invariants

Four rules, checked before anything else. A surface that breaks one of them is
wrong even when it looks right.

1. **Colour never carries meaning alone.** Every state pairs its colour with a
   glyph or a word, so a monochrome terminal (or `NO_COLOR`) loses decoration,
   never information.
2. **Weight tracks risk.** A read is chrome, a mutation carries a rail (§14),
   a decision gets a card (§2). Never the reverse — a card for a read is as
   wrong as a bare row for `rm -rf`.
3. **Esc is always the safe answer**, and a surface says so whenever the safe
   answer is not obvious (`[esc] leave review, change nothing`).
4. **Fold, never hide.** A collapsed group still counts what it swallowed
   (`▸ ⚙ 6 reads · 2 searches`). Nothing is dropped to save space.

---

## 1. Principles

The mechanics that follow from the invariants:

- **One interaction panel.** Components that need keys render in the bottom
  panel, replacing the input textarea while active — exactly how
  `stateConfirmRun` works today. The transcript viewport stays visible above.
  The panel may grow to at most 40% of terminal height; the viewport shrinks
  to make room and restores on dismissal.
- **Transcript entries are passive.** Anything rendered into history (diff
  blocks, activity rows, step headers) is stored raw and re-rendered on
  resize, following the existing `entry`/`renderHistory` cache design. Only
  the *selected* row responds to keys, via focus mode (§7).
- **One grid.** Every transcript row lands on the column grid in §6a. A
  surface that needs a field the grid does not have is a card, not a row.
- **Reuse the palette** (§10). No new colors without adding them there.

---

## 2. Approval Card

The single surface for every approval-gated action (S-048). One container,
three body variants: command, edit, generic tool.

Every card answers the same three questions before it offers a key — what the
action touches, whether shhh can take it back, and whether the network is open
(S-101). A prompt that only says what the action *is* asks the reader to do
the risk assessment themselves, at speed, twenty times a session.

### 2a. Command approval

```
┌─ Approve command (2 of 5) ─────────────── ⛨ bwrap · workspace ─ ⚠ HIGH ─┐
│ Assistant wants to run: rm -rf ./build && npm run build                 │
│ ⚠ HIGH  deletes files recursively (rm -rf)                              │
│                                                                         │
│ touches   ./build — 412 files, 84.0 MB; shhh cannot tell what npm writes│
│ undo      none — nothing it writes is tracked in git                    │
│ network   open — the workspace profile allows network access            │
├─────────────────────────────────────────────────────────────────────────┤
│ Run this command? [y/N] · esc — the safe answer                         │
│ [a] always — not offered: a safety-flagged command is never pre-approved│
└─────────────────────────────────────────────────────────────────────────┘
```

- Title bar states the action kind. Command shown verbatim, wrapped, never
  truncated silently (long commands scroll within the card).
- **Severity leads**, as a word — `⚠ HIGH` (9), `⚠ medium` (214), `⚠ low`
  (241) — beside the first `safety.Check` risk. The border colour tracks it
  and the title rail repeats it: three sayings of one thing, which is what
  makes the card survive mono and a colour-blind reader alike (invariant 1).
  A card carries `HIGH` when the safety checker flagged it, `medium` when it
  resolved to a write or could not be resolved at all, `low` when it resolved
  and writes nothing.
- **The blast-radius block** is `touches` / `undo` / `network` in a 10-column
  label gutter. `touches` names the paths and describes them from the
  filesystem (file count and size for a directory, size for a file, "does not
  exist yet" for one the command creates). `undo` is what could be done
  afterwards: `git` when every resolved path is tracked, `partial`, `none`, or
  `n/a` when nothing in the workspace changes. `network` is what the
  containment profile allows — the thing actually in force, not what the
  command appears to want.
- **Resolution is honest about its limits.** `internal/radius` knows a closed
  set of verbs whose operands are files, plus shell redirection; anything else
  — `npm run build`, a pipe into `sh`, a path the shell expands — is reported
  as `unknown` with the reason, never as `nothing`. A partially resolved
  command states both halves.
- **Containment folds into the title rail** as `⛨ mechanism · profile`
  (S-062). It is the first chip dropped as the terminal narrows; the severity
  chip is what the decision turns on and it is the one that survives.
- `[a]` is absent for flagged actions — they can never be blanket-approved
  (S-059) — and the card says so in a footnote. A missing key with a stated
  reason teaches; a missing key without one reads as a bug.
- The keys sit below a `├───┤` rule so they never blend into the body, and
  where the safe answer is not obvious from `[y/N]` the card names it in
  words (`esc — the safe answer`).

### 2b. Uncontained

When no containment mechanism is available, `⚠ UNCONTAINED` is promoted into
the title rail ahead of the severity, the border goes red, and the body
explains what is missing:

```
┌─ Approve command ─────────────────────────── ⚠ UNCONTAINED ─ ⚠ medium ─┐
│ Assistant wants to run: curl -fsSL https://get.pnpm.io/install.sh | sh │
│ ⚠ medium                                                               │
│                                                                        │
│ touches   unknown — piped into sh; what it runs is not inspected first │
│ undo      unknown — shhh could not resolve what this writes            │
│ network   open — nothing contains this command, so nothing limits it   │
│ ⛨         no sandbox — bwrap not found on PATH; it runs as you         │
├────────────────────────────────────────────────────────────────────────┤
│ Run this command? [y/N] · esc — the safe answer                        │
│ containment is off for this session · /sandbox doctor explains why     │
└────────────────────────────────────────────────────────────────────────┘
```

### 2c. Edit approval (embeds Diff Viewer, §3)

```
┌─ Approve edit ────────────────────────────────────────────── ⚠ medium ─┐
│ Assistant wants to edit: internal/agent/loop.go                        │
│ ⚠ medium                                                               │
│ @@ -138,6 +138,7 @@                                                    │
│   138      for round := 0; ; round++ {                                 │
│ - 140              return results, nil                                 │
│ + 140              return results, ErrRoundLimit                       │
│ + 141          }                                                       │
│ +2 −1 · 1 hunk · undo yes — recorded, and git has this file            │
├────────────────────────────────────────────────────────────────────────┤
│ Apply this edit? [y/N]  (d: full diff)                                 │
└────────────────────────────────────────────────────────────────────────┘
```

- Shows the first hunk(s) that fit; `+N −M · H hunks` summary line always
  present. `[d]` opens the full-screen diff view (§3c) and returns here.
- An edit needs no `touches` row: the diff below **is** the blast radius, in
  full, and says more than a path and a byte count would. The one fact left
  to add is reversibility, and it rides the stats line rather than costing a
  row the diff would otherwise have had. An edit is the one action shhh can
  genuinely take back — the changeset store records the file on both sides of
  the call (S-097), so undo restores it whether or not git ever knew about it.
- `write_file` on a new file renders as an all-additions diff titled
  `Approve new file · path`.

### 2d. Generic tool

A tool that is neither a command nor an edit carries the same block, with the
fields that fit it — the tool supplies them, because shhh cannot resolve an
outbound request the way it resolves a shell command's paths:

```
┌─ Approve tool ────────────────────────────────────────────────── ⚠ low ─┐
│ Assistant wants to use: web_fetch                                       │
│ ⚠ low                                                                   │
│ GET https://pkg.go.dev/context#WithCancel                               │
│                                                                         │
│ domain    pkg.go.dev — the request leaves this machine                  │
│ sends     the URL and a shhh-web/1.0 user-agent — no file contents      │
│ receives  page text into the conversation, bounded to 2 MB              │
├─────────────────────────────────────────────────────────────────────────┤
│ Allow this? [y/N]                                                       │
└─────────────────────────────────────────────────────────────────────────┘
```

A generic approval that carries a command — a process start (S-073) — is
resolved as the command it is, and gets the command card's block.

### 2e. Queue position

When multiple calls await approval, the title carries `(2 of 5)`; `[n]`
denies just the current one, consistent with today's queue semantics.

---

## 3. Diff Viewer

### 3a. Collapsed transcript row

Applied edits land in history as one row (activity-row grammar, §6):

```
  ▎✎ edit    internal/ui/chat/model.go   +12 −4 · 2 hunks · [enter] expand
```

### 3b. Expanded unified view (in transcript, bounded height)

```
  ▎✎ edit    internal/ui/chat/model.go              +12 −4 · 2 hunks  1.1s
    @@ -358,6 +358,10 @@ case toolCallsMsg:
      358   m.accumulateUsage(msg.usage)
    +  359   if m.rounds >= m.maxRounds {
    +  360       return m.stopAtRoundLimit()
    +  361   }
      362   m.messages = append(m.messages, provider.Message{
    @@ -401,3 +405,5 @@ case toolResultsMsg:
    …8 more lines · [enter] full view · [enter again] collapse
```

- Syntax highlighting via the existing `highlight.go` (chroma), then diff
  coloring layered over it: additions green (10), deletions red (9), hunk
  headers cyan (14), line numbers gray (241).
- Intraline emphasis: within a changed line pair, the changed span gets a
  background tint (22 for adds, 52 for dels) rather than a different
  foreground, so syntax colors survive.

### 3c. Full-screen view (large diffs, `/diff` session diff)

Takes over the viewport; status bar shows `diff · j/k scroll · n/p hunk ·
s side-by-side · esc back`. Side-by-side activates on `s` or automatically
at ≥ 120 columns:

```
 internal/agent/loop.go                                   +12 −4 · 2 hunks
 ─────────────────────────────────┬─────────────────────────────────────
  142  if len(calls) == 0 {       │  142  if len(calls) == 0 {
  143      return results, nil    │  143      if a.rounds >= a.max… {
                                  │  144          return results, Err…
                                  │  145      }
  144  }                          │  146  }
```

Truncated cells end with `…`; the pane divider is gray (241).

---

## 4. Selectors

### 4a. Single-select

Used by: plan approval (S-061), the `/run` block picker (S-081), `/mode`
and `/model` menus, the session pickers (`/load`, `/chats`, `/branches` —
S-080), model-asked structured questions.

```
┌─ Plan ready — how should I proceed? ─────────────────────────────┐
│ ❯ 1. Execute plan (accept edits)                                 │
│   2. Execute plan (manual approvals)                             │
│   3. Keep planning — tell me what to change                      │
│   4. Reject plan                                                 │
│                                                                  │
│ ↑↓/jk move · enter select · 1–4 jump · esc cancel                │
└──────────────────────────────────────────────────────────────────┘
```

- Focused row: `❯` pointer, bold, selection background (62) — same
  visual as the existing action bar's `ActiveStyle`.
- Optional per-option description line, gray (241), shown under the
  focused option only (keeps the card short).
- Number keys select immediately. Lists longer than the panel scroll,
  with `…` markers.

### 4b. Multi-select

Used by: staging/patch review (S-068 writer patches), `/memory forget`,
choosing quality-gate checks (S-067).

```
┌─ Apply which changes? ───────────────────────────────────────────┐
│ ❯ [x] internal/agent/loop.go            +34 −6                   │
│   [x] internal/agent/loop_test.go       +88 −0                   │
│   [ ] internal/ui/chat/model.go         +12 −4                   │
│   [x] go.mod                            +1 −0                    │
│                                                                  │
│ space toggle · a all/none · enter apply (3) · esc cancel         │
└──────────────────────────────────────────────────────────────────┘
```

- `[x]` green (10), `[ ]` gray (241). The confirm hint shows the live
  count. `a` toggles all ↔ none.
- Zero-selected + enter = no-op with a one-line notice, not a confirm.

### 4c. Select with optional note

Single- or multi-select plus a free-text note field. Used by: plan
feedback ("keep planning" + what to change), memory confirmation (S-070)
with a correction, `/mode feedback`.

```
┌─ Remember this? ─────────────────────────────────────────────────┐
│ "User prefers table-driven tests for tool packages"              │
│                                                                  │
│ ❯ 1. Save (project)                                              │
│   2. Save (global)                                               │
│   3. Don't save                                                  │
│                                                                  │
│ ┌ note (optional) ─────────────────────────────────────────────┐ │
│ │ only for Go code, not shell scripts▌                         │ │
│ └──────────────────────────────────────────────────────────────┘ │
│ tab note/options · enter confirm · esc cancel                    │
└──────────────────────────────────────────────────────────────────┘
```

- Tab moves focus between the option list and the note; the focused
  region shows the pointer / cursor, the other dims.
- Enter confirms selection + note together. Note is a single-line
  textarea (alt+enter for a rare newline), reusing the existing
  `textarea.Model`.
- An option may declare "note required" (border turns red (9) with a
  `note required` hint if confirmed empty).

---

## 5. Inline Confirm (one-liner)

For yes/no moments that don't warrant a card (e.g. `/clear` with unsaved
work, purge prompts). Renders in the input area, current
`renderRunConfirm` style:

```
Discard 14 unsaved turns and start a new conversation?  [y/N]
```

---

## 6. Activity Rows (the column grid)

Every line of activity — tool call, command, sub-agent, folded group, recovery
row (§17) — is the same row. Fixing the fields is what lets a reader scan one
column instead of parsing sentences.

### 6a. The grid (normative)

Widths are character cells and match `tokens/terminal.css` in the design-system
project field for field (`.ptr .rail .gl .verb .dur .ln .ind1 .ind2 .ind3`).
Nothing in the transcript may invent a width.

| Field | Width | Content |
|---|---|---|
| pointer | 2 | fold state `▾`/`▸`, focus cursor `❯`; blank otherwise |
| mutation rail | 1 | `▎` when the row changed the machine (§14); blank otherwise |
| glyph | 2 | the kind of act (6b), or the state that overrides it (6d) |
| verb | 8 | closed vocabulary (6c), left-aligned, space-padded |
| target | grows | path, command, query, agent name — clips, never wraps |
| outcome | right-aligned | closed vocabulary and counts (6d); never wraps |
| duration | 6 | right-aligned; omitted under 0.5s, `—` when it never ran |
| line numbers | 5 | right-aligned, dim — diff and detail bodies only |
| detail indent | 2 / 4 / 6 | row body, detail body, nested detail |

```
❯ ▎✎ edit    internal/agent/loop.go                 +12 −4 · 2 hunks  1.1s
┬ ┬┬ ┬       ┬                                      ┬               ┬
│ ││ │       │                                      │               ╰ duration · 6ch, right-aligned
│ ││ │       │                                      ╰ outcome · right-aligned, never wraps, never clipped
│ ││ │       ╰ target · grows to fill the row; clips with … , never wraps
│ ││ ╰ verb · 8ch, from a closed vocabulary
│ │╰ glyph · 2ch, the kind of act
│ ╰ mutation rail · 1ch, present only when this row changed the machine (§14)
╰ pointer · 2ch, fold state (▾ ▸) and focus (❯)
```

Three rules follow from fixed widths:

- **The target is the only field that grows.** When the row is too narrow for
  target and outcome together, the target clips with `…`
  (`internal/ui/chat/mod…go`); the outcome never clips, because the outcome is
  the reason to read the row.
- **Duration is a field, not a suffix.** Under 0.5s it renders blank rather
  than `0.0s` — a column of zeroes is noise. A call that never ran renders `—`.
- **Detail bodies indent, they do not re-grid.** Tool output, live tails and
  offered keys sit at indent 2/4/6 in dimmer (245) and carry no fields.

### 6b. Kinds

The glyph says what kind of act the row is; the verb says which one.

```
   ⚙ read    internal/ui/chat/model.go                     412 lines  0.4s
   ⚙ search  ErrRoundLimit ./internal               6 hits · 4 files  0.3s
   ⚙ glob    internal/**/*_test.go                          24 files  0.1s
   ⚙ lsp     refs Agent.runRound                    9 refs · 3 files  0.6s
   ⚙ web     pkg.go.dev/context#WithCancel             4.1kB fetched  1.2s
  ▎✎ edit    internal/agent/loop.go                 +12 −4 · 2 hunks  1.1s
  ▎✎ write   internal/agent/errors.go            new file · 34 lines  0.3s
  ▎✎ patch   sd ErrRoundLimit → RoundsExhausted      4 files · +9 −9  0.8s
  ▎$ run     go build ./cmd/shhh                              exit 0  4.8s
  ▎✎ memory  agent tests live in ./internal/agent    saved · project  0.1s
   ◇ spawn   writer-1 · document the sentinel                started  0.2s
   ◇ agent   writer-1 · document the sentinel            ▸ 2/5 steps   12s
```

| Glyph | Kind | Colour |
|---|---|---|
| `⚙` | read-only tool | accent (214) |
| `✎` | edit, write, patch, memory — anything that persists | accent (214) |
| `$` | shell command | accent (214) |
| `◇` | sub-agent (spawned, mirrored, reporting) | info (12) |

Five different reads share one glyph on purpose: the verb, not the colour,
says which read it was, and `⚙` never mutates.

### 6c. Verbs (closed vocabulary)

Thirteen verbs. A tool that maps onto none of them is a bug in this table, not
a fourteenth verb.

| Verb | Tools |
|---|---|
| `read` | `read_file`, `list_directory`, `evidence` |
| `search` | `search`, `ast_grep`, `jaq`, `tokei` |
| `glob` | `glob`, `fd` |
| `lsp` | `definition`, `references` |
| `web` | `web_fetch`, `web_search` |
| `edit` | `edit_file` |
| `write` | `write_file` |
| `patch` | `sd` — structural find-and-replace across many files |
| `run` | `execute_command`, `process`, `quality_gate` |
| `memory` | `remember` |
| `spawn` | `spawn_agent` — one child |
| `fan-out` | `spawn_agent` — a batch spawned in one round |
| `agent` | a child's mirrored row in the parent transcript (S-077), `agent_report` |

This supersedes the ad-hoc `activityVerbs` map: `list_directory` becomes
`read`, `web_fetch`/`web_search` both become `web` (the target says which),
and `quality_gate` becomes `run`. An unmapped tool name renders as itself,
clipped to 8 columns, and is a signal that this table is stale.

Recovery rows (§17) use `model`, `stream` and `rounds` in the same column.
They are not tool calls, which is why they are named separately here rather
than smuggled into the tool vocabulary.

### 6d. States and outcomes (closed vocabulary)

The state glyph overrides the kind glyph; the outcome names it in words, so
neither depends on the other.

```
   · read    internal/agent/round.go                          queued     —
   ▸ run     go test ./internal/agent/...                   running…    3s
    ok    shhh/internal/agent/session   0.092s
   ✓ read    internal/agent/loop.go                        218 lines  0.4s
   ✓ read    internal/ui/chat/model.go      auto-allowed · read-only  0.2s
  ▎✎ edit    internal/agent/loop.go          +12 −4 · approved · you  1.1s
  ▎✗ run     go test ./internal/agent/...         exit 1 · 1 failing 21.4s
    --- FAIL: TestRoundLimit (0.03s)
      loop_test.go:142: got ErrRoundLimit, want ErrRoundsExhausted
    [enter] expand · [r] rerun · [f] open loop_test.go:142
  ▎⊘ edit    go.mod              denied · you · [w] why · [e] revise     —
  ▎⊘ run     rm -rf ./dist                 denied · auto · /mode why     —
   ✦ run     gofmt -w internal/agent/loop.go                checking  0.4s
```

| Outcome | Glyph | Colour | Meaning |
|---|---|---|---|
| `queued` | `·` | dim (241) | accepted, not started; duration `—` |
| `running…` | `▸` (spinner while animating) | spin (205) | in flight |
| `checking` | `✦` | spin (205) | the auto-mode classifier is deciding |
| `ok`, `exit 0` | `✓` | add (10) | finished with nothing to report |
| `exit N`, `N failing` | `✗` | del (9) | the call failed |
| `approved · you` | kind glyph | add (10) | you allowed it at the card |
| `auto-allowed · <why>` | kind glyph | dim (241) | policy allowed it without asking |
| `denied · you` | `⊘` | dim (241) | your preference — offers `[w] why · [e] revise` |
| `denied · auto` | `⊘` | del (9) | a rule — names the rule, offers `/mode why` |
| counts | kind glyph | dimmer (245) | see below |

Two denials, two colours, two words: `⊘ denied · you` is a preference and
`⊘ denied · auto` is a rule. Both keep the `⊘` glyph, so "you said no" is
never confused with `✗` "it failed".

Counts are the outcome when there is nothing else to say, and each verb has
one shape: `218 lines`, `6 hits · 4 files`, `24 files`, `9 refs · 3 files`,
`4.1kB fetched`, `+12 −4 · 2 hunks`, `new file · 34 lines`,
`4 files · +9 −9`, `saved · project`, `▸ 2/5 steps`.

- Failed rows auto-expand to their bounded detail, error lines first
  (evidence-store view, S-064), and offer their keys underneath. Successful
  rows never auto-expand.
- A running command shows a **live tail** — its last output line at indent 2,
  dimmer (245) — replaced by the outcome on exit.
- Rows group under steps (§13) and fold by verbosity (§13c).

---

## 7. Focus Mode (expanding history)

`ctrl+e` (or click) enters focus mode: the viewport gets a selection
cursor on expandable rows (`❯` in the pointer column, §6a), `j/k` moves
between them, `enter` expands/collapses in place, `esc` returns to the input.
This is the one mechanism behind "[enter] expand" everywhere in the
transcript, so the input textarea keeps all other keys.

Step headers (§13) are selection targets too: `j/k` steps between headers and
rows alike, and `enter` on a header folds or unfolds the whole group. Focus
mode is therefore also how the outline is navigated — no second key set.

---

## 8. Vitals (session state)

The vitals are the session's standing answer to *what mode am I in, how much
context is left, what has this cost*. They are one vocabulary with three
homes: the frame's rails (§12, where they normally live), the free-floating
status bar (the fallback below `minCardWidth` and under takeover surfaces),
and the inspector rail's CONTEXT and SPEND blocks (§15, turn-scoped).

```
──────────────────────────────────────────────────────────────────────────
⏵⏵ auto · round 7/25 · ctx ▰▰▰▰▰▱▱▱ 62% · ↑41.2k ↓9.8k · $0.14 · ◇1 · gpt-5.2
```

### 8a. Segments

| Segment | Meaning |
|---|---|
| `⏵⏵ auto` / `⏵⏵ accept edits` (add 10) | permissive modes |
| `⏸ plan` / `⏸ manual` (accent 214) | gated modes |
| `✦ checking` (spin 205, spinner) | the auto-mode classifier is deciding |
| `round 7/25` (dim 241) | rounds used of the limit; §17 recovers the ceiling |
| `ctx ▰▰▰▰▰▱▱▱ 62%` | context meter (§10c) — bar and number share a colour |
| `↑41.2k ↓9.8k` (dim 241) | tokens in / out this session |
| `$0.14` (body 252) | spend |
| `◇ 2 agents ⚠1` (info 12, badge del 9) | running children, badge when one is blocked |
| `gpt-5.2` (dim 241) | model |

The agents segment is a jump target: `ctrl+a` opens the Agent Manager (§9).
Every segment keeps a glyph or a word, so colour is never the carrier.

### 8b. Field-drop order (normative)

When the rail overflows, fields leave in this order:

```
model / provider detail  →  token counts  →  round counter  →  extras
```

Never dropped: the mode segment, context pressure, spend, and any blocked or
failed state. A rail that has run out of room shows fewer facts, never
truncated ones:

```
⏵⏵ auto · round 7/25 · ctx ▰▰▰▰▰▱▱▱ 62% · ↑41.2k ↓9.8k · $0.14 · ◇1 · gpt-5.2
⏵⏵ auto · 7/25 · ctx ▰▰▰▰▰▱▱▱ 62% · $0.14
⏵⏵ ctx 62% · $0.14
```

### 8c. Width ladder (normative)

Content columns — the terminal minus horizontal padding. One ladder for the
whole surface; §12b and §15 are the two ends of it.

| Columns | Layout |
|---|---|
| ≥ 130 | two panes — 93-column transcript + `│` + 46-column inspector rail (§15) |
| 110–129 | one pane, vitals on their own rail inside the input frame (§12b wide) |
| 70–109 | one pane, vitals folded into the frame's bottom border (§12b compact) |
| < 70 | minimal — mode, context and spend only (§12b narrow) |
| < 12 | no frame — divider, this status bar, and a bare `❯` prompt |

---

## 9. Agent Manager (view, manage, steer sub-agents)

Sub-agents are managed with **the same capability as the orchestrator**:
every child has a full transcript rendered with the same components, an
input box that steers it, its own approval flow, and its own mode —
the attached view *is* the chat surface, pointed at a child.

### 9a. Agent list (`/agents` or `ctrl+a`)

```
┌─ Agents ─────────────────────────────────────────────────────────┐
│ ❯ ● orchestrator                     round 7 · ctx 62% · $0.14   │
│   ◇ researcher-1  auth flow survey   9 tools · running…    $0.02 │
│   ◇ writer-1      extract agent loop ⚠ waiting approval    $0.05 │
│   ✓ researcher-2  db schema map      done · 14 tools       $0.03 │
│   ✗ writer-2      port fish rules    failed · round limit  $0.01 │
│                                                                  │
│ enter attach · x cancel · X kill · esc back                      │
└──────────────────────────────────────────────────────────────────┘
```

- One row per agent: state glyph (`●` current, `◇` running (12), `✓`
  done (10), `✗` failed (9)), name, task label, live status, spend.
- `⚠ waiting approval` rows are red-accented and sort to the top below
  the orchestrator. `x` cancels the agent's current turn; `X` kills the
  agent (inline confirm, §5).
- The list is a live view — statuses update while it is open.

### 9b. Attached view

Attaching replaces the whole surface with the child's session, breadcrumb
in the header, and returns on `esc`:

```
 shhh code · orchestrator ▸ writer-1          esc detach · ctrl+a agents
────────────────────────────────────────────────────────────────────────
    ⚙ read    internal/ui/chat/model.go:700–780                  81 lines
   ▎✎ edit    internal/agent/loop.go              +34 −6 · approved  1.4s
   ▎▸ run     go test ./internal/agent/...                 running…    3s
       ok  github.com/rfizzle/shhh/internal/agent
────────────────────────────────────────────────────────────────────────
⏵⏵ accept edits · round 3/25 · ctx ▰▰▰▱▱▱▱▱ 31% · $0.05 · writer-1
> hold off on model.go — loop.go only for now▌
```

Equal capability, concretely:

| Action | Mechanism (same as orchestrator) |
|---|---|
| Steer | type + enter — queued mid-turn steering (S-058 semantics) |
| Approve/deny | approval cards render here when attached |
| Change mode | Shift+Tab — clamped to the parent's ceiling; over-limit choices are shown disabled |
| Cancel turn | Ctrl+C |
| Kill agent | `/exit` in the child, or `X` from the list |
| Inspect | focus mode, `/diff`, `/stats` — all scoped to the child |

- The child keeps working while you watch; attach is observation +
  steering, never a pause. So does the *parent* — attaching from inside a
  running orchestrator turn is the normal case (§9f).
- Breadcrumb nests if agents spawn agents (`orchestrator ▸ writer-1 ▸
  helper`); `esc` pops one level.

### 9c. Approval routing (detached)

A child's approval requests never wait invisibly:

- **Default — unified queue:** child approvals join the orchestrator's
  approval queue, the card title prefixed with the agent:
  `┌─ writer-1 ▸ Approve edit · internal/agent/loop.go ─┐`. Answering
  there resumes the child; `[g]` on the card jumps into the attached
  view instead.
- The cockpit badge (`◇ 2 agents ⚠1`) and the agent list mark whoever is
  blocked, so a parked approval is always visible from anywhere.

### 9d. Inherited permission state (S-086)

A child decides with the parent's policy, not a fresh one: its mode is
clamped to the parent's, and it inherits the session grants (`[a]`), the
command allowlist, the read-only inspection list, and — in auto mode —
the same classifier the parent uses. Without the classifier, an auto-mode
session still stopped once per child command; with it, children are as
quiet as the orchestrator. Auto-approvals and classifier denials are
written into the child's own transcript, so the attached view shows why
nothing was asked.

### 9e. Writer scope (S-086)

Writers already cannot overwrite each other (worktree per child, patch
reviewed on the way in). What can still collide is two patches over the
same file:

- `spawn_agent` takes optional `paths` — the globs a writer intends to
  change. A second writer whose claim overlaps a live one is refused at
  spawn, with the holder named.
- A declared scope is passed into the writer's system prompt.
- A patch touching files another agent's applied patch already changed
  renders a `⚠` warning row on the apply card.

### 9f. Reachable while the turn runs (S-087)

Children only exist *inside* a parent turn, so a manager you can only open
between turns is a manager you can never open. Every surface in §9 — the
list, the attached view, the routed approval card — opens over a running
turn, and so does every command that leaves the conversation alone:

```
⏵⏵ auto · round 7 · ◇ 2 agents ⚠1 · $0.14
▸ /attach writer-1▌            enter queues steering · ctrl+a agents · / commands · ctrl+c cancel
```

- The turn's stage (streaming, running a command, classifying) and the
  surface on screen are separate state: a surface *borrows* the screen, the
  turn keeps streaming underneath and keeps routing its own results.
- Live mid-turn: `/agents`, `/attach <name>`, `/detach`, `/stats`, `/diff`,
  `/mode`, `/ui`, `/ps`, `/memory`, `/gate`, `/sandbox`, `/evidence`,
  `/copy`, `/plan save`, `/save`, `/help`, `/exit` — plus Ctrl+E focus mode
  and Ctrl+A. Plain text still queues as steering (S-058).
- Idle-only: `/clear`, `/compact`, `/rewind`, `/branches`, `/load`,
  `/chats`, `/model`, `/run` — they rewrite or replace the conversation the
  agent is working in. They name what they'd disturb and wait, and they drop
  out of the completion menu for the duration rather than failing on pick.

---

## 10. Palette, Meters & Drawing Kit

### 10a. Palette (normative assignments)

`components.Palette` and `tokens/colors.css` hold the same tokens; the work of
S-088 was giving each one exactly one job. The token set is unchanged — no
colour was added or removed — so any screen can be checked against this table
in a minute.

| Token | 256 | Design token | Job |
|---|---|---|---|
| add | 10 | `--ansi-add` | diff additions, `✓`, `[x]`, permissive mode, staged hunks, healthy context |
| del | 9 | `--ansi-del` | diff deletions, `✗`, failures, blocked agents, a rule's denial, ctx ≥ 90% |
| accent | 214 | `--ansi-accent` | tool glyphs, `⚠` warnings, gated modes, ctx ≥ 70%, **and the mutation rail (§14)** |
| info | 12 | `--ansi-info` | sub-agents, block headings — **and every key the interface offers** (`[enter]`, `[v]`, `/mode why`) |
| hunk | 14 | `--ansi-hunk` | `@@` hunk headers and nothing else |
| spin | 205 | `--ansi-spin` | **anything in motion** — spinner frames, `▸ running…`, `✦ checking`, the current step's meter cell, the working prompt gutter |
| focusBg | 62 | `--ansi-focus-bg` | selected row background, the cursor block |
| addBg / delBg | 22 / 52 | `--ansi-add-bg` / `--ansi-del-bg` | intraline diff emphasis |
| dimmer | 245 | `--ansi-dimmer` | tool output, live tails, detail bodies, sparklines |
| dim | 241 | `--ansi-dim` | chrome, counts, hints, faint rules, empty meter cells — most of the screen |
| status | 243 | `--ansi-status` | status text, the `⛨` containment line |
| bright | 15 | `--ansi-bright` | headings, the focused row's text |
| body | 252 | `--ansi-body` | ordinary body text |
| subtle | 250 | — | inactive labels in the generate UI only; no design-system counterpart |

Three assignments carry the redesign, and are the ones to check first:
**spin means motion and only motion**, **accent additionally means the
mutation rail**, and **info marks every key the interface offers** — if a key
is written in any other colour, the interface is not offering it.

`tokens/colors.css` also defines canvas-only shades (`--screen`, `--page`,
`--rule-faint`, `--meter-empty`, `--win-*`) that exist so the artboards can be
drawn in a browser. They have no ANSI counterpart: in the terminal the screen
is the terminal's own background, and faint rules and empty meter cells (`▱`)
are dim (241).

### 10b. Backgrounds

Exactly three background tints exist. Anything else is a bug.

| Tint | Token | Use |
|---|---|---|
| selection | focusBg (62) | the focused row, the cursor block |
| addition | addBg (22) | the changed span inside an added line |
| deletion | delBg (52) | the changed span inside a removed line |

The diff tints emphasise a *span within a line* so syntax colours survive
underneath; they never fill a whole row.

### 10c. Meters

Block meters only — `▰` filled, `▱` empty. Never a bar element, never a
percentage without its bar, never a bar without its number.

| Meter | Cells | Colour |
|---|---|---|
| context | 8 in the vitals rail, 22 in the inspector rail and the context card | add (10) below 70%, accent (214) at ≥ 70%, del (9) at ≥ 90% — the bar and the number turn together |
| step progress | 22 | completed steps add (10), the running step spin (205), the rest dim (241) |
| agent progress | 5 | always info (12) — an agent lane is never colour-coded by health |
| countdown | 20 | accent (214), draining right to left (`retry in 38s`, §17) |

```
▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱▱▱   ctx 62%     healthy · add
▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱   ctx 78%     ≥70% · accent, and the number turns too
▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱   ctx 94%     ≥90% · del, and the card in §17b interrupts
▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱   step 3 of 4 done in add, the running step in spin
▰▰▰▱▱                    ◇ writer-1  agent lanes are always info
```

**Sparkline.** `▁▂▃▄▅▆▇█`, eight cells, dimmer (245), never coloured — tokens
per round over the last eight rounds (`▁▂▃▃▄▅▅▆`). It is a shape, not a
measurement; the numbers beside it are the measurement.

**Spinner.** `⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧` at 80ms a frame, spin (205). It is the only
animation in the product. Anything else that wants to move gets a meter.

### 10d. Glyphs

Meaning lives here; colour only reinforces it. The set is closed.

| Glyph | Meaning | Glyph | Meaning |
|---|---|---|---|
| `⚙` | read-only tool | `✓` | done |
| `✎` | edit / write / persist | `✗` | failed |
| `$` | shell command | `▸` | running, or folded |
| `◇` | sub-agent | `▾` | expanded |
| `▎` | this row changed the machine | `·` | queued |
| `❯` | you, and the cursor | `⊘` | denied / skipped |
| `⏵⏵` | permissive mode | `⚠` | risk, or a recoverable stall |
| `⏸` | gated mode | `⛨` | containment |
| `✦` | the classifier is deciding | `@@` | hunk header (hunk 14) |

### 10e. Drawing kit

```
▎ ▌ ▁▂▃▄▅▆▇█ ▰ ▱ · ─ │ ╭ ╮ ╯ ╰ ├ ┤ ┬ ┴ ┌ ┐ └ ┘
```

Takeover cards (§2, §9a, §17) use the square corners `┌ ┐ └ ┘` with a `├ ┤`
divider above the key row. The input frame (§12) uses the rounded `╭ ╮ ╰ ╯`
because it is a persistent surface rather than something that interrupts —
the corner shape alone says which kind of thing you are looking at.

### 10f. Monochrome (S-095)

The first invariant is checked, not asserted. `NO_COLOR`, `TERM=dumb` and
`/ui mono on` swap `components.Palette` for the two greys of
`tokens/colors.css`, and every surface follows because every surface reads
its colours from that one token set — the component styles, the chat and
generate style files, and the saved-chat browser rebuild themselves on the
swap rather than capturing colours once at init.

| Mono token | 256 | Design token | Takes over from |
|---|---|---|---|
| mono-fg | 254 | `--mono-fg` | add, del, accent, info, hunk, spin, bright, body |
| mono-dim | 244 | `--mono-dim` | dim, dimmer, status, subtle |
| mono-bg | 237 | `--mono-bg` | focusBg, addBg, delBg |

Bold, glyphs and layout are untouched — only hue goes. The two colour
sources the palette does not own are declined rather than recoloured: the
diff renderer drops chroma highlighting, and assistant prose renders through
glamour's `ascii` theme, which writes emphasis as `**` instead of as colour.
`NO_COLOR` additionally drops the terminal profile to `termenv.Ascii`, which
flattens even the two greys — the stricter reading that convention asks for.

The check itself lives in `internal/ui/components/mono_test.go`: it renders
every state of every surface with mono on, strips the ANSI, and fails when
two states collapse to the same text. A state that was only ever a hue apart
from another is a failing test, not a review comment. The design-system
project ships the same check as `guidelines/mono-check.html`.

---

## 11. Implementation Notes

- Package `internal/ui/components`: one file per component
  (`approval.go`, `diff.go`, `selector.go`, `multiselect.go`,
  `noteselect.go`, `confirm.go`, `activityrow.go`, `cockpit.go`,
  `agentlist.go`, `frame.go`). The v2 surfaces add `meters.go` (§10c —
  `Meter`, `Sparkline`, `Spinner`, and the frame set every animating host
  ticks),
  `inspector.go` (§15) and `review.go` (§16); the step outline (§13) is a
  layer over the entry list in `internal/ui/chat/steps.go`, beside the
  activity feed and not a component, because it groups history rather than
  rendering a widget.
- The column grid (§6a) lives in one place — field widths are constants in
  `activityrow.go` and every surface that draws a transcript row uses them,
  so a grid change is a one-line change.
- The attached view (§9b) is not a new surface: the chat `Model` renders
  whichever agent is "focused", and every agent (orchestrator included)
  is an `internal/agent` instance with its own transcript, approval
  queue, and mode. Attach = switch focused agent; the orchestrator is
  simply the root of that list. This falls out of the S-056 extraction
  and is the strongest reason to keep it clean.
- Components are plain state + two methods, not full Bubble Tea models:
  `Update(tea.KeyMsg) (done bool, result any)` and `View(width int)
  string`. The chat `Model` owns them via states (as `stateConfirmRun`
  does today) — no nested programs, no goroutines in components.
- `stateConfirmRun`/`renderRunConfirm` refactor into the Approval Card as
  the first consumer; behavior-compatible (y/n/esc semantics unchanged).
- Diff computation: `internal/diff` producing hunks from old/new content
  (edit_file knows both sides; no shelling out to `git diff` except for
  the `/diff` session view).
- Shared palette exported from one place (promote `style.go` values into
  a `components.Palette`); chat and generate UIs consume it so the two
  TUIs stay visually consistent.
- Every component takes an explicit width and handles < 60 columns by
  stacking rather than truncating hints.

---

## 12. Input Frame & Rails (command center, S-082)

The prompt surface — where the user types and reads session vitals — is a
rounded-corner frame whose borders carry information instead of being dead
lines. It is the presentation-only spirit of the pi cockpit spec
(`agent-ui/COCKPIT_SPEC.md` §2/§3/§6/§8) applied to shhh's bottom panel: it
changes chrome, never behavior. The §8 cockpit segments and palette are
re-homed into the frame's rails; the free-floating §8 bar remains the
fallback for takeover surfaces and sub-`minCardWidth` terminals.

### 12a. Anatomy

- **Top rail** (top border): session identity on the left — the title, plus
  the attached-child breadcrumb (S-077) — and the live activity state on the
  right: a spinner + `WORKING` while streaming, running tools, or checking
  permission; dim `idle` otherwise.
- **Prompt gutter** replacing the placeholder sentence: `❯` idle, `▸` while
  the agent works (typed text becomes steering, S-058), and the child's name
  (`writer-1 ❯`) while attached. Wrapped input lines indent under it.
- **Vitals rail**: the §8 cockpit segments (mode, round counter, context
  meter, tokens, spend, agents, model) attached to the frame — a dedicated
  `├─ ─┤` rail in the wide layout, folded into the bottom border otherwise.
- **Bottom rail** (bottom border): contextual key hints that swap by state —
  idle `enter send · / commands · shift+tab mode`, working `enter queues
  steering · ctrl+c cancel`, attached `esc detach · ctrl+a agents` — absorbing
  the old header hint text and the textarea placeholder.
- **Notice rail**: one plain line above the frame that exists only while
  there is something to say — update notice, queued steering count, blocked
  sub-agents, the latest auto-mode denial — and disappears when clear
  (COCKPIT_SPEC.md §6 alert-rail pattern).

### 12b. Layout modes (COCKPIT_SPEC.md §3)

Widths are content columns (terminal minus horizontal padding); these are the
lower three rungs of the §8c ladder. At ≥ 130 the frame keeps its **wide**
layout and spans both panes of the two-pane cockpit (§15).

**wide** (≥ 110): two rails below the input — vitals junction + hints:

```
╭─ shhh code ─────────────────────────────────────────────────────── ⠋ WORKING ─╮
│ ▸ and add a regression test for the parser▌                                   │
│                                                                               │
│                                                                               │
├─ ⏵⏵ accept edits · round 7/25 · ctx ▰▰▰▰▰▱▱▱ 62% · ↑41.2k ↓9.8k · $0.14 · gpt-5.2 ─┤
╰─ enter queues steering · / commands · ctrl+c cancel ──────────────────────────╯
```

**compact** (70–109): one rail — vitals fold into the bottom border, hints
drop (attached, the detach hints move to the top rail's right side):

```
╭─ shhh chat ──────────────────────────────────────────────────────── idle ─╮
│ ❯ ▌                                                                       │
│                                                                           │
│                                                                           │
╰─ ⏸ manual · ctx ▰▰▰▱▱▱▱▱ 31% · ↑12.0k ↓3.4k · $0.05 ──────────── gpt-5.2 ─╯
```

**narrow** (< 70): minimal rail — identity drops from the top rail and the
vitals keep only the never-dropped fields:

```
╭──────────────────────────────── idle ─╮
│ ❯ ▌                                   │
│                                       │
│                                       │
╰─ ⏸ manual · ctx ▰▰▰▱▱▱▱▱ 31% · $0.05 ─╯
```

Below `minCardWidth` (12) the surface degrades to plain rows: the old
divider + §8 status bar + bare input.

**Field-drop order** when a rail overflows (or the layout narrows): model /
provider detail first, then token counts, then round counter / extras /
idle-agent count. Context pressure, spend, and error/blocked state are never
the first fields removed; the mode segment is never dropped.

### 12c. Mode-aware accent (COCKPIT_SPEC.md §8)

The frame border color reflects the permission mode — add (10) for the
permissive modes, accent (214) for the gated ones, spin (205) while the
auto-mode classifier is checking. Attached, it reflects the child's mode.
Every state keeps its textual glyph (`⏵⏵`/`⏸`/`✦` in the vitals, `WORKING`/
`idle` on the top rail), so meaning never depends on color alone.

### 12d. Attached (S-077)

```
╭─ shhh code · orchestrator ▸ writer-1 ───────── esc detach · ctrl+a agents ─╮
│ writer-1 ❯ hold off on model.go▌                                           │
│                                                                            │
│                                                                            │
╰─ ⏵⏵ accept edits · round 3 · running… · $0.05 ──────────────── writer-1 ───╯
```

The vitals scope to the child (its mode, detail, spend, queued steering,
name); the notice rail is orchestrator-scoped and hides while attached.

### 12e. Interplay and layout accounting

- The completion menu (S-078) renders inside the frame, under the input
  rows; the confirm-panel height cap bounds input + menu as before. It stays
  open past the command name for argument values (S-079), re-filtering on the
  token under the cursor.
- Takeover surfaces — approval/plan cards, pickers, the agent list, routed
  child asks, focus/diff hint bars — replace the framed input wholesale and
  keep the divider + §8 status bar stack, so their geometry is unchanged.
- The frame's top and bottom borders occupy the rows the bottom divider and
  status bar otherwise use, so `chromeHeight` stays constant in the compact
  and narrow layouts; the wide vitals rail and the notice rail are accounted
  separately (`frameExtraHeight`) when sizing the viewport.
- The frame is rebuilt every render and never enters the transcript render
  cache, so resize just works.

---

## 13. Step Outline (S-090, S-091)

A forty-tool turn is four lines until you ask for more. A step is a titled
group of consecutive tool calls: ordinal, state glyph, title, a faint rule
stretching to the right-hand stats, tool count and duration.

```
▾ 1  Locate the round accounting ───────────────────────── ✓ 4 tools  6.2s
   ⚙ read    internal/agent/loop.go                        218 lines  0.4s
   ⚙ search  ErrRoundLimit ./internal               6 hits · 4 files  0.3s

▾ 2  Thread the sentinel through the loop ──────────────── ✗ 9 tools 38.1s
   ▸ ⚙       6 reads · 2 searches                     [enter] expand  3.9s
  ▎✎ edit    internal/agent/loop.go      +12 −4 · 2 hunks · approved  1.1s
  ▎✗ run     go test ./internal/agent/...         exit 1 · 1 failing 21.4s
    --- FAIL: TestRoundLimit (0.03s)

▸ 3  Re-run the agent suite ────────────────────────────── ▸ 3 tools  ⠋ 3s
   ▸ run     go test ./internal/agent/...                   running…    3s
    ok    shhh/internal/agent/tool      0.184s

· 4  Report what changed ────────────────────────────────── · queued     —
```

### 13a. Where steps come from

- **Declared.** Plan mode (S-104) produces an ordered list; those steps are
  authoritative and the outline mirrors them, including the ones not started.
- **Inferred.** Otherwise the assistant prose immediately preceding a batch of
  tool calls becomes the step title.
- **Neither.** A turn with no discernible steps renders exactly as it does
  today — a flat list of rows, no empty group chrome.

The grouping is a layer over the existing entry list in
`internal/ui/chat/steps.go`, not a wire protocol. The agent already emits
ordered tool results; inventing a step message would couple every provider to
the UI for nothing.

### 13b. State and folding

| Glyph | State |
|---|---|
| `▸` | running (the ordinal's rule stats show the spinner and elapsed) |
| `✓` | complete |
| `✗` | complete, contained a failure |
| `·` | declared but not started; duration `—` |

- A step is **open while running** and collapses to its header on completion —
  except a step containing a failure, which stays open, because a failure you
  have to scroll to find is a failure you will miss.
- `▾`/`▸` in the pointer column mark expanded and folded. Focus mode (§7)
  moves between step headers as well as rows; `enter` folds or unfolds in
  place.
- Steps re-render from stored raw entries on resize, following the
  `entry`/`renderHistory` cache design — they hold no layout state.

### 13c. Verbosity (`/ui verbosity`)

Three levels, three distinct meanings — today the flag only toggles row
detail. The setting persists per session and appears as argument values in the
completion menu (S-079).

```
── /ui verbosity low ─────────────────────────────────────────────────────
▸ 1  Locate the round accounting ───────────────────────── ✓ 4 tools  6.2s
▸ 2  Thread the sentinel through the loop ──────────────── ✗ 9 tools 38.1s
▸ 3  Re-run the agent suite ────────────────────────────── ▸ 3 tools  ⠋ 3s

── /ui verbosity normal ──────────────────────────────────────────────────
▾ 2  Thread the sentinel through the loop ──────────────── ✗ 9 tools 38.1s
   ▸ ⚙       6 reads · 2 searches                     [enter] expand  3.9s
  ▎✎ edit    internal/agent/loop.go                 +12 −4 · 2 hunks  1.1s
  ▎✗ run     go test ./internal/agent/...         exit 1 · 1 failing 21.4s

── /ui verbosity high ────────────────────────────────────────────────────
   ⚙ read    internal/agent/loop.go                        218 lines  0.4s
   ⚙ read    internal/agent/round.go                        96 lines  0.2s
   ⚙ search  ErrRoundLimit ./internal               6 hits · 4 files  0.3s
    internal/agent/loop.go:88      return ErrRoundLimit
    internal/ui/chat/model.go:301  case errors.Is(err, ErrRoundLim…
```

| Level | Shows |
|---|---|
| `low` | step headers only |
| `normal` | headers, with consecutive read-only calls folded into one counted row |
| `high` | every row expanded with its bounded detail body |

The folded group row obeys invariant 4: it always states what it swallowed
(`▸ ⚙ 6 reads · 2 searches`, 3.9s of it) and expanding restores the individual
rows in place. **Mutations, failures and sub-agent rows are never folded into
a group** — the whole point of the fold is that it only ever hides chrome.

---

## 14. The Mutation Rail

One column, one glyph, one question: *did this row change my machine?*

```
   ⚙ read    internal/agent/loop.go
  ▎✎ edit    internal/agent/loop.go
  ▎$ run     go build ./cmd/shhh
   ◇ agent   writer-1 · document the sentinel
  ▎⊘ run     rm -rf ./dist
```

The rail occupies gutter column 3 (§6a) on every row that **wrote to disk, ran
a command, or was denied**; read-only rows leave it blank. Scrolling a long
transcript, the eye follows a rail down the gutter instead of reading rows
left to right.

- **Commands always carry it.** shhh cannot know whether a command wrote
  something, so it assumes the worst. A `go build` and a `go test` are marked
  alike; the artboards' own examples vary on this and the conservative reading
  is the one that holds.
- **Denials carry it** because a denial is a decision you made, and the point
  of the rail is to find the moments that mattered. `⊘` plus the word already
  says nothing happened (§6d).
- **Sub-agent rows do not.** A child's own transcript carries its own rails; a
  mirrored `◇ agent` row in the parent is a status report, not an act.
- **Colour:** accent (214) for a mutation. A row that **failed keeps a del (9)
  rail for the rest of the session**, so scrolling back finds the break
  without hunting for it.
- The rail is one character wide and never widens, indents, or nests. Detail
  bodies under a railed row do not repeat it.

The rail is invariant 2 made visible: a read is chrome, a mutation carries a
rail, a decision gets a card.

---

## 15. Inspector Rail (two-pane cockpit, S-092)

Past 130 content columns the transcript stops being the whole screen. A
46-column rail on the right answers the three standing questions — what is it
doing, what has it changed, what is it costing — so you stop running `/stats`
and `/diff` to recover what the session already knows.

```
┌───────────── 93 columns ─────────────┐ │ ┌───── 46 columns ─────┐
│ transcript — steps, rows, details    │ │ │ THIS TURN            │
│ wraps to 93, not to terminal width   │ │ │ CHANGES              │
│ takeover surfaces span both panes    │ │ │ AGENTS               │
│ and hide the rail while they show    │ │ │ CONTEXT              │
│                                      │ │ │ SPEND                │
└──────────────────────────────────────┘ │ └──────────────────────┘
                                         ╰ one │ column, dim (241),
                                           full viewport height
```

The split is horizontal only: `chromeHeight` and `syncViewportHeight`
accounting are unchanged, and the input frame (§12) spans both panes because
steering is a session-level act. Below 130 the rail is dropped entirely and
today's single-pane layout is untouched (§8c).

### 15a. Blocks

```
  THIS TURN                        step 3 of 4
  ▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱
  18 tools · 1m 04s elapsed

  CHANGES                               +30 −4
  ▎✎ agent/loop.go                      +18 −3
  ▎✎ ui/chat/model.go                    +9 −1
  ▎✎ agent/errors.go                     +3 −0
   ✗ 1 test failing             TestRoundLimit
  [v] review · [u] undo turn

  AGENTS                             1 running
  ◇ writer-1                 ▰▰▰▱▱ 2/5 · $0.02
    docs/loop.md · 4 tools

  CONTEXT                          62% of 200k
  ▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱▱▱                  124k
  ▁▂▃▃▄▅▅▆ per round              ↑41.2k ↓9.8k

  SPEND                                  $0.14
  gpt-5.2 · $0.12 main · $0.02 ◇
  session total $1.86
```

| Block | Contents |
|---|---|
| THIS TURN | step progress meter, `step 3 of 4`, tool count, elapsed |
| CHANGES | `+N −M` total, one row per changed file with its rail and glyph, failing-test state, `[v] review · [u] undo turn` |
| AGENTS | running children — lane meter, steps, spend, current target and tool count |
| CONTEXT | percent of the window, meter, tokens, the per-round burn sparkline (or `estimated`) |
| SPEND | turn total split main / children, model, session total |

### 15b. Rules

- **Blocks with nothing to say are omitted, not rendered empty.** A session
  with no children has no AGENTS heading at all.
- **The rail never scrolls.** When it does not fit the viewport it truncates
  its longest block first, and the block says so rather than silently ending.
- **The rail is passive**, like `components.Cockpit` — fed by the host model,
  no keys, no state, no goroutines. The keys it prints (`[v]`, `[u]`) are
  handled by the host.
- **Takeover surfaces span the full width and hide the rail** — approval
  cards, pickers, review mode (§16), the agent list — and restore it on
  dismissal.
- Transcript wrapping uses the reduced 93-column pane width, not the terminal
  width.
- **A number nobody reported says so.** Occupancy is provider-reported where
  a response carried usage, and the session's own estimate everywhere else —
  before the first response, after a trim, after `/compact` or a rewind. An
  estimate prefixes its token count with `~` and writes `estimated` where the
  burn sparkline would go, so the hedge survives a monochrome terminal. The
  meter and its percentage are unchanged: an estimate is still the best
  number there is, it is just not a measurement.

The vitals rail on the frame (§12) stays as it is. The inspector rail is
turn-scoped and historical; the vitals rail is session-scoped and live. They
overlap only on context and spend, which is deliberate — those are the two
numbers you want without moving your eyes.

---

## 16. Changeset & Review (E-014: S-097–S-100)

The question after an agent stops is never "what did it say", it is "what did
it change". So a turn closes with three rows: what it did, what changed, and
whether the tests still pass.

```
 ✓ Done · 4 steps · 18 tools · 1m 04s · $0.14                   round 7/25
▎✎ 3 files changed +30 −4 · [v] review · [u] undo turn   all tracked in git
 ✓ go test ./... passing · 41 packages · 12.8s
```

The changed-files row carries the mutation rail (§14) — at a glance the close
of a turn looks like the rows that produced it. Close rows start at the rail
column rather than the pointer column (§6a): they belong to the turn, not to a
step, and nothing folds them.

- The right-hand field annotates the row and is the first thing to drop as
  the terminal narrows: the round counter beside the summary, and beside the
  changed files what git knew about them when they were written — `all
  tracked in git`, `2 tracked · 1 new`, or `no git here`, which is not the
  same claim as untracked (S-097). Turning that into a claim about what can
  be taken back is the approval card's blast-radius line (S-101), not this
  row's job.
- A stat the session cannot report is left out rather than reported as a
  zero: a turn that called nothing says no step or tool count, and an
  unpriced turn reports its tokens, never a made-up zero (§15b).
- A turn that changed nothing is the first row alone. A turn you cancelled
  reads `⊘ Cancelled`, one whose stream broke reads `✗ Failed`, and both
  still carry the changed-files row for what landed before they stopped.
- The verdict row is one row however many checks ran: several runs collapse
  to `✗ checks failing · 2 of 3 passing` rather than one row each. The row
  answers "does it still build", not "what did you run".
- Nothing about the block is a takeover. The rows are transcript entries and
  the keys they offer are handled by focus mode on the row (§7), so the input
  keeps every other key.
- `[u]` (and `/undo [turn]`) puts the turn's files back from the same records,
  and asks first — an inline confirm (§5) stating what it would restore and
  what it would delete. A file that changed since the turn is named on the
  confirm and left alone by the default answer; `[f]` is the deliberate second
  answer that overwrites it. Esc declines and writes nothing. The undo is
  recorded as a changeset of its own, so it closes with its own row, reviews
  with `[v]`, and can itself be undone. A turn whose records were evicted from
  the bounded store is refused with that as the reason (S-097).

### 16a. Review mode

One surface serves the edit approval card (§2c), `/diff`, and `[v]` from the
changeset row: file list left, hunks right, staging per hunk, with the failing
test pinned beside the hunks that claim to fix it.

```
REVIEW turn 7          2 of 3 staged     │ internal/agent/loop.go  2 hunks · +18 −3 · ✓ staged
──────────────────────────────────────── │ @@ -84,9 +84,14 @@ func (a *Agent) runRound(...)
[x] ✎ agent/loop.go             +18 −3   │     84    if a.round >= a.maxRounds {
[x] ✎ ui/chat/model.go           +9 −1   │   −  85        return ErrRoundLimit
[ ] ✎ agent/errors.go            +3 −0   │   +  85        return &RoundsExhausted{Round: a.round,
──────────────────────────────────────── │   +  86            Max: a.maxRounds}
✗ go test ./internal/agent/...           │     86    }
  --- FAIL: TestRoundLimit (0.03s)       │     87    a.round++
  loop_test.go:142                       │ @@ -131,4 +136,9 @@ func (a *Agent) Run(...)
  staged hunks claim the fix · [r] rerun │    136        if err := a.runRound(ctx); err != nil {
──────────────────────────────────────── │   −  137            return err
⛨ nothing is committed                   │   +  137            var ex *RoundsExhausted
  undo restores from git stash           │   +  138            if errors.As(err, &ex) {
                                         │   +  139            return a.ui.OfferMoreRounds(ex)
─────────────────────────────────────────────────────────────────────────────────────────────────
[space] stage hunk · [s] file · [A] all · [enter] apply 2 files · [esc] leave
```

- `[space]` stages the hunk under the cursor, `[s]` the whole file, `[A]`
  everything; `[enter]` applies what is staged. `[esc]` leaves review having
  changed nothing, and the surface says so.
- Nothing is committed by review — `⛨ nothing is committed` is always on
  screen, and `[u] undo turn` restores from the turn's own changeset records
  (S-100), never from git: undo has to work in a directory that was never a
  repository, and it must not touch your index or your stash.
- Unified is the default; `[\]` toggles side-by-side (§3c) for the case where
  a line moved rather than changed.
- Review is a takeover surface: full width, rail hidden (§15b), `esc` returns.

The point is that review is a **place you can return to**. Diffs used to
appear inside the approval prompt and nowhere else, so after approving you had
no way back to what you agreed to.

---

## 17. Recovery States (E-016: S-105–S-109)

Most of a tool's reputation is made in its failures. These paths used to be a
Go error on stderr and a dead session. Every one of them is an ordinary
activity row plus one offered key; only two earn a card, because only two stop
the session dead.

### 17a. Failures are rows, not modals

```
   ✗ model   gpt-5.2 · 401 unauthorized         key ···4f9c rejected  0.3s
    the key loaded from the macOS keychain, added 4 Mar
    [k] replace it · [o] switch to ollama · nothing in the turn was lost

   ⚠ model   rate limited · 40k tok/min tier            retry in 38s     —
    ▰▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱▱▱  waiting · round 7 resumes where it stopped
    [m] finish this turn on gpt-5.2-mini · [esc] stop and keep the 3 edits

   ⚠ stream  dropped mid-reply · 1,204 tokens kept           partial   11s
    "…so I'll thread the sentinel through runRound and then
    [enter] continue from here · [r] ask again · the partial reply stays

   ⚠ rounds  25 of 25 used · ErrRoundsExhausted              stopped 4m12s
    3 files changed +30 −4 · the suite has not been re-run since
    [v] review what it did · [+10] ten more rounds · [u] undo the turn
```

Four failures, four offered keys, one shape. The verbs `model`, `stream` and
`rounds` occupy the same 8-column field as tool verbs (§6c) and the rows obey
the same grid, so a failure reads as part of the turn rather than an
interruption of it.

- **Nothing in the turn is lost.** The three edits survive a rate limit, the
  partial reply survives a dropped stream, and each row says so in words.
- `⚠` (accent 214) is a recoverable stall — it will resume or you can steer
  it. `✗` (del 9) is a call that failed. The distinction is the whole reason
  both exist.
- The countdown meter (§10c) drains right to left while a retry waits.
- Offered keys are info (12) and sit at indent 2 under the row.

### 17b. The two cards

A card is warranted only when the session cannot continue without an answer.

```
┌─ No model provider configured ─────────────────────────────────────────┐
│ shhh looked in four places:                                            │
│   ✗ env       OPENAI_API_KEY, ANTHROPIC_API_KEY — unset                │
│   ✗ config    ~/.shhh/config.toml — no [provider] block                │
│   ✓ keychain  one entry: openai (added 4 Mar)                          │
│   ✓ local     ollama on :11434 — llama-3.3-70b, no tool use            │
│                                                                        │
│ the keychain entry failed to decrypt — that is the likely fix          │
├────────────────────────────────────────────────────────────────────────┤
│ [enter] setup wizard   [p] paste a key   [o] use the local model       │
└────────────────────────────────────────────────────────────────────────┘
```

The card names **every place shhh looked and what it found there**, then says
which one is the likely fix. A missing-key message that does not say where it
looked is a message that cannot be acted on.

```
┌─ Context is nearly full · 94% · 188k / 200k ───────────────────────────┐
│ ▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱                                                 │
│                                                                        │
│ 88k  tool output — mostly go test runs, 6 of them                      │
│ 54k  files read — 14 files, loop.go read 4 times                       │
│ 31k  the conversation                                                  │
│ 15k  memory and system                                                 │
│                                                                        │
│ compacting keeps the plan, the 3 changed files and the failing test    │
│ and drops the older tool output — recovers about 96k (48%)             │
├────────────────────────────────────────────────────────────────────────┤
│ [enter] compact now   [n] new session   [esc] keep going               │
└────────────────────────────────────────────────────────────────────────┘
```

This is the only place in the product that itemises token spend, because it is
the only place where you can act on it. The categories come from S-093's
accounting — tool output, files read, the conversation, memory and system —
and the card states what compacting will keep, what it will drop, and how much
that recovers. `[esc]` keeps going: invariant 3 holds even at 94%.

### 17c. First contact

A first launch in a repo shhh has never seen already knows the repo, and
offers work rather than a blank prompt:

```
shhh 0.9.4                                          [?] keys · [q] quit
──────────────────────────────────────────────────────────────────────

~/src/shhh · go 1.24 · git main · 3 files changed · no .shhh/memory.md

Some things worth doing first:
  ▸ pick up loop-refactor — 3 files changed, 1 test failing, 4m ago
  ⚙ explain what changed in the working tree — reads only, no writes
  ⚙ run go test ./... and triage the failures — one approval, then it
    reports back

[↑↓] choose · [enter] start · or just type what you want
```

- The header line is what shhh already knows: path, toolchain, branch, dirty
  state, package count, whether a project memory exists.
- Suggestions are ordered by what the working tree suggests — an unfinished
  branch first, then read-only offers, then one that needs a single approval.
  Each says what it will cost you in permission.
- Typing anything dismisses the list. It is a starting point, not a wizard.
