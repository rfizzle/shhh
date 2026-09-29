#!/usr/bin/env python3
"""List the key captions the artboards draw against the keys the binary ships.

usage: scripts/design/keymap-check.py [--shhh BIN] [--keys FILE] [PATH ...]

PATH is any mix of HTML files and directories (a directory is read for every
*.html beneath it). With no PATH it reads .design/ui_kits/cockpit/*.html, the
local export of the "shhh Design System" project that the design-sync skill
writes; the design system lives outside this repository, so the export is the
only copy this script can read. The export has no 1-session.html: that
artboard is too large for the design API and is not checked here.
guidelines/*.html and components/*/*.card.html can be passed as well.

The register comes from the binary, never from a table copied into this
file: `shhh keys --json` run with the default (Linux) keyboard, reading each
act's shipped keys and its Words. --shhh names the binary (default ./shhh,
then shhh on PATH); --keys reads a saved `shhh keys --json` instead.

What it reads: every `[key] words` caption on an artboard's screen rows
(`.l`), stopping the words at the next ` · `, the next caption, a run of two
spaces or the row's end. Captions inside prose (`.cap`, `.rat`, `.note`)
are skipped.

What it can know, and what it cannot. The key is matched first, and the
words second: a caption's words are prose, the register's Words are the
canonical phrase, and a surface is allowed words of its own. So a caption is
reported as

  unbound      no act in the register ships this key at all
  moved        the key is shipped, but for acts whose words share nothing
               with the caption's, while an act on another key says the
               same thing (`[tab] mode` where the register binds shift+tab)
  other        the key is shipped for one or two unrelated acts and no act
               on another key says the caption's words either
  spelling     the key is written with a glyph the product never draws
               (`[↵]` for `[enter]`)
  words        the key is shipped for a related act, spelled differently
               (a difference, not a failure)
  surface      a key the register gives three or more acts (enter, esc, y)
               beside words none of them say; almost always a surface's
               own words, listed last so they can be read past

The script cannot tell which surface a row belongs to, so a key shipped on
one surface is accepted on every artboard; and a bracket that is not a key
(a checkbox, a placeholder) may be read as one. It exits 0 and ends with the
counts, so it can be run again and the drift measured.
"""

import argparse
import html.parser
import json
import os
import re
import shutil
import subprocess
import sys
from collections import defaultdict

DEFAULT_GLOB_DIR = os.path.join(".design", "ui_kits", "cockpit")
PROSE = {"cap", "rat", "note"}

# Glyphs an artboard may write for a key, and the keystroke each one means.
# A glyph the product itself draws (the arrows) is not a spelling finding.
ARROWS = {"↑": "up", "↓": "down", "←": "left", "→": "right"}
ALIASES = {
    "↵": "enter", "⏎": "enter", "return": "enter", "ret": "enter",
    "⎋": "esc", "escape": "esc",
    "⇥": "tab", "⇤": "shift+tab",
    "⌫": "backspace", "bksp": "backspace",
    "␣": "space",
    "pg up": "pgup", "pageup": "pgup", "page up": "pgup",
    "pg dn": "pgdown", "pgdn": "pgdown", "pagedown": "pgdown", "page down": "pgdown",
}
MODIFIERS = {"⇧": "shift+", "⌃": "ctrl+", "^": "ctrl+", "⌥": "alt+", "c-": "ctrl+", "m-": "alt+"}
NAMED = {"enter", "esc", "tab", "space", "backspace", "pgup", "pgdown", "home", "end",
         "up", "down", "left", "right", "delete", "insert"}
STOP = {"the", "a", "an", "it", "to", "of", "or", "and", "on", "in", "for", "this",
        "that", "from", "with", "its", "your", "you", "is", "be", "back", "x2", "×2"}


def fail(msg):
    print("keymap-check: " + msg, file=sys.stderr)
    sys.exit(1)


def load_register(args):
    if args.keys:
        with open(args.keys, encoding="utf-8") as f:
            doc = json.load(f)
    else:
        binary = args.shhh or ("./shhh" if os.path.exists("./shhh") else shutil.which("shhh"))
        if not binary:
            fail("no shhh binary: run `make build`, or pass --shhh or --keys")
        env = dict(os.environ, SHHH_KEYS_PLATFORM="linux")
        out = subprocess.run([binary, "keys", "--json"], env=env, capture_output=True, text=True)
        if out.returncode != 0:
            fail("%s keys --json: %s" % (binary, out.stderr.strip()))
        doc = json.loads(out.stdout)
    acts = []
    for g in doc["groups"]:
        for k in g["keys"]:
            acts.append({"name": k["name"], "words": k["does"], "keys": set(k["shipped"])})
    return doc.get("platform", "?"), acts


class Rows(html.parser.HTMLParser):
    """Collects the text of every `.l` row that is not inside prose."""

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.stack = []  # one (is_row, is_prose) per open element
        self.rows = []
        self.buf = None

    def handle_starttag(self, tag, attrs):
        if tag in ("br", "img", "input", "meta", "link", "hr", "wbr"):
            return
        classes = set((dict(attrs).get("class") or "").split())
        prose = bool(classes & PROSE) or any(p for _, p in self.stack)
        row = "l" in classes and not prose and self.buf is None
        if row:
            self.buf = []
        self.stack.append((row, prose))

    def handle_endtag(self, tag):
        if tag in ("br", "img", "input", "meta", "link", "hr", "wbr") or not self.stack:
            return
        row, _ = self.stack.pop()
        if row:
            self.rows.append("".join(self.buf))
            self.buf = None

    def handle_data(self, data):
        if self.buf is not None:
            self.buf.append(data)


BOX = re.compile(r"[─-╿▀-▟]+")


def captions(row):
    """Yields (key, words) for each `[key] words` in one row's text."""
    i, n = 0, len(row)
    while i < n:
        if row[i] != "[":
            i += 1
            continue
        j = i + 1
        while j < n and (row[j] != "]" or (j > i + 1 and row[j - 1] == "+" and j + 1 < n and row[j + 1] == "]")):
            if row[j] == "[" or j - i > 20:
                break
            j += 1
        if j >= n or row[j] != "]" or j == i + 1:
            i += 1
            continue
        key = row[i + 1:j]
        rest = row[j + 1:]
        end = len(rest)
        for stop in (" · ", "  ", "["):
            k = rest.find(stop)
            if k != -1:
                end = min(end, k)
        words = BOX.sub("", rest[:end]).strip(" ·│┃╯╰")
        before = row[:i].strip()
        yield key, words, before
        i = j + 1


def split_key(key):
    """Turns a caption's key into (keystrokes, glyphs the product never draws)."""
    raw = key.strip()
    odd = []
    low = raw.lower()
    if low in ALIASES:
        odd.append(raw)
        return {ALIASES[low]}, odd
    m = re.fullmatch(r"(\d)\s*[-–]\s*(\d)", raw)
    if m:
        return {str(d) for d in range(int(m.group(1)), int(m.group(2)) + 1)}, odd
    # `ctrl+/` holds a slash that is not a separator; split on the others.
    parts = re.split(r"(?<!\+)/", raw) if raw not in ("/",) else ["/"]
    out = set()
    for p in parts:
        p = p.strip()
        if not p:
            continue
        prefix = ""
        for glyph, mod in MODIFIERS.items():
            if p.lower().startswith(glyph) and len(p) > len(glyph):
                prefix, p = mod, p[len(glyph):]
                odd.append(glyph)
                break
        m = re.match(r"(?:(?:ctrl|shift|alt)\+)+", p.lower())
        if m and len(p) > m.end():
            prefix, p = prefix + m.group(0), p[m.end():]
        lowp = p.lower()
        if lowp in ALIASES:
            odd.append(p)
            out.add(prefix + ALIASES[lowp])
        elif p and all(c in ARROWS for c in p):
            out.update(prefix + ARROWS[c] for c in p)
        elif lowp in NAMED or re.fullmatch(r"f\d{1,2}", lowp):
            out.add(prefix + lowp)
        elif len(p) == 1 or prefix:
            out.add(prefix + (p.lower() if prefix and len(p) == 1 and p.isalpha() else p))
        elif len(p) <= 3 and " " not in p:
            out.update(p)  # `jk`, `np`: a pair written as two letters
        else:
            out.add(prefix + p)
    return out, odd


def content(words):
    toks = re.findall(r"[a-z0-9$]+", words.lower())
    return {t[:-1] if len(t) > 3 and t.endswith("s") else t for t in toks if t not in STOP}


def jaccard(a, b):
    return len(a & b) / len(a | b) if a | b else 0.0


def judge(keyset, words, acts):
    """Returns (kind, detail) for one caption, or None when it agrees."""
    have = [a for a in acts if keyset <= a["keys"]] or [a for a in acts if keyset & a["keys"]]
    mine = content(words)
    if have and (not mine or any(a["words"].lower() == words.lower() for a in have)):
        return None
    related = [a for a in have if content(a["words"]) & mine]
    # Another key's act whose words and the caption's contain one another:
    # `agents` in `the agent manager`, `commands` in `the command palette`.
    # A key of many acts (enter, esc) takes only a near match, since one
    # shared word there is what every surface's own words look like.
    many = len({a["words"] for a in have}) >= 3
    strong = sorted(
        (a for a in acts if not (keyset & a["keys"]) and content(a["words"])
         and (mine <= content(a["words"]) or content(a["words"]) <= mine)
         and (not many or jaccard(mine, content(a["words"])) >= 0.5)),
        key=lambda a: (-jaccard(mine, content(a["words"])), not a["name"].startswith("draft."), a["name"]),
    )
    alt = "; ".join("`%s` is [%s] (%s)" % (a["words"], "] or [".join(sorted(a["keys"])), a["name"])
                    for a in strong[:2])
    bound = "; ".join("`%s` (%s)" % (a["words"], a["name"]) for a in (related or have)[:3])
    if not have:
        return "unbound", "no shipped act answers it" + ("; " + alt if alt else "")
    if related:
        return "words", "the register says %s" % bound
    if strong:
        return "moved", "shipped as %s; %s" % (bound, alt)
    if many:
        return "surface", "the key carries %d acts; none says this" % len({a["words"] for a in have})
    return "other", "shipped as %s" % bound


def files(paths):
    if not paths:
        paths = [DEFAULT_GLOB_DIR]
    out = []
    for p in paths:
        if os.path.isdir(p):
            for root, _, names in os.walk(p):
                out.extend(os.path.join(root, n) for n in names if n.endswith(".html"))
        elif os.path.isfile(p):
            out.append(p)
        else:
            fail("%s: no such file or directory (the export is written by the design-sync skill)" % p)
    return sorted(set(out))


KINDS = ["unbound", "moved", "other", "spelling", "words", "surface"]
TITLES = {
    "unbound": "key not shipped",
    "moved": "key shipped for another act; these words are another key's",
    "other": "key shipped for something unrelated",
    "words": "key agrees; the register's words differ",
    "spelling": "key written with a glyph the product does not draw",
    "surface": "a key of many acts, with words of the surface's own",
}


def main():
    ap = argparse.ArgumentParser(usage=argparse.SUPPRESS, description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("paths", nargs="*")
    ap.add_argument("--shhh")
    ap.add_argument("--keys")
    args = ap.parse_args()
    platform, acts = load_register(args)
    names = files(args.paths)
    base = os.path.dirname(os.path.commonpath(names)) if names else ""
    total = 0
    counts = defaultdict(int)
    silent = []
    print("keymap-check: %d artboards against the %s keyboard (%d acts)\n" % (len(names), platform, len(acts)))
    for path in names:
        with open(path, encoding="utf-8") as f:
            parser = Rows()
            parser.feed(f.read())
        if not parser.rows:
            silent.append(path)
            continue
        found = defaultdict(lambda: defaultdict(set))
        for row in parser.rows:
            for key, words, before in captions(row):
                if not key.strip() or (key in ("x", "✓", "✗", "·") and not before.strip("│┃ ▸›")):
                    continue  # a checkbox at the head of a list row
                if not re.search(r"[A-Za-z]", words) or words[:1] in ",;.":
                    continue  # a bracket with no words beside it is not a caption
                keyset, odd = split_key(key)
                if any(len(k) > 1 and "+" not in k and k not in NAMED and k not in ARROWS.values()
                       and not re.fullmatch(r"f\d{1,2}", k) for k in keyset):
                    continue  # `[provider]`, `[89]`: a placeholder, not a key
                total += 1
                if odd:
                    found["spelling"][("[%s]" % key, "the product writes [%s]" % "/".join(sorted(keyset)))].add(words)
                    counts["spelling"] += 1
                verdict = judge(keyset, words, acts)
                if verdict:
                    kind, detail = verdict
                    found[kind][("[%s] %s" % (key, words), detail)].add(words)
                    counts[kind] += 1
        if not found:
            continue
        print("== %s" % os.path.relpath(path, base))
        for kind in KINDS:
            if kind not in found:
                continue
            print("  -- %s" % TITLES[kind])
            for (cap, detail) in sorted(found[kind]):
                print("     %s\n         %s" % (cap, detail))
        print()
    for path in silent:
        print("(%s has no screen rows; nothing read)" % os.path.relpath(path, base))
    print("\ncaptions read: %d" % total)
    print("reported: " + ", ".join("%s %d" % (k, counts[k]) for k in KINDS))
    print("drift (unbound, moved, other, spelling): %d" % sum(counts[k] for k in KINDS if k not in ("words", "surface")))
    return 0


if __name__ == "__main__":
    sys.exit(main())
