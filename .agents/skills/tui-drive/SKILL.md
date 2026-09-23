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
make tui-check                       # every scene; the gate, part of make ci
make tui-shot SCENE=smoke COLS=110   # capture every step of one scene at a width
make tui-longpath                    # one scene from a checkout path past the socket cap
SHHH_BIN=$PWD/bin/tui/shhh scripts/tui/drive.sh --attach scripts/tui/scenes/smoke
```

The third opens the same pane in this terminal, by hand: it needs a terminal
to attach to, so it is for a person, and the binary `tui-shot` built. An agent
uses `tui-shot` and reads the captures. All of them need `tmux` and `python3`, and
`tui-shot` draws a picture as well where `agg` is installed: each snap's
captured cells, with their colour, rendered as a still beside the capture
they came from. `brew install agg` is the whole of it — one binary, no
browser. Without it the run is cells only, and says so. By hand that is
`drive.sh --pictures`.

**The still is the capture drawn, not a second run of the scene.** It is
rendered from the same `.ansi` the `.txt` came from, so the picture and the
text are the same screen and cannot disagree — and there is no second run of
the scene for them to disagree about. What it cannot show is where the cursor
stood: tmux does not capture that either.

A snap is a still. To record the run itself — the stream arriving, the card
landing, the key answering it — add `--record`:

```
SHHH_BIN=$PWD/bin/tui/shhh scripts/tui/drive.sh --record scripts/tui/scenes/smoke
```

Captures land under `bin/tui/<scene>/` and are never committed. For each
`snap` in the scene:

| File | What it is | Read it with |
|---|---|---|
| `<name>.txt` | the terminal's cells, no colour | `cat`; diff against a golden's layout block |
| `<name>.ansi` | the same cells with colour | not for diffing — tmux re-emits colour per cell |
| `<name>.gif` | those same cells drawn, under `--pictures` — one frame, so a still | open it, or read it inline with the file reader |
| `<scene>.cast` | the whole run under `--record`, one per scene rather than per snap | `asciinema play`; `agg` renders it to a `<scene>.cast.gif` beside it |

**A missing picture is said out loud.** The run clears the previous run's
captures and stills before it starts, and holds `agg` to a still per snap
afterwards; where one is missing the run fails and names it, rather than
leaving a picture from an earlier run to be read as this one's. The cells are
unaffected and are still the gate.

Read the `.txt` first: it is the layout, and a column that drifted shows
there. Look at the picture for what text cannot carry — a colour that stopped
meaning what it meant, a rail that is there but dim, a glyph that fell back.
Watch the cast for what a still cannot carry at all: how long the screen sat
empty, what order the rows arrived in, whether a key was answered at once.

The cast is text, so it diffs, and it is the record; the GIF beside it is a
bonus. A machine without `asciinema` says so and records nothing — the run is
the gate, and the recording never decides it. Note that a GIF of a whole run
opens on its first frame, which is an empty terminal: it is for watching, and
a snap's own still is what you read a surface from.

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

A line beginning with `+` continues the reply above it rather than being a
reply of its own, so one reply can carry several parts:

```
Locate the round accounting
+tool:read_file:{"path":"loop.go"}
+tool:read_file:{"path":"round.go"}
+tool:read_file:{"path":"errors.go"}
```

That is what a step is made of, and so what the folded run of read-only rows
inside one needs: the transcript titles a step with the assistant prose
immediately before a batch of calls. Sent as replies of their own the sentence
would be a turn that ended before the calls were asked for, and the scene
would get four rows and no outline.

A `+` with nothing above it is an error naming the file and the line: the
provider refuses to start, and the scene fails before the binary opens rather
than drawing that line as a step title nobody wrote.

A line `[name]` on its own opens a queue, and each agent is then answered from
the queue of its own name rather than all of them from one:

```
[session]
Fanning three writers over the round accounting.
+tool:spawn_agent:{"role":"writer","task":"Say where the counter is read.","name":"writer-1"}
[writer-1]
The counter is read at the top of the loop, and nowhere else.
```

`[session]` is the session the reader types into, `[<name>]` the child
`spawn_agent` gave that name, and `[reading]` the session's own readings of
its run — the summariser, the classifier, the title — which otherwise take
the scene's next reply and leave the turn one short. Each queue is a queue:
its last line repeats once it is used up. A child is routed by its task
rather than by its prompt, because two writers of one session are handed the
same prompt and the task is the scene's own words; an agent no queue was
written for is answered with a line that ends its turn and the provider log
says so, so a fan-out scene writes only the children it is about. A file with
no header is one queue for everybody, which is what a scene written before
queues is and stays: every racer in uniform rounds, because which of them
reaches the endpoint first is a race the file cannot settle.

`steps.txt` is the reader, one step per line:

```
# a comment
setup mkdir -p .shhh && printf 'notes\n' > .shhh/project.md
snap 01-start "Some things worth doing first"
keys "run the tests" Enter
snap 02-card "[y] run it once"
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
- `keys …` is passed to `tmux send-keys`: a quoted string types it, and the
  rest are keys by name.

  | The scene writes | What it presses |
  |---|---|
  | `"a line"`, `y`, `q` | the text, a letter at a time |
  | `Enter` `Escape` `Tab` `Space` `Up` `Down` `Left` `Right` `Backspace` `Home` `End` | itself |
  | `BTab` | shift+tab |
  | `PgUp` `PgDn` | page up, page down |
  | `C-c` `C-o` | a control chord |
  | `C-Space` | ctrl+space |
  | `S-Tab` `S-Enter` `S-Up` `S-Down` `S-Left` `S-Right` | a shift chord |
  | `C-/` `C-_` | ctrl+/ |
  | `M-a` `M-]` | an alt chord |

  Write a chord in the case the register spells it — `M-a`, never `M-A`: the
  capital is a shift the scene never asked for.
- `paste <file>` bracketed-pastes a file the setup wrote, by its path in the
  workspace — `tmux load-buffer` then `paste-buffer -p`, a real bracketed
  paste, which is the one thing `send-keys` cannot do: typed bytes arrive as
  keystrokes, and what a surface does with two hundred lines arriving at once
  is a different question from what it does with two hundred lines typed.
- `snap <name> "<text>"` waits for the text to be on screen, then captures.
  **The text must be the surface's own.** Waiting for the line you just typed
  passes before the reply lands; wait for a word only the reply carries, an
  offer off a card's own key row, the rail's own count.
- `sleep <seconds>` is for the rare step nothing on screen marks. Prefer a
  snap with text; a sleep is a guess about a machine's speed.

A snap whose text never appears fails the run, so every scene is also a
test, and the exit code of `make tui-shot` is its verdict.

The first line of `steps.txt` names the program tests that walk the same
route inside the gate — `# program test: TestProgram_A, TestProgram_B`,
continued on `#   ` lines and closed by a bare `#` — or, where no program
test can, `# program test: none — ` and why. The route is the keys to the
surface and the stream that feeds it; a scene is run only where there is a
terminal, and the program test in `internal/ui/chat` (or `internal/ui` for
the one-shot) is what fails on every platform when the key stops reaching
it. `TestScenes_EachNamesItsRoute` refuses a scene whose head names a
test nobody declares, or says none with no reason.

`size` is a file only a scene with a width to insist on needs — `144 40`,
columns then rows — and `launch` is the other optional file, for a scene
that is not a session:
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
- **Two scenes at once do not collide.** The provider asks the kernel for a
  free port, binds it and says which one it took, and the tmux server is named
  for the run, so a second checkout's `make tui-shot` cannot take the first's
  port or kill its server. Set `PORT` or `SOCK` only to pin one somewhere you
  can look for it.
- **A worktree needs nothing extra.** The tmux socket lives in a directory of
  the run's own under `$TMPDIR`, removed with the run's other scratch — not
  under `bin/tui/<scene>/`, whose path is the checkout's and overflows the
  104-byte cap on a Unix socket from anywhere as deep as
  `.claude/worktrees/<name>/`. There tmux fails with "File name too long" and
  every snap times out, which reads as the scene being broken rather than the
  path being long. An inherited `TMUX_TMPDIR` still wins, and a socket path
  over the cap even so is named and stops the run before the provider starts.
  `make tui-longpath`, which `tui-check` runs after the scenes, is the check
  that keeps it that way.
- **The pane is 120×40 unless told.** A scene whose surface only exists
  past a breakpoint says its own size in a `size` file (`144 40`, columns
  then rows), so `tui-check` runs it where it means to be run; `COLS` and
  `ROWS` on the command line still override it. `ROWS` matters as much as
  `COLS` for anything bound by the forty-per-cent panel rule.
- **The start screen is the first frame.** A scene that types straight away
  is typing over the pick list, which is fine — the draft takes it — but the
  first snap should be the start screen, so a change to it is seen.
- **Do not assert a capture against a golden's ansi block**, and do not
  commit captures. The scene is the record.
