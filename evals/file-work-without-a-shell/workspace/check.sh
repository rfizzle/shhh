#!/bin/sh
# The verdict: CHANGELOG.md gained exactly one line, at its end, saying what
# the task asked for, and nothing else in the checkout moved.
want='- 4.2.7 (stable): src/release/channel.go'

status=$(git status --porcelain)
if [ "$status" != " M CHANGELOG.md" ]; then
	echo "expected only CHANGELOG.md to change, got:"
	echo "$status"
	exit 1
fi
removed=$(git diff -U0 -- CHANGELOG.md | grep '^-' | grep -v '^---' | wc -l)
added=$(git diff -U0 -- CHANGELOG.md | grep '^+' | grep -v '^+++' | grep -v '^+[[:space:]]*$' | wc -l)
if [ "$removed" -ne 0 ] || [ "$added" -ne 1 ]; then
	echo "expected one line added and none removed, got $added added and $removed removed:"
	git diff -- CHANGELOG.md
	exit 1
fi
last=$(grep -v '^[[:space:]]*$' CHANGELOG.md | tail -n 1)
if [ "$last" != "$want" ]; then
	echo "the last line of CHANGELOG.md is not the entry asked for:"
	echo "got:  $last"
	echo "want: $want"
	exit 1
fi
