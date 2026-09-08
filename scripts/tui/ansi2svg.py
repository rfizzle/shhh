#!/usr/bin/env python3
"""Turn a `tmux capture-pane -e` capture into an SVG picture of the screen.

A capture is the terminal's cells with their colours as SGR sequences. The
plain `.txt` beside it is what a diff reads; this is what a person or an
agent looks at, and on macOS `qlmanage` turns the SVG into a PNG the file
tools can show inline. It understands what tmux emits — reset, bold, faint,
italic, underline, reverse, the 16 named colours, 256-colour and truecolour
— and nothing else, on purpose.

    ansi2svg.py <capture.ansi> <out.svg>
"""
import html
import re
import sys

SGR = re.compile(r"\x1b\[([0-9;]*)m")
CELL_W, CELL_H, FONT = 8.4, 18, 14
BG, FG = "#141414", "#d0d0d0"
NAMED = ["#000000", "#cc3333", "#33aa33", "#bbaa22", "#3366cc", "#aa44aa", "#22aaaa", "#bbbbbb",
         "#666666", "#ff5555", "#55dd55", "#ffee55", "#5588ff", "#dd66dd", "#55dddd", "#ffffff"]


def xterm(n):
    if n < 16:
        return NAMED[n]
    if n < 232:
        n -= 16
        r, g, b = n // 36, (n // 6) % 6, n % 6
        return "#%02x%02x%02x" % tuple(0 if v == 0 else 55 + v * 40 for v in (r, g, b))
    v = 8 + (n - 232) * 10
    return "#%02x%02x%02x" % (v, v, v)


def apply(state, params):
    p = [int(x) if x else 0 for x in params.split(";")] if params else [0]
    i = 0
    while i < len(p):
        c = p[i]
        if c == 0:
            state.clear()
        elif c == 1:
            state["bold"] = True
        elif c == 2:
            state["faint"] = True
        elif c == 3:
            state["italic"] = True
        elif c == 4:
            state["underline"] = True
        elif c == 7:
            state["reverse"] = True
        elif c in (22, 23, 24, 27):
            for k in {22: ("bold", "faint"), 23: ("italic",), 24: ("underline",), 27: ("reverse",)}[c]:
                state.pop(k, None)
        elif 30 <= c <= 37:
            state["fg"] = NAMED[c - 30]
        elif 90 <= c <= 97:
            state["fg"] = NAMED[c - 90 + 8]
        elif 40 <= c <= 47:
            state["bg"] = NAMED[c - 40]
        elif 100 <= c <= 107:
            state["bg"] = NAMED[c - 100 + 8]
        elif c == 39:
            state.pop("fg", None)
        elif c == 49:
            state.pop("bg", None)
        elif c in (38, 48) and i + 1 < len(p):
            key = "fg" if c == 38 else "bg"
            if p[i + 1] == 5 and i + 2 < len(p):
                state[key] = xterm(p[i + 2])
                i += 2
            elif p[i + 1] == 2 and i + 4 < len(p):
                state[key] = "#%02x%02x%02x" % (p[i + 2], p[i + 3], p[i + 4])
                i += 4
        i += 1


def main(src, dst):
    with open(src, encoding="utf-8", errors="replace") as fh:
        lines = fh.read().split("\n")
    while lines and not lines[-1].strip():
        lines.pop()
    cols = max((len(SGR.sub("", l)) for l in lines), default=80)
    rows = len(lines)
    w, h = cols * CELL_W + 20, rows * CELL_H + 20
    # The canvas is square, with the screen at the top left: qlmanage makes a
    # square thumbnail and crops whatever does not fit, so a wide screen on a
    # wide canvas came back without its right-hand edge.
    side = max(w, h)
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{side:.0f}" height="{side:.0f}" '
           f'viewBox="0 0 {side:.0f} {side:.0f}" font-family="Menlo, DejaVu Sans Mono, monospace" font-size="{FONT}">',
           f'<rect width="100%" height="100%" fill="{BG}"/>']
    for r, line in enumerate(lines):
        y = 10 + r * CELL_H
        state, col, pos = {}, 0, 0
        spans = []
        for m in SGR.finditer(line):
            text = line[pos:m.start()]
            if text:
                spans.append((dict(state), col, text))
                col += len(text)
            apply(state, m.group(1))
            pos = m.end()
        if line[pos:]:
            spans.append((dict(state), col, line[pos:]))
        for st, c, text in spans:
            fg, bg = st.get("fg", FG), st.get("bg")
            if st.get("reverse"):
                fg, bg = (bg or BG), (st.get("fg", FG))
            x = 10 + c * CELL_W
            if bg:
                out.append(f'<rect x="{x:.1f}" y="{y:.0f}" width="{len(text) * CELL_W:.1f}" height="{CELL_H}" fill="{bg}"/>')
            attrs = f' fill="{fg}"'
            if st.get("bold"):
                attrs += ' font-weight="bold"'
            if st.get("faint"):
                attrs += ' opacity="0.6"'
            if st.get("italic"):
                attrs += ' font-style="italic"'
            if st.get("underline"):
                attrs += ' text-decoration="underline"'
            out.append(f'<text x="{x:.1f}" y="{y + FONT:.0f}" xml:space="preserve"{attrs}>{html.escape(text)}</text>')
    out.append("</svg>")
    with open(dst, "w", encoding="utf-8") as fh:
        fh.write("\n".join(out))


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(sys.argv[1], sys.argv[2])
