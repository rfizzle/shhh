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
`tui-shot` makes a picture as well where `vhs` is installed: the scene is
written as a tape and played again in a real terminal, which leaves a PNG per
snap and a GIF of the run. `brew install vhs` brings vhs, ttyd and ffmpeg;
without them the run is cells only, and says so. By hand that is
`drive.sh --vhs`.

A snap is a still. To record the run itself — the stream arriving, the card
landing, the key answering it — add `--record`:

```
make tui-build
SHHH_BIN=$PWD/bin/tui/shhh scripts/tui/drive.sh --record scripts/tui/scenes/smoke
```

Captures land under `bin/tui/<scene>/` and are never committed. For each
`snap` in the scene:

| File | What it is | Read it with |
|---|---|---|
| `<name>.txt` | the terminal's cells, no colour | `cat`; diff against a golden's layout block |
| `<name>.ansi` | the same cells with colour | not for diffing — tmux re-emits colour per cell |
| `<name>.png` | the screen as a real terminal drew it, under `--vhs` | open it, or read it inline with the file reader |
| `<scene>.gif` | the whole run as a real terminal drew it, under `--vhs` | any viewer |
| `<scene>.tape` | the scene as vhs played it, under `--vhs` | `vhs <scene>.tape` replays it by hand |
| `<scene>.cast` | the whole run under `--record`, one per scene rather than per snap | `asciinema play`; `agg` renders it to a `<scene>.gif` beside it |

Read the `.txt` first: it is the layout, and a column that drifted shows
there. Look at the picture for what text cannot carry — a colour that stopped
meaning what it meant, a rail that is there but dim, a glyph that fell back.
Watch the cast for what a still cannot carry at all: how long the screen sat
empty, what order the rows arrived in, whether a key was answered at once.

The cast is text, so it diffs, and it is the record; the GIF is a bonus and
wants `agg` (`vhs` drives a tape of its own and cannot read a cast). A
machine without `asciinema` says so and records nothing — the run is the
gate, and the recording never decides it.

## Write a scene

A scene is a directory under `scripts/tui/scenes/<slug>/` with two files, and
a third where the scene is not a session.

`replies.txt` is the model, one reply per request; the last line repeats
once they run out. Plain text streams as the assistant's answer, and a
literal `\n` in it is a line break — which is what a one-shot's answer needs,
since the command, the sentence saying what it does and the alternatives are
three lines of one reply. A line `tool:<name>:<json args>` is one tool call,
and the tool names are the ones the toolset registers — `execute_command`,
`read_file`, `write_file`, `spawn_agent` and the rest — with the arguments
the tool takes:

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

`launch` is the third file, and only a scene that is not a session needs it:
one shell line naming what the pane runs, with `$SHHH_BIN` the built binary.
Without it the pane runs `shhh code`. `shhh cmd` is a separate entry point —
there is no key that reaches the one-shot from a session — so a scene for it
says so here rather than typing its way in, and the same line is where a
scene pipes into a surface to see what it does with no terminal on the other
end:

```
clear; echo 'list open ports' | $SHHH_BIN cmd; $SHHH_BIN cmd 'find what is listening'
```

Keep it to single quotes — the line is re-quoted for `--record` — and put a
`clear` between runs of a surface that draws inline, because the one-shot
clears nothing on the way out and a capture would otherwise hold two screens.

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
