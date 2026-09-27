#!/usr/bin/env bash
# Draw every picture the README shows, in one run, from the scenes' own
# captures. Nothing under docs/readme/ is drawn or edited by hand: a picture
# that is wrong is fixed in its scene, and this script is run again.
#
# Each still is one snap of one scene: drive.sh runs the scene against the
# scripted model and captures the pane, the rows that matter are cut out of
# the snap's `.ansi` (the colour carried in from the rows above them), and
# still.py wraps what is left as a one-frame cast for agg to draw — the same
# path `drive.sh --pictures` takes, at the README's size rather than the
# harness's. The hero is a whole scene recorded with `drive.sh --record` and
# drawn by agg from the cast.
#
# The size is the point. GitHub shows a README image at most about 830 pixels
# wide, and a picture drawn at that size is soft on a high-density display and
# falls apart when zoomed. So every picture is drawn at two to three times it:
# at FONT_SIZE 28 a cell is about seventeen pixels wide, which puts a
# 110-column capture near 1850 pixels and a 130-column one near 2200, and the
# browser scales it down. When a picture is too heavy, the frame rate and the
# rows are what give, never the font size.
#
# The font is JetBrains Mono (the Nerd Font build where it is installed, which
# carries the drawing kit and the glyph set the interface draws), falling back
# to agg's own list of monospaced faces. The ground is the dark palette's own,
# #1c1c1c, so the picture reads as the terminal did.
#
# The keyboard is the Linux one. drive.sh defaults PLATFORM to linux, and it
# is left there on purpose: it is the keyboard most readers and the CI runner
# have, and the one every scene's snaps are written against. A Mac ships its
# alt chords on the function row, so a picture taken with PLATFORM=darwin
# would show a key row most readers do not have.
#
#   scripts/tui/readme-pictures.sh            every picture
#   scripts/tui/readme-pictures.sh <scene>    only the pictures from one scene
#
# Wants tmux, python3, agg and asciinema, and builds the binary unless
# SHHH_BIN names one.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
dest=$root/docs/readme
only=${1:-}

FONT_SIZE=${FONT_SIZE:-28}
FONT="JetBrainsMono Nerd Font Mono,JetBrains Mono,Fira Code,SF Mono,Menlo,DejaVu Sans Mono"
# agg's custom theme: the ground, the text, then the sixteen ANSI colours. The
# interface draws in truecolor, so the sixteen are only a fallback.
THEME=1c1c1c,d0d0d0,1c1c1c,d75f5f,87af87,d7af5f,5f87d7,af87d7,5fafaf,bcbcbc,585858,ff8787,afd7af,ffd787,87afff,d7afff,87d7d7,eeeeee

for need in tmux python3 agg asciinema; do
	command -v $need >/dev/null 2>&1 || { echo "readme-pictures.sh: $need is required" >&2; exit 2; }
done

if [ -z "${SHHH_BIN:-}" ]; then
	SHHH_BIN=$root/bin/tui/shhh
	(cd "$root" && CGO_ENABLED=0 go build -o "$SHHH_BIN" ./cmd/shhh)
fi
export SHHH_BIN

# The captures stay under bin/readme/ after the run, never committed, so each
# picture can be read against the `.txt` it was drawn from.
work=$root/bin/readme
rm -rf "$work"
mkdir -p "$work" "$dest"

# The stills: scene, snap, and the rows of the capture to keep, as ranges
# counted from one — a run of blank rows between the transcript and the
# bottom panel, or a notice the picture is not about, is cut out. Width and
# height are the scene's own `size`, or 110 by 40 where it states none.
stills=(
	"smoke 03-approval 1-11,25-40"
	"fanout-lanes 05-three-states 1-25,34-46,49-52"
	"commit-offer 03-close 1-14,37-40"
	"one-shot 02-result 2-7"
	"sprint-lanes 03-sprint-tab 1-28,38-40"
)
# The hero: the whole of one scene, recorded, and how many frames a second it
# is drawn at.
hero_scene=commit-offer
hero_fps=8

size_of() {
	local cols=110 rows=40
	[ -f "$here/scenes/$1/size" ] && read -r cols rows < "$here/scenes/$1/size"
	echo "$cols $rows"
}

# Keep the rows the ranges name. tmux writes a colour where it changes, which
# may be several rows above a row kept, so every SGR sequence from the rows
# cut away goes in front of the next row kept: SGR is cumulative, and each
# kept row is then drawn in the colour it had. Prints how many rows it kept.
crop() {
	python3 - "$1" "$2" "$3" <<'PY'
import re, sys
src, out, ranges = sys.argv[1], sys.argv[2], sys.argv[3]
lines = open(src, encoding="utf-8", errors="replace").read().split("\n")
if lines and lines[-1] == "":
    lines.pop()
keep = set()
for r in ranges.split(","):
    a, _, b = r.partition("-")
    keep.update(range(int(a), int(b or a) + 1))
sgr = re.compile(r"\x1b\[[0-9;:]*m")
kept, carried = [], ""
for n, line in enumerate(lines, 1):
    if n in keep:
        kept.append(carried + line)
        carried = ""
    else:
        carried += "".join(m.group(0) for m in sgr.finditer(line))
open(out, "w", encoding="utf-8").write("\n".join(kept) + "\n")
print(len(kept))
PY
}

draw() {
	agg --text-font-family "$FONT" --font-size "$FONT_SIZE" --theme "$THEME" "$@" >/dev/null
}

driven=" "
drive() {
	local scene=$1 mode=$2 out=$work/$1$2
	case $driven in *" $scene$mode "*) return 0 ;; esac
	read -r cols rows < <(size_of "$scene")
	echo "driving $scene at ${cols}x$rows${mode:+ ($mode)}"
	OUT=$out COLS=$cols ROWS=$rows "$here/drive.sh" $mode "$here/scenes/$scene" >"$work/$scene$mode.log" 2>&1 ||
		{ echo "readme-pictures.sh: $scene did not run — $work/$scene$mode.log:" >&2; sed 's/^/  /' "$work/$scene$mode.log" >&2; exit 1; }
	driven="$driven$scene$mode "
}

for entry in "${stills[@]}"; do
	read -r scene snap ranges <<<"$entry"
	[ -n "$only" ] && [ "$only" != "$scene" ] && continue
	drive "$scene" ""
	read -r cols rows < <(size_of "$scene")
	kept=$(crop "$work/$scene/$snap.ansi" "$work/$scene-$snap.ansi" "$ranges")
	python3 "$here/still.py" "$work/$scene-$snap.ansi" "$work/$scene-$snap.cast" "$cols" "$kept"
	draw --cols "$cols" --rows "$kept" --last-frame-duration 1 --fps-cap 1 \
		"$work/$scene-$snap.cast" "$dest/$scene-$snap.gif"
	echo "  $dest/$scene-$snap.gif  $(wc -c <"$dest/$scene-$snap.gif" | tr -d ' ') bytes"
done

if [ -z "$only" ] || [ "$only" = "$hero_scene" ]; then
	drive "$hero_scene" --record
	cast=$work/$hero_scene--record/$hero_scene.cast
	[ -s "$cast" ] || { echo "readme-pictures.sh: no recording at $cast" >&2; exit 1; }
	read -r cols rows < <(size_of "$hero_scene")
	draw --cols "$cols" --rows "$rows" --fps-cap "$hero_fps" --idle-time-limit 1.5 --last-frame-duration 4 \
		"$cast" "$dest/$hero_scene.cast.gif"
	echo "  $dest/$hero_scene.cast.gif  $(wc -c <"$dest/$hero_scene.cast.gif" | tr -d ' ') bytes"
fi
