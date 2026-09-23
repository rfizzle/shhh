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
#     press <seconds> <key> …   the keys <seconds> apart, timed by tmux itself
#     paste <file>              bracketed-paste a file the setup wrote
#     shell <shell>             run beside the binary, mid-scene, in the
#                               workspace with the run's environment and
#                               $SHHH_BIN; output to shell.log, and a failure
#                               fails the run
#     snap <name> [text] [also …]
#                               capture the screen once <text> is on it; every
#                               further string must be on that capture too
#     sleep <seconds>           wait, for the rare step nothing on screen marks
#     wide <cols> <step>        the step, only where the pane is <cols> or wider
#     narrow <cols> <step>      the step, only where the pane is narrower; the
#                               two stack, to bound a step on both sides
#
# A snap that names text polls the screen for it and fails the run when it
# never appears, so a scene is also a test: the exit code says whether every
# step drew what it said it would. The strings after the first are compared,
# not awaited: the first says when the screen is ready, and the rest must be
# on the screen it was ready on — a row the capture is for, which a wait word
# alone would let go missing unnoticed. One absent from the capture is looked
# for again for COMPARE seconds, a frame late at most, and then fails the run
# naming the snap and the string. Captures land under $OUT as <name>.txt (the
# cells) and <name>.ansi (the cells with colour).
#
# `press` is for a gesture the binary times, such as the rewind's two escapes
# inside half a second: the keys leave in one tmux command and the server
# keeps the gap, so no process the harness starts between them can stretch
# it past the window on a loaded host.
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
# Two runs on one host do not meet. The provider asks the kernel for a free
# port, binds it and says which one it took, and the tmux server is named for
# the run, so a second checkout's run cannot take the first's port or kill
# its server; naming PORT or SOCK overrides both, for a reader who wants to
# know where to look. The tmux socket lives with the run's other scratch under $TMPDIR and
# not under OUT, because a Unix socket's path is capped near 104 bytes and
# OUT's is the checkout's: from a worktree under .claude/worktrees/<name>/
# the cap is already spent, tmux answers "File name too long" and every snap
# times out. Nothing extra is needed to drive a scene from a worktree.
#
# Environment: SHHH_BIN (the binary; default ./shhh), COLS/ROWS (the pane;
# over the scene's own size, else 120x40), OUT (captures; default
# bin/tui/<scene>), WAIT (seconds a snap waits for its text; default 20),
# COMPARE (seconds a compared string is looked for again; default 2),
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
COMPARE=${COMPARE:-2}
# Named for this run rather than for the script, so a second run does not
# talk to — or kill — the first one's tmux server.
SOCK=${SOCK:-shhh-tui-$$}

for need in tmux python3; do
	command -v $need >/dev/null 2>&1 || { echo "drive.sh: $need is required (brew install $need / apt-get install $need)" >&2; exit 2; }
done
[ -x "$SHHH_BIN" ] || { echo "drive.sh: no binary at $SHHH_BIN — run make tui-check, or set SHHH_BIN" >&2; exit 2; }
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
#
# Run inside shhh's containment — the `tui` quality suite — this is also the
# only place that works. Containment denies /tmp, where tmux puts its socket
# by default, and hands the command a TMPDIR of the session's own: a private
# /tmp on bubblewrap, a directory under shhh's data directory on Seatbelt.
# TMUX_TMPDIR is not among the variables that reach a contained command, so
# there the socket is always under that directory.
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
	echo "drive.sh: the tmux socket path is ${#sockpath} bytes and a Unix socket's is capped at 104: $sockpath — set TMUX_TMPDIR to a shorter directory; inside shhh's containment it is TMPDIR, the session's own under shhh's data directory, that has to be shorter" >&2
	exit 1
fi
# Everything the last run left, gone before this one starts. A capture is
# rewritten every run and a picture is not, so a still from a scene that has
# since been renamed would sit in the directory being read as this run's — and
# a picture nobody took is the one thing a picture must never be. The logs are
# left to the redirections that write them, except the shell steps', which
# each append to one.
rm -f "$OUT"/*.txt "$OUT"/*.ansi "$OUT"/*.gif "$OUT"/*.cast "$OUT/shell.log"
printf '[behavior]\nprovider_retries = 0\n' > "$home/config/shhh/config.toml"
(cd "$ws" && git init -q && git -c user.email=tui@shhh -c user.name=tui commit -q --allow-empty -m init)

# The provider asks the kernel for a free port, binds it, and says which one
# it took: a port this script chose and the provider bound a moment later is a
# window anything else on the host can take it in. PORT names one instead, for
# a reader who wants to know where to look.
portfile=$work/port
asked=${PORT:-0}
python3 "$here/fakeprovider.py" "$asked" "$scene/replies.txt" > "$portfile" 2> "$OUT/provider.log" &
provider=$!
# Up before the binary asks, or the first turn reports a model it never
# reached. The port line is written once the socket is bound and listening, so
# reading it is the whole of the wait. The poll stays short and the bound is
# long: on a loaded host a python interpreter can take several seconds just to
# start, and two seconds failed a run at a load of ~8 with the provider still
# on its way up. A provider that died instead — a replies file it could not
# read is the usual reason — is not waited for: it said why on the way out,
# and that is worth more than the rest of the bound.
provider_wait=30
wait_for_provider() {
	tries=$((provider_wait * 5))
	while [ "$tries" -gt 0 ]; do
		PORT=$(sed -n 's/^port //p' "$portfile")
		[ -n "$PORT" ] && return 0
		kill -0 "$provider" 2>/dev/null || return 1
		sleep 0.2
		tries=$((tries - 1))
	done
	return 1
}
if ! wait_for_provider; then
	if [ "$asked" = 0 ]; then port_words="a free port"; else port_words="port $asked"; fi
	if kill -0 "$provider" 2>/dev/null; then
		echo "drive.sh: the scripted model (fakeprovider.py, pid $provider, asked for $port_words) did not say it was listening within ${provider_wait}s — $OUT/provider.log:" >&2
	else
		echo "drive.sh: the scripted model (fakeprovider.py, asked for $port_words) exited before it was listening — $OUT/provider.log:" >&2
	fi
	sed 's/^/  /' "$OUT/provider.log" >&2
	exit 1
fi

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
#
# `stty -tabs` first, because a capture is cells and a hard tab is not one.
# The renderer moves the cursor across blank cells with a tab wherever the
# pty's TABDLY says tabs are real, and tmux marks every blank cell a tab
# crosses so that capture-pane prints one `\t` for the run — a draft that
# holds `and check the exit` then captures as `and check\tthe exit` on the
# frames where a stop happened to land on that space, and a snap waiting for
# the sentence never sees it. With the tab delay set the renderer moves by
# spaces and cursor sequences instead, and the kernel expands any tab a
# program writes, so what is captured is what is on the screen.
run="stty -tabs; export $envs SHHH_BIN=$SHHH_BIN; $launch"
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
	# A step one side of a width: `wide <cols>` runs it where the pane is at
	# least that wide, `narrow <cols>` where it is narrower, and the two
	# together bound it on both sides. A scene is driven at its own size by
	# the gate and at any COLS by hand, and a row that only exists past a
	# breakpoint — the inspector rail — is a failure to wait for on the other
	# side of it, where what the scene is for may still be there.
	skip=0
	while :; do
		case $line in
		wide\ *|narrow\ *) ;;
		*) break ;;
		esac
		side=${line%% *}
		line=${line#* }
		at=${line%% *}
		line=${line#* }
		case $at in
		""|*[!0-9]*)
			echo "drive.sh: $name: $side needs a column count before the step: $side $at $line" >&2
			failed=1
			break
			;;
		esac
		if [ "$side" = wide ]; then
			[ "$COLS" -ge "$at" ] || skip=1
		else
			[ "$COLS" -lt "$at" ] || skip=1
		fi
	done
	[ "$failed" = 1 ] && break
	[ "$skip" = 1 ] && continue
	case $line in
	""|\#*|setup\ *) continue ;;
	keys\ *)
		eval "set -- ${line#keys }"
		tmux -L "$SOCK" send-keys -t scene "$@"
		;;
	paste\ *)
		# A bracketed paste of a file the setup wrote, which is the one
		# thing send-keys cannot do: typed bytes arrive as keystrokes, and
		# what a surface does with two hundred lines arriving at once is a
		# different question from what it does with two hundred lines typed.
		# -p is the bracketing; the newline the buffer carries reaches the
		# program the way a terminal's own paste delivers it.
		tmux -L "$SOCK" load-buffer -b scene -- "$ws/${line#paste }" ||
			{ echo "drive.sh: $name: no such file to paste: ${line#paste }" >&2; exit 1; }
		tmux -L "$SOCK" paste-buffer -p -d -b scene -t scene
		;;
	shell\ *)
		# A second process beside the one in the pane — another shhh, most
		# often, which the session under test has to hear from while it runs.
		# It sees the same home and the same store the pane does, so what it
		# reads and writes is that session's machine rather than this one's.
		(cd "$ws" && export $envs SHHH_BIN="$SHHH_BIN" && eval "${line#shell }") >> "$OUT/shell.log" 2>&1 ||
			{ echo "drive.sh: $name: shell step failed: ${line#shell } — $OUT/shell.log:" >&2; sed 's/^/  /' "$OUT/shell.log" >&2; failed=1; }
		;;
	press\ *)
		# A gesture the binary times — two escapes inside the rewind's half
		# second — sent as one tmux command, with the gap between the keys
		# kept by the tmux server. The same keys as `keys … / sleep / keys …`
		# cost a process start per step, and on a loaded host those starts
		# alone can outlast the window the binary is measuring.
		eval "set -- ${line#press }"
		gap=$1
		shift
		seq=(send-keys -t scene "$1")
		shift
		for k in "$@"; do
			seq+=(";" run-shell -d "$gap" ";" send-keys -t scene "$k")
		done
		tmux -L "$SOCK" "${seq[@]}"
		;;
	sleep\ *)
		sleep "${line#sleep }"
		;;
	snap\ *)
		eval "set -- ${line#snap }"
		snapname=$1
		want=${2:-}
		shift
		[ $# -gt 0 ] && shift
		# Every string after the wait word is compared, not awaited: the wait
		# word says when the screen is ready, and the rest must be on that
		# same screen. A capture missing one is looked at again for up to
		# COMPARE seconds — a frame the terminal had not finished drawing —
		# and never for the whole of WAIT, because a row that is gone is the
		# failure this exists to catch and it should not cost a timeout.
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
		if [ "$failed" = 0 ] && [ $# -gt 0 ]; then
			settle=$(( $(date +%s) + COMPARE ))
			while :; do
				missing=()
				for also in "$@"; do
					grep -qF -- "$also" "$OUT/$snapname.txt" || missing+=("$also")
				done
				[ ${#missing[@]} -eq 0 ] && break
				[ "$(date +%s)" -ge "$settle" ] && break
				sleep 0.3
				screen > "$OUT/$snapname.txt"
			done
			for also in ${missing[@]+"${missing[@]}"}; do
				echo "drive.sh: $name/$snapname: saw \"$want\" but the capture does not hold: $also" >&2
				failed=1
			done
		fi
		tmux -L "$SOCK" capture-pane -p -e -t scene > "$OUT/$snapname.ansi" 2>/dev/null
		step=$((step + 1))
		# The mark says what the snap found: a tick for text that drew, a
		# cross for a wait that ran out or a compared string the capture did
		# not hold, so the line under a failure does not read as a pass. The
		# count is how many strings the capture was compared against.
		compared=
		[ $# -gt 0 ] && compared="  + $# compared"
		if [ "$failed" = 1 ]; then
			echo "  $snapname  ✗ \"$want\"$compared"
		else
			echo "  $snapname${want:+  ✓ \"$want\"}$compared"
		fi
		;;
	*)
		echo "drive.sh: $name: unknown step: $line" >&2
		failed=1
		;;
	esac
	[ "$failed" = 1 ] && break
done < "$scene/steps.txt"

# No capture holds a tab. A `.txt` is the screen's cells, and a blank cell is a
# space; a `\t` in one is the renderer's hard-tab move getting past the
# `stty -tabs` above, and it reads as a straight column that is crooked in the
# text. Checked here rather than in the Makefile, because every capture is
# written by this script — `tui-shot`, each scene of `tui-check` and the long
# path alike — and the old captures were cleared at the start, so every `.txt`
# under OUT is this run's. Each hit is named by file and line.
tabbed=$(grep -Hn "$(printf '\t')" "$OUT"/*.txt 2>/dev/null)
if [ -n "$tabbed" ]; then
	echo "drive.sh: $name: a capture holds a tab where the screen has spaces:" >&2
	printf '%s\n' "$tabbed" | sed 's/^/  /' >&2
	failed=1
fi

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
