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

### 2e. Queue strip and batch approval

Five separate cards, one after the other, is how you train someone to hit
enter without reading. When more than one call awaits approval the card gets a
strip above it listing the stack in the order it will be asked (S-102):

```
  ●○○○○  5 pending  ·  [A] answers the 3 marked
  ▸ 1 go test ./internal/agent/...                          ⚠ low  [A]
    2 npm run build                                         ⚠ low  [A]
    3 edit internal/ui/chat/model.go                  +9 −1  ⚠ medium
    4 rm -rf ./dist                                         ⚠ HIGH
    5 write docs/loop.md                             +12 −0  ⚠ low  [A]
┌─ Approve command (1 of 5) ───────────────── ⛨ bwrap · workspace ─ ⚠ low ─┐
```

- One dot per decision still waiting, the current one filled; the title
  carries `(1 of 5)`, which the dots — drawn over what is left — cannot say.
- Items keep the number they had when the round queued them, so an item's
  number does not move as the ones ahead of it are answered.
- `[A]` approves the current action and every queued action the session would
  classify the same way, with the count on the key (`A: approve 3 like this`).
  "The same way" is the category the `[a]` session grant would use, read from
  one matcher — commands with commands, edits with edits.
- Membership is a `[A]` mark on the row, stated before the key applies it, and
  never a colour. What the strip cannot fit is counted on a final row —
  `… 3 more, 1 marked` — never dropped.
- A safety-flagged action is in no batch: it is taken out and asked on its
  own, whatever else is queued. So is anything the session grants do not
  cover.
- `[A]` is not a session grant. It answers the decisions on the strip and
  nothing the model asks for afterwards; each member is still re-checked
  against the mode and the safety checker as it reaches the head of the queue.
- `[n]` denies just the current one, consistent with today's queue semantics.
- The strip's rows sit above the card rather than inside its 40% bound: taking
  them out of the card would spend the decision's own space on the list of
  decisions. The strip itself is bounded — six rows, fewer on a short
  terminal.

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

Used by: the `/run` block picker (S-081), `/mode` and `/model` menus, the
session pickers (`/load`, `/chats`, `/branches` — S-080), model-asked
structured questions. Plan approval is a single-select too, but with the
plan itself above the options — see §4d.

```
┌─ Switch mode ────────────────────────────────────────────────────┐
│ ❯ 1. manual — every consequential tool call asks                 │
│   2. accept-edits — file edits apply; commands ask               │
│   3. auto — allowlisted commands run; classifier judges          │
│   4. plan — read-only research                                   │
│                                                                  │
│ ↑↓/jk move · enter select · 1–4 jump · esc cancel                │
└──────────────────────────────────────────────────────────────────┘
```

- Focused row: `❯` pointer, bold, selection background (62) — same
  visual as the existing action bar's `ActiveStyle`.
- Optional per-option description line, gray (241), shown under the
  focused option only (keeps the card short).
- Number keys select immediately, and they count the list rather than the
  window: option 12 is `12.` whether or not it is the first row showing.
- The filtered variant (the palette, §18a) adds a query line above the
  options, group rails the pointer steps over, and rows dimmed behind `⊘`
  for an option that cannot be acted on right now. It is unnumbered, because
  a digit typed into a filter is a digit.

**Lists longer than the panel scroll (S-116).** The card is a window onto
the list, not the first N rows of it:

```
┌─ Switch model ───────────────────────────────────────────────────┐
│ ↑ 2 more                                                         │
│   3. claude-sonnet-5                                             │
│   4. claude-haiku-4-5                                            │
│   5. gpt-5.2                                                     │
│ ❯ 6. gpt-5.2-codex                                               │
│     fast, cheap, tools                                           │
│ ↓ 8 more                                                         │
│ ↑↓/jk move · enter select · 1–14 jump · esc cancel               │
└──────────────────────────────────────────────────────────────────┘
```

- **The window follows the pointer, and only the pointer.** It moves when the
  focus leaves it — up to meet a pointer above, down one option at a time to
  reach one below — and stands still while the pointer moves inside it. A
  list that re-centred on every keystroke would be unreadable.
- **It is therefore path-dependent**, and deliberately so: an option reached
  from above sits at the foot of the window, the same option reached from
  below sits at its head. Neither is a jump.
- **Markers count what they hide** (invariant 4) — `↑ 2 more` / `↓ 8 more` —
  counting options rather than rows, because an option is what the pointer
  can be scrolled to. Group rails (§18a) are labels for options and are not
  counted; a run that hid nothing selectable keeps a bare `…`.
- **Everything pinned comes off the budget first**: the query line above the
  list, the key hints below it, and — in §4c — the note field, so a long list
  scrolls rather than pushing the note off the card. The card's total height
  is the bottom panel's accounting and the window may never buy itself a row.
- A list that fits is not windowed at all: no markers, and no row spent
  saying that nothing was hidden.

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

### 4d. Plan card (S-103)

Plan approval, with the plan priced above the options. A plan is the
cheapest place in the product to disagree with an agent, so it states its
whole blast radius once and shows the consequence of the option you are
on — and no other.

```
┌─ Plan · make the round limit recoverable ─────────────── 4 steps ┐
│ 1 Locate the round accounting                         read only  │
│   internal/agent/loop.go · internal/agent/round.go               │
│ 2 Add a RoundsExhausted sentinel                ✎ creates 1 file │
│   internal/agent/errors.go · new type, no signature changes      │
│ … 2 more steps                                                   │
│                                                                  │
│ 3 files touched · no deletes · no network · reversible           │
├──────────────────────────────────────────────────────────────────┤
│ ❯ 1. Run the whole plan — accept-edits mode                      │
│     edits apply as they come; commands and other actions ask     │
│   2. Run it unattended — auto mode                               │
│   3. Step through it — manual approvals                          │
│   4. Keep planning — tell me what to change                      │
│   5. Reject the plan                                             │
│ ↑↓/jk move · enter select · 1–5 jump · s save · esc keep planning│
└──────────────────────────────────────────────────────────────────┘
```

- **Steps.** Numbered title in body (252), the intent right-aligned in
  the same row, the paths beneath in dimmer (241). The intent uses the
  §14 tool glyphs and no others: `✎ edits/creates/deletes N files`,
  `$ runs`, and the two that persist nothing — `read only`, `network` —
  which carry no glyph at all. The intent is dropped before a title is
  clipped; a title cut in half says less than a missing label does.
- **Paths are never guessed.** A step renders the files plan mode's
  `files:` line named and nothing else. No paths, no row.
- **Summary.** One computed line: files touched, deletes, network,
  reversibility — the last from the same git-tracked check as §2. Its
  qualifier (`— every file is tracked in git`) is dropped rather than
  clipped when the terminal cannot carry both.
- **Only the focused option explains itself.** Five consequences stacked
  at once is a wall, not a choice.
- **Every execution option names the mode it enters.** Accepting a plan
  is never an unstated mode change.
- **Height.** The card gets 60% of the terminal, not §1's 40%: an
  approval card's context is the transcript behind it, but a plan card's
  context *is* the card. Steps are what shrinks, dropped whole and
  counted (`… 2 more steps`); the options and keys never are.
- **A plan with no structure still renders** — as prose with the same
  options below it, and with nothing claimed about a radius that was
  never parsed.

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

It is also where the rows that offer keys without expanding are answered: a
turn's changeset row (§16) and a provider failure (§17a). Both are passive
renderers, and holding their keys here is what lets the input keep `v`, `u`,
`r`, `c`, `e` and `p` for typing. `ctrl+e` opens on the failure that ended a
turn where there is one, rather than on the close rows after it.

### 7a. Where the keyboard is (S-115)

There are two panes and one keyboard, and every rule here follows from
saying which pane has it.

**The input's rule: while the prompt has the keyboard, the transcript hears
no keys at all.** Not the arrow keys, not the pager letters, nothing. A
viewport handed every keystroke scrolls the history out from under the
sentence being written — bubbles binds `j`, `k`, `u`, `d`, `f`, `b` and the
spacebar by default, so "just find the buffer" paged the transcript four
times on its way into the box. Anything the transcript can be moved by is
therefore a gesture or a key a draft cannot produce.

**The transfers**, and there are only these:

| | |
|---|---|
| `ctrl+e` | reading mode, cursor on the last selectable row |
| `pgup` / `pgdn` | reading mode, paged in that direction |
| `↑` on an empty draft | reading mode — *only* where the input history has nothing left to recall |
| wheel | scrolls, and transfers nothing |

`↑` belongs to the input history wherever there is one; that convention is
older than this surface, and `pgup` is the transfer for a session that has
one. `pgdn` with nothing below is not a transfer either: the bottom of the
transcript is where the prompt already stands.

The wheel is the exception that proves the rule. It reads, and reading is not
a decision: the draft keeps the keyboard, so a scroll mid-sentence never
swallows the next keystroke. It reaches the full-screen diff (§3c) and review
mode (§16a) when those own the screen, because the transcript behind them is
not what is being looked at. Mouse reporting costs the terminal's own
click-drag selection, which is a real trade and so a real setting: `/ui mouse
off` gives it back and leaves the keyboard as the only way through.

**Reading mode is focus mode**, not a second, lesser one. A pager key that
opened its own surface would be a fourth list implementation by another name,
and the row cursor, the `[enter]` expansions and the keys a close row or a
failure offers all have to come with it. A transcript with rows but nothing
expandable in them opens without a cursor — prose is read, not navigated —
and `j`/`k` are a line of scroll there. Only an empty transcript still
refuses, because there is nothing to open onto.

**The ways back are `esc` and typing.** Esc is the safe answer everywhere
(invariant 3). Typing is the one a reader reaches for without thinking, so
any printable character that is not reading mode's own hands the keyboard
back *and lands in the draft* — the keystroke is not spent on the exit. The
letters reading mode keeps are its own work: `j`/`k`, `q`, and a row's offer
keys while the row under the cursor actually offers them. Where it does not,
`v` is a letter again.

**The rail says which pane has it.** The line under the header is a plain
divider while the input does and carries the transcript's name when the
transcript does:

```
──────────────────────────────────────────────── READING 4/12 ─
```

The word carries the meaning and the accent is decoration (invariant 1); too
narrow for the word, it goes back to a divider rather than clipping it, since
the hint bar under the transcript says the same thing in full. The two panes
are never both dressed as the active one: reading mode replaces the framed
input (§12) with its hint bar, so the frame's own accent is absent exactly
when the rail is present.

The start screen (§17c) is where all of this is introduced, on a second key
line under the suggestions. That line outlives the typing that dismisses the
suggestions, because these keys outlive it too.

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
| `round 7/25` (dim 241) | rounds used of the limit, `+10` beside it while a round-limit pause offers more (§17a) |
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

### 9a. Agent list (`/agents` or `ctrl+a`, S-077 · S-111)

```
┌─ Agents ─────────────────────────────── 1 needs you · 1 running ─┐
│ ❯ ● orchestrator  this session           round 7 · streaming…    │
│   ⚠ runner-2   go test ./...   ⚠ needs you · 3 tools · $0.01     │
│     waiting approval: run go test ./internal/agent/...           │
│   ◇ writer-1   docs/loop.md      ▰▰▱▱▱ 2/5 · 6 tools · $0.02     │
│   ✓ reader-3   survey internal/ui    done · 12 tools · $0.04     │
│     the rails and the frame are one component                    │
│   ✗ patcher-4  apply patch           failed · 1 tool  · $0.01    │
│     round limit (25) reached                                     │
│                                                                  │
│ enter attach · a answer · x cancel · X kill · esc back           │
└──────────────────────────────────────────────────────────────────┘
```

- One row per agent: state glyph (`●` current, `◇` running (12), `⚠`
  blocked (9), `✓` done (10), `✗` failed (9)), name, task label, live
  progress, spend.
- **A row's progress is a fan-out lane's progress** (§9g), from one
  renderer: the meter where the spawn declared a step count and the
  spinner where it did not, the outcome word once the child settles, and
  the same `tools · spend` behind it. What the transcript says about a
  child and what the manager says about it cannot drift apart, because
  they are the same function. The orchestrator is not a child and keeps
  its own status text.
- The title rail carries the same tally the fan-out header states — whoever
  needs an answer first, and in del.
- **Blocked children sort to the top below the orchestrator** and say what
  they are waiting for on the line beneath. `⚠ needs you` without saying
  what for sends the reader looking, and so does `failed`: a settled row
  states the reason under it the same way.
- `[a]` answers a blocked row's approval **here**. The card (§2, §9c)
  renders over the list and hands the list back on either answer —
  opening the manager *because* something needs you must not then send you
  into that child's session to say yes. `[g]` and `[ctrl+a]` drop from the
  card's hints: the manager is already what is underneath.
- `[r]` runs a failed child again on its original task. The attempt is what
  restarts, not the agent: it keeps its name, its place in the batch and its
  transcript, and gets a fresh conversation, a fresh worktree if it writes,
  and a fresh token budget — an attempt that inherits the spend that killed
  it fails again before it has done anything. The earlier spend is still
  counted; the reason the attempt failed stays above the retry.
- `[a]` and `[r]` are offered only on rows that can act on them, and the hint
  run states what the *focused* row can do. A key that does nothing is not an
  offer.
- `enter` attaches, `x` cancels the agent's current turn, `X` kills the agent
  behind an inline confirm (§5) that states what survives as well as what does
  not — its transcript stays and the other agents keep running.
- The list is a live view — statuses update while it is open, and it opens
  over a running turn like every other surface in §9 (§9f).

**Deviation from `ui_kits/cockpit/Agents.html`.** The artboard's manager binds
kill to `[k]` and adds `[K] kill all`. Both are corrected here: `x`/`X` are what
the manager has always meant, a rebind would silently retire muscle memory, and
killing every child at once is Ctrl+C's job (§9f) — a list row is the wrong
place to reach for it. The artboard is left for a design-side pass.

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

### 9g. Fan-out lanes in the transcript (S-110)

Children only exist inside a parent turn, and until this story they wrote
their spawn rows into that turn's feed one at a time. Three children read as
one confused stream. A round that spawned two or more collapses to one block —
one lane each, updating in place:

```
 ◇ fan-out   3 agents                              1 needs you · 2 running 1m12s
   ⚠ agent   scout-3  other ErrRoundLimit …  ⚠ needs you · 3 tools · $0.01   18s
    waiting approval: read ../shhh-plugins/registry.go
   ◇ agent   writer-1  docs/loop.md            ▰▰▱▱▱ 2/5 · 6 tools · $0.02   12s
   ✓ agent   tester-2  internal/agent tests         done · 9 tools · $0.03   41s
    all four packages pass
    [ctrl+a] agents
```

- One block per *round*, not per session: a later round's children are a
  later block. A round that spawned one child keeps its inline row — a block
  is for genuine fan-out.
- The header names the size of the fan-out and what it still owes you.
  Whoever needs an answer is said first and in del; the tally of finished
  children is left to the lanes until nothing is running, when it becomes the
  whole story. The field never clips (§6a), so it says two things at most.
- A lane sits on the §6a grid with `agent` in the verb column — §6c's name
  for a child's row in the parent transcript, which is exactly what a lane
  is. **The child's name goes in the target field**, not the verb column:
  a name is not a word from a closed vocabulary, and clipped to eight columns
  `researcher-1` and `researcher-2` become the same string. The task follows
  it, dimmed, and is what clips as the terminal narrows.
- A lane draws its five-cell meter only where the spawn declared a step count
  (`steps` on `spawn_agent`, §9h); without one it spins beside the word
  `working`. A ratio nobody supplied is never invented (§10c, S-094).
- Blocked lanes sort to the top and say `⚠ needs you` in words, with what
  they are waiting for stated underneath and `[ctrl+a] agents` offered once
  under the block. The block scrolls with the transcript, so the answer lives
  in the manager rather than in the lane — since S-111 that is one key away
  and does not cost a detour through the child (§9a).
- A settled lane stops drawing progress, which no longer measures anything,
  and keeps its outcome and the first line of its report instead.
- The block is a passive transcript entry: it stores the batch number and
  reads the lanes off the supervisor every render, which is what lets it stay
  live and re-render at any width. It is its own block rather than a step
  member, because a finished step folds (§13b) and the children outlive the
  step that spawned them — a fold must never hide a child that needs you.

**Deviation from `ui_kits/cockpit/Agents.html`.** The artboard draws the lanes
under the step header that spawned them, with the child's name in the verb
column. Both are corrected here: names longer than eight columns are the
common case, and the fold that comes with living inside a step would hide a
blocked lane. The artboard is left for a design-side pass.

### 9h. Declared step counts (S-110)

`spawn_agent` takes an optional `steps` — the number of steps the task breaks
into — and the lane shows progress against it. A child's step is §13's step:
an announcement followed by its calls, counted in the child's own transcript.
A count outside 1–20 is dropped rather than clamped, because a clamped
denominator is an invented one.

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
  The join is made once, when the assistant announcement that heads a batch of
  calls is appended: it is matched against the steps still unclaimed, and the
  entry is stamped with the number of the step it carries out. A declared step
  then takes the plan's number **and the plan's title**, so the outline, the
  rail's PLAN block and `/plan` are reading one list. Steps are claimed once
  each and in the order the run reached them, so a step carried out early keeps
  its own number where it happened rather than being renumbered into place;
  steps nobody has reached trail the turn as queued headers; and an
  announcement matching no declared step is marked `+` in the ordinal column —
  work off the plan is shown as such, never renumbered into it.
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

The ordinal column carries the plan's number for a declared step, the outline's
own count where no plan is running, and `+` for a step the running plan never
declared.

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

  PLAN                             2 of 4 done
  ✓ Locate the round accounting           6.2s
  ✓ Add a RoundsExhausted sentinel       38.1s
  ▸ Return it from runRound
  · Offer more rounds in the chat model
  ⚠ 1 off plan
  /plan for the whole list

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
| PLAN | an approved plan's steps as a live checklist — state glyph, title, elapsed per finished step, a drift note, and `/plan` for the whole list |
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
  handled by the host. PLAN prints `/plan` rather than a bracketed key: the
  input textarea owns every unmodified letter, so a `[p]` there would be an
  offer nothing accepts.
- **PLAN is the one block that is not turn-scoped.** It follows the approved
  plan, which can outlive the turn that started it; a plan through its list is
  retired by the next instruction, and `/plan drop` forgets one early. Below
  130 columns there is no rail, and `/plan` is the whole checklist — nothing is
  lost, it just has to be asked for.
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
- One turn ending does not close twice, and one thing closes a turn instead of
  this block: a turn that stopped at its tool-round ceiling ends on §17a's
  `rounds` row, which says the same three things and offers the way on as well
  (S-109).
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

A provider failure is classified before it reaches any surface (S-106,
`internal/provider/failure.go`) into a closed vocabulary of nine: `unauthorized`,
`rate limited`, `quota exhausted`, `overloaded`, `context too long`, `network`,
`malformed response`, `cancelled`, and `unclassified` for everything the table
has no case for. The classes are the provider package's; the keys are the UI's.

```
   ✗ model   gpt-4o · 401 unauthorized                key ···4f9c rejected  0.3s
    Incorrect API key provided
    [e] enter a new key · [p] switch provider · nothing in the turn was lost

   ⚠ model   gpt-4o · 429 rate limited                        retry in 38s  0.3s
    Rate limit reached for gpt-4o. Please try again in 38s.
    [r] try again · [p] switch provider · nothing in the turn was lost

   ✗ model   gpt-4o · 400 context too long                 over the window  0.3s
    This model's maximum context length is 128000 tokens
    [c] compact now · [r] then try again · compacting keeps the plan and the recent turns

   ✗ model   gpt-4o · 400 unclassified                      message below   0.3s
    Unknown parameter: 'reasoning.effort'
    [r] try again · [p] switch provider · nothing in the turn was lost
```

The verb `model` occupies the same 8-column field as a tool verb (§6c) and the
row obeys the same grid, so a failure reads as part of the turn rather than as
an interruption of it. The pointer and mutation-rail columns stay blank: a
failed request changed nothing.

- **The target names the model, then the class.** The model is body text, the
  class dim behind it — the one place the grid's single-styled target field is
  split (`gridLineWith`), because which model failed and how are two facts.
- **The outcome is the one thing that decides what to do next**, never a repeat
  of the class: `key ···4f9c rejected`, `retry in 38s`, `the account, not the
  rate`, `over the window`, `never reached it`. It right-aligns and never
  clips.
- **The detail body is the provider's own words**, bounded to three lines at
  indent 4. It is why `unclassified` is a class rather than an error path: the
  message that could not be named still gets said, and the outcome
  (`message below`) points at it.
- `⚠` (accent 214) is a stall the session comes back from — rate limited,
  overloaded, network. `✗` (del 9) is a call that is over until you do
  something. `⊘` (dim 241) is a stop you asked for. All three carry the state
  in words as well, so the palette is reinforcement (invariant 1).
- **Nothing in the turn is lost**, and the row says so in words. A class where
  that is not the useful sentence says the useful one instead: quota says
  `waiting will not clear this one`.
- Offered keys are info (12) at indent 4. **A key the session cannot honour is
  not offered** — no `[k]` without a way to replace the key, no `[p]` without a
  second provider to switch to — because an offer that does nothing is worse
  than no offer.
- **The keys are handled by focus mode on the row** (§7), the way the changeset
  row's `[v]` and `[u]` are (§16), so the input keeps all four letters for
  typing — which matters more here than anywhere else, since "run the tests
  again" and "check what it did" are exactly what gets typed after a failure.
  `ctrl+e` opens on the failure rather than on the close rows that follow it:
  those are chrome about a turn that broke, and this is the row holding the way
  out. Entering a key is `[e]`, not `[k]`, because `k` is the focus cursor's
  own.
- `[e]` opens a masked prompt in the bottom panel — a bullet per rune, the key
  never echoed, the replaced key named only by its last four characters. It
  takes effect for the session; esc keeps the key that was already there.

The one-shot renders the same row from the same classification, with the way
out stated as a command (`export OPENAI_API_KEY`, `shhh config set
provider.api_key`) rather than as a key, because nothing is listening for one
by then. Piped output gets one classified line and no chrome.

Two more verbs share this field: `stream` is S-107's and `rounds` is S-109's.

```
   ⚠ stream  dropped mid-reply · ~1,204 tokens kept · 2 tool calls  partial  11s
    …so I'll thread the sentinel through runRound and then
    [c] continue from here · [r] ask again from scratch · the partial reply stays

   ⚠ rounds  25 of 25 used · the turn's own bound            stopped 4m12s
    3 files changed +30 −4 · the suite has not been re-run since
    [v] review what it did · [+10] ten more rounds · [u] undo the turn
```

- **A drop is a second row, not a replacement for the first.** Whatever broke
  the stream still gets its `model` row above, with its class and the
  provider's own words; the `stream` row under it is only the offer. One row
  cannot both name a network failure and describe a reply, and the two are
  answered differently.
- **`[c]`, not the `[enter]` this section used to draw.** Enter is how the
  input sends what you just typed, and a row cannot have it while there is a
  draft under it — the same reason replacing a key is `[e]`. Both keys are
  pressed in focus mode, like every other recovery key.
- **The count is an estimate and says so.** A request that dropped never
  reported usage, so the `~` is `len/4`, the same arithmetic §15a's occupancy
  uses. Finished tool calls are counted beside it, because they change what
  continuing means: with calls, continuing *is* the round, resumed.
- **Only finished calls are kept.** A call whose arguments stopped halfway is
  a fragment of a decision the model never made, and running it would be worse
  than losing it (`internal/provider/partial.go`).
- **Taking the offer spends it.** The row keeps its words and loses its keys,
  because the conversation has moved past the partial and sending it again
  would send the model its own reply twice.

The `rounds` row is the odd one in this section: nothing failed. The turn
reached a bound the session set for it, so the row is a checkpoint rather than
a report of a break — and it is the *only* recovery row that stands where a
turn's close block would be (§16).

- **It replaces the close, it does not sit above one.** It already answers what
  the turn did, what it changed and what the ways on are; a close block beside
  it would offer `[v]` and `[u]` twice on adjacent rows. A turn that is granted
  more rounds closes in the ordinary way when it really ends, once, for the
  whole turn.
- **The qualifier is what makes this number this number** — `the turn's own
  bound` on the first stop, and `10 already granted` on the ones after it,
  because `35 of 35 used` on its own reads like a limit nobody chose. It is
  *not* a Go sentinel: no identifier from the source reaches the transcript,
  which is the same rule §17a applies to a provider's error strings.
- **Every clause is conditional on the thing it names.** A turn that changed
  nothing says so rather than reporting three zeroes, and one whose edits are
  still covered by a check says nothing about the suite. `[v]` and `[u]` are
  offered only when there is a changeset to act on.
- **`[+10]` draws the grant, not the keystroke.** The key is `+`, which is what
  focus mode's hint line names; the bracket says what pressing it buys. Taking
  it continues *the same turn* — nothing is added to the conversation, the
  counter is not reset, the changeset goes on collecting under the same turn
  number — so the turn is priced as one thing and `[u]` still takes all of it
  back.
- **The grant expires with the turn.** A new user message gets the configured
  ceiling back and spends the standing offer, because a turn the session has
  moved past cannot be given more rounds.
- **The counter on the rail is part of the surface** (§8a): `round 25/25 +10`
  while the offer stands, `round 25/35` once it is taken. The bound and the
  price of lifting it are both on screen for the whole decision.

A request that was never answered has nothing to keep, and waiting is the
whole remedy. It grows a countdown under it instead of an offer to continue:

```
   ⚠ model   gpt-4o · 429 rate limited                        retry in 20s  0.3s
    Rate limit reached for gpt-4o. Please try again in 20s.
    ▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱ retry in 12s · attempt 1 of 3
    [m] finish this turn on gpt-4.1 · [esc] stop and keep the 3 edits
```

- **The meter is §10c's countdown**, twenty cells in accent, draining right to
  left, with the seconds stated beside it in whole numbers — a tenth of a
  second flickering twice a second is noise, not precision.
- **The bound is on screen, every attempt.** Three automatic retries, counted
  across the stall and reset by any request the provider actually answers. A
  limit you cannot see is indistinguishable from a hang.
- **The row above hands over its keys while the wait runs.** Two sets of
  offers for one stall would be two answers to the same question; when the
  wait ends the row has them back.
- **The wait owns the keyboard.** Nothing is streaming and the input is not
  live, so `[m]` can be a bare letter here where it could not be on a row.
  `[esc]` stops at any point and says what stopping keeps.
- **`[m]` names the closest cheaper model in the provider's own catalog**
  (S-083/S-084's metadata) — closest rather than cheapest, because the point is
  to finish the turn and the least capable model is the least likely to. It is
  never invented, never from another provider, and not offered at all when the
  pricing table cannot rank the model in hand. Taking it switches and resumes
  immediately: the limit belonged to the model being left behind.
- **A model switched mid-turn is on the record twice** — in the transcript, and
  in `/stats`, which splits the session's spend per model as soon as there is
  more than one. A turn that finished on two models was two things, and
  pricing it as one is a number nobody can reconcile.

### 17b. The two cards

A card is warranted only when the session cannot continue without an answer.

```
┌─ No model provider configured ─────────────────────────────────────────┐
│ shhh looked in four places:                                            │
│   ✗ env       SHHH_API_KEY, OPENAI_API_KEY — unset                     │
│   ✗ config    ~/.config/shhh/config.toml — no provider api_key         │
│   ✗ profiles  no .toml in ~/.config/shhh/providers                     │
│   ✓ local     localhost:11434 — llama3.3, qwen2.5-coder                │
│                                                                        │
│ the local runtime is already answering — that is the quickest way in   │
├────────────────────────────────────────────────────────────────────────┤
│ [enter] setup wizard   [p] paste a key   [o] use llama3.3 locally      │
└────────────────────────────────────────────────────────────────────────┘
```

The card names **every place shhh looked and what it found there**, then says
which one is the likely fix. A missing-key message that does not say where it
looked is a message that cannot be acted on — and "SHHH_API_KEY or
OPENAI_API_KEY is not set", which is what this replaced, names two of the four
and none of the findings.

The four places are the search the resolution actually does
(`internal/resolve/survey.go`), in its own order: the environment variables the
dialect reads, the config files in search order, the gateway profile
directories (S-084), and a local model runtime, probed once with a bounded
request to `GET {base}/models`. A key that was found is named by its last four
characters and never by more.

- **`✓` is "something was there", not "it worked"** — only a request can answer
  the second question. A profile that loaded with its key variable unexported
  is the failure that looks most like no provider at all, so it is reported as
  a profile that was found with the variable named.
- **Every offer is one that can be honoured.** `[o]` appears only when
  something local actually answered, and names the model it would start on.
  `[enter]` picks a provider and takes a key; `[p]` takes a key for the
  provider that was already resolved. Both ask afterwards whether to save it,
  so meeting this card twice is a choice.
- **A terminal that is not one gets the same information printed plainly** and
  no offers, because there is nobody there to press a key.
- Esc declines, and the session exits on what it came in with — nothing is
  written, and nothing is printed twice.

```
┌─ Context is nearly full ──────────────────────── 94% · 188k / 200k ────┐
│ ▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱▱                                                 │
│                                                                        │
│ 88k  tool output — 6 results                                           │
│ 54k  the conversation — 14 messages                                    │
│ 31k  system prompt                                                     │
│ 15k  project context                                                   │
│                                                                        │
│ compacting keeps the plan, 3 changed files and the last 2 turns        │
│ and drops the older turns and their tool output — recovers about       │
│ 96k (48%)                                                              │
│ keeping going asks nothing further — the oldest tool output is elided  │
│ before each request from here, and what falls out does not come back   │
├────────────────────────────────────────────────────────────────────────┤
│ [enter] compact now · [n] new session · [esc] keep going               │
└────────────────────────────────────────────────────────────────────────┘
```

This is the only place in the product that itemises token spend, because it is
the only place where you can act on it (S-108).

- **The categories are S-093's accounting**, in the card's own words rather
  than in the field names: tool output, the conversation, the system prompt,
  the project context inside it, the tool definitions. They are exhaustive by
  construction, so they sum to the total on the title rail; a category with
  nothing in it is dropped rather than printed as a zero, and one the
  accounting cannot characterise loses its clause rather than gaining an
  invented one. Largest first — the biggest is the one you can act on.
- **The meter is the inspector rail's meter**: `MeterCellsRail`, `MeterPressure`,
  and the session's own trim thresholds, which is what makes the card and the
  two rails turn colour at the same two numbers (§10c). The frame takes the
  meter's colour, so the bar and the numbers beside the title are one field
  in the sense that matters — they change together.
- **The prediction is arithmetic, not a promise.** What compaction keeps is
  named only where the thing exists: the plan when one is being carried out,
  the changed files when the changeset has any, the turns the tail will
  actually hold. What it drops is read off the same accounting. The recovery
  is the total less what survives, less an allowance for a summary that has
  not been written — hence *about*.
- **`[esc]` keeps going: invariant 3 holds even at 94%** — and the card says
  what that costs before you press it. With tool output to trim, S-055 elides
  the oldest of it before every request from there on and nothing asks again;
  with none left, the first request that overruns the window fails instead of
  shrinking (§17a's `context too long`).
- **Once per crossing, not once per turn.** It is raised as a turn closes, by
  the same transition that appends the close rows, and re-arms only after the
  occupancy has fallen back under the threshold. It waits rather than steals:
  a surface already on screen, an attached child, or a steering message queued
  for the next turn all defer it to the next turn's end.
- **The warning threshold keeps what it had** — a colour change in the rails
  and nothing that stops you.

### 17c. First contact

A first launch in a repo shhh has never seen already knows the repo, and
offers work rather than a blank prompt:

```
shhh 0.9.4                                          [?] keys · [q] quit
──────────────────────────────────────────────────────────────────────

~/src/shhh · go 1.24 · git main · 3 files changed · 41 packages

context  AGENTS.md — in the system prompt
gate     default — vet, test · runs without asking

Some things worth doing first:
❯ ▸ pick up (last session) — 7 turns · $0.42 · 4m ago
  ⚙ explain what changed in the working tree — reads only, no writes
  ⚙ run the default quality gate and triage what fails — one approval,
    then it reports back

[↑↓] choose · [enter] start · or just type what you want
[pgup] or [ctrl+e] read the transcript · [esc] or type to come back · [ctrl+k] palette
```

- The header line is what shhh already knows: path, toolchain, branch, dirty
  state, package count. Clauses drop from the right as the terminal narrows
  and the path is never one of them — a header that cannot say where it is has
  nothing left to say. A count the bounded walk cut short reads `41+`.
- Two labelled notes follow, because both govern what happens next without
  being asked: what was read into the system prompt, and which quality-gate
  suite is in effect. A gate that is not configured names the file it looked
  for; one that exists but does not load reads `unreadable`, because a broken
  gate is not an absent one.
- Suggestions are ordered by what the working tree suggests — a session to
  pick up first, then a read-only offer, then one that needs a single
  approval. There are always three, and each says what it will cost you in
  permission. The resume offer is priced from the observability record that
  covers it; without one the price clause is dropped rather than printed as
  `$0.00`.
- Focus is the `❯` pointer, not only the highlight: a background survives no
  monochrome terminal, and the row's own `▸`/`⚙` already means something else.
- `enter` types the focused suggestion into the input and submits it, so
  choosing an offer and typing it are the same act down to the dispatch.
- Typing anything dismisses the list — the offers and their keys go, the facts
  stay — because the input owns every ordinary key and `↑` belongs to the
  input history again the moment there is a draft.
- **The second key line is navigation** (§7a), and it outlives that dismissal,
  because its keys do: the wheel, `pgup` and `ctrl+e` all work with a
  half-written draft in the box. This is the one screen every user sees, so it
  is where the two panes are introduced.
- The screen is spent by the first thing the session says to the model, or by
  a conversation loaded into it. `/clear` after either does not bring it back;
  `/clear` on a session that never said anything does, because that session
  really is still new.

---

## 18. Reach (E-018: S-112–S-114)

### 18a. The palette (S-112)

`ctrl+k` opens one prompt over everything the session can reach. `/` is for a
command you are already typing; the palette is for one you are looking for.

```
┌─ Palette ──────────────────────────────────────────── 14 results ┐
│ ❯ mod█                                                           │
│ COMMANDS                                                         │
│ ❯ /model                                                         │
│     Switch the model (bare /model opens a picker)                │
│   /mode  shift+tab                                               │
│ SESSIONS                                                         │
│   loop-refactor                                                  │
│ FILES                                                            │
│   internal/agent/model.go                                        │
│ … 6 more — keep typing                                           │
│ enter run · tab complete · ↑↓ move · esc dismiss                 │
└──────────────────────────────────────────────────────────────────┘
```

- It is the §4a single-select with a query line above it, not a fourth list:
  same card, same pointer, same bottom-panel accounting. What it adds is the
  query row, the group rails, and a result count on the title rail — the count
  is of *matches*, not of rows showing, because the rail is where you find out
  that there are more.
- Three groups, always in this order: **COMMANDS** from the S-078 registry
  with their descriptions and their key bindings, **SESSIONS** from the saved
  chats, **FILES** from the paths this session changed and the checkout's most
  recently modified files. The dynamic two are read when the palette opens and
  never per keystroke (S-079's rule).
- Matching is subsequence across all three groups; an exact command name
  outranks everything, so a command typed in full is never left under a longer
  sibling. When the panel cannot hold every match, each group keeps a share of
  it rather than the first group taking the card — a query that found
  something in three places has to say so.
- `enter` runs, `tab` writes it into the draft, `esc` dismisses and keeps the
  draft. A file has nothing to run, so both keys append its path to the draft.
  Everything else is text: `j` is a letter and `2` is a digit, which is why
  the card is unnumbered here.
- **An unavailable command is dimmed, not dropped.** While a turn runs, the
  commands that would rewrite the conversation it is working in (§9f) render
  behind `⊘` with the reason on the description row, and choosing one answers
  with the notice that names what it would disturb. The completion menu drops
  them because it is completing what you are typing; the palette keeps them
  because it is where you look when you cannot find a command, and "it is not
  here" is the answer that sends you hunting.
- It opens over a running turn like the rest of §9f's live surfaces, and the
  turn keeps streaming underneath. Attached to a child (§9b) the key keeps its
  textarea meaning: the orchestrator's commands are not what the keyboard is
  pointed at.
- `ctrl+k` takes the binding from the textarea's delete-to-end-of-line, which
  is the one key the input gives up for it.

### 18b. The one-shot result (S-113)

Most people meet shhh here rather than in the agent: one prompt, one command,
one decision. The screen printed the command and waited for a key. It now
explains the command by default, says what the command can reach, and moves
the default when the answer is destructive.

```
❯ shhh find every process listening on a port above 8000

$ lsof -nP -iTCP -sTCP:LISTEN | awk '$9 ~ /:[89][0-9]{3}$/'
  lsof lists listening TCP sockets without resolving names; awk keeps the rows
  whose address ends in a port from 8000–9999.
  ⛨ writes unknown · no network · no sudo

[↵] run  [e] edit  [r] revise  [x] explain  [c] copy  [s] save  [esc] quit
```

- **The explanation is on by default**, as one line under the command: a
  command you do not understand is a command you should not run, and safe
  only-if-you-remember-a-flag is not safe. `-e`/`--explain` now buys the long
  form rather than the only form, `[x]` asks for it on demand, and
  `--silent`/`behavior.silent_mode` still suppresses both. The one line
  arrives under a live key row — blocking the keys for one sentence would make
  the default worse than the flag it replaced. Two streams answering to the
  same message types is what makes stream messages carry their stream's id: a
  cancelled explanation's last message must not be read as the next command's
  own.
- **The containment line is `internal/radius`**, the same resolver §2a's
  approval cards use, folded from three rows into one phrase: what it writes,
  whether it leaves the machine, and whose privileges it runs with. It
  inherits the resolver's honesty with it — a verb shhh cannot account for
  reads `writes unknown · network unknown`, never `read-only`, because a
  command nobody resolved is not one anybody can promise stays local. A
  safety-flagged command gets `⚠` lines above it carrying `internal/safety`'s
  own words.
- **The bar is a row of bracketed keys, not a menu.** Arrow-then-enter to
  reach a key that is already printed on the box costs two keystrokes and
  makes this the one surface in shhh where a hint is not the key. Every hint
  run in the session UI reads this way (§7, §16, §17a) and now so does the
  front door. A key the surface cannot honour is not drawn.
- **The safe default moves with the risk.** On an ordinary command enter runs.
  On a destructive one enter spends itself saying what would be affected —
  the resolved paths, described as the filesystem holds them now — and running
  takes a deliberate `y`. The keys do not change, so nothing is re-learned;
  what the default *does* changes, which is the half that matters. An
  unresolved destructive command says so in the block rather than showing an
  empty list.

```
$ find ~/src -name node_modules -type d -prune -exec rm -rf {} +
  -prune stops find from descending into a directory it just deleted.
  ⚠ recursive forced deletion — may destroy files irrecoverably
  ⛨ writes /home/you/src · no network · no sudo
  would affect
    /home/you/src — at least 20000 files, 2.8 GB

[↵] show what it would affect  [y] run it  [d] dry run  [e] edit  …  [esc] quit
```

- **`[d]` is offered only where a dry run exists** (`internal/dryrun`). The
  derivation is a closed table of commands built to be asked rather than told
  — `rsync --dry-run`, `git clean --dry-run`, `make -n`, `terraform plan`,
  `find … -print` in place of `-delete` or an `-exec` clause, `sed` without
  `-i` — because every entry is a claim that the rewrite is harmless, and a
  wrong claim runs the real command while the surface says it did not. A line
  the table cannot rewrite ends the offer for the whole command; a line that
  changes nothing rides along unaltered, since running it is what makes the
  rewrite around it mean anything. `rm` has no dry run and is never given one:
  enter's radius block is what answers for it. Output is bounded to twenty
  lines and what is past that is counted (invariant 4).
- **A revise keeps what it is being compared to.** The previous command and
  the feedback that replaced it stay dimmed above the new one — not history
  you scroll for, the thing the new answer is an answer to — with the count on
  the key row and `[u]` back to it. Stepping back restores the command, its
  explanation and the conversation, and asks the model for nothing: the
  explanation was said about that exact command and nothing about it changed.
- **A deliberate `y` is not asked for twice.** `behavior.safety_warnings`
  re-prompted on stderr after the TUI closed; the surface now carries that
  decision, so the result reports it and the second prompt is skipped for
  anything the surface already confirmed.
- **Non-TTY output is untouched.** Piped, shhh still prints the command to
  stdout and nothing else — no explanation, no containment line, no keys —
  and a failure is the one classified line §17a's one-shot paragraph
  describes.

Departures from `ui_kits/cockpit/OneShot.html`, all deliberate:

- The artboard steps back with `[b]`; this is `[u]`, which is what undo is
  called on the changeset row (§16) and in the round-limit offer (§17a). One
  letter for one meaning is worth more than the artboard's mnemonic.
- The artboard's `[a] alternatives` was S-114 and arrived after this story;
  see §18c. Until it did, its absence changed nothing about the row.
- The artboard's enter reads `list what it would delete`. The resolver
  describes writes, not deletions, so the key says `show what it would
  affect` — a command that truncates a file is not deleting it and the key
  should not claim otherwise.
- The artboard writes the destructive line by hand (`deletes directories ·
  not reversible`). It is `internal/safety`'s own warning instead, so the
  front door and the approval cards say the same sentence about the same
  command.
- The artboard's `[s]` is `save as alias`; shhh saves snippets, so it is
  `[s] save`.

### 18c. The commands it did not pick (S-114)

A generator that can only say one thing has already chosen for you. Asked for
"every process listening above 8000" the model weighs `lsof` against `netstat`
against `ss`, picks one, and throws the reasoning away — and the answer it kept
is the portable one when you wanted the fast one about as often as not. The
alternatives were free the whole time; only the surface for them was missing.

```
$ lsof -nP -iTCP -sTCP:LISTEN | awk '$9 ~ /:[89][0-9]{3}$/'
  lsof lists listening TCP sockets without resolving names.
  ⛨ writes unknown · no network · no sudo

[↵] run  [e] edit  [r] revise  [a] 2 others  [x] explain  [c] copy  [s] save  [esc] quit

┌─ Alternatives ──────────────────────────────────────────────────┐
│ ❯ ◆ lsof -nP -iTCP -sTCP:LISTEN | awk '$9 ~ /:[89][0-9]{3}$/'   │
│     the command on screen                                        │
│     netstat -anv -p tcp | grep LISTEN                            │
│     ss -ltn                                                      │
│ ↑↓ move · enter choose · esc back                                │
└──────────────────────────────────────────────────────────────────┘
```

- **The count is the label, `[a]` is the key.** `[a] 2 others` says the one
  thing worth knowing before pressing — whether there is anything behind it —
  while keeping the row's rule that the key printed is the key pressed (§18b).
  Nothing is drawn when the generation offered nothing, which is most of them.
- **The response is structured, and command-first.** JSON is the obvious
  envelope and the wrong one: the command streams onto the screen as it
  arrives, and a front door whose first frames are `{"com` is worse than the
  one it replaced. `internal/proposal` reads a line-oriented shape instead —
  everything before a `--- alternatives` sentinel is the command, exactly as
  it always was, and each line after it is another command with an optional
  `# one phrase · like this` under it. The streaming view stops at the
  sentinel, and at a line that could still become one, so the section never
  flickers on its way to the picker.
- **Parsing is total, so asking costs nothing.** A response with no sentinel
  is one choice and no alternatives — every provider and profile that cannot
  produce the section, and the fence-stripping path S-113 shipped with. The
  prompt says the section is optional in as many words, because a model that
  believes it is required will pad an `ls` with two commands it had to invent.
- **The picker is `components.Select`** (§4a, S-078), not a list this surface
  draws itself: moving, choosing and backing out are the keys they are
  everywhere else. The current command is marked `◆` in the label rather than
  by the focus bar — the reader has to be able to find it without moving the
  pointer onto it (invariant 1) — and the focused row's tradeoff renders under
  it, which is what the choice is being made on.
- **Choosing arms, it does not run.** The chosen command gets its own
  explanation, its own containment line and its own default, through the same
  `arm` every other command on this surface goes through. An alternative is a
  command like any other; running it straight out of the picker would be the
  one path to execution that skipped the screen that vets it.
- **The offers travel with the command.** A revise generates its own set, and
  `[u]` puts the old ones back with the command they belonged to (§18b). An
  edit rewrites the choice it started from and drops that choice's tradeoff —
  the phrase was said about a command that no longer exists — while the other
  offers stand, because they were alternatives to the request, not to the typo.
- **Piped, they are never asked for.** The one-shot's stdout is one command by
  contract, so the pipe path builds the prompt without the section at all
  rather than printing a response it would have to strip.

Departures from `ui_kits/cockpit/OneShot.html`:

- The artboard's picker runs on enter. Here enter chooses and returns to the
  key row, so the alternative arrives on the screen that explains it.
- The artboard's key row reads `[a] alternatives`; the count is more useful
  than the noun, and the noun is what the card is titled.
