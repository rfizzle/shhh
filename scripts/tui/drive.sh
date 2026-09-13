#!/usr/bin/env bash
# Drive the built shhh binary through a scene in a tmux pane, against the
# scripted provider, and capture the screen at every step the scene names.
#
# A scene is a directory holding two files, and optionally a third:
#
#   replies.txt   what the model says, one reply per request (fakeprovider.py)
#   launch        what the pane runs, one shell line; default: $SHHH_BIN code
#   size          the pane, columns then rows; default 120 40
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
# A capture is cells; --pictures draws them. Each snap's `.ansi` — the same
# cells with their colour — is wrapped as a one-frame asciicast (still.py) and
# drawn by agg, which leaves a still per snap beside the capture it came from.
# That is the picture a person or an agent reads for what text cannot carry: a
# colour, a weight, a glyph that fell back. It wants agg (`brew install agg`)
# and nothing else; the tmux pass is the gate and wants not even that.
#
# The picture is drawn from the capture rather than by playing the scene over
# again in a browser, which is both lighter and exact: the still and the `.txt`
# beside it are the same bytes, so they cannot disagree about what was on
# screen, and there is no second run of the scene for them to disagree about.
#
# --record adds the motion: the whole run wrapped in
# asciinema, written beside the captures as <scene>.cast — text, so it diffs,
# and playable with `asciinema play`. Where agg is installed the cast is
# rendered to a GIF beside it; that is a bonus, and the cast is the record. A
# machine without asciinema says so and records nothing: the run is the gate,
# and the recording never decides it.
#
#   drive.sh <scene-dir>              run the steps and capture
#   drive.sh --pictures <scene-dir>   the same, and draw a still per snap
#   drive.sh --record <scene-dir>     the same, and record the run as a .cast
#   drive.sh --attach <scene-dir>     open the same pane in this terminal instead
#
# Two runs on one host do not meet. The provider's port is a free one asked
# of the kernel as the run starts and the tmux server is named for the run,
# so a second checkout's run cannot take the first's port or kill its server;
# naming PORT or SOCK overrides both, for a reader who wants to know where to
# look. The tmux socket lives with the run's other scratch under $TMPDIR and
# not under OUT, because a Unix socket's path is capped near 104 bytes and
# OUT's is the checkout's: from a worktree under .claude/worktrees/<name>/
# the cap is already spent, tmux answers "File name too long" and every snap
# times out. Nothing extra is needed to drive a scene from a worktree.
#
# Environment: SHHH_BIN (the binary; default ./shhh), COLS/ROWS (the pane;
# over the scene's own size, else 120x40), OUT (captures; default
# bin/tui/<scene>), WAIT (seconds a snap waits for its text; default 20),
# PORT and SOCK (the provider's port and the tmux server's name; both per run
# unless set), and TMUX_TMPDIR (the tmux socket directory; a directory of the
# run's own under $TMPDIR unless set).
set -u
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)

attach=0
record=0
pictures=0
while [ $# -gt 0 ]; do
	case $1 in
	--attach) attach=1; shift ;;
	--record) record=1; shift ;;
	--pictures) pictures=1; shift ;;
	*) break ;;
	esac
done
scene=${1:?usage: drive.sh [--attach] [--record] [--pictures] <scene-dir>}
scene=$(cd "$scene" && pwd) || exit 1
name=$(basename "$scene")

SHHH_BIN=${SHHH_BIN:-$root/shhh}
# A scene that needs a particular pane says so in a `size` file — `144 40`,
# columns then rows — because a rail that only appears past a breakpoint is
# not on a 120-column screen to be waited for. The environment still wins, so
# a reader can ask for one scene at another width without editing it.
scene_cols=; scene_rows=
[ -f "$scene/size" ] && read -r scene_cols scene_rows < "$scene/size"
COLS=${COLS:-${scene_cols:-120}}
ROWS=${ROWS:-${scene_rows:-40}}
OUT=${OUT:-$root/bin/tui/$name}
WAIT=${WAIT:-20}
# Named for this run rather than for the script, so a second run does not
# talk to — or kill — the first one's tmux server.
SOCK=${SOCK:-shhh-tui-$$}

for need in tmux python3; do
	command -v $need >/dev/null 2>&1 || { echo "drive.sh: $need is required (brew install $need / apt-get install $need)" >&2; exit 2; }
done
[ -x "$SHHH_BIN" ] || { echo "drive.sh: no binary at $SHHH_BIN — run make tui-check, or set SHHH_BIN" >&2; exit 2; }
# The port is asked of the kernel rather than fixed at a number two runs
# would both pick. The kernel names one nothing is listening on and the
# provider takes it a moment later; that gap is the only race, and the wait
# below is what closes it — a provider that lost the port has already died,
# and the wait says so by name instead of leaving every snap to time out.
if [ -z "${PORT:-}" ]; then
	PORT=$(python3 -c 'import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()') || PORT=
	[ -n "$PORT" ] || { echo "drive.sh: could not get a free port for the scripted model — set PORT" >&2; exit 2; }
fi
if [ "$pictures" = 1 ]; then
	command -v agg >/dev/null 2>&1 || { echo "drive.sh: --pictures needs agg (brew install agg)" >&2; exit 2; }
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
work=$(mktemp -d "${TMPDIR:-/tmp}/shhh-tui.XXXXXX") || { echo "drive.sh: could not make a scratch directory under ${TMPDIR:-/tmp}" >&2; exit 1; }
home=$work/home
ws=$work/ws
# The tmux socket goes here too. It cannot go under OUT: a Unix socket's path
# is capped at 104 bytes on macOS and OUT's begins with the checkout's own
# path, so a worktree under .claude/worktrees/<name>/ overflows it and tmux
# fails with "File name too long" — which reaches the reader as every snap
# timing out. Under the run's own scratch the path is short whatever the
# checkout is called, and it is removed with the rest of the scratch. An
# inherited TMUX_TMPDIR still wins, for a reader with somewhere of their own.
TMUX_TMPDIR=${TMUX_TMPDIR:-$work/t}
export TMUX_TMPDIR
# Set before anything is started, so a run that stops here still takes its
# scratch — the socket directory with it — away.
provider=
cleanup() {
	tmux -L "$SOCK" kill-server 2>/dev/null
	if [ -n "$provider" ]; then
		kill "$provider" 2>/dev/null
		wait "$provider" 2>/dev/null
	fi
	rm -rf "$work"
}
trap cleanup EXIT
mkdir -p "$home/config/shhh" "$ws" "$OUT" || { echo "drive.sh: could not make the run's directories under $work and $OUT" >&2; exit 1; }
mkdir -p "$TMUX_TMPDIR" || { echo "drive.sh: could not make the tmux socket directory $TMUX_TMPDIR" >&2; exit 1; }
# tmux puts its socket at $TMUX_TMPDIR/tmux-<uid>/<name>. Said here, with the
# path, rather than left to tmux to report as a name that is too long.
sockpath=$TMUX_TMPDIR/tmux-$(id -u)/$SOCK
if [ ${#sockpath} -ge 104 ]; then
	echo "drive.sh: the tmux socket path is ${#sockpath} bytes and a Unix socket's is capped at 104: $sockpath — set TMUX_TMPDIR to a shorter directory" >&2
	exit 1
fi
# Everything the last run left, gone before this one starts. A capture is
# rewritten every run and a picture is not, so a still from a scene that has
# since been renamed would sit in the directory being read as this run's — and
# a picture nobody took is the one thing a picture must never be. The logs are
# left to the redirections that write them.
rm -f "$OUT"/*.txt "$OUT"/*.ansi "$OUT"/*.gif "$OUT"/*.cast
printf '[behavior]\nprovider_retries = 0\n' > "$home/config/shhh/config.toml"
(cd "$ws" && git init -q && git -c user.email=tui@shhh -c user.name=tui commit -q --allow-empty -m init)

python3 "$here/fakeprovider.py" "$PORT" "$scene/replies.txt" 2> "$OUT/provider.log" &
provider=$!
# Up before the binary asks, or the first turn reports a model it never
# reached. A provider that is gone is not waited for: it lost the port to
# something else and the log says so, which is worth more than ten more
# tries at a port that will never be ours.
wait_for_provider() {
	for _ in 1 2 3 4 5 6 7 8 9 10; do
		kill -0 "$provider" 2>/dev/null || return 1
		python3 -c "import socket; socket.create_connection(('127.0.0.1', $PORT), 1).close()" 2>/dev/null && return 0
		sleep 0.2
	done
	return 1
}
wait_for_provider || { echo "drive.sh: the scripted model never came up on port $PORT — $OUT/provider.log" >&2; exit 1; }

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

# The pictures. Each snap's captured cells, with their colour, drawn as a
# still beside the capture they came from: still.py wraps the `.ansi` as a
# one-frame asciicast and agg draws it, since one frame is one picture.
#
# Drawn from the capture rather than from a second run of the scene in a
# browser. That is lighter — agg is one binary and wants neither — and it is
# exact: the still and the `.txt` beside it are the same bytes, so they cannot
# disagree about what was on screen, and there is no second run for them to
# disagree about. What it cannot show is where the cursor stood, which tmux
# does not capture either.
#
# Held to what it wrote, like everything else this script asks another program
# for: a still that never arrived is said out loud rather than left for
# whoever reads the directory to notice.
render_pictures() {
	rendered=0
	unwritten=""
	for ansi in "$OUT"/*.ansi; do
		[ -e "$ansi" ] || break
		snap=$(basename "$ansi" .ansi)
		# The cast between the capture and the picture is scaffolding, so it
		# is built with the rest of the run's scaffolding: the capture
		# directory holds only what is meant to be read.
		python3 "$here/still.py" "$ansi" "$work/$snap.cast" "$COLS" "$ROWS" || return 1
		agg --cols "$COLS" --rows "$ROWS" --last-frame-duration 1 --fps-cap 1 \
			"$work/$snap.cast" "$OUT/$snap.gif" >/dev/null 2>&1
		if [ -s "$OUT/$snap.gif" ]; then
			rendered=$((rendered + 1))
		else
			unwritten="$unwritten $snap.gif"
		fi
	done
	if [ -n "$unwritten" ]; then
		echo "drive.sh: agg drew nothing for:$unwritten" >&2
		return 1
	fi
	echo "pictures: $rendered stills under $OUT"
}
if [ "$pictures" = 1 ] && [ "$failed" = 0 ]; then
	render_pictures || failed=1
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
		# agg renders the cast, the same way it draws the stills. It writes
		# beside the cast rather than over a snap's own name: the run and a
		# moment of it are two different pictures.
		if command -v agg >/dev/null 2>&1; then
			agg "$cast" "$OUT/$name.cast.gif" >/dev/null 2>&1 && echo "recording: $OUT/$name.cast.gif"
		else
			echo "drive.sh: brew install agg to render $cast as a GIF" >&2
		fi
	else
		echo "drive.sh: asciinema recorded nothing to $cast" >&2
	fi
fi
echo "captures: $OUT ($step)"
exit $failed
