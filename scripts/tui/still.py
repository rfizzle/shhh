#!/usr/bin/env python3
"""Turn one captured screen into a one-frame asciicast, for agg to draw.

A capture is the terminal's cells (drive.sh), and the `.ansi` half of it
carries their colour. That is everything a picture of the screen needs, so the
picture is drawn from the capture rather than by playing the scene a second
time in a browser: the still and the text beside it are then the same bytes,
and cannot disagree about what was on screen.

agg reads asciicast v2 and nothing else, so the capture is wrapped as a cast
of one event at time zero. One event is one frame, and a one-frame GIF is a
still.

    still.py <capture.ansi> <out.cast> <cols> <rows>
"""
import json
import sys

ESC = "\x1b"


def main() -> None:
    if len(sys.argv) != 5:
        sys.exit("still.py: <capture.ansi> <out.cast> <cols> <rows>")
    src, out, cols, rows = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
    # A capture is bytes a terminal wrote, and a byte that is not valid UTF-8
    # is a glyph that went wrong rather than a reason to draw nothing.
    lines = open(src, encoding="utf-8", errors="replace").read().split("\n")
    if lines and lines[-1] == "":
        lines.pop()
    # Each row is placed where it was captured rather than written out with
    # newlines between them. A line that fills the last column wraps on its
    # own, and the newline after it would then advance a second time — which
    # draws the whole screen shifted up by however many full-width rows it has,
    # and a picture of the wrong layout is worse than no picture.
    body = [ESC + "[2J", ESC + "[H"]
    for i, line in enumerate(lines[:rows]):
        body.append(ESC + "[%d;1H" % (i + 1) + line)
    with open(out, "w", encoding="utf-8") as fh:
        fh.write(json.dumps({"version": 2, "width": cols, "height": rows}) + "\n")
        fh.write(json.dumps([0.0, "o", "".join(body)]) + "\n")


if __name__ == "__main__":
    main()
