#!/usr/bin/env python3
"""One line per changed golden case, for reading a golden diff at a glance.

A case is rendered at several widths in two palettes, so one change to a
surface moves up to eight files named <case>.w<width>[.mono].txt under a
testdata/golden/ directory. This folds them into one line per case:

    <case>  <n> files  w60,w80,w110,w130  color+mono  +<added> -<removed>

Usage: python3 -I scripts/golden-summary.py [git-diff-args...]
With no arguments it summarises the working tree against HEAD (staged and
unstaged); `--cached`, `HEAD~1` or `master...` go straight to git diff.
The counts are git's --numstat, the machine form of --stat.
"""

import re
import subprocess
import sys

NAME = re.compile(r"(?:^|/)testdata/golden/(?P<case>.+?)\.w(?P<w>\d+)(?P<mono>\.mono)?\.txt$")
OTHER = re.compile(r"(?:^|/)testdata/golden/(?P<case>.+)$")


def main(args):
    args = args or ["HEAD"]
    out = subprocess.run(
        ["git", "diff", "--numstat", *args, "--", "*testdata/golden/*"],
        check=True, capture_output=True, text=True,
    ).stdout
    cases = {}
    for line in out.splitlines():
        add, rem, path = line.split("\t", 2)
        m = NAME.search(path)
        if m:
            key = (path.rsplit("/", 1)[0], m["case"])
            width = int(m["w"])
            palette = "mono" if m["mono"] else "color"
        elif OTHER.search(path):
            key = (path.rsplit("/", 1)[0], OTHER.search(path)["case"])
            width, palette = None, None
        else:
            continue
        c = cases.setdefault(key, {"files": 0, "w": set(), "p": set(), "add": 0, "rem": 0})
        c["files"] += 1
        if width is not None:
            c["w"].add(width)
            c["p"].add(palette)
        # A binary file reports "-" for both counts.
        c["add"] += int(add) if add.isdigit() else 0
        c["rem"] += int(rem) if rem.isdigit() else 0
    for (_, case), c in sorted(cases.items(), key=lambda kv: kv[0][1]):
        widths = ",".join("w%d" % w for w in sorted(c["w"])) or "-"
        pal = "+".join(p for p in ("color", "mono") if p in c["p"]) or "-"
        print("%s  %d file%s  %s  %s  +%d -%d" % (
            case, c["files"], "" if c["files"] == 1 else "s", widths, pal, c["add"], c["rem"]))
    if not cases:
        print("no golden changed")


if __name__ == "__main__":
    main(sys.argv[1:])
