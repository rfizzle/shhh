---
name: tui-drive
description: How to run, drive and look at the shhh TUI — the built binary in a tmux pane against a scripted model, captured as text and as a picture at a stated width. Use when a change touches anything under internal/ui or a surface docs/interface/surfaces.md names, when asked to run or screenshot the TUI, when writing or ticking an acceptance criterion about a surface, and when a golden passes but the question is whether the key, the panel or the stream actually reaches the screen.
---

# Driving the TUI

A golden (`internal/ui/golden`) is a surface rendered in-process at four
widths in two palettes. It proves the render. This skill is the other half:
the built binary, opened in a real terminal, driven by keys, with a model
that says exactly what the scene needs, and a capture of the screen at every
step. It proves that the key reaches the surface, that the surface lands in
the panel the register put it in, and that the provider's stream arrives
through the whole program.

**A change to a surface is accepted with both.** The rule and its reasons
are in `AGENTS.md` under *Driving the binary*; this is how to do it.

## Run it

```
make tui-check                       # the smoke scene; the gate, part of make ci
make tui-shot SCENE=smoke COLS=110   # capture every step of a scene at a width
make tui-run  SCENE=smoke            # open the same pane in this terminal, by hand
```

`tui-run` needs a terminal to attach to, so it is for a person. An agent
uses `tui-shot` and reads the captures. Both need `tmux` and `python3`, and
`tui-shot` makes a picture as well where `qlmanage` exists (macOS); elsewhere
the `.svg` is the picture.

Captures land under `bin/tui/<scene>/` and are never committed. For each
`snap` in the scene:

| File | What it is | Read it with |
|---|---|---|
| `<name>.txt` | the terminal's cells, no colour | `cat`; diff against a golden's layout block |
| `<name>.ansi` | the same cells with colour | not for diffing — tmux re-emits colour per cell |
| `<name>.svg` / `<name>.png` | a picture of the screen | the file viewer; the PNG can be read inline |

Read the `.txt` first: it is the layout, and a column that drifted shows
there. Look at the picture for what text cannot carry — a colour that stopped
meaning what it meant, a rail that is there but dim, a glyph that fell back.

## Write a scene

A scene is a directory under `scripts/tui/scenes/<slug>/` with two files.

`replies.txt` is the model, one reply per request; the last line repeats
once they run out. Plain text streams as the assistant's answer. A line
`tool:<name>:<json args>` is one tool call, and the tool names are the ones
the toolset registers — `execute_command`, `read_file`, `write_file`,
`spawn_agent` and the rest — with the arguments the tool takes:

```
Here is what I found.
tool:execute_command:{"command":"go test ./internal/agent/..."}
tool:ask:{"question":"Which store?","shape":"choose","options":[{"label":"sqlite","recommended":true},{"label":"files"}]}
That settles it.
```

`steps.txt` is the reader, one step per line:

```
# a comment
setup mkdir -p .shhh && printf 'notes\n' > .shhh/project.md
snap 01-start "Some things worth doing first"
keys "run the tests" Enter
snap 02-card "Run this command?"
keys y
snap 03-ran "ok ·"
keys C-c
keys C-c
snap 04-exit "that is everything the screen was holding"
```

- `setup <shell>` runs in the scene's fresh workspace before the binary
  starts — the place to put a file, a `.shhh/` directory or a commit the
  surface reads. The workspace is a new repository with one empty commit,
  under a home of its own, so nothing on the machine leaks in.
- `keys …` is passed to `tmux send-keys`: a quoted string types it, and
  `Enter`, `Escape`, `Tab`, `BTab` (shift+tab), `Up`, `Down`, `PgUp`,
  `C-c`, `C-o`, `C-/`, `M-a` (alt+a) are keys by name.
- `snap <name> "<text>"` waits for the text to be on screen, then captures.
  **The text must be the surface's own.** Waiting for the line you just typed
  passes before the reply lands; wait for a word only the reply carries, a
  card's own question, the rail's own count.
- `sleep <seconds>` is for the rare step nothing on screen marks. Prefer a
  snap with text; a sleep is a guess about a machine's speed.

A snap whose text never appears fails the run, so every scene is also a
test, and the exit code of `make tui-shot` is its verdict.

Pick the width. `COLS` is the terminal, and the four the goldens use are 60,
80, 110 and 130 — the breakpoints in `docs/interface/principles.md#one-grid`.
Capture at the width the item names, or at the narrowest the surface must
fit at when it names none, and at a wider one if the surface changes shape
across a breakpoint.

## Tick the criterion

An acceptance criterion about a surface reads, in the item's own words:

> Driven capture: a scene at `scripts/tui/scenes/<slug>/` that reaches the
> card through the built binary (`make tui-shot SCENE=<slug> COLS=60`), its
> `.txt` read against the artboard and its picture looked at before this box
> is ticked.

Ticking it means: the scene is in the tree, the run passed, and you read the
capture — say what you compared it with. A surface no scene can reach (a
print-mode row, a served session's event) says so in the criterion rather
than leaving it out.

## What bites

- **The scripted model is openai-compatible SSE only.** That is the dialect
  a `base_url` alone redirects, the same choice the CLI's print-mode tests
  make. A scene cannot exercise the Anthropic or Gemini stream loops.
- **Two scenes at once collide** on the provider's port and the tmux server.
  Set `PORT` and `SOCK` to run them side by side.
- **The pane is 120×40 unless told.** `ROWS` matters as much as `COLS` for
  anything bound by the forty-per-cent panel rule.
- **The start screen is the first frame.** A scene that types straight away
  is typing over the pick list, which is fine — the draft takes it — but the
  first snap should be the start screen, so a change to it is seen.
- **Do not assert a capture against a golden's ansi block**, and do not
  commit captures. The scene is the record.
