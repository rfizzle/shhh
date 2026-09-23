#!/usr/bin/env bash
# Drive every scene it is handed, a few at a time, and end on one line that
# says how many passed and where each failure stopped.
#
#   check.sh <scene-dir>…     (make tui-check hands it every scene)
#
# The scenes run side by side because nothing on the host is theirs to share:
# drive.sh asks the kernel for each run's provider port and names each run's
# tmux server, so a second scene cannot take the first's port or kill its
# pane. TUI_JOBS bounds how many run at once (default 3). The bound is the
# point: every scene is a binary, a tmux server and a scripted model, a snap
# waits a fixed time for its text, and a host loaded past what the scenes
# leave room for fails snaps that would have passed one at a time.
# TUI_JOBS=1 is the serial run.
#
# A scene's output is held until the scene is over and then printed whole,
# under a lock, so two scenes finishing together cannot interleave their
# lines — which is why this is a script and not a line of the Makefile. A
# failing scene does not stop the others: a snap whose surface was reworded
# then costs one line of the summary rather than every scene after it, and
# the exit status is still non-zero when any scene failed.
#
# Environment: SHHH_BIN (the binary, handed to drive.sh), TUI_JOBS (how many
# scenes at once; default 3), and whatever else drive.sh reads (WAIT, COLS…).
set -u
here=$(cd "$(dirname "$0")" && pwd)

# One scene, called by xargs below: run it into a log, record its status, and
# print the log once it is done. It always answers 0 — the verdict is the
# status file, and a non-zero answer would ask xargs to stop starting scenes.
if [ "${1:-}" = --one ]; then
	scene=$2
	name=$(basename "$scene")
	log=$TUI_CHECK_LOGS/$name.log
	"$here/drive.sh" "$scene" >"$log" 2>&1
	echo $? >"$TUI_CHECK_LOGS/$name.status"
	until mkdir "$TUI_CHECK_LOGS/lock" 2>/dev/null; do sleep 0.1; done
	printf 'Driving %s...\n' "$name"
	cat "$log"
	rmdir "$TUI_CHECK_LOGS/lock"
	exit 0
fi

[ $# -gt 0 ] || { echo "check.sh: no scenes to drive" >&2; exit 2; }
jobs=${TUI_JOBS:-3}
case $jobs in
'' | *[!0-9]* | 0)
	echo "check.sh: TUI_JOBS is how many scenes run at once, 1 or more: $jobs" >&2
	exit 2
	;;
esac

TUI_CHECK_LOGS=$(mktemp -d "${TMPDIR:-/tmp}/shhh-tui-check.XXXXXX") || { echo "check.sh: could not make a log directory under ${TMPDIR:-/tmp}" >&2; exit 1; }
export TUI_CHECK_LOGS
trap 'rm -rf "$TUI_CHECK_LOGS"' EXIT

printf '%s\0' "$@" | xargs -0 -n 1 -P "$jobs" "$0" --one

# The summary is read in the order the scenes were handed over, not the order
# they finished in, so two runs of the same tree name their failures the same
# way. Where a scene stopped is the first snap drive.sh named in a failure; a
# scene that failed outside its snaps (a setup, a stray tab) is named alone.
total=0
passed=0
failures=
for scene in "$@"; do
	name=$(basename "$scene")
	total=$((total + 1))
	status=$(cat "$TUI_CHECK_LOGS/$name.status" 2>/dev/null)
	if [ "$status" = 0 ]; then
		passed=$((passed + 1))
		continue
	fi
	snap=$(sed -n "s|^drive\.sh: $name/\([^:]*\): .*|\1|p" "$TUI_CHECK_LOGS/$name.log" 2>/dev/null | head -n 1)
	failures="$failures · $name failed${snap:+ at $snap}"
done
echo "$passed/$total scenes passed$failures"
[ "$passed" = "$total" ]
