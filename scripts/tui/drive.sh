#!/usr/bin/env bash
# Drive the built shhh binary through a scene in a tmux pane, against the
# scripted provider, and capture the screen at every step the scene names.
#
# A scene is a directory holding two files, and optionally a third:
#
#   replies.txt   what the model says, one reply per request (fakeprovider.py)
#   launch        what the pane runs, one shell line; default: $SHHH_BIN code
#   steps.txt     what the reader does, one step per line:
#
#     setup <shell>             run in the workspace before the binary starts
#     keys <tmux send-keys …>   type; Enter, Escape, Tab, BTab, Up, C-c, "a line"
#     snap <name> [text]        capture the screen once <text> is on it
#     sleep <seconds>           wait, for the rare step nothing on screen marks
#
# A snap that names text polls the screen for it and fails the run when it
# never appears, so a scene is also a test: the exit code says whether every
# step drew what it said it would. Captures land under $OUT as <name>.txt (the
# cells) and <name>.ansi (the cells with colour).
#
# A capture is cells; --vhs adds what they look like. The scene is written
# out as a vhs tape and played again in a real terminal (ttyd in a headless
# browser), which leaves a PNG per snap and a GIF of the run beside the
# captures. That is the picture a person or an agent reads for what text
# cannot carry — a colour, a weight, a glyph that fell back. It needs vhs,
# ttyd and ffmpeg (`brew install vhs` brings all three); the tmux pass is the
# gate and needs none of them.
#
# A snap is a still. --record adds the motion: the whole run wrapped in
# asciinema, written beside the captures as <scene>.cast — text, so it diffs,
# and playable with `asciinema play`. Where agg is installed the cast is
# rendered to a GIF beside it; that is a bonus, and the cast is the record. A
# machine without asciinema says so and records nothing: the run is the gate,
# and the recording never decides it.
#
#   drive.sh <scene-dir>            run the steps and capture
#   drive.sh --record <scene-dir>   the same, and record the run as a .cast
#   drive.sh --vhs <scene-dir>      the same, then play it in vhs for pictures
#   drive.sh --attach <scene-dir>   open the same pane in this terminal instead
#
# Environment: SHHH_BIN (the binary; default ./shhh), COLS/ROWS (the pane,
# default 120x40), OUT (captures; default bin/tui/<scene>), WAIT (seconds a
# snap waits for its text; default 20), PORT and SOCK (the provider's port and
# the tmux server's name, for two scenes running at once).
set -u
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)

attach=0
record=0
vhs=0
while [ $# -gt 0 ]; do
	case $1 in
	--attach) attach=1; shift ;;
	--record) record=1; shift ;;
	--vhs) vhs=1; shift ;;
	*) break ;;
	esac
done
scene=${1:?usage: drive.sh [--attach] [--record] [--vhs] <scene-dir>}
scene=$(cd "$scene" && pwd) || exit 1
name=$(basename "$scene")

SHHH_BIN=${SHHH_BIN:-$root/shhh}
COLS=${COLS:-120}
ROWS=${ROWS:-40}
OUT=${OUT:-$root/bin/tui/$name}
WAIT=${WAIT:-20}
PORT=${PORT:-8765}
SOCK=${SOCK:-shhh-tui}

for need in tmux python3; do
	command -v $need >/dev/null 2>&1 || { echo "drive.sh: $need is required (brew install $need / apt-get install $need)" >&2; exit 2; }
done
[ -x "$SHHH_BIN" ] || { echo "drive.sh: no binary at $SHHH_BIN — run make tui-check, or set SHHH_BIN" >&2; exit 2; }
if [ "$vhs" = 1 ]; then
	for need in vhs ttyd ffmpeg; do
		command -v $need >/dev/null 2>&1 || { echo "drive.sh: --vhs needs $need (brew install vhs brings vhs, ttyd and ffmpeg)" >&2; exit 2; }
	done
fi
# The pane is opened in the scene's own workspace, so a relative path to the
# binary would be resolved there and found nowhere.
SHHH_BIN=$(cd "$(dirname "$SHHH_BIN")" && pwd)/$(basename "$SHHH_BIN")
[ -f "$scene/replies.txt" ] && [ -f "$scene/steps.txt" ] || { echo "drive.sh: $scene needs replies.txt and steps.txt" >&2; exit 2; }

# Recording is a bonus, never a gate: a missing recorder is said out loud and
# the run goes on. An attached pane is the reader's to drive and stops when
# they detach, which would leave a half-written cast, so it is not recorded.
cast=$OUT/$name.cast
if [ "$record" = 1 ] && [ "$attach" = 1 ]; then
	echo "drive.sh: --record records a driven run; an attached pane is not recorded" >&2
	record=0
fi
if [ "$record" = 1 ] && ! command -v asciinema >/dev/null 2>&1; then
	echo "drive.sh: no asciinema — recording nothing (brew install asciinema); the run is otherwise unchanged" >&2
	record=0
fi

# Everything the run touches is its own: a home so no developer setting or
# saved chat leaks in, and a fresh repository to work in, because the start
# screen and the approval card both read the checkout they are opened in.
work=$(mktemp -d "${TMPDIR:-/tmp}/shhh-tui.XXXXXX")
home=$work/home
ws=$work/ws
mkdir -p "$home/config/shhh" "$ws" "$OUT"
printf '[behavior]\nprovider_retries = 0\n' > "$home/config/shhh/config.toml"
(cd "$ws" && git init -q && git -c user.email=tui@shhh -c user.name=tui commit -q --allow-empty -m init)

python3 "$here/fakeprovider.py" "$PORT" "$scene/replies.txt" 2> "$OUT/provider.log" &
provider=$!
cleanup() {
	tmux -L "$SOCK" kill-server 2>/dev/null
	kill "$provider" 2>/dev/null
	wait "$provider" 2>/dev/null
	rm -rf "$work"
}
trap cleanup EXIT
# Up before the binary asks, or the first turn reports a model it never
# reached.
for _ in 1 2 3 4 5 6 7 8 9 10; do
	python3 -c "import socket; socket.create_connection(('127.0.0.1', $PORT), 1).close()" 2>/dev/null && break
	sleep 0.2
done

# The setup lines run before the binary does, so a scene can put a file, a
# .shhh directory or a commit in the workspace it will be opened on.
while IFS= read -r line; do
	case $line in
	setup\ *) (cd "$ws" && eval "${line#setup }") || { echo "drive.sh: setup failed: ${line#setup }" >&2; exit 1; } ;;
	esac
done < "$scene/steps.txt"

envs="HOME=$home XDG_CONFIG_HOME=$home/config XDG_DATA_HOME=$home/data"
envs="$envs SHHH_PROVIDER=openai-compatible SHHH_BASE_URL=http://127.0.0.1:$PORT/v1 SHHH_API_KEY=scripted SHHH_MODEL=scripted-model SHHH_REASONING=medium"
envs="$envs TERM=xterm-256color COLORTERM=truecolor"

# What the pane runs. `shhh code` is the default because most scenes are the
# session, but it is one of four sizes and the others are separate entry
# points rather than something typed into the session — a scene reaching the
# one-shot cannot get there with keys. A `launch` file is that scene's own
# line: ordinary shell, with $SHHH_BIN the built binary, so a scene can also
# pipe into a surface to see what it does with no terminal on the other end.
# Keep it to single quotes: the line is re-quoted for the recorder below.
launch="\$SHHH_BIN code"
if [ -f "$scene/launch" ]; then
	launch=$(grep -v '^[[:space:]]*#' "$scene/launch" | grep -m1 .)
	[ -n "$launch" ] || { echo "drive.sh: $scene/launch names no command" >&2; exit 2; }
fi
# The environment is exported rather than prefixed with env(1), because a
# launch line is a whole shell line and a prefix would reach only its first
# command.
run="export $envs SHHH_BIN=$SHHH_BIN; $launch"
# The recorder wraps the binary inside the pane, so the cast is the pane's own
# size and every cell tmux sees is a cell it saw. -q keeps asciinema's
# diagnostics off the screen, where a snap would otherwise read them.
[ "$record" = 1 ] && run="asciinema rec -q --overwrite -c \"$run\" \"$cast\""

tmux -L "$SOCK" kill-server 2>/dev/null
# The pane outlives the binary so the exit banner can be captured too.
tmux -L "$SOCK" new-session -d -s scene -x "$COLS" -y "$ROWS" -c "$ws" "$run; sleep 60"

if [ "$attach" = 1 ]; then
	echo "$launch against $scene/replies.txt — detach with ctrl+b d"
	tmux -L "$SOCK" attach -t scene
	exit 0
fi

screen() { tmux -L "$SOCK" capture-pane -p -t scene 2>/dev/null; }

failed=0
step=0
while IFS= read -r line || [ -n "$line" ]; do
	case $line in
	""|\#*|setup\ *) continue ;;
	keys\ *)
		eval "set -- ${line#keys }"
		tmux -L "$SOCK" send-keys -t scene "$@"
		;;
	sleep\ *)
		sleep "${line#sleep }"
		;;
	snap\ *)
		eval "set -- ${line#snap }"
		snapname=$1
		want=${2:-}
		deadline=$(( $(date +%s) + WAIT ))
		while [ -n "$want" ] && ! screen | grep -qF -- "$want"; do
			if [ "$(date +%s)" -ge "$deadline" ]; then
				echo "drive.sh: $name/$snapname: waited ${WAIT}s and never saw: $want" >&2
				failed=1
				break
			fi
			sleep 0.2
		done
		# One more frame so a row that arrived with the text has drawn too.
		sleep 0.3
		screen > "$OUT/$snapname.txt"
		tmux -L "$SOCK" capture-pane -p -e -t scene > "$OUT/$snapname.ansi" 2>/dev/null
		step=$((step + 1))
		echo "  $snapname${want:+  ✓ \"$want\"}"
		;;
	*)
		echo "drive.sh: $name: unknown step: $line" >&2
		failed=1
		;;
	esac
	[ "$failed" = 1 ] && break
done < "$scene/steps.txt"

# The vhs pass. The tape is the scene translated: a keys line is Type and
# the named keys, a snap is a screen-scoped Wait and a Screenshot, a sleep is
# a Sleep. The binary starts from a shell inside vhs's own terminal, so the
# environment goes in as Env lines and the size is pinned with stty: vhs
# sizes its window in pixels, and a scene's width is a column count that has
# to be exact, because the frame changes shape at the breakpoints. Every
# path in the tape is quoted, because the parser reads a bare leading slash
# as a regex and a bare leading digit as a number.
#
# The second of Sleep after each screenshot is not padding. A card that has
# just been drawn takes the keyboard a moment after it appears, and a key
# typed into that moment lands in the draft underneath it; tmux's capture
# takes long enough on its own that the pass above never noticed.
# A modified key is a letter in tmux (C-c) and a capital in vhs (Ctrl+C); a
# modified named key (C-Space, S-Up) is the same word in both.
tape_keyname() {
	case ${#1} in
	1) printf '%s' "$1" | tr '[:lower:]' '[:upper:]' ;;
	*) printf '%s' "$1" ;;
	esac
}
tape_key() {
	case $1 in
	Enter|Escape|Tab|Space|Up|Down|Left|Right|Backspace|Home|End) echo "$1" ;;
	# vhs has no spelling for a control chord on a punctuation key: after
	# Ctrl+ its parser wants a letter, a named key or a second modifier, and
	# refuses the whole tape over one. The byte the terminal actually
	# delivers has no such problem — ctrl+/ and ctrl+_ are both the unit
	# separator — so it is typed rather than named. tmux takes the key by
	# name and needs none of this.
	C-/|C-_) printf 'Type "\037"\n' ;;
	BTab) echo "Shift+Tab" ;;
	PgUp|PPage) echo "PageUp" ;;
	PgDn|NPage) echo "PageDown" ;;
	C-*) echo "Ctrl+$(tape_keyname "${1#C-}")" ;;
	M-*) echo "Alt+$(tape_keyname "${1#M-}")" ;;
	S-*) echo "Shift+$(tape_keyname "${1#S-}")" ;;
	*) printf 'Type `%s`\n' "$1" ;;
	esac
}
tape_regex() { printf '%s' "$1" | sed 's/[][\\.*+?(){}|^$\/]/\\&/g'; }
write_tape() {
	echo "Output \"$OUT/$name.gif\""
	echo "Set Shell bash"
	echo "Set FontSize 14"
	echo "Set Padding 10"
	echo "Set Width $(( COLS * 9 + 40 ))"
	echo "Set Height $(( ROWS * 20 + 40 ))"
	echo "Set TypingSpeed 20ms"
	for kv in $envs; do
		echo "Env ${kv%%=*} \"${kv#*=}\""
	done
	echo "Hide"
	echo "Type \"cd $ws && stty cols $COLS rows $ROWS && export SHHH_BIN=$SHHH_BIN && $launch\""
	echo "Enter"
	echo "Sleep 500ms"
	echo "Show"
	while IFS= read -r line || [ -n "$line" ]; do
		case $line in
		""|\#*|setup\ *) continue ;;
		keys\ *)
			eval "set -- ${line#keys }"
			for k in "$@"; do tape_key "$k"; done
			;;
		sleep\ *) echo "Sleep ${line#sleep }s" ;;
		snap\ *)
			eval "set -- ${line#snap }"
			[ -n "${2:-}" ] && echo "Wait+Screen@${WAIT}s /$(tape_regex "$2")/"
			echo "Sleep 300ms"
			echo "Screenshot \"$OUT/$1.png\""
			echo "Sleep 1s"
			;;
		esac
	done < "$scene/steps.txt"
}
if [ "$vhs" = 1 ] && [ "$failed" = 0 ]; then
	tmux -L "$SOCK" kill-server 2>/dev/null
	# The pass above used the replies up and worked in the workspace; the tape
	# starts both again from the top.
	kill "$provider" 2>/dev/null; wait "$provider" 2>/dev/null
	python3 "$here/fakeprovider.py" "$PORT" "$scene/replies.txt" 2>> "$OUT/provider.log" &
	provider=$!
	rm -rf "$ws"; mkdir -p "$ws"
	(cd "$ws" && git init -q && git -c user.email=tui@shhh -c user.name=tui commit -q --allow-empty -m init)
	while IFS= read -r line; do
		case $line in
		setup\ *) (cd "$ws" && eval "${line#setup }") ;;
		esac
	done < "$scene/steps.txt"
	write_tape > "$OUT/$name.tape"
	if vhs "$OUT/$name.tape" > "$OUT/vhs.log" 2>&1; then
		echo "pictures: $OUT/$name.gif, and a png per snap"
	else
		# vhs writes its screenshots only when the whole tape played, so a
		# wait that timed out leaves no picture at all; the log says which.
		echo "drive.sh: vhs failed — $OUT/vhs.log" >&2
		grep -m1 'timeout waiting' "$OUT/vhs.log" | cut -c1-160 >&2
		failed=1
	fi
fi

if [ "$failed" = 1 ]; then
	echo "drive.sh: the model was asked $(grep -c '^POST' "$OUT/provider.log" 2>/dev/null || true) times — $OUT/provider.log" >&2
fi
if [ "$record" = 1 ]; then
	# The recorder writes the file as the binary exits, a moment after the last
	# snap read the banner it drew on the way out.
	for _ in 1 2 3 4 5; do [ -s "$cast" ] && break; sleep 0.2; done
	if [ -s "$cast" ]; then
		echo "recording: $cast"
		# agg is what renders a cast; vhs drives a tape of its own and cannot
		# read one, so it is named here only to say where the GIF went.
		if command -v agg >/dev/null 2>&1; then
			agg "$cast" "$OUT/$name.gif" && echo "recording: $OUT/$name.gif"
		elif command -v vhs >/dev/null 2>&1; then
			echo "drive.sh: vhs drives a .tape, not a .cast — brew install agg to render $cast as a GIF" >&2
		fi
	else
		echo "drive.sh: asciinema recorded nothing to $cast" >&2
	fi
fi
echo "captures: $OUT ($step)"
exit $failed
