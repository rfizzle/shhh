# shhh — TUI Component Catalog

> Visual components for the coding-agent TUI (`shhh chat` / `shhh code`).
> Companion to DESIGN.md. Implemented per backlog story S-076; consumed by
> S-048 (approvals), S-061 (plan mode), S-070 (memory), S-074 (diffs),
> S-075 (activity feed & cockpit), S-082 (input frame).
>
> **v3 — S-121, with §7c added by S-125 and §7d by S-153.** A fifth invariant, §7a rewritten,
> §7b, §7c and §19 added, and §4a, §8, §10c and §15 brought onto the artboards
> that now specify them. It continues what v2 (S-088) started — the file
> describes one grammar rather than a record of how it grew — and where the
> two disagree the artboard wins and the older text goes, rather than sitting
> beside it, except where a guideline has since restated a rule the artboard
> predates (§7c). The source is the `shhh Design System` project in Claude
> Design (projectId `8bd9b60d-8d86-403e-a591-c15a9ebccfd9`, read with the
> DesignSync tool, not from the published Artifact, which is a viewer):
> `tokens/terminal.css` for the column grid, `tokens/colors.css` for the
> palette, `guidelines/` for the rules, `ui_kits/cockpit/` for the artboards
> (`Main`, `Steps`, `Changeset`, `Edges`, `Sheet`, `Reading`, `Interrupt`,
> `Lists`, `Tools`). E-013 through E-021 implement it; nothing below exists
> until a story builds it.

---

## Invariants

Five rules, checked before anything else. A surface that breaks one of them is
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
5. **A key is inert until the surface that offers it holds the keyboard** —
   and that surface says so. The one holding it names itself in a labelled
   rail (`DRAFT`, `DECISION 1/2`, `READING 5/12`); the ones that do not render
   their keys grey beside the one key that hands the keyboard over. Invariant
   3 depends on this one: `esc` can only be the safe answer if it reaches the
   surface you think you are answering, and the same rule keeps `[v]` from
   reviewing a turn while you are typing the letter v. Checkable: cover the
   colours, and if two surfaces are on screen and neither names the keyboard's
   owner in words, the screen is under-specified (§7a, §7b, §12a).

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

A card arrives when the agent needs it, which is not always when the reader is
ready for it. What it may and may not do to a half-typed draft — and when its
letters become live keys at all — is §7b, and it applies to every variant
below.

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
│ Run this command? [y/N] · [n] deny — the safe answer                    │
│ [a] always — not offered: a safety-flagged command is never pre-approved│
│ [esc] back to your draft — the decision stays waiting, nothing is denied│
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
- **`[a]` grants the shape of the call it is showing** (S-054, S-138), not the
  category: on a command card it records the command's leading bare words
  (`always allow "go test"` for `go test ./internal/ui/...`), and on an edit
  card the edited file's own directory (`always allow edits in
  internal/ui/chat/`). The scope is written on the key row, because a key
  whose scope is not stated is a key pressed on a guess.

  It used to grant the whole category — every command, or every edit, for the
  rest of the session — which put the only rung above "this once" out of reach
  of anyone who wanted to stop being asked about one thing. The blanket grants
  still exist; they are `/permissions allow <commands|edits>` now, because a
  decision about every call the rest of the session will make is one to type,
  not one to press while a card is in front of you. `/permissions grants`
  lists what has been granted and `/permissions revoke` takes it back — the
  way out a session grant did not have, since switching back to manual mode
  never cleared one (the grant is consulted before the mode is).
- `[a]` is absent for flagged actions — they can never be pre-approved
  (S-059) — and the card says so in a footnote. A missing key with a stated
  reason teaches; a missing key without one reads as a bug.
- The keys sit below a `├───┤` rule so they never blend into the body, and
  where the safe answer is not obvious from `[y/N]` the card names it in
  words (`[n] deny — the safe answer`). It names `[n]` rather than `esc`
  because esc on this card hands the keyboard back to the draft and leaves
  the decision waiting (§7b) — which the last hint row says, since a key
  whose meaning changed is worth a row.
- **The card is inert until it holds the keyboard** (invariant 5) — where
  there is a draft to protect. Arriving beside a live sentence it is drawn in
  the gated state above: dimmed keys, `not live yet`, the `[ctrl+g]` that
  hands the keyboard over. Arriving on an empty, idle draft it holds the
  keyboard itself and answers `[y]`/`[n]` with no chord in front, keeping
  `[a]`, `[d]` and `[A]` behind the handover. Both states, and why, are §7b.

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
│ Run this command? [y/N] · [n] deny — the safe answer                   │
│ containment is off for this session · /sandbox doctor explains why     │
│ [esc] back to your draft — the decision stays waiting, nothing is denied│
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

- Syntax highlighting via the existing `highlight.go` (chroma for the
  tokenising, the palette for the colours — §10a's syntax register), then diff
  coloring layered over it: additions green (10), deletions red (9), hunk
  headers cyan (14), line numbers gray (241). The register and the gutter are
  two layers, not two palettes: the row's own four tokens stay on the marker,
  the number and the hunk header, and the body underneath is info, accent,
  bright, body, dimmer and dim.
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
- **Every row carries its own description**, gray (241), in a column of its
  own after the label (S-126). A catalog you have to walk to read is a
  catalog you cannot compare, which is the whole reason `/model` shows prices
  at all. The one exception is the plan card, where the description is the
  consequence of taking the option rather than a property of it (§4d).
- **The short field at the end of the row** is right-aligned against the card
  edge: a key binding, a price, `this one`, the reason an unavailable row is
  unavailable. It is one clause and never a sentence, and it survives when the
  description gives up its width — the description is the row explaining
  itself, the short field is a label on the row.
- The description column is measured over the whole list, not over the window,
  so it does not shift under the reader as the window slides.
- Number keys select immediately, and they count the list rather than the
  window: option 12 is `12.` whether or not it is the first row showing. The
  numbering is right-aligned in a column of its own, so option 9 and option 10
  start their labels in the same place.
- Group rails (`COMMANDS`, `SESSIONS`, `SESSION`, `WORKSPACE`) are labels the
  pointer steps over, not options. An option that cannot be acted on right now
  is dimmed behind `⊘` with its reason, never dropped (invariant 4).

**A choice with two readings gets two keys** (S-136). `/model` is the one card
where taking an option means one thing now and another thing from now on, and a
card that silently picked one of those would be making the decision for the
reader:

```
┌─ Switch model ──────────────────────── 24 available · 8 showing ─┐
│  ❯ 2. claude-sonnet-5     better diffs · $3 / $15                │
│    3. gemini-3-flash      1M ctx · $0.30 / $2.50                 │
│                                                                  │
│ ↑↓/jk move · enter this session · d and make it default ·        │
│ / filter · esc cancel                                            │
└──────────────────────────────────────────────────────────────────┘
```

- **Both are offers, so neither is ever dropped** as the card narrows — the
  width ladder sheds the number-jump reminder and then `j/k`, exactly as it
  did with one key.
- **Naming the second key means naming the first.** `enter select` becomes
  `enter this session` the moment `d` names something specific, or enter is
  the unlabelled half of a pair.
- **The alt key is a bare letter**, so it is text while the filter row is open
  — the rule `j`/`k` already follow. A model name with a `d` in it types.
- **A key that cannot be honoured is not offered**: a session with nowhere to
  write a config file gets the card with `enter select` and nothing else.

**A list longer than the panel is a window onto the list** (S-116;
`ui_kits/cockpit/Lists.html` is normative for everything from here to the end
of §4a). The card shows a window, never the first N rows of it:

```
┌─ Switch model ──────────────────────── 24 available · 8 showing ─┐
│ ↑ 6 more                                                         │
│     7. claude-opus-4.6    deepest reasoning · $15 / $75          │
│     8. claude-sonnet-4.6  better diffs · $3 / $15                │
│    11. gemini-3-pro       1M ctx · $2 / $12                      │
│  ❯ 12. gemini-3-flash     1M ctx · $0.30 / $2.50                 │
│    14. deepseek-r2        no tool use       not usable here      │
│ ↓ 10 more                                                        │
├──────────────────────────────────────────────────────────────────┤
│ [↑↓] move  [enter] switch  [/] filter  [esc] keep gpt-5.2        │
└──────────────────────────────────────────────────────────────────┘
```

- **The window follows the pointer, and only the pointer.** It moves when the
  focus leaves it — up to meet a pointer above, down one option at a time to
  reach one below — and stands still while the pointer moves inside it. A
  list that re-centred on every keystroke would be unreadable.
- **It is therefore path-dependent**, and deliberately so: an option reached
  from above sits at the foot of the window, the same option reached from
  below sits at its head. Neither is a jump, and the option is `12.` in both.
  What changes is the markers — `↑ 4` and `↓ 12` in one, `↑ 11` and `↓ 5` in
  the other — and they always sum with the rows on screen to the whole list.
- **Markers count options, not rows** (invariant 4): `↑ 6 more` is six models
  you have not seen, not six screen lines. A count of rows would change
  meaning every time the card resized.
- **What counts as an option is what the pointer can be scrolled to.** Group
  rails are labels for options and are not counted; a run that hid nothing
  selectable keeps a bare `…`, because writing `↑ 1 more` there would promise
  an option that does not exist.
- **Numbering counts the list, never the window.** Option 12 is `12.`
  wherever it happens to sit, so a number you read is a number you can type.
- **Everything pinned comes off the budget first, in this order**: the query
  line, the key hints, the note field (§4c), and then the options take what is
  left. A fourteen-row card with nine pinned rows shows three options and lets
  the markers carry the other six. The card's total height is the bottom
  panel's accounting and the window may never buy itself a row.
- A list that fits is not windowed at all: no markers, and no row spent
  saying that nothing was hidden.

**The filter row.** Past a dozen entries walking is the slow way, so the same
component pins a query line above the window. `[/]` opens it, typing narrows
the list, `[ctrl+u]` clears it, `[esc]` leaves the picker without changing
anything.

```
┌─ Switch model ──────────────────────────────────── 24 available ─┐
│ ▸ mini█                                       4 of 24 match      │
│  ❯ 1. gpt-5.2-mini        $0.60 / $4                             │
│    2. gpt-5.1-mini        $0.40 / $3                             │
│    3. o4-mini             $1 / $6                                │
│    4. phi-5-mini          local via ollama                       │
├──────────────────────────────────────────────────────────────────┤
│ [enter] switch  [ctrl+u] clear  [esc] keep gpt-5.2               │
└──────────────────────────────────────────────────────────────────┘
```

- **The filter makes a new list.** Numbering and both markers count matches —
  a number you can type has to address the list you can see — and the query
  row states both counts (`4 of 24 match`) so the catalog it came from is
  never hidden.
- **The matched run is bold, never tinted.** Exactly three background tints
  exist inside a screen (§10b) and each already means one thing; a fourth for
  search hits would cost more than it buys, and bold survives mono (§10f).
- **The component does not filter.** The caller passes the matches and the
  query that produced them, so the match rule stays where it is chosen rather
  than hiding inside a primitive.
- **No match is a row, not an empty pane.** The card holds its size, the query
  row keeps both counts, a line names the nearest thing that does exist, and
  the key that clears the filter stays on the key row:

```
┌─ Switch model ──────────────────────────────────── 24 available ─┐
│ ▸ sonnet-5█                                   0 of 24 match      │
│   no match for "sonnet-5"                                        │
│   closest is claude-sonnet-4.6                                   │
├──────────────────────────────────────────────────────────────────┤
│ [ctrl+u] clear the filter  [esc] keep gpt-5.2                    │
└──────────────────────────────────────────────────────────────────┘
```

- Numbering is the picker's choice, not the filter's: `/model` numbers its
  matches because a digit there selects, and the palette (§18a) does not,
  because there the query line is the surface and a digit typed into it is a
  digit. Everything else is shared — `/model`, session history, memory
  destinations, the file picker, and `shhh config` (§19a) get the window and
  the filter row for free.

**Two surfaces are not windowed, and each says why.**

- **The plan card's options** (§4d). Four decisions and seven steps do not
  compete for the same rows: the decisions are what you are answering, so they
  render whole, and the steps are evidence, so they fold and count instead —
  `… 4 more steps  [s] show them · 2 of the 4 write`. A windowed option list
  here would hide the thing you are agreeing to.
- **Staging** (§4b). You are accounting for every hunk, so hiding four of them
  behind `↓ 4 more` would be a trap rather than a fold. This is the staging
  multi-select specifically; a multi-select that is an ordinary list of
  choices (`/memory forget`, gate checks) windows like any other.

**The agent list was the third, and is not any more (S-124).** A fan-out wide
enough to overflow the card is still the problem the screen should be showing,
but a list the pointer can walk off the bottom of is the worse of the two, and
the design says what to do when it happens: window it with the selector's
rules and keep every blocked child above the window. So the manager windows,
with its head run — the current agent and the blocked children that sort with
it (§9a) — pinned above the window and off the budget before it is drawn.
Opening the manager *because* something needs you and then having to scroll to
find it would undo the reason the list is there.

**What the implementation read (S-123).** The artboard settles the shape; six
things it does not spell out were decided against it and are recorded here
rather than left in the code to be rediscovered.

- **The marker's form is the queue strip's, and that is now the decision.**
  S-116 borrowed `… N more` from `QueueStrip` with no artboard to check it
  against; `Lists.html` draws the same form, so the borrowing stops being an
  improvisation. What did change is its paint: the marker is plain dim, not
  the italic grey the key hints use — it is a count, not an aside.
- **The title rail carries the counts the window makes true**: `24 available ·
  8 showing` while the card is windowing, `24 available` once a filter has
  made a shorter list that fits. A list that fits with no filter open spends
  no rail saying that nothing was hidden, and a caller with its own chip — the
  palette's match count — keeps it.
- **While the query line is open, a digit is a digit and so is `j`.** The
  query line is the surface then, which is the reading §18a already made for
  the palette; the filter row generalizes it rather than making the palette an
  exception. The cost is that the numbers cannot be typed while filtering; the
  hazard it avoids is a model name with a `5` in it switching the model
  halfway through being typed, which is a key acting where it was not offered
  (invariant 5). Numbering itself stays the caller's prop and keeps counting
  the matched list, so a number that is read is still a number that addresses
  what is on screen.
- **The key row sheds rather than clips** (invariant 4). Adding `/ filter` made
  the row longer than a 60-column card, so it comes down a ladder: the
  number-jump reminder goes first, because every row on screen is already
  carrying its own number, then `j/k`, which is a second name for a key the
  row still offers. What an offer *is* — the filter, the selection, the way
  out — never goes.
- **"The card holds its size" on a no-match is about rows, not padding.** The
  empty card keeps its frame, its query row with both counts, its key row with
  `[ctrl+u]` on it, and two rows that say what was not found and what is
  nearest. It does not pad itself back to the height the unfiltered list had —
  a filtered list that matched four is shorter too, and an empty pane is what
  the rule forbids, not a shorter card.
- **The palette is this component with the row always open** (§18a). It
  stopped drawing its own query line when the filter row landed, so the two
  cannot disagree about what one looks like; the `▸` prompt and the block
  cursor are the card's now. The palette keeps the halves that are its own:
  the match rule, the group rails, and the chip that counts matches rather
  than a catalog.

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
- **A row states what it costs on its own row**, right-aligned against the
  card edge in the same field the single-select uses (§4a, S-126): a staging
  list's `+34 −6` is what you are deciding about, so it is never a summary
  underneath the thing being decided.
- Zero-selected + enter = no-op with a one-line notice, not a confirm.
- **A multi-select that is an ordinary list of choices windows** (S-124) —
  `/memory forget`, the gate's checks — on the selector's own window, with the
  notice and the key row pinned off the budget first. Staging is the exception
  above and stays whole.
- **A marker over a windowed multi-select says how many of the rows it is
  hiding are ticked**: `↑ 11 more · 2 checked`. This is the one thing the
  single-select case never had to answer — a single-select loses nothing by
  scrolling, and a multi-select can scroll the user's own answer off the card.
  The key row goes on stating the total (`enter apply (3)`), so between them
  the card says how many are checked and where they went. A run hiding nothing
  ticked says only what it hid.

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

`[y/N]` is a bare-letter offer like any other, so it is gated the same way: an
inline confirm that appears over a live input renders its letters as not-yet-
live until the confirm holds the keyboard (§7b, invariant 5). Where it
replaces the input outright — the common case, `/clear` typed and submitted —
it already holds the keyboard and the letters are live on arrival.

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
  offered keys sit at indent 2/4/6 in dimmer (245) and carry no fields. Output
  a program painted itself is re-painted into the palette on the way in
  (§10i); it never arrives with colours of its own.

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

## 7. Focus Mode & Where the Keyboard Is

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

Which surface holds the keyboard, and which keys it may offer while it does,
is §7c. Where every key in the product is declared — once, so the hint and
the handler cannot disagree — is §7d.

### 7a. Reading mode: where the keyboard is (S-115, S-122, S-140)

There are two panes and one keyboard, and every rule here follows from
saying which pane has it. `ui_kits/cockpit/Reading.html` is normative for how
the mode is dressed.

**The input's rule: while the prompt has the keyboard, the transcript hears
no keys at all.** Not the pager letters, nothing. A viewport handed every
keystroke scrolls the history out from under the sentence being written —
bubbles binds `j`, `k`, `u`, `d`, `f`, `b` and the spacebar by default, so
"just find the buffer" paged the transcript four times on its way into the
box. Anything the transcript can be moved by is therefore a gesture or a key
a draft cannot produce.

**Scrolling is not a transfer.** S-115 wrote this rule with only one exception
to it — the wheel — and made every keyboard gesture hand the keyboard over.
S-140 corrected that: reading is not a decision, and the reader scrolling back
to check a path mid-sentence is not asking to stop writing the sentence.
Handing the keyboard over to answer that question takes the draft off the
screen (the frame is *replaced*, §12), which is a mode change charged for a
glance. So every gesture below reads the transcript and leaves the keyboard
where it is:

| | |
|---|---|
| `pgup` / `pgdn` | pages the transcript, and transfers nothing |
| `shift+↑` / `shift+↓` | one line, and transfers nothing (`ctrl+↑`/`ctrl+↓` alias it — terminals disagree about which they report) |
| wheel | scrolls, and transfers nothing — when reporting is on |
| click-drag | selects transcript text, and transfers nothing — when reporting is on |
| `ctrl+o` | opens the step in flight, and transfers nothing — §13d |
| `ctrl+r` | cycles the reasoning level, and transfers nothing — §8a |
| `ctrl+x` | mouse reporting on/off, from any surface, saved to the config |

**There is one transfer, and it is `ctrl+e`.** It opens reading mode with the
cursor on the last selectable row. A reader who wants the row cursor, the
`[enter]` expansions and the keys a close row or a failure offers asks for
them by name; nothing else takes the keyboard by surprise on the way past.
`pgdn` with nothing below does nothing at all, because the bottom of the
transcript is where the prompt already stands.

`↑`/`↓` are the input history's, in every state. They used to become the
transfer on an empty draft with no history left to recall, which made the key
mean different things depending on how much history a session happened to
have — unlearnable, and worse than unlearnable on a terminal with alternate
scroll on, where a flick of the wheel arrives as `↑` and opened reading mode
(see below).

The wheel reaches the full-screen diff (§3c) and review mode (§16a) when those
own the screen, because the transcript behind them is not what is being looked
at.

**Alternate scroll, and why the wheel was never inert (S-140).** DECSET 1007
makes a terminal translate wheel notches into cursor keys for a full-screen
program, and Ghostty, iTerm2, WezTerm, Alacritty and Terminal.app all ship
with it on. So on most terminals the wheel was not doing nothing while
reporting was off — it was sending bare `CSI A`/`CSI B`, indistinguishable
from the arrow keys, hundreds at a time: scrubbing the input history on an
empty draft, walking the cursor through a half-written one, moving the start
screen's suggestion. Turning reporting on appeared to fix it, because tracking
supersedes 1007 — which meant a reader paid their terminal's click-drag
selection to stop a bug. shhh now asks the terminal to stop synthesising
(XTSAVE/XTRESTORE, so the setting survives us as its owner had it), which is
what makes this section's claim about the wheel true rather than aspirational.

**Keeping the synthetic wheel was investigated and declined (S-140).**
Suppressing 1007 throws away something real: those synthetic arrows are a
wheel that costs no mouse tracking, so a terminal that sent them could have
had wheel scrolling *and* its own click-drag selection at the same time — the
trade §7a describes as unavoidable, avoided. Routing them would have been
safe by design, because the split is capability-gated rather than a second
keymap: synthetic arrows scroll, real ones stay the input history's, and `↑`
goes on meaning history on every terminal whether the wheel works there or
not. The whole thing turns on one question — can the two be told apart — and
it was measured on Ghostty rather than argued about:

| requested | real arrows | wheel | |
|---|---|---|---|
| DECCKM alone | `\eOA` | `\eOA` | identical |
| nothing | `\e[A` | `\e[A` | identical |
| kitty disambiguate | `\e[A` | `\e[A` | identical |
| kitty disambiguate + DECCKM | `\e[A` | `\eOA` | **distinguishable** |

They separate, but only in the last combination, and the mechanism explains
why: the kitty keyboard protocol reports real keys and ignores DECCKM, which
its specification requires; alternate scroll goes down the legacy cursor-key
path, which honours DECCKM. Two code paths, and setting both modes drives a
wedge between them. DECCKM alone cannot do it because xterm's specification
has alternate scroll honouring DECCKM — both move together, which is what the
first row measures.

It was declined for what reaching that row costs. The kitty protocol
re-encodes exactly the keys that were ambiguous — `esc` becomes `CSI 27 u`,
`ctrl+i` and `ctrl+m` separate from `tab` and `enter` — and those are
load-bearing here (invariant 3 is *esc is always the safe answer*). Bubbletea
v1 does not parse CSI-u; §12f already works around that by catching unnamed
sequences to find shift+enter, and level 2 of `modifyOtherKeys` was declined
for the same reason. So the price of the gesture is shhh owning a decoder for
every key the protocol touches, not a routing change for two.

Underneath that, the divergence is unspecified. Which internal path a terminal
routes alternate scroll through is not something any specification constrains,
so the result is an emergent property of one implementation and could change
without anyone calling it a break. And it is kitty-protocol terminals only.
The finding is recorded rather than acted on: the same gesture arrives free,
permanently, and on every terminal the moment the transcript stops living in
the alternate screen, which is where this should be paid for instead.

**Scrolling away pauses the follow, and the notice rail says so.** A
transcript scrolled off its live end stops being moved by the turn streaming
into it. While scrolling cost a handover the labelled rail said `READING` and
a mode is its own explanation; now that the draft keeps the keyboard, the only
thing that changed is in the pane nobody is looking at. So the notice rail
(§12) carries `↓ 12 lines below · [pgdn] the live end` until the reader walks
back to it, which re-pins on arrival. It is a notice and not an offer: `pgdn`
is already on the bar above, and there is nothing here to hand out twice.

The number is the notice's; the proportion is the scroll gutter's (§10g), one
column down the pane's right edge. The two say different things and are both
worth having: the notice is a count and appears only once the follow is
paused, the gutter is a shape and is there the whole time.

**Mouse reporting is off by default**, because it costs the terminal's own
click-drag selection and the two sides of that trade are not the same size.
Scrolling has substitutes here — `pgup`/`pgdn`, `ctrl+e`, `j`/`k` all read the
transcript. Selecting text has none: a transcript is something people copy out
of, and a surface that quietly takes that away is broken in a way no key can
answer, because the reader reaching for it has a mouse in their hand and no
reason to suspect a setting. So the wheel is the side you ask for.

- **`ctrl+x` is the ask, and it is answered above every surface** — the draft,
  reading mode, the full-screen diff. The moment of wanting to copy something
  arrives at any of them, and a chord that only worked in one would miss it.
  The letter is what was left rather than what it stands for (the textarea
  underneath claims a, b, d, e, f, k, n, p, t, u, v, w; this surface spends c,
  d, e, g and j; the terminal keeps s, q and z; `ctrl+o` opens a step's detail,
  §13d), so the start screen's navigation line and `/help` both name it — a
  chord with no mnemonic is learned by being written down.
- **The answer is saved** (`appearance.mouse`), because a preference about a
  physical input device is not a per-session opinion. `/ui mouse <on|off>` is
  the same setting said in words, and takes the same path so the two cannot
  drift.
- **Both notes state the trade, not an improvement.** Turning it on says what
  the drag now does; turning it off says what the terminal takes back.

**With reporting on, shhh owns the selection (S-145).** The old note told the
reader to hold shift, which is a true answer to a smaller question. A
terminal's own selection can only reach what is on the screen: copying an
answer three viewport-heights long meant scroll, select, paste, repeat, with
no seam anywhere to say where the joins went — and the seams are exactly what
gets lost. So while reporting is on, press anchors a selection inside the
transcript, drag extends it, and release copies it. Shift-drag remains what
the terminal does for what is visible, and is documented as that rather than
as the answer to a long copy.

- **Dragging past the edge scrolls, and a stationary pointer keeps
  scrolling.** A drag held at the first or last row of the pane is a pointer
  the terminal reports nothing about, so the scroll runs off a timer of its
  own (60ms a line) rather than off the motion events. Every way a selection
  can end — release, `esc`, `ctrl+x`, `/ui mouse off`, a resize, a takeover
  surface — fences the timer, so a tick that outlived its drag scrolls
  nothing. It stops itself at the end of the transcript.
- **Selecting pauses the follow**, exactly as scrolling away does (§7a
  above), and for the same reason twice over: a transcript that jumped to its
  live end mid-drag would tear the selection off the text it was covering.
- **The coordinates are the render's, not the screen's.** An anchor is a
  visual line index plus a display column, so it survives scrolling and
  survives a turn streaming more underneath it. It does not survive a change
  of pane width, because every line reflows and there is no remapping that is
  not a guess — so a width change drops the selection rather than copying the
  wrong thing. A height change keeps the range and ends the drag.
- **What is copied is what was on the screen, joined back up.** No escape
  codes, no selection styling, no reading gutter. Soft wraps rejoin into
  prose — the boundary is recovered by asking whether the next row's first
  word could have fitted on the end of this one, which is a greedy wrapper
  run backwards — while paragraph breaks, list items and code lines keep
  their newlines, and the blank row between two transcript entries comes
  through as the blank line it is drawn as. The renderer's own left margin is
  chrome and goes; a code block's indentation is content and stays.
- **A click is not a selection.** A press that never moved copies nothing and
  lights nothing, which is what keeps the surface's one promise about the
  mouse: shhh draws no click targets, so no drag can start by triggering
  something.
- **The highlight is reverse video**, not a background colour — it says
  "selected" in mono exactly as loudly as in colour (invariant 1), and it is
  what a terminal's own selection looks like, so the feedback matches the
  gesture.
- **It is the normal transcript's alone.** The full-screen diff (§3c) and
  review mode (§16a) keep their own wheel behaviour, reading mode renders
  through a cursor gutter nobody wants pasted (§7), and an attached child's
  session is a different transcript in the same viewport (§18).
- **A failed copy says so in the transcript** — the same place `/copy`'s
  failures go — and keeps the selection, so the retry after installing a
  clipboard tool is not another six screens of dragging. A successful one
  costs one line on the notice rail (§12a) and no transcript row, so the pane
  does not move away from what the reader just selected.

**Reading mode is focus mode**, not a second, lesser one. A key that opened
its own surface would be a fourth list implementation by another name, and the
row cursor, the `[enter]` expansions and the keys a close row or a failure
offers all have to come with it. This is why S-140 could demote the pager keys
without adding a mode: scrolling the transcript was never the part that needed
a surface, and taking the keyboard was the price the pager keys had been
paying to reach one they did not want.

**The ways back are `esc` and typing.** Esc is the safe answer everywhere
(invariant 3). Typing is the one a reader reaches for without thinking, so
any printable character that is not reading mode's own hands the keyboard
back *and lands in the draft* — the keystroke is not spent on the exit. The
letters reading mode keeps are its own work: `j`/`k`, `q`, and a row's offer
keys while the row under the cursor actually offers them. Where it does not,
`v` is a letter again.

**Two things dress a pane as active, and only the pane holding the keyboard
has them.**

```
──── READING 5/12 ──────────────────────────────────────────────
❯▎✎ edit  internal/agent/loop.go          +18 −3 · approved  1.1s
   ✗ run  go test ./internal/agent/...          1 failing   21.4s
```

- **The labelled rail.** The line under the header carries the mode's name and
  its position — `READING 5/12` — set four cells in from the left, on a rail
  that runs to the full width. It is not the `Rule` component's trailing
  variant with a label hung off the right end, which is what reading mode
  borrowed when it had no artboard to read. It is the same rail `DRAFT` and
  `DECISION 1/2` draw (§7b), down to the paint: the label is info and bold,
  the rule under it is dim like every other divider
  (`guidelines/invariant-inert-keys`). While the input holds the keyboard the
  same line is a plain divider and says nothing.
- **The lit row.** The row under the cursor takes `focusBg` with its whole
  content in bright, and the `❯` pointer sits outside the highlight in the
  pointer column (§6a). It still reads over a row that carries a mutation rail
  (§14): the rail is drawn inside the highlight, keeps its accent and its
  glyph, and the bright text is what changes.
- **Both come off the instant the input takes the keyboard back**, and the
  frame takes its mode accent instead. The two panes are never both dressed as
  active: reading mode replaces the framed input (§12) with its hint bar, so
  the frame's accent is absent exactly when the rail is labelled, and the rail
  is a plain divider exactly when the frame is accented. Verified as a
  rendered pair, not asserted (S-122).

**The hint bar replaces the frame; it does not sit under it.** A plain
divider, then the mode's keys, with the position on the right:

```
────────────────────────────────────────────────────────────────
[j/k] move · [enter] expand · [q] back to the prompt  row 5 of 12
▎this row · [v] review the 3 files · [u] undo turn · [esc] nothing
```

- The mode keys are one line, in this order: `[j/k] move`, `[enter] expand`,
  `[ctrl+o] step detail` (§13d), `[-] collapse` once something is expanded,
  `[?] keys` (§7d), `[q] back to the prompt`. The right-hand field is the
  position (`row 5 of 12 · step 2`, `2 rows expanded`), and it is the first
  thing to drop as the terminal narrows. The drawing above is 64 columns,
  which is exactly where `[?]` and `[ctrl+o]` have already gone; at 130 the
  line reads `[j/k] move · [enter] expand · [ctrl+o] step detail · [?] keys ·
  [q] back to the prompt`.
- **`[ctrl+o]` says which of its three things it is doing.** `step detail`
  where the step under the cursor is closed, `close the detail` where it is
  open, and greyed with `this row is not in a step` beside it where the cursor
  is outside every step — the same treatment `[enter] expand` gets over prose,
  and for the same reason (invariant 1).
- **A row's own keys are a second line, prefixed by that row's `▎`**, so a key
  that acts on one row never reads as a key that acts on the session. A row
  that offers none renders no second line at all — nothing says "this row has
  nothing to offer".
- Two bottom elements is how you get a session where nobody can tell which one
  `enter` belongs to, which is why the frame goes rather than dims.

**Expansion is bounded.** `[enter]` opens the row in place — six lines and a
count, never the whole 47-line log. The count is the offer, and the keys that
take it up sit on the same line as the count: `… 41 more lines  [enter] full
screen · [f] open loop_test.go:142 · [r] rerun`, `… hunk 2 of 2  [enter]
review both · [\] side by side`.

**Narrow.** The rule is that the word goes before it clips: at 100 columns
the label still fits, at 62 it does not, and the rail drops to a bare divider
rather than showing a cut one. The key line shortens with it — `[j/k] move ·
[q] prompt`, position `5/12`. Nothing is truncated, and the lit row still says
which row it is, which is why dropping the word costs nothing.

**Prose is read, not navigated.** A transcript with rows but nothing
expandable in them opens without a cursor, `j`/`k` are a line of scroll, and
the rail carries a bare `READING` with no position, because there are no
addressable rows to count. `[enter] expand` stays on the hint bar in grey with
its reason beside it — `nothing on this row expands` — rather than
disappearing (invariant 1).

**An empty transcript refuses, and says so at most once.** On the start screen
(§17c) `[↑↓]` already belong to the suggestion list, so `[k]` has no second
meaning there and the refusal is silent. On a session with a real but empty
transcript the key does have a meaning and no rows to spend it on, so it says
so once, in dim, and does not repeat: a refusal that fires on every keypress
teaches a reader to stop reading refusals.

The start screen (§17c) is where all of this is introduced, on a second key
line under the suggestions. That line outlives the typing that dismisses the
suggestions, because these keys outlive it too.

**Four things S-122 settled, recorded rather than left silent.**

- **The narrow rule is the breakpoint, not the arithmetic.** The label fits in
  62 columns and still goes there, so "before it clips" is read as
  `guidelines/layout-breakpoints`' minimal band: below 70 content columns the
  rail is a bare divider. That is the same line the frame's vitals collapse
  on, so a narrow terminal loses its chrome all at once rather than a field at
  a time.
- **The position narrows before the keys do, and drops last.** `row 5 of 12 ·
  step 2` → `row 5 of 12` → `5 of 12` → `5/12`, then `[?] keys` goes whole,
  then `[ctrl+o] step detail` goes whole, then `[q] back to the prompt`
  becomes `[q] prompt`, then `[enter] expand` leaves whole, and only when none
  of that is enough does the field go. The two keys that go first are the two
  a reader can lose and still find: `[?]` acts on nothing in the transcript at
  all and `/help` names it (S-153), and `[ctrl+o]` is the only other offer on
  the bar that acts past the row under the cursor and the only one with a home
  outside this mode — the draft answers the same chord and `/help` names it
  too (S-137). A key that explains the keys goes before a key that does
  something. "First to drop" is about the position's own fields; the lit row
  is what still says which row it is.
- **`[-] collapse` is offered while the row under the cursor is open**, not
  while anything anywhere is. A key on the bar that the surface cannot honour
  is an offer nothing accepts, which is the thing invariant 5 exists to stop;
  where nothing is open, `-` is a character and lands in the draft like any
  other. The right-hand field still counts every open row (`2 rows expanded`),
  so the bar says what is open even when the cursor is not standing on it.
- **A row's own keys stack rather than clip.** They are one line where they
  fit, one line without the `this row ·` words where they nearly do, and the
  row's `▎` repeated on a second line where they do not — an offer folded out
  of sight is an offer nobody can take (invariant 4).

**The cursor keeps a column of its own.** Reading mode indents the transcript
by the pointer column rather than writing `❯` into the row's own (§6a), so a
step header under the cursor keeps its `▾`/`▸` and a folded group keeps the
glyph that says it is folded. The artboard draws one column because it draws
no lit header; the fold state is a fact about the row and the cursor is not
allowed to spend it.

### 7b. When a decision lands mid-sentence (S-117, S-125)

An approval arrives when the agent needs it, not when the reader is ready, so
roughly once a session it lands on top of a half-typed sentence. Two things
have to be true at once, and invariant 5 is what makes them compatible: the
draft survives with its cursor where it was, and the card's `[y]` does nothing
until the card holds the keyboard — because until then `y` is a letter, and it
belongs in the sentence. `ui_kits/cockpit/Interrupt.html` is normative.

**Ungated — the card is up, the draft still has the keyboard.**

```
┌─ Approve edit ───────────────────────────────────────── ⚠ low ─┐
│ ▎✎ internal/ui/chat/model.go              +9 −1 · 1 hunk       │
│ touches   internal/ui/chat/model.go — one case in the switch   │
│ undo      yes — tracked in git, undo restores it               │
│ network   closed                                               │
├────────────────────────────────────────────────────────────────┤
│ [y] approve   [n] deny   [a] always   [d] diff    not live yet │
│ [ctrl+g] answer it — until then these letters go to your draft │
└────────────────────────────────────────────────────────────────┘

──── DRAFT ─────────────────────────────────────────────────────
┌─ shhh code · ~/src/shhh · loop-refactor ───────── ⏸ 1 waiting ─┐
│ ▸ also add a --max-rounds █lag while you're in there           │
├────────────────────────────────────────────────────────────────┤
│ ⏵⏵ auto · round 7/150 · ctx ▰▰▰▰▰▱▱▱ 62% · $0.14 · gpt-5.2     │
└─ [ctrl+g] answer it · [enter] queue · [esc] stop the run ──────┘
```

- **The card's keys render as not-yet-live** — all four dim, with `not live
  yet` on the same line. The state is said in words on the keys themselves
  rather than left to be inferred from a border colour (invariant 1). A key
  that is not yet live is a different thing from one that is unavailable
  (§18a's `⊘`), and the two never render alike: one is waiting for the
  keyboard, the other cannot be pressed at all.
- **The key that hands the keyboard over is offered on the card**, on its own
  line, in the live treatment the other four do not have: `[ctrl+g] answer
  it`. It is the only live key the card has, and the card says what happens to
  the letters until it is pressed.
- **The draft keeps the frame's accent**, because the frame is where the
  keystrokes are going. The card is bordered but undressed.
- **A letter goes into the sentence.** Pressing `y` here leaves the edit
  waiting and puts a `y` in the draft, and that is the correct outcome. The
  alternative — routing `y` to the card whenever a card exists — means a
  sentence containing the word `yes` can approve a shell command.
- The frame's top rail counts what is waiting (`⏸ 1 waiting`, §12a) and its
  bottom rail carries the three keys that matter while one is: `[ctrl+g]`
  answer it, `[enter]` queue this for the next round, `[esc]` stop the run.

**Gated — `ctrl+g`, and the card has it.** The rail above the card reads
`DECISION 1/2`. The four keys go live in the ordinary treatment and gain their
consequences (`a: always allow edits in internal/ui/chat/` — the scope [a]
grants, §2a), and the safe answer is
stated because it is not obvious (invariant 3): `[esc] back to your draft —
the edit stays waiting, nothing is denied`. Esc here leaves the decision
unanswered; it does not deny it.

**The frame is undressed, not disabled.** It drops its mode colour and its
block cursor and keeps every character, and its rail states the position it is
holding — `50 characters, cursor at 24` — so the reader can see that nothing
moved while they were not typing into it.

**The return.** Answering hands the keyboard straight back to the draft, at
the same character. It does not clear the draft, submit it, or move the cursor
to the end. A second waiting decision is announced in the top rail rather than
replacing the card just closed — a queue that deals itself the next card is a
queue answered by momentum (§2e).

**And it only applies where there is a sentence (S-138).** The rule above is
about a card landing on top of one. Most cards do not: they land while the
reader is watching a turn work with an empty box, and there the handover buys
nothing — there is no sentence for the letter to belong to, and the reader who
came to press `[y]` was pressing it twice, once per approval, all session.

So the arrival state is decided by whether there is anything to protect. A
draft with characters in it, or a keyboard touched within the last second,
arrives ungated exactly as above. A draft that is empty and idle arrives
**held**: the card has the keyboard, its answers are live, and the frame it
replaced is not drawn, because an empty draft has nothing to hold (§7b's
undressed block already made that call).

```
──── DECISION ──────────────────────────────────────────────────────────
┌─ Approve command ──────────────────────────────────────── ⚠ medium ─┐
│ Assistant wants to run: go test ./internal/ui/...                   │
│ touches   unknown — shhh cannot tell what go writes                 │
├─────────────────────────────────────────────────────────────────────┤
│ Run this command? [y/N] · [ctrl+g] for [a] · any other key goes to  │
│ your draft                                                          │
│ [esc] back to your draft — the decision stays waiting, nothing is   │
│ denied                                                              │
└─────────────────────────────────────────────────────────────────────┘
```

A card holding the keyboard by arrival is not the same as one that was handed
it, and it claims less:

- **It answers `[y]`, `[n]`, `[enter]`, `[esc]` and `[ctrl+c]`, and nothing
  else.** Those are the keys a reader walks up to a card to press.
- **`[a]`, `[d]` and `[A]` wait for the handover**, and the card says so on
  its key row. They are the keys whose consequence outlives the call, and they
  are the letters a sentence is most likely to open with — "always", "also",
  "add". `ctrl+g` buys them, which is the same thing it has always bought:
  the whole keyboard.
- **Every other key hands the keyboard back and goes into the draft.** A
  reader who came to type a message rather than answer loses neither the first
  letter of it nor the decision, and the card is ungated from that keystroke
  on, so the rest of the sentence flows normally.
- **The plan card (§4d) and the memory proposal (S-070) never arrive held.**
  Both take typed input — a choice moved with `j/k`, a note written into a
  field — so a card that took the keyboard would be a card eating a sentence.
  They arrive once per turn rather than once per tool call, so the handover
  costs them nothing.
- **A decision the turn reaches while a surface has the screen** has not
  arrived in front of anyone. It is armed when the surface closes — one
  keystroke ago — so it lands ungated. One that was already holding the
  keyboard keeps it, because the surface it just came back from was most
  likely its own full-screen diff.

The residual hazard is named rather than hidden: a reader whose first
keystroke after an idle second is `y` or `n` answers the card with it. That
is the trade — one chord per approval against a one-letter window on a card
that is on screen, unmissable, and standing where the input box was.

**It generalises, and that is the point.** Any surface offering a bare
single-character key while a live input is on screen either holds the keyboard
exclusively while those keys are live, or renders them as not-yet-live and
offers the one key that hands the keyboard over. Most surfaces pass by
construction, because a takeover surface holds the keyboard by definition; the
ones worth auditing are those that render alongside a live input. §7c is that
audit — the register of every keyed surface and which of the two positions it
is in — and S-125 is where it was taken.

**What S-117 reaches.** Four surfaces arrive unbidden and are gated by this
rule: the approval card (§2), the `/run` confirm, the plan card (§4d) and a
child agent's routed approval (§9c). The memory proposal (S-070) rides the
approval state and is gated with it. Everything else a reader opens on purpose
— the pickers, review mode, the undo confirm, the pressure card — is S-125's
to audit, and most of it passes by construction.

**Three departures, recorded rather than left silent.**

- **`[esc] stop the run` is `[ctrl+c]` here.** The ungated frame's bottom rail
  reads `[ctrl+g] answer it · [enter] queues steering · [ctrl+c] stop the
  run`. Esc on this surface clears the draft and has since S-058, and the
  frame is the surface holding the keyboard, so rebinding it would break
  invariant 3 rather than serve it. Ctrl+C is what ends a turn everywhere else
  in the product, and no draft can produce it either.
- **The high-severity card names `[n]`, not `[esc]`, as its safe answer.**
  §7b's own rule makes esc on a gated card the return to the draft, so a card
  that told the reader esc was the safe *answer* would be naming a key that
  answers nothing. The card says `[n] deny — the safe answer` and, on the line
  below, what esc does instead.
- **The plan card keeps its own esc.** On the approval cards esc leaves the
  decision waiting because leaving and denying are different acts. On the plan
  card "keep planning" already is the answer that decides nothing and returns
  to the draft (§4d), so esc keeps that meaning: replacing one safe answer
  with another would only lose the mode it names.

**What the rail costs.** The `DECISION` rail is one row above the card and the
gated draft block is three more, so the panel a gated decision occupies is the
card's own 40% bound (§1) plus its rail, plus the draft when there is one to
hold. The card itself is bounded as if it stood alone, in both states: a
decision the reader cannot read is not one they can make, so the transcript
gives up the rows instead.

---

### 7c. The register of keyed surfaces (S-125)

§7b is a rule about one card. This is the same rule asked of everything else
that offers a bare letter, and the answer written down, because a rule nobody
can check against a list is a rule each new surface gets to rediscover.

Two positions are allowed, and there is no third.

- **A takeover holds the keyboard exclusively.** Its state is routed before
  the input sees a key and the input is not live while it is up, so its
  letters are live because nothing else is listening. It names itself in a
  rail where it has one.
- **A surface that does not hold the keyboard renders its keys grey** — out of
  info, which is the colour that means "you can press this" (§10a) — **and
  offers the one key that hands the keyboard over**, live, beside them.

| surface | position | keys | how it gets the keyboard |
|---|---|---|---|
| approval card (§2), `/run` confirm, plan card (§4d), a child's routed approval (§9c) | arrives ungated | `y n a d A` / `s` / `g` | `ctrl+g` (§7b) |
| reading mode and its per-row offers (§7a) | takeover | `j k q -` and the row's own | `ctrl+e` |
| the selector family (§4), the model picker, the rewind picker, the palette (§18a) | takeover | digits, `j k` | the command or key that opens it |
| review mode (§16a) | takeover | `s A n p` | `[v]`, `/review`, `/diff` |
| the agent list (§9a) | takeover | `x X j k` | `ctrl+a`, `/agents` |
| the undo confirm (§5), the key entry (§17a), the full-screen diff (§3c) | takeover | `y n f` | the key that opens it |
| the context pressure card (§17b), the retry countdown (§17a) | takeover | `n` | it opens on its own and takes the keyboard |
| the changeset row (§16) | transcript row | `v u` | `ctrl+e`, then the cursor on the row |
| a provider failure's row, a dropped stream's, a round-limit pause's (§17a) | transcript row | `e p r c v u + !` | `ctrl+e`, then the cursor on the row |

**The transcript rows are what the audit found.** Everything above them passes
by construction — a takeover has the keyboard by definition, and the four
unbidden decisions were fixed by S-117. The rows did not: they are passive
entries whose keys are handled by reading mode on the row, so beside a live
draft `[v] review` was an offer nothing accepts, painted in the colour that
says it is one.

```
▎✎ 3 files changed +30 −4 · [v] review · [u] undo turn · [ctrl+e] to use them
▎✎ 3 files changed +30 −4 · [v] review · [u] undo turn
```

- **The first line is the state a reader meets most of the time**: the draft
  below has the keyboard, `v` is a letter, and the row says so — its keys
  grey, and `[ctrl+e] to use them` in the live treatment they do not have.
  The words are the row-sized form of the card's `not live yet`; a row cannot
  spend three lines on the card's version of this and does not have to.
- **The second is reading mode with the cursor somewhere else.** The keys are
  still not live, and `ctrl+e` is not the way to them either — it is how you
  leave. The mode's own bar names the way (`[j/k] move`) and the lit row says
  where the cursor is, so the row offers nothing, which is exactly true.
- **Under the cursor the keys go back to info and the handover disappears**,
  because there is nothing left to hand over. Reading mode's bar names the
  same offers on its own line (`▎this row · [v] review · [u] undo turn`), and
  the two are read off the same list so they cannot drift.
- **The keys that are not live yet drop first as the terminal narrows**, and
  the key that makes them live drops last: `· [ctrl+e] read` is what a
  60-column row keeps. A key that is not live is not an offer; the one that
  turns it into one is.
- **`[enter] expand` on a diff row (§3a) and a folded group row (§13c) is a
  label, not an offer**, and both draw it in the hint treatment. Enter belongs
  to the draft until reading mode takes the keyboard — which is the same
  reason §17a's continue key is `[c]` and its key-entry key is `[e]` — so the
  words say what the row does under the cursor rather than advertising a key
  standing open.

**Three departures, recorded rather than left silent.**

- **`ui_kits/cockpit/Changeset.html` draws `[v]` and `[u]` in info on the
  transcript row.** The artboard predates invariant 5;
  `guidelines/invariant-inert-keys` is the newer statement and it is
  unambiguous — "surfaces that do not hold it show their keys grey, plus the
  one key that hands it over". Where the two disagree the guideline wins,
  because it is the rule and the artboard is one drawing of a screen taken
  before the rule existed. The artboard is still normative for the row's
  fields, its order and its note.
- **The handover is offered on the row, not once on the frame's rail.** Saying
  it once would be cheaper and it is where §12a puts screen-level keys, but
  the rail is not beside the keys it explains, and a reader who has scrolled
  to a failure four turns back is looking at the row. The cost is the phrase
  repeated once per close row on screen, which is at most a few.
- **A row in reading mode with the cursor elsewhere states its waiting keys in
  a shade alone.** Invariant 1 would ask for words, and the words are there —
  on the rail (`READING 5/12`) and on the mode's own bar, which names the
  offers of the row the cursor is actually on. Repeating "not live yet" on
  every unfocused row would say the same thing a dozen times to answer a
  question the rail has already answered.

**The inspector rail's `[v]` and `[u]` stay unprinted** (§15c). The rail has no
way to hold the keyboard at all, so neither position is open to it: the keys
are live on the changeset row in the transcript, where reading mode can reach
them, and the rail states the facts instead. S-125 settles that as the
answer rather than as a gap.

**The table above is now data** (§7d). S-153 moved it into `internal/ui/keys`
as a list of surfaces, each with its position, how it reaches the keyboard and
the bindings it offers — so the checks this section asks for are run rather
than reread, and the rows here and the rows in the code are the same rows.

---

### 7d. The key register (S-153)

§7c is an audit written as a table, and §7c says why that is not enough: "a
rule nobody can check against a list is a rule each new surface gets to
rediscover." The list is now a Go package — `internal/ui/keys` — and every
key in the product is declared in it once, as a binding carrying both halves:
the keystrokes a handler answers, and the spelling and words a hint prints.

**The problem it closes is drift, not ignorance.** Before it, a key was
written down twice — as a literal in the handler and as prose in the hint —
in different files, with nothing making them agree. Sixty-eight chord literals
across twenty files, and a `/help` that had never heard of `ctrl+g`: the chord
§7b is built on, the only way to reach a waiting decision's keys from a live
draft, and the single most load-bearing keystroke in this document. That
omission is what the register found on its first run.

**What the register owns, and what it does not.**

- **It owns the key.** A surface that offers `[v]` and a handler that answers
  `v` read the same binding, so a spelling that changes changes in both places
  or in neither.
- **It does not own contextual words.** `[r]` is `try again` on a failure row
  and `ask again from scratch` on a dropped stream — the same key, the same
  dispatch, meaning something more specific in each place. The binding carries
  the words a surface has no better ones for; a surface with better ones keeps
  them. The copy is the reason this product's key rows read the way they do,
  and a register that flattened them would be a downgrade sold as consistency.
- **It does not decide what is live.** Whether a row's keys can be pressed is
  a question about state, and §7c is the answer; the register says what a key
  *is*, not whether the surface offering it holds the keyboard.
- **It is not yet a rebinding layer.** Nothing reads config. The shape is the
  one that would make rebinding a config change rather than a code change, and
  that is as far as S-153 goes.

**A surface is one keyboard.** The register's rows are §7c's, split finer
where §7c's prose bundled several: the retry countdown, the pressure card and
the key prompt are three surfaces rather than one row of "it opens on its
own", and each supporting TUI (§19) is its own row rather than one row of
"the screens". The test that forced the split is the useful one — no surface
may answer one keystroke with two bindings — because a surface where `enter`
means two things is a surface where the first case in a switch silently wins.

**Three tests are the register's whole point.**

- **No chord is written down outside it.** The source under `internal/ui` is
  parsed and any string literal that is a key row segment — a chord, alone or
  with the two or three words a hint puts beside it — fails. Bare letters are
  not policed: `j`, `k` and `enter` are ordinary characters all over this tree
  and a test that could not tell those from an offer is a test people turn
  off. Chords are worth policing precisely because no sentence produces one.
- **Every key the input offers is named in `/help`.** Asserted beside the
  text, against the register's own list of the input frame's keys. This is the
  test that found `ctrl+g`.
- **Every declared binding is on a surface.** A key declared and listed
  nowhere is a key nothing lists, which is the state §7c was written to end.

#### `[?]` — the register on the page

The four supporting TUIs have offered `[?] keys` since S-127: the compact key
row swapped for the whole list, in place, and swapped back by the same key.
S-153 gives reading mode the same key, because reading mode is the one surface
in a chat session that can hold a bare letter.

```
────────────────────────────────────────────────────────────────
[j/k] move
[enter] expand
[ctrl+o] step detail
[-] collapse
[pgup] page up
[pgdn] page down
[?] hide the keys
[q] back to the prompt
▎[v] review
▎[u] undo turn
```

- **It replaces the hint bar rather than overlaying it**, and the panel grows
  into the transcript the way every other bottom panel does (§12e). Two
  bottom elements is the thing §7a refuses; a list floating over the pane
  would be a third.
- **What it adds over the bar is completeness, not longer prose.** The bar
  sheds keys as the terminal narrows and never says which; this is where they
  went. The words are the register's own — a second, fuller vocabulary would
  be a second place to drift.
- **The row's own offers come with it, under the row's `▎`.** The question is
  "what can the keyboard do from here", and the mode's keys are only half the
  answer. A row that offers none adds no rows.
- **The panel is bounded like every other one** (§1: 40% of the screen). What
  does not fit is counted on a final row rather than dropped in silence
  (invariant 4).
- **The list closes with the mode.** It is a reading of this surface, not a
  preference about it, so the next time reading mode opens the question has
  not been asked yet — which is how the supporting TUIs treat their own `[?]`.

**`?` is not a key from the draft, and `/help` is the door that is.** A bare
letter beside a live sentence is a letter (invariant 5, §7c), and `?` is a
letter people type. So from the input it lands in the draft, and the register
a reader wants is in `/help` — which now names `ctrl+g`, names what `?` opens,
and is checked against the register rather than maintained beside it.

**One departure, recorded.** `?` in reading mode is a character reading mode
takes away: a reader who was about to leave the mode by typing a question can
no longer start that question with `?`. It is the same price `q`, `j`, `k` and
`-` already pay on this surface, and it buys the one key that answers "what
else is here" — which on a bar that sheds its own offers as the terminal
narrows is worth more than the first character of a rare sentence.

---

## 8. Vitals (session state)

The vitals are the session's standing answer to *what mode am I in, how much
context is left, what has this cost*. They are one vocabulary with three
homes: the frame's rails (§12, where they normally live), the free-floating
status bar (the fallback below `minCardWidth` and under takeover surfaces),
and the inspector rail's CONTEXT and SPEND blocks (§15). All three are
session-scoped and live; the one thing in the product that is scoped to the
turn is the rail's THIS TURN block, and §8d is that turn while it is running.

```
──────────────────────────────────────────────────────────────────────────
⏵⏵ auto · round 7/150 · ctx ▰▰▰▰▰▱▱▱ 62% · ↑41.2k ↓9.8k · $0.14 · ◇1 · think high · gpt-5.2
```

### 8a. Segments

| Segment | Meaning |
|---|---|
| `⏵⏵ auto` / `⏵⏵ accept edits` (add 10) | permissive modes |
| `⏸ plan` / `⏸ manual` (accent 214) | gated modes |
| `✦ checking` (spin 205, spinner) | the auto-mode classifier is deciding |
| `round 7/150` (dim 241) | rounds used of the limit, `+50` beside it while a round-limit pause offers more, and `round 7/∞` for a turn running without a ceiling (§17a) |
| `ctx ▰▰▰▰▰▱▱▱ 62%` | context meter (§10c) — bar and number share a colour |
| `↑41.2k ↓9.8k` (dim 241) | tokens in / out this session |
| `$0.14` (body 252) | spend |
| `◇ 2 agents ⚠1` (info 12, badge del 9) | running children, badge when one is blocked |
| `think high` (body 252) | reasoning level, when one is asked for (S-139) |
| `gpt-5.2` (body 252) | model |

The agents segment is a jump target: `ctrl+a` opens the Agent Manager (§9).
Every segment keeps a glyph or a word, so colour is never the carrier.

**What answers is not chrome (S-139).** The rail's right-hand pair — the
reasoning level and the model — reads in body, not in the dim the rest of the
segments use. Everything to its left is a *measurement* of the session: how
full the context is, what it has cost, how many rounds it has taken. The pair
on the right is the session's *identity*: which model is answering, and how
hard it is being asked to think. Those are the two facts a reader checks
before trusting an answer or before spending on one, and they had been drawn
as furniture.

The level is stated only when there is one. Off is the default and means no
reasoning field is sent at all, which is not a state worth a segment: a rail
says what the session is doing, and a session doing nothing extra has nothing
to say. `ctrl+r` cycles the four levels — off → low → medium → high, wrapping
— and a level applies from the next model request, which a change made
mid-turn says out loud, because a setting that takes effect one request later
otherwise looks ignored.

### 8b. Field-drop order (normative)

When the rail overflows, fields leave in this order:

```
model / provider detail  →  reasoning level, token counts  →  round counter  →  extras
```

The model leaves before the level it is being asked to think at. Which model
is answering is recoverable — `/model` says it, the config file holds it, and
it rarely changes mid-session; the level is the thing the reader most likely
just pressed a key to change, and is watching to confirm.

Never dropped: the mode segment, context pressure, spend, and any blocked or
failed state. A rail that has run out of room shows fewer facts, never
truncated ones:

```
⏵⏵ auto · round 7/150 · ctx ▰▰▰▰▰▱▱▱ 62% · ↑41.2k ↓9.8k · $0.14 · ◇1 · think high · gpt-5.2
⏵⏵ auto · round 7/150 · ctx ▰▰▰▰▰▱▱▱ 62% · ↑41.2k ↓9.8k · $0.14 · think high
⏵⏵ auto · 7/150 · ctx ▰▰▰▰▰▱▱▱ 62% · $0.14
⏵⏵ ctx 62% · $0.14
```

### 8c. Width ladder (normative)

Content columns — the terminal minus horizontal padding. One ladder for the
whole surface; §12b and §15 are the two ends of it.

| Columns | Layout |
|---|---|
| ≥ 130 | two panes — 93-column transcript pane (92 of text + the §10g gutter) + `│` + 46-column inspector rail (§15) |
| 110–129 | one pane, vitals on their own rail inside the input frame (§12b wide) |
| 70–109 | one pane, vitals folded into the frame's bottom border (§12b compact) |
| < 70 | minimal — mode, context and spend only (§12b narrow) |
| < 12 | no frame — divider, this status bar, and a bare `❯` prompt |

### 8d. The running turn's status (S-118)

While a turn runs, one line changes and the rest of the screen holds still. It
lives in the frame's activity slot (§12a) — where dim `idle` sits, and where
`WORKING` used to, a word that was true of every moment of every turn — and it
*resolves into* the turn summary rather than being replaced by one.

```
⠋ thinking… 4.2s · ↑41.2k ↓2.1k · $0.06
⠋ running go test 12.4s · ↑41.2k ↓2.1k · $0.06
✓ done · 1m 04s · 18 tools · $0.14
```

**The phases are a closed vocabulary**, and there are four:

| Phase | When |
|---|---|
| `thinking…` | the model is reasoning before it acts — the reasoning stream, where a provider has one |
| `deciding…` | the auto-mode classifier is judging a call (`✦ checking` in §8a is this phase seen from the vitals) |
| `running <tool>` | a tool is executing, named |
| `streaming…` | prose is arriving |

Anything else is a phase nobody defined: pick the nearest of the four rather
than inventing a fifth.

**The fields tick.** Elapsed counts up on the tick loop, the token counts move
as tokens arrive, and the cost is derived from them — all three are the turn's
live numbers rather than the last thing a response reported. Elapsed reads
`4.2s` under ten seconds and whole seconds above it, so the digit that changes
is never the one carrying the magnitude.

**Field-drop order (normative).** The §8b rule applied to a different set of
fields, not a second rule:

```
⠋ running go test 12.4s · ↑41.2k ↓2.1k · $0.06       drop 0
⠋ running 12.4s · ↑41.2k ↓2.1k · $0.06               drop 1 — the tool argument
⠋ running 12.4s · $0.06                              drop 2 — token counts
⠋ running · $0.06                                    drop 3 — elapsed
```

Phase and cost never drop: what it is doing and what it is costing are the two
things the line exists to say.

**It resolves, it is not replaced.** `✓ done · 1m 04s · 18 tools · $0.14` is
the same line finished, in place, and a turn that failed resolves into its
failure instead (§17a). The transcript separately gains §16's close rows,
which lead with the step count (`✓ Done · 4 steps · 18 tools · 1m 04s ·
$0.14`) because a row in history is read after the fact and a status line is
read during. Same four facts, two orders, two homes — and the numbers agree.

Elapsed follows its phase with a space rather than a separator, which is what
`components/meters/TurnStatus` renders and what the example above states; the
`·` that the delivered collapse ladder drew there was the one place the two
disagreed, and the example is the form that shipped (S-118).

**It is a component, not free text** (§10c): the frame's status slot was a
string and `components.Spinner` took a static elapsed, so this landed as
`components.TurnStatus` beside `Meter`, `Sparkline` and `Spinner` rather than
as a widening of `cockpit.go` (S-118).

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
│   ✗ patcher-4  apply patch           failed · 1 tool  · $0.31    │
│     token budget (~200k) exceeded                                │
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
- **A child's round limit is a check-in, not a failure** (S-144). A child
  runs unbounded unless its spawn asked for one; where it did, reaching it
  parks the child just long enough to say what it has done, what is left and
  what it is doing next, and then it carries on with a budget twice the size.
  The row says `running · check-in 2` while it happens — a child on its third
  check-in is a task outgrowing the interval it was given, which is worth
  seeing without being worth stopping for. The token budget is what still
  fails a child, and it says where the child got to before it does.
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
- **A batch too wide for the card scrolls** (S-124, §4a): the head run — the
  orchestrator and the blocked children sorted under it — is pinned above the
  window, the rest scroll through the selector's window, and the markers count
  agents rather than lines, so a child carrying a `waiting approval:` line is
  two rows and one agent. The list still does not sort its own rows: a sort
  that happens inside the component is a sort nobody can check against the
  transcript, so a blocked child the host left below the fold scrolls with the
  others and the fix is the host's sort.

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
⏵⏵ accept edits · round 3/∞ · ctx ▰▰▰▱▱▱▱▱ 31% · $0.05 · writer-1
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
clamped to the parent's, and it inherits the session grants (`[a]` and
`/permissions allow`, scoped ones included), the
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
S-088 was giving each one exactly one job, and P2-1 gave each one a value at
every profile a terminal can report. The token set is unchanged — no colour
was added or removed — so any screen can be checked against this table in a
minute.

| Token | Truecolor | 256 | 16 | Design token | Job |
|---|---|---|---|---|---|
| add | `#5fd75f` | 10 | 10 | `--ansi-add` | diff additions, `✓`, `[x]`, permissive mode, staged hunks, healthy context |
| del | `#ff5f5f` | 9 | 9 | `--ansi-del` | diff deletions, `✗`, failures, blocked agents, a rule's denial, ctx ≥ 90% |
| accent | `#ffaf00` | 214 | 11 | `--ansi-accent` | tool glyphs, `⚠` warnings, gated modes, ctx ≥ 70%, **and the mutation rail (§14)** |
| info | `#5f87ff` | 12 | 12 | `--ansi-info` | sub-agents, block headings — **and every key the interface offers** (`[enter]`, `[v]`, `/mode why`) |
| hunk | `#5fd7d7` | 14 | 14 | `--ansi-hunk` | `@@` hunk headers and nothing else |
| spin | `#ff5faf` | 205 | 13 | `--ansi-spin` | **anything in motion** — spinner frames, `▸ running…`, `✦ checking`, the current step's meter cell, the working prompt gutter |
| focusBg | `#5f5fd7` | 62 | 12 | `--ansi-focus-bg` | selected row background, the cursor block |
| addBg / delBg | `#005f00` / `#5f0000` | 22 / 52 | 2 / 1 | `--ansi-add-bg` / `--ansi-del-bg` | intraline diff emphasis |
| dimmer | `#8a8a8a` | 245 | 8 | `--ansi-dimmer` | tool output, live tails, detail bodies, sparklines, the scroll gutter's thumb (§10g) |
| dim | `#626262` | 241 | 8 | `--ansi-dim` | chrome, counts, hints, faint rules, empty meter cells, the scroll gutter's track — most of the screen |
| status | `#767676` | 243 | 8 | `--ansi-status` | status text, the `⛨` containment line |
| bright | `#eaeaea` | 15 | 15 | `--ansi-bright` | headings, the focused row's text |
| body | `#d0d0d0` | 252 | 7 | `--ansi-body` | ordinary body text |
| subtle | `#bcbcbc` | 250 | 7 | `--ansi-subtle` | inactive labels in the generate UI only |

Three assignments carry the redesign, and are the ones to check first:
**spin means motion and only motion**, **accent additionally means the
mutation rail**, and **info marks every key the interface offers** — if a key
is written in any other colour, the interface is not offering it.

**A token is written for every profile, not derived for one (S-152).** Ten of
the fifteen hexes are exactly the 256 index beside them, because the colour
cube and the greyscale ramp are colours a design can name. The other five —
add, del, info, hunk and bright — live in the range a terminal theme owns,
where 10 is whatever green the user's config calls green and 12 is a blue dark
enough on some themes to lose a key the interface was offering. Those five are
the design system's own colours on a truecolor terminal.

The three columns are not redundant. A hex alone would be degraded by the
renderer, and that degradation walks only the 6×6×6 cube: body (`#d0d0d0`) and
bright (`#eaeaea`) both come back as 188, and two rungs of the grey ladder
land on one colour. Sixteen colours genuinely cannot hold six greys, which is
why the 16 column collapses dim, dimmer and status onto 8 — that is the
profile invariant 1 exists for, and the glyphs and words carry the distinction
there. `palette_test.go` is this table: it fails when the code drifts from it,
when two tokens collapse at truecolor or 256, and when the grey ladder stops
descending.

**The syntax register (§3b).** Inside a code body the palette is read as a
syntax register rather than as state: structure in info, values in accent, the
names a reader scans for in bright, the glue between them in dimmer, comments
in dim. Add, del, hunk and spin are never used there, because those four say
something about the *row* — this line was added, that one removed, the hunk
starts here, this is moving — and a string literal drawn in add would have the
card contradicting its own gutter.

The sixteen colours a terminal theme owns are not a seventeenth set of
tokens: output a program painted itself maps onto the table above before it is
drawn (§10i), so a detail body is the same fifteen colours as the row over it.

`tokens/colors.css` also defines canvas-only shades (`--screen`, `--page`,
`--rule-faint`, `--meter-empty`, `--win-*`) that exist so the artboards can be
drawn in a browser. They have no ANSI counterpart: in the terminal the screen
is the terminal's own background, and faint rules and empty meter cells (`▱`)
are dim.

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
| category | 22 | accent (214) — one share of a total nobody set a threshold on (§19c). del (9) where the share is a cost you did not ask for, and info (12) where it is a sub-agent's |

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

**One tick source (normative).** A running turn drives up to three spinners at
once — the frame's activity slot (§12a, where the turn status line of §8d
sits, and where an attached child still reads `WORKING`), the transcript's
live line, and the running activity row's `▸` (§6d) — and all three show the
same frame from
the same tick. Three timers is three different truths about one turn, and a
tick dropped on a state handoff freezes all three at once (S-119).

One source has a consequence worth stating with it: the loop must run exactly
while something is moving, and it is started from one place rather than from
each transition — otherwise a handoff that forgets to carry a tick ends the
animation everywhere at once, which is the shape S-119 fixed
(`internal/ui/chat/spin.go`). A row nothing is ticking keeps §6d's still `▸`
rather than standing on one braille frame, because a stopped spinner reads as
a hang.

The same tick is what repaints the arriving message (§10h). It is not a fourth
animation — it is the rule applied to the one thing on screen that was moving
on the network's clock instead of the session's.

**Turn status.** The one line that changes while a turn runs — spinner frame,
phase, ticking elapsed, token counts, cost — is a meter and not free text:
`TurnStatus` beside `Meter`, `Sparkline` and `Spinner`. §8d is its vocabulary,
its ticking fields and its drop order.

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
| `▣` | a staged image | `▤` | a staged document |
| `≡` | staged text | | |

The last three are the staging area's (§12g) and nothing else's. They were
added by S-151 rather than found in the set, which is worth saying plainly:
the set is closed, and closing it means an addition is a decision recorded
here, not a glyph a surface reached for. Their shape is the argument for
them — `▤` is `≡` inside the boundary that makes lines a single artifact, and
`▣` is a frame with a subject in it — so three files staged together are told
apart by drawing rather than by hue, which is what a chip strip needs from
invariant 1.

### 10e. Drawing kit

```
▎ ▌ ▁▂▃▄▅▆▇█ ▰ ▱ · ─ │ ┃ ╭ ╮ ╯ ╰ ├ ┤ ┬ ┴ ┌ ┐ └ ┘
```

`┃` is the light rule's heavy twin and is the scroll gutter's thumb (§10g),
and nothing else. It is the only glyph in the kit whose whole job is to be
told apart from another glyph in the same column, which is why it is a weight
of `│` rather than a shape of its own.

Takeover cards (§2, §9a, §17) use the square corners `┌ ┐ └ ┘` with a `├ ┤`
divider above the key row. The input frame (§12) uses the rounded `╭ ╮ ╰ ╯`
because it is a persistent surface rather than something that interrupts —
the corner shape alone says which kind of thing you are looking at.

### 10f. Monochrome (S-095)

The first invariant is checked, not asserted. `NO_COLOR`, `TERM=dumb` and
`/ui mono on` swap `components.Palette` for the two greys of
`tokens/colors.css`, and every surface follows because every surface reads
its colours from that one token set — each painting package rebuilds its whole
`Styles` struct from the new tokens rather than capturing colours once at
init (§11).

| Mono token | Truecolor | 256 | 16 | Design token | Takes over from |
|---|---|---|---|---|---|
| mono-fg | `#e2e2e2` | 254 | 15 | `--mono-fg` | add, del, accent, info, hunk, spin, bright, body |
| mono-dim | `#7d7d7d` | 244 | 8 | `--mono-dim` | dim, dimmer, status, subtle |
| mono-bg | `#32363f` | 237 | 0 | `--mono-bg` | focusBg, addBg, delBg |

Bold, glyphs and layout are untouched — only hue goes. Sources of colour the
palette does not own are declined rather than recoloured, and the syntax
register is declined with them even though it *is* the palette: a diff body is
where the `+`/`−` styling is already carrying the distinction that matters,
and a second grey ladder over it would be decoration the reader has to unpick.
So the diff renderer drops highlighting, assistant prose renders through
glamour's `ascii` theme, which writes emphasis as `**` instead of as colour,
and a program's own output loses every colour it asked for (§10i).
`NO_COLOR` additionally drops the terminal profile to `termenv.Ascii`, which
flattens even the two greys — the stricter reading that convention asks for.

The check itself lives in `internal/ui/components/mono_test.go`: it renders
every state of every surface with mono on, strips the ANSI, and fails when
two states collapse to the same text. A state that was only ever a hue apart
from another is a failing test, not a review comment. The design-system
project ships the same check as `guidelines/mono-check.html`.

### 10g. The scroll gutter (S-147)

One column down the right edge of the transcript pane, saying where in the
whole transcript the pane is and how much of the whole it is showing. The
track is `│`, the thumb `┃`, and the thumb is as tall a share of the gutter as
the pane is of the transcript.

```
┌──── 75-column transcript ─────┐┐        ┐
│ ▎✎ edit  internal/agent/loop.go││        │  the thumb is a fifth of the
│ ▎✗ run   go test ./...        ─││        │  gutter because a fifth of the
│    --- FAIL: TestRoundLimit    │┃  ← the │  transcript is on the screen
│                                │┃    pane│
│  ✓ Done · 2 steps · 6 tools    ││        │
└────────────────────────────────┘│        │
                                  ┘        ┘
                                  76th column, dim (241) track,
                                  dimmer (245) thumb
```

Four rules, and the first is the one that costs something:

- **The column is reserved whether or not anything is drawn in it.** A gutter
  that appeared on the first overflow would reflow every line of the
  transcript at the moment the reader least expects it, and a reflow drops
  the selection (§7a) and throws away the render cache (§13). So the
  transcript wraps to one column *inside* its pane, always, and the gutter is
  empty until there is something below. The two-pane split of §15 therefore
  hands the transcript 92 of its 93 columns.
- **The thumb touches an end only at that end.** It leaves the top as soon as
  one line is above it and returns to the bottom only at the live end,
  because whether there is anything below is the one thing the gutter is read
  for. Floor division alone would park it on the top for the first several
  lines of scroll; rounding would park it on the bottom before the live end.
  Neither is a rounding question — both are claims about the transcript.
- **It is a shape, not a measurement.** The notice rail already says
  `↓ 12 lines below · [pgdn] the live end` when the follow is paused (§7a),
  and that is the number. The gutter gives what a count cannot — proportion,
  and a reading that is on the screen while the reader is still pinned to the
  live end, which is exactly when the count says nothing. It is drawn the way
  the sparkline is, and for the same reason.
- **Nothing in it is clickable**, for the reason §7a gives about every other
  cell of the pane: a press inside the transcript anchors a selection, and a
  gutter you were meant to grab would make every selection started near the
  right edge a gamble.

It is the transcript's, so every surface the viewport shows carries it — the
feed, reading mode, an attached child's session. The full-screen diff (§3c)
and review mode (§16a) do not: they take the pane rather than fill the
viewport, they scroll themselves, and each already says so on its own status
bar.

The two glyphs are one dim/dimmer step apart and collapse onto the same grey
in mono, where the stroke carries all of it (invariant 1). That pair is in
`mono_test.go` like every other.

### 10h. The streaming render (S-149)

The transcript freezes every step block but the last (§13), so the one thing
it redraws each frame is the message still arriving. Two rules govern how
often that redraw happens and how much of the message it costs.

**The repaint rides the tick.** A chunk that lands while the tick chain of
§10c is running does not repaint the transcript; it records that a repaint is
owed, and the next tick pays it. So the arriving answer moves at 80ms a frame,
the same clock the spinner beside it moves on — which is the point. §10c
allows the session one clock because three would be three truths about one
turn, and a second timer opened to smooth the text would be exactly that. A
chunk that lands with nothing ticking repaints itself, because nothing else is
going to.

**The redraw costs the tail, not the answer.** Behind the repaint the message
is a markdown document, and it was re-parsed whole every time — a 4,000-token
answer arriving in 1,500 chunks re-parsing a document that grows to 16KB,
1,500 times. Instead the render is cut at a **stable prefix**: the latest
position after a blank line at which no construct can still be open, which is
rendered once and kept, so a chunk renders only what follows it. The search
for that position scans what has arrived since the last one, not the whole
message. Measured over a 6KB answer in 12-byte chunks, the render work drops
by an order of magnitude, and the saving grows with the length of the answer,
because what it removes is quadratic.

The cut is only allowed where the two renders glued back together are the
**same bytes** as one render of the whole. Not visually the same — the same
bytes. The selection (§7a, S-145) is a pair of coordinates into that string,
and the message is re-rendered whole the instant it stops arriving and becomes
a transcript entry; a byte of drift would move the selection under the cursor
and jump the transcript on the last token. So the boundary refuses everything
it cannot vouch for: an open fence, a list that may still be open, a table, a
block quote, indented code, a heading or a rule (glamour closes both with a
line the seam cannot follow), a setext underline on either side of the cut,
and any HTML block or link-reference definition anywhere in the message, since
those two carry meaning from one part of a document to another and cannot
survive being rendered as two. Anything refused falls back to rendering the
whole message, which is where the product started; the cache is an
optimisation and never a second opinion about what the message says.

The blank line glamour draws between two blocks is padded, and which padding
it uses depends on the blocks either side of it. It is therefore never written
down: the tail is rendered with a one-line paragraph in front of it and that
paragraph's own lines are then dropped, so the seam is the one glamour itself
chose. The contract is asserted in `streammd_test.go` over a corpus with one
document per construct named above, at every prefix, at all four widths, in
both palettes.

### 10i. Foreign output (S-150)

A detail body is the one place in the transcript where bytes shhh did not
write reach the screen: a failed command's output, a running command's live
tail, a provider's error body (§6a, §17a). Programs emit `\x1b[31m` and trust
the terminal to pick a red. Inside shhh that red is whatever the reader's
theme decided — frequently illegible against the terminal's own background,
and in every case a colour §10a does not list, sitting one indent away from
rows that spent S-088 getting one job per token.

So the line is read before it is drawn, and re-painted as runs of text
carrying a lipgloss style like every other surface. The sixteen colours a
terminal theme owns map onto the tokens that mean the same thing here:

| Theme colour | Token | Why |
|---|---|---|
| black | dim (241) | the terminal's black is the background on half the terminals there are |
| red | del (9) | a failure |
| green | add (10) | a pass |
| yellow | accent (214) | a warning |
| blue | info (12) | — |
| magenta | spin (205) | — |
| cyan | hunk (14) | — |
| white | body (252) | ordinary text; bright white is bright (15) |

The bright half is not a second palette. shhh has one token per meaning, so a
program's red and its bright red are both del — a failure is a failure. **Bold
is what still says which of the two the program was emphasising**, and bold,
faint, italic, underline, strikethrough and reverse all pass through
untouched.

Four rules decide what does not:

- **Backgrounds are dropped, not remapped.** §10b says exactly three
  background tints exist and all three collapse onto `--mono-bg`, which means
  selection (§7a). A program painting a block of a detail body would be
  drawing the reading cursor.
- **An explicit colour is kept.** `38;5;n` and `38;2;r;g;b` are colours the
  program could see when it chose them, and ones the palette has no token to
  stand in for. They are kept as asked for and degrade through the renderer
  like anything else.
- **Under mono no foreign colour survives at all** — not even a grey step.
  This is the diff renderer's answer to chroma (§10f), not a recolouring: a
  detail body is exactly where the words are already carrying the
  distinction.
- **The sequence vocabulary is closed.** The only sequence a detail body
  carries is the one that colours text. Cursor moves, erases, mode changes,
  window titles and OSC 52 clipboard writes all leave by the same door rather
  than by name, and a bare `\r` is read the way a terminal reads it — the
  progress line rewrote itself, so the last write is the line.

It is `repaint` in `internal/ui/components/foreign.go`, called from the one
function that draws an indented body, so a surface cannot acquire foreign
output without acquiring the door with it.

Ported from Crush's `internal/ui/common/ansi16.go`. Crush rewrites the SGR
parameters in place and lets the bytes through; painting through lipgloss
instead puts foreign output behind the same renderer as everything else, so
the colour profile, `NO_COLOR` and the mono swap reach it without this file
knowing they exist.

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
  TUIs stay visually consistent. Each package derives its styles from that
  token set into a single `Styles` struct built by one `newStyles` — the four
  packages that paint (`components`, `chat`, `browse`, `ui`) have one
  constructor each rather than a scatter of `applyXStyles` functions mutating
  each other's globals, so a surface cannot be left out of a palette swap by
  forgetting to call one more of them.
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
  right: the turn status line (§8d) while streaming, running tools or
  checking permission, `⏸ N waiting` while decisions are queued and ungated
  (§7b), and dim `idle` otherwise.
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
- **Staged rail** (§12g): one plain line between the notices and the frame,
  carrying a chip per attachment waiting to ride on the next message. It is
  under the notice rail because what is staged leaves with the sentence being
  typed and the notices do not, so it is the rail that belongs against the
  box.

### 12b. Layout modes (COCKPIT_SPEC.md §3)

Widths are content columns (terminal minus horizontal padding); these are the
lower three rungs of the §8c ladder. At ≥ 130 the frame keeps its **wide**
layout and spans both panes of the two-pane cockpit (§15).

**wide** (≥ 110): two rails below the input — vitals junction + hints:

```
╭─ shhh code ───────────────────────────────── ⠋ running go test 12.4s · $0.14 ─╮
│ ▸ and add a regression test for the parser▌                                   │
│                                                                               │
│                                                                               │
├─ ⏵⏵ accept edits · round 7/150 · ctx ▰▰▰▰▰▱▱▱ 62% · ↑41.2k ↓9.8k · $0.14 · gpt-5.2 ┤
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
Every state keeps its textual glyph (`⏵⏵`/`⏸`/`✦` in the vitals, the phase
word or dim `idle` on the top rail), so meaning never depends on color alone.

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
  and narrow layouts; the wide vitals rail, the notice rail and the staged
  rail are accounted separately (`frameExtraHeight`) when sizing the
  viewport.
- The frame is rebuilt every render and never enters the transcript render
  cache, so resize just works.

### 12f. The draft's second key, and what rides with it (S-134)

Two things the prompt surface could not do. Enter sends, so a multi-line
draft needed a key that does not; and a message could carry only text, so a
screenshot had to be described instead of shown.

**Shift+Enter is the newline.** It is also the one key a terminal is least
likely to report: in the legacy encoding Enter is a bare CR with nowhere to
put a modifier, so a terminal that has not been asked to say more sends the
same byte for both. The session asks — xterm's `modifyOtherKeys` level 1, on
at start and off at exit — and reads back both shapes the answer arrives in
(`CSI 13 ; mods u` and `CSI 27 ; mods ; 13 ~`). Any modifier on Enter means
the same thing here, so ctrl+enter works too. A terminal that ignores the
request keeps `alt+enter` and `ctrl+j`, which stay bound; the rail names
shift+enter, because naming the fallback would teach the wrong key.

**Attachments are named, never drawn.** An image on this surface would fight
every rule the §6a grid has, and a terminal cannot show one honestly anyway.
So the bytes are staged and a mark and a name carry them:

- **Staged rail** (§12g): one chip per attachment while it waits — its kind's
  mark, its name, its size.
- **Bottom rail**: `ctrl+v attach` joins the idle hints, after
  `shift+enter newline`.
- **The user's own transcript row**: `attached: shot.png (412 KB)` under the
  sentence, in the system treatment. Base names only — the row must not leak
  the sender's directory layout.

Three doors, one staging area. `ctrl+v` reads the clipboard: a copied
screenshot or a file the file manager copied is staged, and plain text still
pastes into the draft, so the chord never stops doing what it did. A path
dragged into the terminal arrives as a bracketed paste and is attached when
it points at an image or a document — a pasted path to a source file stays
text, because that is the only way to write one into a sentence. `/paste` is
the explicit form and the only one that can name a file the clipboard never
touched.

What is staged rides on the next user message — a fresh turn, or the first
queued steering line when one is waiting — and is spent by going out, so an
attachment can only ever ride once. It is saved with the turn, so a resumed
session keeps the screenshot the question was about rather than a sentence
pointing at nothing. Refusals are by name and out loud: a file too large, or
bytes shhh cannot carry, say which file and why, because an attachment
dropped quietly is a question the model answers wrong.

### 12g. The staged rail (S-151)

`2 attachments · 4 KB` was true and said nothing. The count is the one fact
about a staging area a reader never has to be told — they just attached them
— and the names are the ones they do: two screenshots and a spec read the
same as three screenshots, and a file attached by accident looked exactly
like one attached on purpose.

So the rail is a chip per file: the §10d mark for what kind of thing it is,
its base name, and its size.

```
▣ shot.png 412 KB · ≡ notes.md 2 KB · ▤ spec.pdf 1.1 MB
╭─ shhh chat ──────────────────────────────────────────────────────── idle ─╮
│ ❯ what changed between these two▌                                         │
╰─ ⏸ manual · ctx ▰▰▰▱▱▱▱▱ 31% · ↑12.0k ↓3.4k · $0.05 ──────────── gpt-5.2 ─╯
```

- **The mark and the name are body text; the size is dim.** Nothing here is
  coloured by kind, so the glyph carries the whole distinction and the strip
  reads the same in mono (invariant 1). The size is a count and is drawn like
  every other count on the rails.
- **A row that runs out of room gives up whole chips and counts them** —
  `▣ shot.png 412 KB · ≡ notes.md 2 KB · +1 more`. Half a name is a file that
  cannot be named to `/paste drop`, and the number of files you are not
  looking at is the one thing the row cannot otherwise say. The last rung
  keeps one chip whatever the width: a strip that is only a number has lost
  the thing it is for, so there the name clips like any other field.
- **A name is capped at 20 columns, cut at the tail.** The head is the half
  that tells `screenshot-2026-08-29-at-14-02-11.png` from the one beside it.
- **It is orchestrator-scoped**, like the notice rail above it (§12d):
  attached, the keyboard is pointed at a child and `ctrl+v` is a textarea key
  again, so the orchestrator's staging area is not what the reader is looking
  at.

**Nothing on a chip is a key, and nothing on it is clickable.** The strip sits
above a live draft, so a key written on it would be an offer nothing accepts
(§7c), and a `✕` would be a button on a surface that has no mouse targets.
Taking one back out is `/paste drop <name>`, with the staged names offered by
the completion menu (S-079) so the handle never has to be typed from memory;
`/paste clear` still drops the set. That is why the name is the field a chip
gives up last — it is the handle, not just a label.

Two departures from Crush's chips, recorded rather than left silent. Its strip
numbers each chip and removes one on the digit after `ctrl+r`; here the digits
would be letters going into the sentence below, which is the exact shape
invariant 5 exists to stop, and shhh's `ctrl+r` is the reasoning level
(S-139). And its `✕` is a mouse target: shhh has click targets on nothing yet,
and a button that only looks like one is worse than no button.

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

### 13d. One step's detail (`ctrl+o`, S-137)

`/ui verbosity high` scoped to a single step. Every row in it shows its output
body, bounded; nothing else on the screen moves.

```
── before ───────────────────────────────────────────────────────────────
▸ 1  Locate the round accounting ───────────────────────── ✓ 4 tools  6.2s
▾ 2  Thread the sentinel through the loop ──────────────── ✗ 9 tools 38.1s
   ▸ ⚙       6 reads · 2 searches                     [enter] expand  3.9s
  ▎✎ edit    internal/agent/loop.go                 +12 −4 · 2 hunks  1.1s
  ▎✗ run     go test ./internal/agent/...         exit 1 · 1 failing 21.4s
    --- FAIL: TestRoundLimit (0.02s)

── ctrl+o, on step 2 ────────────────────────────────────────────────────
▸ 1  Locate the round accounting ───────────────────────── ✓ 4 tools  6.2s
▾ 2  Thread the sentinel through the loop ─────── ✗ 9 tools · detail 38.1s
   ⚙ read    internal/agent/loop.go                        218 lines  0.4s
    88: return ErrRoundLimit
   ⚙ search  ErrRoundLimit ./internal               6 hits · 4 files  0.3s
    internal/agent/loop.go:88      return ErrRoundLimit
  ▎✎ edit    internal/agent/loop.go                 +12 −4 · 2 hunks  1.1s
  ▎✗ run     go test ./internal/agent/...         exit 1 · 1 failing 21.4s
    --- FAIL: TestRoundLimit (0.02s)
        loop_test.go:142: want 75, got 25
```

**The transcript had two ways to see what a call returned and a gap between
them.** `[enter]` in reading mode opens one row, unbounded, and costs a
keyboard handover to reach. `/ui verbosity high` (§13c) opens every row of
every step and is a setting rather than a gesture. What neither answers is the
question a reader actually holds — *what did this step do* — and the moment
they hold it is usually mid-turn, with a half-written sentence in the box.

- **The chord is answered from both surfaces and means the same thing on
  each.** From the draft it opens the step in flight, which is the one being
  watched; from reading mode it opens the step the cursor stands in, header or
  row alike. Pressing it again closes it.
- **From the draft it transfers nothing** (§7a): the sentence in the box
  survives being curious, which is the same rule the wheel follows. This is
  the reason the feature is a chord and not a second reading-mode key.
- **Opening unfolds the step; closing leaves it unfolded.** A folded step is
  its header and nothing else, so opening the bodies of rows nobody can see
  would be a chord that reports success and shows nothing. Closing does not
  fold it back: the reader is looking at those rows.
- **An opened step gives its counted group row back** (§13c). Answering "what
  did this step do" with `▸ ⚙ 6 reads · 2 searches` would be the fold hiding
  the thing that was asked for rather than the chrome around it.
- **The bodies are bounded to eight lines, as high verbosity's are.** A step
  is nine calls wide often enough that unbounded detail would push its own
  header off the screen — and the one row that wants its whole output still
  has `[enter]` on it, which stays unbounded inside an opened step. The step's
  answer is the default for its rows, never a ceiling on one asked for by
  name.
- **The header says `· detail` beside its count**, and only where the answer
  is yours. At high verbosity every step is open, and a word repeated on every
  header says nothing about any of them; what the marker is for is the one
  step taller than the setting would have made it.
- **The answer lives on the entry that titles the step**, beside `stepFold`
  and `groupFold` (§13b), and is resolved at render time rather than stamped
  onto the rows. Steps still hold no layout state of their own, and a call that
  lands after the chord was pressed arrives already open — a step in flight is
  a step still growing.
- **A transcript with no step says so once.** The notice names what carries
  detail rather than reporting the refusal, and a second press with still
  nothing to open is silent: a refusal that fires on every keypress teaches a
  reader to stop reading refusals (§7a).

**One departure.** The start screen's navigation line does not name `ctrl+o`,
though §7a's rule for a chord with no mnemonic says it should. That line is
already over its budget — at 80 columns it clips inside `[ctrl+k]` and loses
the mouse chord entirely — so appending a fifth offer would bury the newest
chord where it is cut first. `/help` names it, and reading mode's hint bar
offers it in place, which is where a key is actually learned. Fixing the
navigation line is its own story; this one records the debt rather than adding
to it silently.

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

## 15. Inspector Rail (two-pane cockpit, S-092, S-120)

Past 130 content columns the transcript stops being the whole screen. A
46-column rail on the right answers the standing questions — what is this turn
doing, what has this session changed, what is it costing — so you stop running
`/stats` and `/diff` to recover what the session already knows.
`components/frame/InspectorRail` and `guidelines/rail-session-scope.html` are
normative.

```
┌──────────── 93 columns ─────────────┐┐ │ ┌───── 46 columns ─────┐
│ transcript — steps, rows, details   │┃ │ │ THIS TURN            │
│ wraps to 92, not to terminal width  │┃ │ │ CHANGES              │
│ takeover surfaces span both panes   ││ │ │ AGENTS               │
│ and hide the rail while they show   ││ │ │ CONTEXT              │
│                                     ││ │ │ SPEND                │
└─────────────────────────────────────┘┘ │ └──────────────────────┘
                                       │ ╰ one │ column, dim (241),
                                       │   full viewport height
                                       ╰ the scroll gutter (§10g), the
                                         pane's own last column
```

The split is horizontal only: `chromeHeight` and `syncViewportHeight`
accounting are unchanged, and the input frame (§12) spans both panes because
steering is a session-level act. Below 130 the rail is dropped entirely and
today's single-pane layout is untouched (§8c).

### 15a. Scope: one turn-scoped block, the rest are the session

**`THIS TURN` is the turn. `CHANGES`, `AGENTS`, `CONTEXT` and `SPEND` are the
session.** A file edited in turn 2 is still on screen in turn 8, because "what
has this session done to my machine" does not reset when the agent starts a
new turn.

Two blocks can count files, so **both say their scope in words** — `3 files
this turn` and `session · +96 −11`. That is the rule that stops the two
numbers reading as a contradiction, and it is why neither is allowed to print
a bare count.

Three rules follow from session scope:

- **Repeat edits collapse to one row.** One row per path, carrying the net
  count and how many turns produced it: `▎✎ agent/loop.go +21 −4  3t`. Eight
  rows for one file is a log, not a state.
- **The list folds when the rail is shorter than it.** Files touched in the
  current turn keep their rows; the rest become `… 5 more  +63 −6`, and the
  fold carries its own counts (invariant 4). `[v] review all 8` still opens
  everything. This is a fold, not the §4a window — the rail has no pointer to
  scroll, so it has no markers either.
- **Alerts outlive their turn.** A failing test or a broken build sits above
  the file rows with the turn that caused it (`✗ 1 test failing  turn 7`) and
  stays until the workspace is clean. A red row that clears itself because a
  new turn started is the exact failure this rail exists to prevent.

### 15b. Blocks

```
  THIS TURN                        step 3 of 4
  ▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱
  3 files this turn +30 −4 · 18 tools · 1m 04s

  PLAN                             2 of 4 done
  ✓ Locate the round accounting           6.2s
  ✓ Add a RoundsExhausted sentinel       38.1s
  ▸ Return it from runRound
  · Offer more rounds in the chat model
  ⚠ 1 off plan
  /plan for the whole list

  CHANGES                     session · +96 −11
   ✗ 1 test failing                    turn 7
     TestRoundLimit · [r] rerun
  ▎✎ agent/loop.go                 +21 −4    3t
  ▎✎ ui/chat/model.go                    +9 −1
  ▎✎ agent/errors.go                     +3 −0
  … 5 more                             +63 −6
  [v] review all 8 · [u] undo turn

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

| Block | Scope | Contents |
|---|---|---|
| THIS TURN | turn | step progress meter, `step 3 of 4`, files touched this turn, tool count, elapsed |
| PLAN | plan | an approved plan's steps as a live checklist — state glyph, title, elapsed per finished step, a drift note, and `/plan` for the whole list |
| CHANGES | session | `session · +N −M` total, one row per changed path with its rail, glyph, net counts and turn count, alerts above them, `[v] review all N · [u] undo turn` |
| AGENTS | session | children — lane meter, steps, spend, current target and tool count |
| CONTEXT | session | percent of the window, meter, tokens, the per-round burn sparkline (or `estimated`) |
| SPEND | session | this turn's spend as the headline, split main / children, with the model and `session total $1.86` under it — both numbers labelled in words, per §15a |

Block order is fixed — THIS TURN, PLAN, CHANGES, AGENTS, CONTEXT, SPEND — and
the rail drops from the bottom when it runs out of rows.

**Departure from the design system.** `InspectorRail.prompt.md` fixes the
order as THIS TURN, CHANGES, AGENTS, CONTEXT, SPEND and carries no PLAN block.
PLAN shipped in S-104 and is kept here, second, because a plan through its list
is the one thing a reader checks as often as the changes; the block is
recorded as a departure rather than silently resolved either way, and S-126 is
where it is settled.

**Two keys the block does not print.** The artboard's CHANGES ends on `[v]
review all 8 · [u] undo turn`, and its alert detail row offers `[r] rerun`.
Neither is rendered (S-120), for the reason §15c gives for PLAN printing
`/plan`: the input textarea owns every unmodified letter, so a bracketed key
in the rail is an offer nothing accepts — invariant 5. `[v]` and `[u]` are
live on the changeset row in the transcript, where focus mode holds the
keyboard (§16), and the alert's second row states what the command said
(`exit 1`) instead. S-125's audit settled that as the answer rather than as a
gap: the rail has no way to hold the keyboard at all, so neither of §7c's two
positions is open to it and the keys stay off it.

### 15c. Rules

- **Blocks with nothing to say are omitted, not rendered empty.** A session
  with no children has no AGENTS heading at all.
- **The rail never scrolls.** When it does not fit the viewport it truncates
  its longest block first, and the block says so rather than silently ending.
- **The rail is passive**, like `components.Cockpit` — fed by the host model,
  no keys, no state, no goroutines. The keys it prints (`[v]`, `[u]`, `[r]`)
  are handled by the host. PLAN prints `/plan` rather than a bracketed key: the
  input textarea owns every unmodified letter, so a `[p]` there would be an
  offer nothing accepts — invariant 5 in the one place where the answer is to
  print a command instead of a key.
- **PLAN follows the plan, not the session or the turn.** An approved plan can
  outlive the turn that started it; a plan through its list is retired by the
  next instruction, and `/plan drop` forgets one early. Below 130 columns
  there is no rail, and `/plan` is the whole checklist — nothing is lost, it
  just has to be asked for.
- **Takeover surfaces span the full width and hide the rail** — approval
  cards, pickers, review mode (§16), the agent list — and restore it on
  dismissal. A decision is a takeover only once it holds the keyboard (§7b):
  while its keys are still not-yet-live the card rides above a live frame, the
  panes behind it are what the reader is looking at, and the rail stays. A
  card landing must not reflow the screen it landed on.
- Transcript wrapping uses the reduced pane width less the scroll gutter's
  column — 92, not 93 and not the terminal width (§10g).
- **A number nobody reported says so.** Occupancy is provider-reported where
  a response carried usage, and the session's own estimate everywhere else —
  before the first response, after a trim, after `/compact` or a rewind. An
  estimate prefixes its token count with `~` and writes `estimated` where the
  burn sparkline would go, so the hedge survives a monochrome terminal. The
  meter and its percentage are unchanged: an estimate is still the best
  number there is, it is just not a measurement.

**Both rails are session-scoped, and they do not overlap by accident.** The
vitals rail on the frame (§12) is the session at a glance on one line — mode,
pressure, spend — and the inspector rail is the session in detail, block by
block. They share context and spend deliberately: those are the two numbers
you want without moving your eyes. What used to be said here — that the
inspector rail is turn-scoped and historical while the vitals rail is
session-scoped and live — was the distinction S-120 removed, and it is gone
rather than qualified.

---

## 16. Changeset & Review (E-014: S-097–S-100)

The question after an agent stops is never "what did it say", it is "what did
it change". So a turn closes with three rows: what it did, what changed, and
whether the tests still pass.

```
 ✓ Done · 4 steps · 18 tools · 1m 04s · $0.14                  round 7/150
▎✎ 3 files changed +30 −4 · [v] review · [u] undo turn · [ctrl+e] to use them
 ✓ go test ./... passing · 41 packages · 12.8s
```

Drawn here in the state a reader meets most of the time: the draft below has
the keyboard, so `[v]` and `[u]` are grey and the key that makes them live is
the one in the live treatment (§7c). Under reading mode's cursor they are
ordinary keys again and the handover goes, because there is nothing left to
hand over.

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
  unpriced turn reports its tokens, never a made-up zero (§15c).
- A turn that changed nothing is the first row alone. A turn you cancelled
  reads `⊘ Cancelled`, one whose stream broke reads `✗ Failed`, and both
  still carry the changed-files row for what landed before they stopped.
- The verdict row is one row however many checks ran: several runs collapse
  to `✗ checks failing · 2 of 3 passing` rather than one row each. The row
  answers "does it still build", not "what did you run".
- Nothing about the block is a takeover. The rows are transcript entries and
  the keys they offer are handled by focus mode on the row (§7), so the input
  keeps every other key — and because the input keeps them, the row renders
  them as keys that are not live yet until the cursor is standing on it
  (§7c, invariant 5).
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
- Review is a takeover surface: full width, rail hidden (§15c), `esc` returns.

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
  Which is why the row renders them grey with `[ctrl+e] to use them` beside
  them until the cursor arrives (§7c): a key the input is keeping is not an
  offer, and it must not be painted as one.
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

   ⚠ rounds  150 of 150 used · the turn's own bound          stopped 4m12s
    3 files changed +30 −4 · the suite has not been re-run since
    [v] review what it did · [+50] more rounds · [u] undo the turn

   ⚠ rounds  200 of 200 used · 50 already granted            stopped 7m48s
    5 files changed +112 −40
    [v] review what it did · [+100] more rounds · [!] let it run · [u] undo the turn
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
  reported usage, so the `~` is `len/4`, the same arithmetic §15c's occupancy
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
  bound` on the first stop, and `50 already granted` on the ones after it,
  because `200 of 200 used` on its own reads like a limit nobody chose. It is
  *not* a Go sentinel: no identifier from the source reaches the transcript,
  which is the same rule §17a applies to a provider's error strings.
- **Every clause is conditional on the thing it names.** A turn that changed
  nothing says so rather than reporting three zeroes, and one whose edits are
  still covered by a check says nothing about the suite. `[v]` and `[u]` are
  offered only when there is a changeset to act on, and `[!]` only when a grant
  has already been taken.
- **`[+50]` draws the grant, not the keystroke.** The key is `+`, which is what
  focus mode's hint line names; the bracket says what pressing it buys. Taking
  it continues *the same turn* — nothing is added to the conversation, the
  counter is not reset, the changeset goes on collecting under the same turn
  number — so the turn is priced as one thing and `[u]` still takes all of it
  back.
- **The grant doubles.** Each one is everything the turn has been given
  already, plus another block: 50, then 100, then 200. A stop you have
  answered once with "keep going" is not a question worth asking again at the
  same interval, and a checkpoint that charges a flat toll stops being a
  checkpoint and becomes a turnstile. Three presses put the ceiling past any
  turn that finishes, which is the shape the row is meant to have: it goes
  quiet on its own.
- **`[!]` is the second stop's offer, not the first's.** The first stop is the
  checkpoint doing the job it exists for — you have not yet seen this turn
  stopped, so the question is worth asking. Once you have answered it, the
  more useful of the two answers is to stop being asked: `[!]` lifts the
  ceiling for the rest of the turn, and the rail counts up against no bound.
  The way out of a turn told to run is the one it always was, which is
  interrupting it — that is the trade the key states, and the reason it is not
  offered until the checkpoint has stopped the turn once.
- **Both expire with the turn.** A new user message gets the configured ceiling
  back and spends the standing offer, because a turn the session has moved past
  cannot be given more rounds. A session that should never stop says so once,
  at the command line (`shhh code --max-rounds 0`) or in the config file
  (`behavior.max_tool_rounds` negative), rather than by a key pressed in the
  middle of a turn — that is the unattended run, where there is nobody to press
  one.
- **The counter on the rail is part of the surface** (§8a): `round 150/150 +50`
  while the offer stands, `round 150/200` once it is taken, and `round 214/∞` once
  `[!]` or `--max-rounds 0` has removed the ceiling. The bound and the price of
  lifting it are both on screen for the whole decision — and where there is no
  bound the counter keeps its shape rather than inventing a number for it. Not
  words: the rail joins its segments with `·`, so `round 214 · no bound` would
  read as two facts rather than one.

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
│   ✗ profiles  no ~/.config/shhh/providers.toml                         │
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
dialect reads, the config files in search order, the gateway profiles
(S-084, S-142), and a local model runtime, probed once with a bounded
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
[pgup] or [shift+↑↓] scroll · [ctrl+e] select rows · [ctrl+k] palette · [ctrl+x] mouse
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
  because its keys do: the wheel, `pgup`, `shift+↑↓` and `ctrl+e` all work with
  a half-written draft in the box. This is the one screen every user sees, so
  it is where the two panes are introduced. It names scrolling and the
  handover apart, because S-140 made them two things: the first three read the
  transcript and leave the keyboard alone, and `ctrl+e` is the one that asks
  for the rows.
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
┌─ Palette ────────────────────────────────────── 9 of 41 matches ─┐
│ ▸ mod█                                                           │
├──────────────────────────────────────────────────────────────────┤
│ …                                                                │
│ ❯ /model      switch the model from the next round               │
│   /mode       gated, auto or read-only          [tab]            │
│   /memory     what shhh remembers about this repo                │
│   ⊘ /compact  summarise and free context    idle only            │
│ SESSIONS                                                         │
│   loop-refactor          3 files changed · 4m ago                │
│ ↓ 4 more                                                         │
└─ [enter] run · [tab] complete · [esc] close ─────────────────────┘
```

- It is the §4a single-select with its filter row always open, not a fourth
  list: same card, same pointer, same window, same bottom-panel accounting.
  What it adds is the group rails and a result count on the title rail — the
  count is of *matches* against the whole reach (`9 of 41 matches`), not of
  rows showing, because the rail is where you find out that there are more.
- The window's markers behave here as they do everywhere (§4a). Above, the run
  that scrolled off hid only the `COMMANDS` rail, so the marker is a bare `…`;
  below, four options and the `FILES` rail are hidden and the marker counts the
  four. It is the one card in the product where both marker forms are visible
  at once, and it is the reason the rule is stated in terms of options.
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
  the card is unnumbered here and `/model`'s is not (§4a).
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

---

## 19. The Supporting TUIs (E-021: S-127–S-130)

`shhh config`, `shhh history`, `shhh metrics` and `shhh doctor` predate the
cockpit, and each invented its own list, its own table and its own idea of a
value. (`shhh doctor` predates it as `shhh code doctor`, which is the finding
S-130 opens with — see §19d.) They are the four things this system already has: a row with fixed
fields (§6a), a window with markers and a filter row (§4a), a block meter
(§10c), and a card for anything that changes your machine (§2). Nothing new is
introduced — the four screens are re-cut from parts that exist, and
`ui_kits/cockpit/Tools.html` is normative. The gain is that a reader who knows
the cockpit already knows these.

Common to all four:

- A header line naming the command and its subject, with `[?] keys · [q] quit`
  on the right, and a rule under it — the same header the start screen uses
  (§17c). `[?]` is where that key was invented; reading mode borrowed it in
  S-153, and all five now draw their lists from the same register (§7d).
- One hint line at the foot in the §12a bracketed-key grammar, and a
  right-hand field that annotates it and drops first (§16).
- They are takeover surfaces: full width, no inspector rail (§15c), the §8
  free-floating vitals bar where vitals apply at all.
- Group rails (`SESSION`, `WORKSPACE`, `MODEL`) are labels, not options (§4a),
  and every list in them is the §4a window.
- Invariant 5 holds as it does everywhere: these screens own the keyboard for
  as long as they are up, which is why their bare letters are live on arrival
  (§7b).

### 19a. `shhh config` (S-127)

```
shhh config · ~/.shhh/config.toml · 2 overridden by this repo  [?] keys · [q] quit
──────────────────────────────────────────────────────────────────────────────────────

  SESSION
   permission mode   ⏵⏵ auto                              repo · .shhh/config.toml
 ❯ model             gpt-5.2                                   user · 24 available
     ↑ 6 more
      claude-sonnet-4.6   better diffs · 200k ctx                         $3 / $15
      gemini-3-flash      1M ctx                                     $0.30 / $2.50
     ↓ 16 more   [/] filter · [enter] take it · [esc] keep gpt-5.2
   round limit       150                                                   default
   verbosity         normal — reads fold, mutations never do                  user

  WORKSPACE
   sandbox           ⛨ workspace-write                    unavailable on this host
   network           allowed — approvals still ask per domain                 user
   memory            .shhh/memory.md · 4 entries                              repo

[↑↓] move · [enter] change · [r] reset · [w] write to the repo file    14 settings
```

- **A value is a row; changing one is a picker under it.** `[enter]` opens the
  §4a window inline beneath the row being changed, indented one level, with
  its own markers, its own filter row and its own `[esc]` that keeps the
  current value. It is not a modal over the screen — the setting you are
  changing stays visible above the options.
- **Every row states where its value came from** — `default`, `user`, `repo`,
  with the file where the answer is not obvious — because "why is this on" is
  the only question a config screen is ever asked.
- **A value the host cannot honour says so** rather than being hidden:
  `sandbox ⛨ workspace-write   unavailable on this host` in del (9). Invariant
  4 — the setting is not dropped because the machine cannot keep it.
- **Nothing is written until `[w]`**, and the header counts the overrides
  standing against the file it would write. `[r]` resets one row to its
  default; `[esc]` discards the lot.
- Masked secrets render as the last four characters and nothing else
  (`···4f9c`), in the config rows and in `shhh doctor` alike.

**What shipped (S-127).** `internal/cli/config.go` ran its own Bubble Tea
program with its own list, its own three phases and its own idea of a value;
it now hosts `components.ConfigScreen` and owns nothing but config semantics —
what a setting means, what its default is, which answers it offers, and when
any of it reaches the file. The screen is a passive renderer like every other
component: an edit resolves to a `ConfigChange` the host applies to its own
copy, and the host hands back fresh rows, which is why the screen can draw
`⏵⏵ auto` without knowing what a permission mode is.

- **The list is `Select`.** The window, the markers, the numbering rule, the
  filter row with both its counts and the bolded matched run all arrive from
  §4a rather than being written a second time. The screen adds the match rule
  — a setting is found by its name or by the config key behind it — because
  the card never filters.
- **Two lists, one component.** The settings and the picker under the row
  being changed are both `Select`, at two indents. That is what makes `[/]`
  mean the same thing in both places.
- **A value is a column.** `SelectOption` gained `Value`/`ValueTone`: the
  row's own answer, between the label column and the description, in a colour
  of its own. A list that sets none renders exactly as it did, which is why
  only the config goldens are new. The design system's `Select.d.ts` declares
  a tone for `meta` and none for `detail`, and its `Select.jsx` draws the
  detail `c-dim` unconditionally — but `Tools.html`'s own config rows paint
  `⏵⏵ auto` in add and `⛨ workspace-write` beside a del meta, which is a row
  the primitive as declared cannot express. Recorded here rather than written
  back: reading the design project is this story's business, editing it is
  not (the S-126 precedent).
- **Group rails were chrome and are labels.** `decision/Select` draws a rail
  `c-info b`; Go drew it dim. S-126 audited the selector and missed it because
  no Go surface had a rail worth looking at until this one. Fixed for every
  list at once, which is why the palette goldens moved.
- **Nothing is written until `[w]`.** The old wizard saved on every keystroke,
  including on the way past a row it had only been walked through. Edits are
  staged now, the header counts them, `[w]` asks through the §5 inline confirm
  before writing, and `[esc]` discards. `[w]` is not offered at all while
  nothing is staged (invariant 5).

**The departures.**

- **The picker's keys sit on their own row rather than on its `↓ N more`
  marker.** The artboard puts `[/] filter · [enter] take it · [esc] keep
  gpt-5.2` on the marker to save a line; `listOverflowRow`'s note field is for
  saying what a marker is hiding (S-124's ticked-rows count), and an offer is
  not that. The keys go where every other surface's keys go — the foot.
- **The field a setting with no answers opens is inline, not a card.** §19a's
  own rule is that changing a value happens under the row rather than over the
  screen; a card for a one-line field would be the modal that rule exists to
  refuse. It is the filter row's `▸ text█` grammar, which the reader has
  already met on every picker in the product. The masked entry is
  `SecretPrompt` and the write-back is `Confirm`, both as specified.
- **The masked entry's own key row is dropped where the screen shows it.** It
  offers `[enter]` and `[esc]`, and the screen's foot already does; two rows
  offering the same two keys is one row too many.
- **There is no `repo` source, because there is no repo layer.** `config.Load`
  reads the first file that exists and stops. The three readings a row can
  carry are therefore `default`, `user` and `unwritten`; the artboard's `repo ·
  .shhh/config.toml` and its "2 overridden by this repo" wait on a config
  system that layers, which is not a rendering story.
- **`shhh config` has no vitals bar.** §19 offers one "where vitals apply at
  all", and none apply to a screen that is not in a session.

### 19b. `shhh history` (S-128)

```
shhh history · 41 sessions · $18.42 all time                     [?] keys · [q] quit
────────────────────────────────────────────────────────────────────────────────────

▸ round█                    6 of 41 │ loop-refactor · gpt-5.2 · 7 rounds
──────────────────────────────────  │   make the round limit recoverable
❯ loop-refactor · 4m ago            │
      18 tools · $0.14              │ ▎✎ 3 files changed +30 −4
  round-limit-spike · yesterday     │   ▎internal/agent/loop.go   +18 −3
  ui-metrics-pane · yesterday       │   ▎internal/ui/chat/model.go  +9 −1
  tool-round-tripping · mon         │  ✗ 1 test failing · TestRoundLimit
↓ 1 more   matching round           │   ▁▂▃▃▄▅▅▆ tokens per round
──────────────────────────────────  │   ctx 62% at exit
35 sessions hidden · [ctrl+u] clear │

[enter] resume · [v] review · [x] delete · [esc] back  nothing resumed until [enter]
```

- **Two panes: the search on the left, the session it selects on the right.**
  The left pane is the §4a window with its filter row pinned above it; the
  right pane is a preview, not a second list, and has no cursor of its own.
- **The preview is built from rows the reader already knows** — the opening
  instruction, the changeset row with its mutation rail (§14, §16), the
  failing-check row, and the per-round sparkline (§10c). A session is
  previewed in the grammar it was recorded in.
- **The filter says what it hid.** `6 of 41` on the query row, `35 sessions
  hidden by the filter · [ctrl+u] clear it` under the list (§4a).
- **Nothing is resumed until `[enter]`**, and the hint line says so. `[v]`
  reviews the session's files through review mode (§16a) and `[x]` deletes the
  transcript behind an inline confirm (§5).
- History is the longest list in the product, which is why the window and the
  filter row are load-bearing here rather than a nicety.

**What shipped (S-128).** `internal/cli/history.go` ran on
`internal/ui/browse`, which invented a list, a query line, a detail page and
an action bar of its own; it now hosts `components.HistoryScreen` and owns
nothing but history semantics — what an entry means, how long ago it was, and
what its action and its exit code add up to. The screen is a passive renderer
like every other component: `[c]`, `[s]` and `[x]` resolve to a
`HistoryCommand` the host carries out against its own store, and the host
hands back fresh rows, which is why the screen can draw `exit 128` in del
without knowing what an exit code is.

- **The list is `Select`.** The window, the markers, the filter row with both
  its counts and the bolded matched run all arrive from §4a rather than being
  written a second time. The screen adds the match rule — an entry is found by
  what was asked *or* by what came back — because the card never filters, and
  because a reader hunting a command they half remember has both to work with.
- **A history row is the §6a grid.** Glyph, target, outcome, right-aligned
  duration: `$ delete every log file older than a week   exit 0   1.4s`. The
  glyph is `$` for a command that was generated, `✗` for one that exited
  non-zero, `⊘` for one that was dismissed and `·` for one never run — and
  each of the four states its outcome in words beside it, so invariant 1 holds
  with the colour covered.
- **The exit code outranks the action.** A command that was run and failed
  says so however it was reached. A command that was never run says what was
  done with it instead — `copied`, `saved`, `dismissed`, `not run` — and one
  recorded before the exit-code column existed says `run · exit not recorded`
  rather than claiming a clean exit, because inventing the one fact the reader
  came for is the worst thing this screen could do.
- **The preview is a preview.** It has no cursor, no keys and no second
  pointer: the title (when, which model, what was done), the prompt in full,
  the command on the §6a grid with its outcome and duration, and the token
  line. A command too long for the pane continues on the lines under it rather
  than clipping — it is the thing `[enter]` would run (invariant 4).
- **Nothing is re-run until `[enter]`**, and the field beside the key row says
  it. That sentence is what the key row gives ground for: the movement
  reminder goes first, then `[s]`, and `[/]` last, because `[/]` is what this
  screen is for. Where no rung leaves room, the field goes (§16) and the row
  keeps every offer and wraps.

**The departures.**

- **The artboard previews agent sessions; `shhh history` browses one-shot
  generations.** Its right pane shows `loop-refactor · gpt-5.2 · 7 rounds`, a
  changeset with its mutation rail, a failing check and a per-round sparkline
  — a coding-agent session. What `shhh history` is over is the `requests`
  table: one prompt, one command, and what was done with it. So the preview is
  built from the rows a request actually has, `[enter]` re-runs the command
  rather than resuming a session, `[v] review its 3 files` has no files to
  review, and there is no sparkline because a one-shot has no rounds. Agent
  sessions are recorded separately (S-065, `agent_sessions`) and have no
  browser at all; if one is ever built, §19b's artboard is its specification
  and this screen is not it. **This is the second place the design system
  describes a surface the product does not have** — S-130's own note found the
  first, `shhh doctor` — and it is recorded rather than resolved here because
  which of the two moves to make is a product decision, not a rendering one.
  (S-130 has since made its own: the command was promoted and widened, §19d.
  Nobody has yet asked for the agent-session browser this pane draws.)
- **The row keys leave the key row while the query line is open.** The
  artboard offers `[enter] resume it · [v] review · [x] delete` under an open
  filter row. Invariant 5 says a key is inert until the surface offering it
  holds the keyboard, and while the query line is open `x` is a letter. The
  row's own letters are therefore not offered there; `[enter]`, which no query
  line can consume, still is.
- **`[ctrl+u]` on an already-empty filter closes the query line.** §4a's
  reading is that `esc` leaves the picker rather than closing the filter — "a
  filter you have to escape twice is a mode" — and on a picker that is right,
  because leaving is cheap. On a takeover screen leaving means going back to
  the shell, and a reader who has finished searching still wants `[c]` and
  `[x]`. Clearing a filter that is already clear is the one keystroke with
  nothing else it could mean, so it is what closes the row.
- **A long prompt gives way to its outcome.** §4a's grid gives a label wider
  than half the card the whole row and drops the fields behind it. History is
  the one list where the field behind the label is the reason to read the row,
  and the prompt is the one field on it that runs to any length — so the
  prompt folds with `…` and the outcome stays. The preview beside it carries
  the prompt in full, which is what makes that a fold rather than a loss.
- **The list's rules are drawn only around a filter that is open.** The
  artboard rules the left pane above and below. With no query row above and no
  hidden count below, two rules would be framing nothing.
- **The header counts entries, not spend.** `41 sessions · $18.42 all time` is
  a session browser's accounting. A one-shot's cost is not recorded per
  request in a form a header could total honestly, so the header states what
  it can — `6 entries · 2 run` — and spend stays where §19c puts it.
- **`internal/ui/browse` survives.** S-128's own note offered to delete the
  package if the audit found nothing worth keeping, and nothing here was worth
  keeping. But `shhh snippets` and `shhh chat --resume` are still on it, so it
  stays until they move; `shhh history` no longer reaches it.

### 19c. `shhh metrics` (S-129)

```
shhh metrics · last 7 days · 41 sessions · 612 tool calls  $18.42 · [q] quit
────────────────────────────────────────────────────────────────────────────

  MODEL                TURNS  TOOLS  ↑ TOK  ↓ TOK  SPEND   PER DAY
  gpt-5.2                184    421   2.9M   318k $12.80   ▃▄▄▅▆▅█
  claude-sonnet-4.6       46    138   1.1M    96k  $4.71   ▁▂▅▃▂▆▄
  gemini-3-flash          12      4    88k     7k  $0.13   ▁▁▂▁▁▁▃

  where the money went                                                7 days
  edits     ▰▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱▱▱▱▱  $9.94 · 54%                203 approvals
  reads     ▰▰▰▰▰▰▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱  $5.11 · 28%                    312 calls
  ◇ agents  ▰▰▰▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱  $2.41 · 13%                  19 children
  retries   ▰▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱  $0.96 ·  5%               31 rate limits
```

- **Fixed-width, right-aligned numeric columns.** The column grid of §6a
  applied to a table: the reader scans one column rather than parsing rows.
- **One sparkline per row, in dimmer (245), never coloured** (§10c). A
  coloured sparkline would imply a threshold nobody set. The numbers beside it
  are the measurement; the sparkline is the shape.
- **The spend split uses the same `▰▱` meter as context pressure**, with the
  number always stated beside the bar (§10c), and it is the accent-coloured
  category meter rather than the context ladder — no threshold colours here.
- A category with nothing in it is left out rather than drawn as an empty bar,
  and `retries` keeps its del (9) fill because a retry is a cost you did not
  ask for.

**What shipped (S-129).** `internal/cli/metrics.go` printed a fifteen-column
tabwriter table and had no Bubble Tea at all. It hosts
`components.MetricsScreen` now and owns everything the screen deliberately
does not — what the store recorded, what a token costs, what became of a
command, and which of those readings there is anything to say about. Every
number reaching the screen is already a string and every share is already a
percentage, which is why it draws `$12.80` and `94% answered` without knowing
what a price table or an exit code is.

- **The artboard decided it is a surface.** The story left the choice between
  a TUI and a printed table to the artboard, and the artboard's header offers
  `[q] quit` — so it is a takeover surface like the other three. `--table`,
  and any non-terminal stdout, still prints the old table, because a metrics
  run is the one thing in this product people pipe.
- **The table is the §6a grid.** Every numeric column is measured against its
  own cells and right-aligned in that width, so the reader scans a column
  rather than parsing rows. Columns give ground whole as the terminal narrows,
  in a stated order — the sparkline first, because §19c already says it is the
  shape and not the measurement — and `MODEL` and `SPEND` never go.
- **One sparkline per row, in dimmer, never coloured**, and the host supplies
  a full seven-day run with zeroes for the days nothing ran: a shape drawn
  only over the days that happened would be a different week on every row.
- **Every ratio is the block meter, and only ratios are.** The three readings
  the store can answer — answered, exited 0, liked — are `Meter` blocks with
  the number stated beside the bar; a reading it has nothing for is left out
  rather than drawn as a row of empty bars, which is §19c's own rule about
  empty categories applied to blocks.
- **`MeterCategory` and `MeterUnasked` are new tones**, not a new meter: the
  accent-coloured category share of §19c, and the del one for a cost nobody
  asked for. Both are recorded in §10c above, and neither is a departure —
  the design system's own `Meter.d.ts` already declares `tone?: 'add' | 'del'
  | 'acc' | 'info'` as a forced tone beside the automatic pressure ladder, so
  this is Go catching up with the primitive rather than adding to it. The
  seven-cell sparkline is likewise within `Sparkline.d.ts`, which calls eight
  points "the standard width in the rail" rather than the only one.

**The departures.**

- **The artboard's screen is over a store this command does not read.** Its
  header counts sessions and tool calls, its columns are `TURNS` and `TOOLS`,
  and its split is edits / reads / agents / retries — that is
  `agent_sessions` and `agent_events`, which `shhh observe` reads. `shhh
  metrics` is over the `requests` table: one prompt, one command, and what was
  done with it. So the grammar is the artboard's and the content is the
  store's, exactly the move S-128 made. This is the third place the design
  system describes a surface the product does not have, and like §19b's
  session preview it is recorded rather than resolved. (§19d's was the one of
  the three that got resolved: S-130 promoted and widened the command instead,
  because unlike a session browser and an agent-event store, the checks the
  doctor artboard asks for were over subsystems the product already had.)
- **The split is by what became of the command.** `requests` records an action
  per row, so the categories are `$ run`, `copied`, `saved`, `edited`,
  `⊘ dismissed` and `· never used`, with `✗ no answer` keeping the del fill
  the artboard gives `retries` — a request that never answered is not a thing
  that was done with a command, it is the cost of there having been no
  command. Where nothing can be priced at all — a gateway whose catalog
  returns bare ids — the split is over tokens and its title says so, because
  the split is the reading and the currency is only the unit it happened to be
  in.
- **`PER DAY` is headed `TOK · 7d`.** The trend is tokens, and the column sits
  beside `SPEND`, where `PER DAY` would as easily read as money. The span is
  in the heading because the totals follow `--window` and the trend does not.
- **Latency has no bar.** S-129 asks for the meter on "latency and
  success-rate columns where a ratio is being shown"; a latency has no
  denominator, and a bar drawn against a scale nobody set is the fabricated
  ratio §10c refuses. `TTFT` and `P95` stay columns of the table.
- **The number beside a category bar takes the bar's colour.** The artboard
  paints `$9.94 · 54%` dim beside an accent bar; §10c's rule is that the bar
  and its number turn together, and `Meter` renders the pair in one styling
  pass.
- **No `[?]` and no key row at the foot.** §19's common shape gives all four
  screens a hint line; this one's only key is `[q]`, which the header already
  offers — the same reading that dropped the masked entry's own key row in
  §19a. §19c's header agrees with it: it writes `$18.42 · [q] quit` where the
  artboard writes `[?] keys · [q] quit`.
- **The header clips its subject rather than dropping its keys.** §19a and
  §19b clip the left and let `[?] keys · [q] quit` go, because both have a
  foot key row to fall back on. This screen has neither that nor a second key,
  so the spend and `[q]` are fixed and the subject is what gives ground —
  otherwise a takeover surface would be left with no stated way out of it
  (invariant 5).
- **A screen too short for its blocks names the ones that went.** Blocks are
  dropped whole from the bottom and the marker lists them by title; the table
  gives ground last and says how many models it is holding back. A marker that
  only said "2 more" would leave the reader guessing which two readings the
  screen is sitting on (invariant 4).

### 19d. `shhh doctor` (S-130)

```
shhh doctor · 9 checks · ⠹ running                      2.1s · [q] quit
───────────────────────────────────────────────────────────────────────

   ✓ binary     shhh 0.9.4 · darwin/arm64 · via brew               0.0s
   ✓ provider   openai · key ···4f9c accepted                      0.3s
   ⚠ provider   anthropic · no key — /model greys 4   [k] add one  0.0s
   ✗ sandbox    sandbox-exec not found                             0.1s
       every approval will show ⚠ UNCONTAINED until this is fixed
       [f] show the 3-line fix · [m] switch to ⏸ gated for this host
   ✓ git        ~/src/shhh · 3 files changed, all tracked          0.2s
   ▸ tests      go test ./internal/agent/...   running             ⠹ 2s
   · update     check for a newer shhh          queued                —

   ✗ 1 failed · 1 warning · 6 passed · 1 running    [c] copy the report
```

- **A check is a tool call, so it is the §6a row**: glyph, verb, target,
  outcome, right-aligned duration, with the same state vocabulary (§6d) — `✓`,
  `⚠`, `✗`, `▸ running`, `· queued` with an em-dash duration.
- **A failure states its consequence in the product's own words.** `every
  approval will show ⚠ UNCONTAINED until this is fixed` is what the reader
  will actually see on the next approval card, quoted from it.
- **The fix is offered on the row that failed**, not in a footer: `[f] show
  the 3-line fix`, `[m] switch to ⏸ gated for this host`, `[k] add one`. This
  is §17a's rule — failures name the fix, not the blame — applied to a
  diagnostic.
- The summary row counts every outcome including the ones still running, and
  `[c]` copies the report as text, because the next thing that happens to a
  doctor run is that it gets pasted into an issue.

**The name was settled by promoting the command (S-130).** The design system
called this surface `shhh doctor` and what was built was `shhh code doctor`,
scoped to the sandbox ladder — so the artboard showed a screen for a command
that did not exist under that name. Of the two ways out, S-130 took the one
that makes the design system's name true: `shhh doctor` is now a top-level
command over ten checks — the binary, the config file, the provider and where
its key came from, the local store, command containment, container sandboxes,
the workspace, the tools on PATH, durable memory, and whether a newer shhh
exists. `shhh code doctor` runs the containment pair of the same ten, so the
two commands can no longer report differently on one machine, and `/sandbox
doctor` is unchanged because in a session the question really is only about
containment.

**What shipped (S-130).** `internal/cli/doctor.go` hosts
`components.DoctorScreen` and owns everything the screen deliberately does
not: what a check looks at, what its answer means, what it will cost the
reader, and what the fix is. Every judgement is a pure reading of what was
probed — `doctorSandbox` is a function of a `sandbox.Availability`, not of
this machine — which is what lets the whole report be checked on a machine
with no containment mechanism, no provider key and no repository.

- **A check is the §6a row and nothing else.** Pointer, blank rail, glyph,
  eight-column name, target, right-aligned outcome, six-column duration. The
  state vocabulary is §6d's: `✓` passed, `⚠` warned, `✗` failed, `⊘` nothing
  to check, `▸` running, `·` queued with an em-dash duration. Nothing was
  added to the grid.
- **The consequence is quoted from where the reader will meet it.** `every
  approval will show ⚠ UNCONTAINED, and an approved command runs as you` is
  the approval card's own words (§2b); `no session will start until a key is
  found` is what the no-provider card says (§17b). A failure that stated only
  itself would leave the reader to work out whether it mattered.
- **The fix is on the row, and only the row under the pointer offers it.**
  `[f]` opens the fix at §6a's nested indent, under the consequence; the rows
  that also have one carry the same key grey (§7c), because on this screen the
  surface holding the keyboard is one row. A run with nothing to fix draws no
  pointer and offers no `[↑↓]` (invariant 5).
- **What gives ground first is what has nothing to say.** A short terminal
  drops in order of how little a check is asking of the reader — the passes
  and the skips, then the rows still to answer, and the failure last — and the
  marker names what went. The summary row is still counting every check either
  way (invariant 4).
- **Checks run one at a time and the screen redraws between them,** which is
  what makes the artboard's `▸ running` / `· queued` picture the honest one:
  the update check talks to the network and the provider check probes a local
  port. The header's spinner and the running row's are the same frame from one
  tick source (§10c).

**The departures.**

- **`provider` became `model`.** §6c's verb field is eight columns and
  `provider` fills all eight, leaving the target beside it with no gap — the
  artboard's own `provider` row has the same overrun. `model` is the verb §17a
  already gives a provider failure, so the two rows line up. Every check name
  is held to seven columns by a test rather than by convention.
- **A key that was found is reported as found, never as accepted.** The
  artboard says `key ···4f9c accepted`, and accepting one means spending a
  request on it. A diagnostic that billed you for running it is a diagnostic
  nobody runs, so the row says which of the four places answered and stops
  there.
- **`[k] add one` and `[m] switch to ⏸ gated for this host` are not offered.**
  Both write to the config file, and §19a's rule is that nothing is written
  until `[w]` on the screen that owns config. What is offered instead is `[f]`
  — the fix named, in the words that would fix it — and `[r]`, which re-runs
  every check so the loop closes without leaving the screen. `[c]` is the
  third, and copies the report.
- **The foot's two halves are swapped.** §19's other three screens put the
  keys on the line and a field beside them; here the summary is the line and
  the keys annotate it, which is what the artboard draws. On a diagnostic the
  thing to read is what the run found.
- **The header carries `[?]`** where the artboard shows only `[q] quit`. §19's
  common rule gives every one of these screens `[?] keys · [q] quit`, and
  unlike `shhh metrics` this screen has five keys, so the list earns its
  place. As on that screen it is the left that clips: a takeover surface that
  dropped `[q]` would have no stated way out (invariant 5).
- **Three of the artboard's ten rows are checks this product cannot make.**
  `toolchain go 1.24.1 · gopls 0.16.2 · 41 packages indexed` is an indexed
  workspace shhh does not keep, and `▸ tests go test ./internal/agent/...` is
  a test run a diagnostic has no business starting on your machine. What
  shipped in their place are readings the product does have: `tools` for what
  is on PATH, `store` for the local database, and `memory` for what the
  project remembers.

---

## 20. The Primitives Register (S-126)

Seven Go renderers shipped before the design system had a page to check them
against. S-121 landed those pages; this is the audit that reads each renderer
against the primitive that now specifies it, and writes the answer down — the
S-095 and S-125 pattern, where a rule stated once becomes a list a new surface
has to be entered into rather than a paragraph each surface is trusted to have
read.

Two positions are allowed, and there is no third: a divergence is either fixed
in Go, or it is a departure recorded here with its reason. What counts as a
divergence is what a reader sees on the row. A prop named `detail` in the
design system and `Desc` in Go is not a finding; a description that appears on
one row in the design and on none of the others in the binary is.

| renderer | primitive | outcome |
|---|---|---|
| `selector.go` | `decision/Select` | fixed: the description column, the meta field, the right-aligned numbering. Two departures. The group rail followed later, under S-127 (§19a) |
| `multiselect.go` | `decision/MultiSelect` | fixed: the meta field. Two departures |
| `noteselect.go` | `decision/NoteSelect` | inherits the selector's three. One departure |
| `plancard.go` | `decision/PlanCard` | conformant. Two departures |
| `inspector.go`'s PLAN block | `decision/PlanChecklist` | conformant. Two departures |
| `diff.go` | `activity/DiffView` | conformant. Two departures |
| `palette.go` | `frame/Palette` | fixed: the key binding is the row's meta field now |
| `startscreen.go` | `frame/StartScreen` | conformant |
| `fanout.go` | `activity/FanoutLane` | conformant. One departure |
| `agentlist.go` | `decision/AgentList` | conformant. One departure |

### 20a. What was fixed

- **The description was hiding.** `Select.prompt.md` and `Lists.html` draw
  every option's continuation on the option's own row; the Go card drew it
  under the focused option and nowhere else, which is what the old §4a bullet
  said ("keeps the card short"). That bullet predates the artboard §4a itself
  declares normative, and it cost the reader the thing a catalog is for: a
  `/model` list where the prices only appear one at a time cannot be compared,
  only walked. Every row carries its description now, and the plan card keeps
  the old behaviour under a name that says it is the exception (§4d).
- **The short field did not exist.** The design gives every option a
  right-aligned `meta` — the price on a model, `[tab]` on a command, `not
  usable here` on a row that cannot be taken. Go had no such field, so the
  palette hand-padded key bindings into the label text: a second column the
  component knew nothing about, which a filter that shortened the list could
  not keep aligned. One field, aligned by the card, for the single-select and
  the multi-select alike.
- **The numbering stepped sideways at ten.** `1. ` and `10. ` are different
  widths, so a list of a dozen entries moved its labels one column left
  halfway down. The artboard right-aligns the number in a column of its own;
  so does the card now.
- **An open filter row said nothing.** A row opened by a key names what the
  key was for until something is typed into it, which is the design's own
  default and costs nothing once it has content.

A list where nothing has a description or a short field spends no columns on
either, so a fixed menu of answers — `/mode`, the recovery cards' options —
renders exactly as it did before.

### 20b. The departures

Each of these is a place the binary does something the design system does not,
on purpose. The reason is here so the next reader finds a decision rather than
a discrepancy.

- **An unavailable option carries `⊘`.** `Lists.html` and `Palette.prompt.md`
  paint the row grey and leave it at that. Invariant 1 does not allow a shade
  to be the only difference between two states, and §10f's mono palette has
  one grey to spend, so the glyph stays and the words in the meta field stay
  with it. Same precedent as §7c: where a guideline and an artboard disagree
  about a rule, the guideline wins.
- **An unavailable option is still selectable.** `Select.d.ts` says `disabled`
  is "shown but not selectable". In the palette, choosing the row is how the
  surface says why — the reason is a clause, not a chip, and it is stated on
  the row rather than swallowed. Nothing acts on the choice, so no key does
  anything it did not offer (invariant 5); it is a row that answers rather
  than a row that refuses.
- **The multi-select keeps its `❯`.** The staging artboard draws the focused
  row as a checkbox inside the focus background with no pointer. §4b's own
  sketch draws the pointer, and a background is the one focus treatment mono
  cannot carry, so the pointer stays.
- **The multi-select does not colour the `@@` field itself.** `MultiSelect.d.ts`
  splits a staging row into `hunk`, `label`, `added` and `removed`; Go takes a
  label and a meta field and lets the staging caller compose them. The row
  reads the same; the split would be a prop-shape change, which this audit is
  not for.
- **The note field is a labelled row, not a nested frame.** `NoteSelect`
  draws `╭─ note ─╮ … ╰─╯` around the text. A card's height is the bottom
  panel's accounting (§4a) and a nested frame spends two of its rows on a
  border, taken from the option window — so Go writes `┄ note (optional)` above
  the text and spends the rows on options instead. The label still names the
  field, and turns red on `note required` the same way the border would.
- **The plan card's fold row does not offer a key.** `Lists.html` writes `…
  4 more steps  [s] show them · 2 of the 4 write`. `s` is already the plan
  card's save key on §4d's own key row, and an audit is not where a key
  binding gets reassigned. The row states the count; whether the steps become
  unfoldable, and on which key, is a product decision for whoever needs it.
- **The plan card numbers its options and points with `❯`.** The artboard
  draws them unnumbered behind `▸`. §4d's key row offers `1–5 jump`, and a
  number you can read has to be a number you can type (§4a), so the numbering
  stays and the pointer stays the one every other list uses.
- **The rail checklist has a fourth state.** `PlanChecklist` declares `done`,
  `running` and `todo`. A step that finished and contained a failure is none
  of the three, and the checklist is the answer to "where are we", so `✗` is
  drawn there for the same reason the step outline draws it (§13b).
- **The rail checklist names a command, not keys.** The artboard offers
  `[p] full plan · [x] drop step 4` on the rail; Go writes `/plan for the
  whole list`. §15c settled this under S-125: the rail has no way to hold the
  keyboard, so neither of §7c's two positions is open to it, and a key printed
  there would be an offer nothing accepts.
- **Diff line numbers are as wide as the file, not five columns.**
  `DiffView.d.ts` fixes the gutter at five. §3b's own sketch uses three, and
  three columns of padding on a short file are three columns taken from the
  code the row exists to show. The width is the largest line number in the
  hunk.
- **A deletion's gutter marker is `-`.** The design writes `−` (U+2212)
  because that is the right character for a *count* — `+18 −3` — and Go uses
  it there. In the gutter the marker is part of a unified diff, which is a
  format other tools read, so it stays a hyphen.
- **A fan-out lane's verb is `agent`, not the child's name.**
  `FanoutLane.d.ts` puts the name in the 8-column verb field. §6c's verb
  vocabulary is closed and its field never grows, and `researcher-1` and
  `researcher-2` clip to the same eight columns — so the verb is `agent`, the
  name leads the target field, which is the only field allowed to grow, and
  the lane still lines up with the rows around it.
- **The agent manager kills on `X`, not `k`.** `AgentList.prompt.md` offers
  `[k] kill`. `k` is the move-up key on every list in the product, including
  this one, and a movement key that also kills a process is the worst kind of
  false offer. `x` cancels the turn, `X` kills the agent, and the pair reads
  as one escalation.

## 21. One Provider, Several Endpoints (S-142)

### 21a. The shape of the problem

A gateway profile (S-084) was one file, one name, one address. That is the
shape of a hosted API and it is not the shape of a gateway: a deployment
serves its Claude models on the Messages dialect at a path of its own, its
OpenAI-shaped models at the root, and its reasoning families through the
Responses API — one deployment, one key, one set of house rules, three
addresses.

Under one-file-one-address the only way to say that was three profiles. Three
copies of the key variable, three copies of the headers, three copies of every
rule the whole gateway needs — and `/model` could not cross between them,
because they were three providers as far as the session was concerned.
Switching from `gpt-5.2` to `claude-opus-5` meant switching provider first.

So a profile is a provider **with endpoints inside it**:

```toml
[[provider]]
name        = "gateway"
base_url    = "https://gw.internal/v1"
api_key_env = "GATEWAY_API_KEY"

  [[provider.models]]
  id = "gpt-5.2"

  [[provider.endpoint]]
  match    = ["claude-*"]
  api      = "anthropic-messages"
  base_url = "https://gw.internal/anthropic"

    [[provider.endpoint.models]]
    id = "claude-opus-5"
```

- **An endpoint says only what differs.** Everything it leaves unset it
  inherits: the key, the dialect, the catalog path, the headers, the rules.
  The block above is the whole of what is different about the Claude address,
  and that is the point — a profile that repeated the key on every endpoint
  would be the three files again with extra syntax.
- **Headers merge, rules concatenate.** A collision goes to the endpoint; the
  profile's rules run first, then the endpoint's, so a quirk that is true of
  the whole gateway is written once and a quirk that is true of one address
  sits with that address.
- **The profile's own fields are the default endpoint** — where a model that
  no endpoint claims is sent. That is why `base_url` stays required even when
  every model is routed: it is the answer to a question every session can ask.

### 21b. Which endpoint a model goes to

Two claims, checked in that order:

1. **A declared id.** `[[provider.endpoint.models]]` naming `claude-opus-5` is
   the user naming a model and an address in one breath, and nothing overrides
   it — including a glob on another endpoint. An id declared at the provider
   level is a declaration too, and pins that model to the default address.
2. **A `match` glob.** `["claude-*"]` catches the models nobody enumerated —
   the ones a catalog endpoint will hand back tomorrow.

Anything unclaimed goes to the default. A profile with no endpoints therefore
routes everything to the default and behaves exactly as it did before
endpoints existed, which is the compatibility the feature is built around.

- **A model claimed twice is refused at load**, naming both endpoints. Either
  could be the one meant, and picking silently would send a session's traffic
  to an address the user did not choose — the same reasoning that refuses a
  malformed rewrite rule rather than letting it quietly do nothing.
- **An endpoint with neither `models` nor `match` is refused too.** Nothing
  can reach it, so it is a typo with no other reading.
- **Routing happens per request, not per session.** The model travels in
  `provider.CompletionOpts`, so `/model claude-opus-5` mid-session crosses
  from the chat dialect to the Messages dialect with nothing rebuilt. Each
  endpoint's client is built the first time a request needs it and kept: an
  endpoint the session never touches costs nothing, and — the reason that
  matters — an endpoint whose key is unset does not fail a session that was
  never going to send it anything.
- **A base URL override collapses the routing.** Routing is a map from models
  to addresses; an override naming one address for everything has already
  answered the question the map exists to answer, so the profile is pinned to
  it. Only an override made for this session reaches a profile —
  `provider.base_url` and `SHHH_BASE_URL` arrive as `ConfigBaseURL` and a
  profile has never read them, because a base URL set for one provider
  silently repointing another is worse than a setting that does nothing.

### 21c. Turning the catalog query off (S-143)

`discovery_disabled = true` stops the `GET {base_url}/models` the picker makes
and leaves the declared models as the whole list. A gateway that publishes
hundreds of ids the key cannot actually use, one whose catalog is slow or
absent, one that should simply not be asked — none of those are shhh's
judgement to make, and all of them are one line of the user's config.

- **The capability is hidden, not answered.** A disabled endpoint's provider
  stops being a `provider.ModelLister` rather than returning its declared
  models from `ListModels`. The picker reads the capability (§`canPickModel`),
  so hiding it sends bare `/model` straight onto the declared catalog: no
  query surface, no ten-second budget, no request at all. Answering instead
  would show a reader a query that ran and came back with what they already
  had, which is a different and untrue story.
- **The switch is a `*bool`.** Endpoints inherit, and a plain `false` cannot
  be told from "not said here" — so an endpoint could never re-enable the
  query under a provider that turned it off. The pointer also has to survive
  the migration: emitting `false` for an unset field would turn every migrated
  endpoint into one that overrides its profile.
- **A profile nothing can enumerate stops offering.** Every route disabled or
  on the Messages dialect, which has no catalog, means the router has nothing
  to ask; it is wrapped the same way rather than running a query that could
  only return the catalog the picker already holds.
- **It is the catalog, not a gate.** `/model <name>`, `--model` and
  `SHHH_MODEL` still take any name. The declared list is what the picker
  offers, and it was never what the session was allowed to run — the same
  rule the curated catalogs have always followed.

### 21d. One file

Providers live in `<config-dir>/providers.toml`, one `[[provider]]` block
each. The `providers/` directory beside it still loads — every profile written
before this is still read — and the single file is read first, so a name in
both resolves to the one file.

`shhh providers migrate` folds the directory into the file: it reads
everything in load order, writes it as `[[provider]]` blocks, and leaves the
originals alone. `--prune` removes them, `--dry-run` prints the file it would
write and changes nothing.

- **The migration re-emits rather than concatenates.** TOML nesting means the
  directory form's top-level keys and `[[models]]` tables have to be re-keyed
  to live under `[[provider]]`; a concatenation would produce a file that
  parses into something else entirely. The emitter writes a fixed, readable
  order and omits every unset field, because the result is a file someone has
  to keep editing for years.
- **A file that would not parse stops the write.** A provider that failed to
  load is a provider that would silently vanish from the consolidated file, so
  the migration refuses and names what it could not read.
- **`migrate` then `migrate --prune` is two commands, and the second one
  works.** Between them every original is shadowed by the file the first
  wrote — dead, ignored by the loader — and a plan that only counted files
  still contributing a provider would find nothing to prune and strand the
  originals on disk forever. Redundant means "the one file stands in for
  this", which covers both.
- **`shhh providers` prints one block per endpoint**, with the rules and the
  key check repeated under each, because "which rules apply where" is the
  question a routed profile makes possible to get wrong.

---

## 22. The Chrome Outside the TUI (S-146, S-148)

Every surface in this file is a Bubble Tea program that draws itself. The
binary has a second face — `shhh --help`, the line a mistyped flag prints on
its way out, the man page a packager installs, and the lines left behind when
the alternate screen hands the terminal back. None of it was designed.

### 22a. Help, errors and the man page (S-146)

That face was cobra's default: usage dumped in one undifferentiated column,
and a failure rendered as `Error: unknown flag: --nope` followed by the entire
usage block again, which is the opposite of §17a's rule that a failure names
one thing and one way out.

`fang` renders those three. It is applied once, at the `Execute` call site in
`internal/cli/root.go`; no command knows about it.

- **Help is sectioned, not dumped.** `USAGE`, `COMMANDS`, `FLAGS` as labelled
  blocks, each command and flag on its own row with its description in a
  second column. Same reason the activity feed is a column grid (§6a): a list
  you scan needs an axis.
- **A failure is a labelled block with one way out.** `ERROR`, the sentence,
  then `Try --help for usage.` — and no usage dump. The shape §17a asks of a
  failure row: what went wrong, then the command that fixes it.
- **Colour is still only decoration.** The labels are words (`ERROR`,
  `FLAGS`), so `NO_COLOR` and a monochrome terminal lose the tint and keep
  every distinction. Invariant 1 holds outside the TUI exactly as inside it.
- **`shhh man` generates the page**, hidden from the command list because it
  is for whoever builds the package, not for whoever runs the binary.
- **`shhh completion <shell>` stays shhh's own.** cobra's generated command
  steps aside when one of that name already exists, and shhh's is the
  documented surface — bash, zsh, fish, with the install lines in its help.
- **The version line is unchanged.** It still carries the cached update
  notice, because the notice lives in the version *template* and fang sets
  only the version string.
- **No flag description opens with an acronym.** fang title-cases the first
  word of every description, which turns `API key` into `Api key`. The six
  descriptions that started with one were rewritten to start with an ordinary
  word rather than fight the renderer over casing it cannot be told to skip.
- **Signals stay where they were.** fang can turn SIGINT into a cancelled
  root context; shhh does not take it. The interactive surfaces read ctrl+c
  as a keystroke in raw mode, so the option would only reach the non-TUI
  paths — and there it would swallow the *second* ctrl+c on anything that
  does not honour the context promptly, which is a worse answer than the
  default. `runner.Run` already forwards signals to the command it spawned,
  the one case where the process is not the thing being asked to stop.

### 22b. The exit banner (S-148)

The chat surfaces run on the alternate screen. Quitting does not leave the
session in the scrollback the way a scrolling program would — it hands the
terminal back exactly as it was found, and everything the session drew is gone
in one frame. The vitals go with it: which slot the conversation is in, how
long it got, what the sitting cost, and whether any of it was written down.
All of that used to be answered with `Chat session ended.`

The banner is what the terminal keeps:

```
session  (last session) · 12 turns
spent    $0.42
resume   shhh code --continue
```

- **It is the bookend of §17c.** The first-contact screen offers `pick up
  (last session) — 7 turns · $0.42`, and this is where that offer comes from.
  Both name the slot with the same word, so the string a reader last saw on
  the way out is the one they are offered on the way back in.
- **The slot named is the one that was written**, not the one the session was
  working under. Quitting autosaves to `(last session)` whatever `/save`
  called the conversation, so naming the working slot would point a
  `--continue` at an older copy. The banner reads the autosave's own
  condition, which is why it cannot promise a resume that was not taken.
- **The resume command is the command that was running.** `shhh chat` and
  `shhh code` reopen the same slot but not the same toolset, so the banner
  offers back the face the session was wearing.
- **That command is never clipped.** It is the one thing on this surface a
  reader has to be able to retype, and a command with its tail eaten is not a
  shorter command, it is a wrong one. Everything else clips, and the session
  row has a drop ladder of its own: the turn count goes before the name is
  touched, because a session a reader cannot name is one they cannot find
  again.
- **Nothing spent is no row**, never `$0.00` — the rule §17c holds the resume
  offer to. A model with no price reports tokens instead.
- **A conversation that could not be written names no slot and offers no
  command.** The `resume` row reads `not saved · chat persistence was
  unavailable`, and the turn count stays, because how much was lost is the
  part still true. The failure a reader must not discover by typing is the one
  that quietly reopens something older (§17a).
- **A session that never said anything prints nothing at all.** There is
  nothing to resume and nothing to report, and the shell prompt says more
  about that than a line acknowledging it would.
- **Colour is decoration here too.** Three labels and one sentence carry every
  distinction, so a monochrome terminal loses the tint and keeps all of it
  (invariant 1).
- **No wordmark and no parting line.** A banner whose first two lines say
  nothing is a banner a reader learns to skip, and the one line here that has
  to be read is a command.
- It is written to stderr, where everything else the command says about itself
  goes, so a redirected stdout still carries only what the session produced.
