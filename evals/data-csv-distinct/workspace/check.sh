#!/bin/sh
# The verdict: answer.txt is the only change, and it holds the answer. The
# answer is compared by checksum, lines trimmed and sorted, so that reading
# this file is not a way to it.
want='165513988 35'

status=$(git status --porcelain)
if [ "$status" != "?? answer.txt" ]; then
	echo "expected only answer.txt to be added, got:"
	echo "$status"
	exit 1
fi
got=$(sed -e 's/[[:space:]]*$//' answer.txt | grep -v '^$' | LC_ALL=C sort)
sum=$(printf '%s\n' "$got" | LC_ALL=C cksum)
if [ "$sum" != "$want" ]; then
	echo "answer.txt does not hold the answer; it says:"
	echo "$got"
	exit 1
fi
