# shhh — TUI Component Catalog

> Visual components for the coding-agent TUI (`shhh chat` / `shhh code`).
> Companion to DESIGN.md. Implemented per backlog story S-076; consumed by
> S-048 (approvals), S-061 (plan mode), S-070 (memory), S-074 (diffs),
> S-075 (activity feed & cockpit).

---

## 1. Principles

- **One interaction panel.** Components that need keys render in the bottom
  panel, replacing the input textarea while active — exactly how
  `stateConfirmRun` works today. The transcript viewport stays visible above.
  The panel may grow to at most 40% of terminal height; the viewport shrinks
  to make room and restores on dismissal.
- **Transcript entries are passive.** Anything rendered into history (diff
  blocks, activity rows) is stored raw and re-rendered on resize, following
  the existing `entry`/`renderHistory` cache design. Only the *selected* row
  responds to keys, via a lightweight focus mode (§7).
- **Never color alone.** Every state pairs color with a glyph or text
  (`✓ ✗ ⚠ ⏵ ⏸ ✦ ❯ ▸ ✎`), so monochrome terminals stay usable.
- **Esc always dismisses / declines safely.** No component makes Esc do
  something destructive.
- **Reuse the palette** (§8). No new colors without adding them there.

---

## 2. Approval Card

The single surface for every approval-gated action (S-048). One container,
three body variants: command, edit, generic tool.

### 2a. Command approval

```
┌─ Approve command ────────────────────────────────────────────────┐
│ $ rm -rf ./dist && npm run build                                 │
│ ⚠ deletes files recursively (rm -rf)                             │
│ ⛨ contained · workspace profile · network on                     │
│                                                                  │
│ [y] allow   [n] deny   [a] always (session)   [esc] deny         │
└──────────────────────────────────────────────────────────────────┘
```

- Title bar states the action kind. Command shown verbatim, wrapped, never
  truncated silently (long commands scroll within the card).
- `⚠` lines come from `safety.Check`, red (9). When any are present, `[a]`
  is absent — flagged actions can never be blanket-approved (S-059).
- `⛨` line reports containment state (S-062): mechanism + profile, gray
  (243) when contained, yellow `⚠ uncontained` (214) when no mechanism.

### 2b. Edit approval (embeds Diff Viewer, §3)

```
┌─ Approve edit · internal/agent/loop.go ──────────────────────────┐
│ @@ -142,7 +142,9 @@ func (a *Agent) runRound(                    │
│   142    if len(calls) == 0 {                                    │
│ - 143        return results, nil                                 │
│ + 143        if a.rounds >= a.maxRounds {                        │
│ + 144            return results, ErrRoundLimit                   │
│ + 145        }                                                   │
│   146    }                                                       │
│ +3 −1 · 1 hunk                                                   │
│                                                                  │
│ [y] allow   [n] deny   [a] always edits   [d] full diff          │
└──────────────────────────────────────────────────────────────────┘
```

- Shows the first hunk(s) that fit; `+N −M · H hunks` summary line always
  present. `[d]` opens the full-screen diff view (§3c) and returns here.
- `write_file` on a new file renders as an all-additions diff titled
  `Approve new file · path`.

### 2c. Queue position

When multiple calls await approval, the title carries `(2 of 5)`; `[n]`
denies just the current one, consistent with today's queue semantics.

---

## 3. Diff Viewer

### 3a. Collapsed transcript row

Applied edits land in history as one row (activity-row grammar, §6):

```
│ ✎ edit internal/ui/chat/model.go        +12 −4 · 2 hunks   [enter] expand
```

### 3b. Expanded unified view (in transcript, bounded height)

```
│ ✎ internal/ui/chat/model.go                          +12 −4 · 2 hunks
│ @@ -358,6 +358,10 @@ case toolCallsMsg:
│    358   m.accumulateUsage(msg.usage)
│ +  359   if m.rounds >= m.maxRounds {
│ +  360       return m.stopAtRoundLimit()
│ +  361   }
│    362   m.messages = append(m.messages, provider.Message{
│ @@ -401,3 +405,5 @@ case toolResultsMsg:
│ …8 more lines · [enter] full view · [enter again] collapse
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

Used by: plan approval (S-061), `/run` block picker, `/mode` menu,
model-asked structured questions.

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

## 6. Activity Rows (compact feed)

Grammar for every tool call in the transcript (S-075):
`glyph name key-arg → outcome · counts · duration`.

```
│ ⚙ search  advanceExecQueue              3 matches          0.1s
│ ⚙ read    model.go:580–660              81 lines           0.0s
│ ▸ $ go test ./internal/agent/...        running…           4s
│     ok  github.com/rfizzle/shhh/internal/agent  0.31s
│ ✎ edit    internal/agent/loop.go        +12 −4 · approved
│ ✗ $ go vet ./...                        exit 1             0.8s
│ ◇ agent   researcher: find auth flows   running… 2 tools   12s
```

- Glyphs: `⚙` read-only tool (214), `$` command, `✎` edit/write, `✗`
  failure (9), `▸` running (spinner replaces it while animating), `◇`
  sub-agent (12).
- A running command shows a **live tail**: its last output line, gray
  (245), indented beneath the row; replaced by the outcome on exit.
- Failed rows auto-expand to their bounded detail (error lines first —
  evidence-store view from S-064). Successful rows stay collapsed.
- `/ui verbosity high` renders all rows expanded; `low` hides counts.

---

## 7. Focus Mode (expanding history)

`ctrl+e` (or click) enters focus mode: the viewport gets a selection
cursor on expandable rows (`❯` in the gutter), `j/k` moves between them,
`enter` expands/collapses in place, `esc` returns to the input. This is
the one mechanism behind "[enter] expand" everywhere in the transcript,
so the input textarea keeps all other keys.

---

## 8. Cockpit Rail (status bar v2)

Extends the current status bar; degrades by dropping right-side segments
first when narrow (existing behavior):

```
──────────────────────────────────────────────────────────────────────
⏵⏵ accept edits · round 7/25 · ctx ▰▰▰▰▰▱▱▱ 62% · ↑41.2k ↓9.8k · $0.14 · gpt-5.2
```

Mode segment states (auto-mode/S-059/S-060):

| Segment | Meaning |
|---|---|
| `⏵⏵ auto` / `⏵⏵ accept edits` (10) | permissive modes |
| `⏸ plan` / `⏸ manual` (214) | gated modes |
| `✦ checking` (205, spinner) | classifier deciding |
| `◇ 2 agents` (12) | running sub-agents |

Context meter: 8-cell bar, green → yellow (214) at 70% → red (9) at 90%,
matching S-055's warning thresholds. Percent shown beside it.

---

## 9. Palette

Additions on top of the existing `style.go` values (256-color):

| Token | Color | Use |
|---|---|---|
| add | 10 | diff additions, `[x]`, permissive mode |
| del | 9 | diff deletions, errors, required-note, ctx ≥90% |
| addBg / delBg | 22 / 52 | intraline emphasis backgrounds |
| hunk | 14 | `@@` hunk headers |
| accent | 214 | tool glyphs, warnings, gated modes, ctx ≥70% |
| info | 12 | sub-agents, user accents |
| focusBg | 62 | selected option/row background |
| dim / dimmer | 241 / 245 | chrome, counts, live tail |
| spin | 205 | spinners, `✦ checking` |

---

## 10. Implementation Notes

- New package `internal/ui/components`: one file per component
  (`approval.go`, `diff.go`, `selector.go`, `multiselect.go`,
  `noteselect.go`, `confirm.go`, `activityrow.go`, `cockpit.go`).
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
