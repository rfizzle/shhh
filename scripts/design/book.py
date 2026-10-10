#!/usr/bin/env python3
"""Build the design book: a static HTML reading of the goldens and the scenes.

The goldens are the design record. Every surface is captured by its golden
test at several widths in two palettes, and every driven scene leaves a text
and a colour capture for each step it names. The book is those same bytes laid
out for a reader, with the words from docs/interface/surfaces.md beside them,
so it cannot drift from the binary and nothing is drawn by hand.

    python3 -I scripts/design/book.py [--root DIR] [--out DIR]
    python3 -I scripts/design/book.py --ansi < capture.ansi   (a fragment to stdout)

The output is on demand and not committed (docs/design/ is gitignored). It
needs no network: the styles are inline, and the one script on the index is the
name filter. The colour/mono switch on a surface page is a checkbox in CSS.
Standard library only.
"""
import argparse
import html
import os
import re
import shlex
import shutil
import sys

# GOLDEN_DIRS are the three golden directories, each under the name that
# keeps two goldens of the same name apart in the book.
GOLDEN_DIRS = (
    ("chat", "internal/ui/chat/testdata/golden"),
    ("components", "internal/ui/components/testdata/golden"),
    ("ui", "internal/ui/testdata/golden"),
)
SURFACES = "docs/interface/surfaces.md"
SCENES = "scripts/tui/scenes"
CAPTURES = "bin/tui"
PICTURES = "docs/readme"
# The size the book must stay under, checked after the build.
# A ceiling on the whole book, so a runaway capture set is noticed rather
# than written: the goldens alone are about 14 MB, and a full set of scene
# captures under bin/tui adds about 10 MB more, so 64 MB is well clear of
# any honest build and well short of a mistake.
BUDGET = 64 * 1024 * 1024

# SECTION_OF names the surfaces.md section that speaks for a golden: golden
# name -> the heading's anchor. It is the one piece of hand upkeep. An entry
# that is missing or wrong shows on the index (a golden under "no words yet",
# or words that do not fit) and never fails the build; a golden with no entry
# is then tried against the headings' words (section_by_words).
SECTION_OF = {
    # rows
    "activity-rows": "the-activity-row",
    "agent-rows": "the-activity-row",
    "block-heading": "the-activity-row",
    "step-outline": "the-step",
    "step-cards": "the-step",
    "steps-screen": "the-supporting-screens",
    "steps-screen-plan": "the-supporting-screens",
    "large-step-open": "the-step",
    "folded-rows": "the-activity-row",
    "git-write-rows": "the-activity-row",
    "git-write-cards": "the-approval-card",
    "live-command": "the-activity-row",
    "live-card-words": "the-activity-row",
    "fetch-wait": "the-activity-row",
    "wait-lines": "the-activity-row",
    "stale-edit-row": "the-activity-row",
    "notebook-rows": "the-activity-row",
    "resumed-row": "the-activity-row",
    "resumed-changes": "the-activity-row",
    "multi-edit-card": "the-approval-card",
    "thinking-prose": "the-think-row",
    "prose-column": "the-leading-columns",
    "prose-names": "the-leading-columns",
    "syntax-register": "the-leading-columns",
    "transcript-grid": "the-leading-columns",
    "diff-view": "the-diff-view",
    "turn-close": "the-turns-close",
    "turn-close-selection": "the-turns-close",
    "turn-total": "the-turns-close",
    "turn-status": "the-turns-close",
    "status-row": "the-turns-close",
    "summary-row": "the-turns-close",
    "todo-run-row": "the-backlog-runs-row",
    "todo-sprint": "the-sprint-board",
    "sprint-board": "the-sprint-board",
    "recovery-rows": "the-recovery-row",
    "recovery-selection": "the-recovery-row",
    "compact-receipt": "the-compaction-receipt",
    "progress-update": "the-progress-checkpoint",
    "round-limit-pause": "the-progress-checkpoint",
    # panels
    "reading-mode": "reading-mode",
    "review-mode": "reading-mode",
    "read-run": "reading-mode",
    "readings-screen": "reading-mode",
    "scroll-gutter": "reading-mode",
    "scroll-gutter-rail": "reading-mode",
    "search-counts": "reading-mode",
    "search-sweep": "reading-mode",
    "transcript-search": "reading-mode",
    "copy-block": "reading-mode",
    "prompt-frame": "the-input-frame",
    "prompt-frame-height": "the-input-frame",
    "compose-row": "the-input-frame",
    "draft-grammar": "the-input-frame",
    "grown-draft": "the-input-frame",
    "mode-word": "the-input-frame",
    "mode-picker": "the-input-frame",
    "paste-token": "the-input-frame",
    "paste-reader-hint": "the-input-frame",
    "picture-footer": "the-input-frame",
    "queue": "the-input-frame",
    "queue-list": "the-input-frame",
    "queue-mixed": "the-input-frame",
    "queue-strip": "the-input-frame",
    "sent-tray": "the-input-frame",
    "suggestion": "the-input-frame",
    "history-search": "the-input-frame",
    "interrupt": "the-input-frame",
    "press-again": "the-input-frame",
    "steer-from-session": "the-input-frame",
    "header-row": "the-input-frame",
    "density-high": "the-input-frame",
    "density-low": "the-input-frame",
    "density-normal": "the-input-frame",
    "completion-menu": "the-completion-menu",
    "inspector-rail": "the-inspector-rail",
    "inspector-steps": "the-inspector-rail",
    "inspector-alerts": "the-inspector-rail",
    "staged-rail": "the-inspector-rail",
    "summary-card": "the-session-summary",
    "agent-list": "the-agent-manager",
    "fanout-block": "the-agent-manager",
    "spawn-card": "the-agent-manager",
    "child-ask-verdicts": "the-agent-manager",
    "child-auto-approved": "the-agent-manager",
    "child-request-routed": "the-agent-manager",
    "kill-confirm": "the-agent-manager",
    "handoff": "the-agent-manager",
    "attached-rail": "the-agent-manager",
    "screen-attached": "the-agent-manager",
    # cards
    "approval-card": "the-approval-card",
    "command-card-git-store": "the-approval-card",
    "command-amend": "the-approval-card",
    "command-amended": "the-approval-card",
    "command-errors": "the-approval-card",
    "failed-command-card": "the-approval-card",
    "failure-card": "the-approval-card",
    "scaffold-card": "the-approval-card",
    "planned-card": "the-approval-card",
    "plan-card": "the-approval-card",
    "commit-card": "the-approval-card",
    "classifier-ask": "the-approval-card",
    "classifier-denial": "the-approval-card",
    "classifier-standing": "the-approval-card",
    "auto-approved": "the-approval-card",
    "decision-note": "the-approval-card",
    "grant-list": "the-approval-card",
    "on-close-gate": "the-approval-card",
    "sandbox-session": "the-approval-card",
    "secret-prompt": "the-approval-card",
    "question-card": "the-question-card",
    "question-tabs": "the-question-card",
    "question-waiting": "the-question-card",
    "note-select": "the-question-card",
    "branch-picker": "selectors",
    "chat-picker": "selectors",
    "model-picker": "selectors",
    "provider-pick": "selectors",
    "provider-card": "selectors",
    "snippet-confirm": "the-inline-confirm",
    "rewind-fold": "the-rewind",
    "rewind-picker": "the-rewind",
    "rewind-row": "the-rewind",
    "rewind-scope": "the-rewind",
    # takeover surfaces
    "palette": "the-palette",
    "key-list": "the-key-list",
    "key-list-popup": "the-key-list",
    "key-entry": "the-key-list",
    "start-screen": "the-start-screen",
    "start-face": "the-start-screen",
    "start-instruction": "the-start-screen",
    "start-offers": "the-start-screen",
    "start-profile": "the-start-screen",
    "start-reading": "the-start-screen",
    "new-session-row": "the-start-screen",
    "help-chat": "the-supporting-screens",
    "alerts-screen": "the-supporting-screens",
    "alerts-output": "the-supporting-screens",
    "doctor-screen": "the-supporting-screens",
    "flakes-screen": "the-supporting-screens",
    "history-screen": "the-supporting-screens",
    "metrics-screen": "the-supporting-screens",
    "notes-screen": "the-supporting-screens",
    "rate-screen": "the-supporting-screens",
    "sources-screen": "the-supporting-screens",
    "spend-screen": "the-supporting-screens",
    "tools-screen": "the-supporting-screens",
    "turns-screen": "the-supporting-screens",
    "screen-family": "the-supporting-screens",
    "config-screen": "the-settings-screen",
    "config-screen-scope": "the-settings-screen",
    "edit-pane": "the-editor-pane",
    "editor-pane": "the-editor-pane",
    "backlog-screen": "the-backlog-screen",
    "item-draft": "the-backlog-screen",
    "todo-groom": "the-backlog-screen",
    "todo-no-repo": "the-backlog-screen",
    "batch-order": "the-backlog-screen",
    "profile-screen": "the-profile-drafter",
    "profile-drafter": "the-profile-drafter",
    "profile-commands": "the-profile-drafter",
    "profile-opened": "the-profile-drafter",
    "profile-tools": "the-profile-drafter",
    "profile-whole": "the-profile-drafter",
    "toolchain-draft": "the-profile-drafter",
    "toolchain-setup": "the-profile-drafter",
    "context-screen": "the-context-surface",
    "pressure-card": "the-context-surface",
    "safety-screen": "the-safety-reading",
    "attachment-chips": "a-staged-attachment",
    "attachment-view": "a-staged-attachment",
    "one-shot-alternatives": "the-one-shot-result",
    "one-shot-destructive": "the-one-shot-result",
    "one-shot-generating": "the-one-shot-result",
    "one-shot-result": "the-one-shot-result",
    "one-shot-revise": "the-one-shot-result",
    "explain-view": "the-one-shot-result",
    "snippet-empty": "the-supporting-screens",
    "snippet-filter-nothing": "the-supporting-screens",
    "snippet-filter-open": "the-supporting-screens",
    "snippet-filtered": "the-supporting-screens",
    "snippet-keys": "the-supporting-screens",
    "snippet-listing": "the-supporting-screens",
    "snippet-notice": "the-supporting-screens",
    "snippet-rename": "the-supporting-screens",
    "snippet-windowed": "the-supporting-screens",
    "windowed-lists": "the-supporting-screens",
    "lists": "the-supporting-screens",
    "plan-checklist": "the-inspector-rail",
    "resolved-verification": "the-inspector-rail",
    "tree-moved": "the-step",
    "tree-moved-footer": "the-step",
    "chat-todo": "the-inspector-rail",
    # outside the TUI
    "exit-banner": "when-you-are-not-there",
    "provider-failures": "when-you-are-not-there",
}

# Words that carry no meaning in a heading or a golden name.
STOP = {"the", "a", "an", "of", "and", "to", "in", "on", "is", "are", "when", "you", "what"}

CSS = """
:root{color-scheme:dark}
body{margin:0;background:#161616;color:#d0d0d0;font:15px/1.5 system-ui,sans-serif}
header.top{padding:12px 24px;border-bottom:1px solid #333;background:#1c1c1c}
header.top a{color:#8ab4f8;text-decoration:none}
main{padding:16px 24px 48px;max-width:1500px}
h1{font-size:22px;margin:8px 0}h2{font-size:18px;margin:24px 0 8px}h3{font-size:15px;margin:16px 0 6px;color:#aaa}
h4{font:13px monospace;margin:8px 0 2px;color:#9a9a9a}
a{color:#8ab4f8}
pre.cells{background:#1c1c1c;color:#d0d0d0;margin:0 0 8px;padding:10px 12px;overflow-x:auto;
  font:13px/1.25 ui-monospace,Menlo,Consolas,monospace;border:1px solid #2a2a2a;white-space:pre}
.width{display:inline-block;vertical-align:top;margin:0 16px 16px 0;max-width:100%}
.width.first{display:block}
.meta{color:#888;font-size:13px}
input#mono{display:none}
label.toggle{display:inline-block;padding:2px 10px;border:1px solid #555;cursor:pointer;margin:6px 0;border-radius:3px}
#mono:checked ~ main .color{display:none}
#mono:not(:checked) ~ main .mono{display:none}
#mono:checked ~ main label.toggle{background:#333}
iframe.words{width:100%;max-width:900px;height:420px;border:1px solid #2a2a2a;background:#191919;resize:vertical}
.words code{background:#262626;padding:0 3px}
.words table{border-collapse:collapse}.words td,.words th{border:1px solid #333;padding:2px 8px}
.words pre{background:#1c1c1c;padding:8px;overflow-x:auto}
.words li.l2{margin-left:24px}
.step{color:#777;font:12px monospace;margin:2px 0}
.snap{margin:12px 0}
.caption{font-size:13px;color:#b8b8b8;margin:2px 0}
ul.index{list-style:none;padding-left:0}ul.index li{padding:1px 0}
.nowords{color:#b8a35a}
input#filter{width:100%;max-width:420px;padding:6px 8px;background:#1c1c1c;color:#d0d0d0;border:1px solid #555}
.note{color:#888;white-space:pre-wrap;font-size:13px}
"""

# --- the ANSI renderer -----------------------------------------------------

ESC = "\x1b"
ESC_SYMBOL = "␛"  # how a golden's ansi block writes ESC
SGR = re.compile(r"\x1b\[([0-9;:]*)m")
ANY_ESC = re.compile(r"\x1b\[[0-9;:?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]")
ANSI16 = (
    "#000000", "#cd0000", "#00cd00", "#cdcd00", "#0000ee", "#cd00cd", "#00cdcd", "#e5e5e5",
    "#7f7f7f", "#ff0000", "#00ff00", "#ffff00", "#5c5cff", "#ff00ff", "#00ffff", "#ffffff",
)
DEFAULT_FG, DEFAULT_BG = "#d0d0d0", "#1c1c1c"


def palette(n):
    if n < 16:
        return ANSI16[n]
    if n < 232:
        n -= 16
        step = (0, 95, 135, 175, 215, 255)
        return "#%02x%02x%02x" % (step[n // 36], step[n // 6 % 6], step[n % 6])
    g = 8 + (n - 232) * 10
    return "#%02x%02x%02x" % (g, g, g)


class Style:
    """The state an SGR sequence leaves: colours as ('i', n) or ('x', '#rrggbb')."""

    __slots__ = ("fg", "bg", "bold", "dim", "italic", "underline", "reverse", "strike")

    def __init__(self):
        self.reset()

    def reset(self):
        self.fg = self.bg = None
        self.bold = self.dim = self.italic = self.underline = self.reverse = self.strike = False

    def key(self):
        return (self.fg, self.bg, self.bold, self.dim, self.italic, self.underline, self.reverse, self.strike)

    def apply(self, params):
        p = [int(x) if x.isdigit() else 0 for x in re.split(r"[;:]", params)] if params else [0]
        i = 0
        while i < len(p):
            c = p[i]
            i += 1
            if c == 0:
                self.reset()
            elif c == 1:
                self.bold = True
            elif c == 2:
                self.dim = True
            elif c == 3:
                self.italic = True
            elif c == 4:
                self.underline = True
            elif c == 7:
                self.reverse = True
            elif c == 9:
                self.strike = True
            elif c == 22:
                self.bold = self.dim = False
            elif c == 23:
                self.italic = False
            elif c == 24:
                self.underline = False
            elif c == 27:
                self.reverse = False
            elif c == 29:
                self.strike = False
            elif 30 <= c <= 37:
                self.fg = ("i", c - 30)
            elif 90 <= c <= 97:
                self.fg = ("i", c - 90 + 8)
            elif 40 <= c <= 47:
                self.bg = ("i", c - 40)
            elif 100 <= c <= 107:
                self.bg = ("i", c - 100 + 8)
            elif c == 39:
                self.fg = None
            elif c == 49:
                self.bg = None
            elif c in (38, 48):
                col = None
                if i < len(p) and p[i] == 5 and i + 1 < len(p):
                    col = ("i", min(p[i + 1], 255))
                    i += 2
                elif i < len(p) and p[i] == 2 and i + 3 < len(p):
                    col = ("x", "#%02x%02x%02x" % tuple(min(v, 255) for v in p[i + 1:i + 4]))
                    i += 4
                else:
                    i = len(p)
                if col:
                    if c == 38:
                        self.fg = col
                    else:
                        self.bg = col


def span_attrs(st, used):
    """The class list and inline style for a style, noting each class used."""
    fg, bg = st.fg, st.bg
    if st.reverse:
        fg, bg = bg or ("x", DEFAULT_BG), fg or ("x", DEFAULT_FG)
    classes, style = [], []
    for col, prefix, prop in ((fg, "f", "color"), (bg, "g", "background")):
        if col is None:
            continue
        if col[0] == "i":
            classes.append("%s%d" % (prefix, col[1]))
            used.add((prefix, col[1]))
        else:
            style.append("%s:%s" % (prop, col[1]))
    for on, cls in ((st.bold, "b"), (st.dim, "d"), (st.italic, "i"), (st.underline, "u"), (st.strike, "s")):
        if on:
            classes.append(cls)
            used.add(cls)
    return classes, style


def opaque(st):
    """True when trailing spaces in this style can be seen and must stay."""
    return st.bg is not None or st.reverse or st.underline or st.strike


def render_ansi(text, used=None):
    """Turn text carrying SGR sequences into HTML: one <span> per run of one
    style, closed at every line end so a line stands alone. Returns the HTML;
    the classes it used are added to `used` when that set is given."""
    if used is None:
        used = set()
    text = text.replace(ESC_SYMBOL, ESC)
    st = Style()
    out = []
    for line in text.split("\n"):
        runs = []  # [(style key, Style copy as attrs, text)]
        pos = 0
        for m in ANY_ESC.finditer(line):
            if m.start() > pos:
                runs.append((st.key(), _copy(st), line[pos:m.start()]))
            pos = m.end()
            sgr = SGR.fullmatch(m.group(0))
            if sgr:
                st.apply(sgr.group(1))
        if pos < len(line):
            runs.append((st.key(), _copy(st), line[pos:]))
        out.append(_line_html(runs, used))
    return "\n".join(out)


def _copy(st):
    c = Style()
    for f in Style.__slots__:
        setattr(c, f, getattr(st, f))
    return c


def _line_html(runs, used):
    # Spaces after the last mark that can be seen carry nothing and are the
    # bulk of a padded line, so they are dropped; a coloured background keeps
    # them because there they are the picture.
    while runs and not opaque(runs[-1][1]) and not runs[-1][2].strip(" "):
        runs.pop()
    if runs and not opaque(runs[-1][1]):
        k, s, t = runs[-1]
        runs[-1] = (k, s, t.rstrip(" "))
    merged = []
    for k, s, t in runs:
        if not opaque(s) and not t.strip(" "):
            # A foreground colour on blank cells draws nothing.
            k, s = None, Style()
        if merged and merged[-1][0] == k:
            merged[-1][2].append(t)
        else:
            merged.append((k, s, [t]))
    parts = []
    for _, s, ts in merged:
        t = html.escape("".join(ts), quote=False)
        classes, style = span_attrs(s, used)
        if not classes and not style:
            parts.append(t)
            continue
        # One class needs no quotes, and a span is the most repeated thing in the book.
        attrs = (" class=%s" % classes[0] if len(classes) == 1 else ' class="%s"' % " ".join(classes)) if classes else ""
        if style:
            attrs += ' style="%s"' % ";".join(style)
        parts.append("<span%s>%s</span>" % (attrs, t))
    return "".join(parts)


def style_css(used):
    rules = []
    for item in sorted(used, key=str):
        if isinstance(item, tuple):
            prefix, n = item
            prop = "color" if prefix == "f" else "background"
            rules.append(".%s%d{%s:%s}" % (prefix, n, prop, palette(n)))
        else:
            rules.append({
                "b": ".b{font-weight:bold}", "d": ".d{opacity:.6}", "i": ".i{font-style:italic}",
                "u": ".u{text-decoration:underline}", "s": ".s{text-decoration:line-through}",
            }[item])
    return "".join(rules)


# --- goldens ---------------------------------------------------------------

GOLDEN_FILE = re.compile(r"^(?P<name>.+)\.w(?P<width>\d+)(?P<mono>\.mono)?\.txt$")


class Render:
    """One golden file read: its header words and its panels."""

    def __init__(self, path):
        self.header = {}
        self.panels = []  # [(label, ansi text)]
        self._read(path)

    def _read(self, path):
        with open(path, encoding="utf-8", errors="replace") as fh:
            lines = fh.read().split("\n")
        layout = ansi = None
        for i, line in enumerate(lines):
            if line.startswith("# ") and ":" in line and layout is None:
                k, _, v = line[2:].partition(":")
                self.header.setdefault(k.strip(), v.strip())
            elif line.startswith("── layout "):
                layout = i + 1
            elif line.startswith("── ansi "):
                ansi = i + 1
                break
        if layout is None or ansi is None:
            return
        lay = lines[layout:ansi - 2]  # the rule is preceded by a blank line
        body = lines[ansi:]
        while body and body[-1] == "":
            body.pop()
        # The label of a panel is written plain, so its line is the same in
        # both blocks; a content line that happens to open with a dot is
        # coloured and differs between them.
        labels = [
            i for i, l in enumerate(lay)
            if l.startswith("· ") and i < len(body) and body[i] == l and (i == 0 or lay[i - 1] == "")
        ]
        cuts = labels or [0]
        if labels and labels[0] != 0:
            cuts = [0] + labels
        for n, start in enumerate(cuts):
            end = cuts[n + 1] - 1 if n + 1 < len(cuts) else len(body)
            label = ""
            if start in labels:
                label = body[start][2:]
                start += 1
            self.panels.append((label, "\n".join(body[start:end])))


class Golden:
    def __init__(self, pkg, name):
        self.pkg, self.name = pkg, name
        self.renders = {}  # (width, mono) -> path
        self.slug = name

    def widths(self):
        ws = sorted({w for w, _ in self.renders})
        # The widths a reader compares come first, in the order of the breakpoints.
        head = [w for w in (110, 60, 80, 130) if w in ws]
        return head + [w for w in ws if w not in head]


def read_goldens(root):
    found = {}
    for pkg, rel in GOLDEN_DIRS:
        d = os.path.join(root, rel)
        if not os.path.isdir(d):
            continue
        for f in sorted(os.listdir(d)):
            m = GOLDEN_FILE.match(f)
            if not m:
                continue
            g = found.setdefault((pkg, m["name"]), Golden(pkg, m["name"]))
            g.renders[(int(m["width"]), bool(m["mono"]))] = os.path.join(d, f)
    goldens = sorted(found.values(), key=lambda g: (g.name, g.pkg))
    count = {}
    for g in goldens:
        count[g.name] = count.get(g.name, 0) + 1
    for g in goldens:
        if count[g.name] > 1:
            g.slug = "%s-%s" % (g.pkg, g.name)
    return goldens


# --- surfaces.md -----------------------------------------------------------


def slugify(title):
    t = re.sub(r"`([^`]*)`", r"\1", title)
    t = re.sub(r"\*\*?([^*]*)\*\*?", r"\1", t).lower()
    t = re.sub(r"[^\w\s-]", "", t)
    return re.sub(r"\s+", "-", t.strip())


class Section:
    def __init__(self, level, title, anchor):
        self.level, self.title, self.anchor = level, title, anchor
        self.lines = []


def read_sections(root):
    path = os.path.join(root, SURFACES)
    if not os.path.isfile(path):
        return []
    sections, fence = [], False
    with open(path, encoding="utf-8") as fh:
        for line in fh.read().split("\n"):
            if line.startswith("```"):
                fence = not fence
            m = None if fence else re.match(r"^(#{1,3}) (.+)$", line)
            if m:
                sections.append(Section(len(m[1]), m[2].strip(), slugify(m[2])))
            elif sections:
                sections[-1].lines.append(line)
    return sections


def words(s):
    out = set()
    for w in re.findall(r"[a-z0-9]+", s.lower().replace("'", "")):
        if w in STOP:
            continue
        out.add(w[:-1] if len(w) > 3 and w.endswith("s") else w)
    return out


def section_by_words(name, sections):
    """The heading whose words are all in the golden's name; the one with the
    most words wins, and the earlier of two equals."""
    have = words(name.replace("-", " "))
    best, score = None, 0
    for s in sections:
        if s.level != 3:
            continue
        w = words(s.title)
        if w and w <= have and len(w) > score:
            best, score = s, len(w)
    return best


def section_for(name, sections):
    by_anchor = {s.anchor: s for s in sections}
    if name in SECTION_OF and SECTION_OF[name] in by_anchor:
        return by_anchor[SECTION_OF[name]]
    return section_by_words(name, sections)


def inline_md(s):
    s = html.escape(s, quote=False)
    s = re.sub(r"`([^`]+)`", r"<code>\1</code>", s)
    s = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", s)
    s = re.sub(r"(?<![\w*])\*([^*\s][^*]*)\*(?![\w*])", r"<em>\1</em>", s)
    s = re.sub(r"\[([^\]]+)\]\([^)]*\)", r"\1", s)
    return s


def render_md(lines):
    """The small part of Markdown surfaces.md uses: paragraphs, lists, tables,
    block quotes and fences. Links keep their text and lose their target."""
    out, para, i = [], [], 0

    def flush():
        if para:
            out.append("<p>%s</p>" % inline_md(" ".join(para)))
            para.clear()

    while i < len(lines):
        line = lines[i]
        if line.startswith("```"):
            flush()
            i += 1
            block = []
            while i < len(lines) and not lines[i].startswith("```"):
                block.append(lines[i])
                i += 1
            out.append("<pre>%s</pre>" % html.escape("\n".join(block), quote=False))
        elif line.lstrip().startswith("|"):
            flush()
            rows = []
            while i < len(lines) and lines[i].lstrip().startswith("|"):
                cells = [c.strip() for c in lines[i].strip().strip("|").split("|")]
                if not all(re.fullmatch(r":?-+:?", c) for c in cells):
                    rows.append(cells)
                i += 1
            out.append("<table>%s</table>" % "".join(
                "<tr>%s</tr>" % "".join("<%s>%s</%s>" % ("th" if n == 0 else "td", inline_md(c), "th" if n == 0 else "td")
                                         for c in r) for n, r in enumerate(rows)))
            continue
        elif re.match(r"^\s*([-*]|\d+\.) ", line):
            flush()
            items = []
            while i < len(lines) and (re.match(r"^\s*([-*]|\d+\.) ", lines[i]) or (lines[i].startswith("  ") and items)):
                m = re.match(r"^(\s*)([-*]|\d+\.) (.*)", lines[i])
                if m:
                    items.append([len(m[1]) >= 2, m[3]])
                elif lines[i].strip():
                    items[-1][1] += " " + lines[i].strip()
                i += 1
            out.append("<ul>%s</ul>" % "".join(
                '<li class="%s">%s</li>' % ("l2" if deep else "l1", inline_md(t)) for deep, t in items))
            continue
        elif line.startswith(">"):
            flush()
            quote = []
            while i < len(lines) and lines[i].startswith(">"):
                quote.append(lines[i].lstrip("> "))
                i += 1
            out.append("<blockquote>%s</blockquote>" % inline_md(" ".join(quote)))
            continue
        elif line.strip() == "":
            flush()
        else:
            para.append(line.strip())
        i += 1
    flush()
    return "\n".join(out)


# --- pages -----------------------------------------------------------------


def page(title, body, used=None):
    return (
        '<!doctype html><html lang="en"><head><meta charset="utf-8">'
        "<title>%s</title><style>%s%s</style></head><body>%s</body></html>\n"
        % (html.escape(title), CSS, style_css(used or set()), body)
    )


def top(root_path):
    return '<header class="top"><a href="%sindex.html">design book</a></header>' % root_path


def golden_page(g, section):
    used = set()
    first = g.renders.get((110, False)) or g.renders[min(g.renders)]
    head = Render(first).header
    body = [top("../"), '<input type="checkbox" id="mono">', "<main>", "<h1>%s</h1>" % html.escape(g.name)]
    meta = [head.get("surface", "")]
    if head.get("cursor") not in (None, "none"):
        meta.append("cursor " + head["cursor"])
    meta.append("golden: %s" % GOLDEN_DIRS[[p for p, _ in GOLDEN_DIRS].index(g.pkg)][1])
    body.append('<p class="meta">%s</p>' % html.escape(" · ".join(m for m in meta if m)))
    body.append('<label class="toggle" for="mono">mono</label>')
    for n, w in enumerate(g.widths()):
        body.append('<div class="width%s"><h3>%d columns</h3>' % (" first" if n == 0 else "", w))
        for mono in (False, True):
            path = g.renders.get((w, mono))
            if not path:
                continue
            body.append('<div class="%s">' % ("mono" if mono else "color"))
            for label, text in Render(path).panels:
                if label:
                    body.append("<h4>%s</h4>" % html.escape(label))
                body.append('<pre class="cells">%s</pre>' % render_ansi(text, used))
            body.append("</div>")
        body.append("</div>")
    if section:
        # The section is one file per heading and the page frames it: forty
        # goldens speak through the input frame's section, and the book would
        # carry its text forty times.
        body.append(
            '<h2>%s</h2><p class="meta">docs/interface/surfaces.md#%s</p>'
            '<iframe class="words" src="../words/%s.html" title="%s"></iframe>'
            % (html.escape(section.title), section.anchor, section.anchor, html.escape(section.title))
        )
    else:
        body.append('<p class="nowords">No words yet: no section of surfaces.md is mapped to this golden.</p>')
    body.append("</main>")
    return page(g.name, "".join(body), used)


# --- scenes ----------------------------------------------------------------

ACTIONS = ("setup", "keys", "press", "type", "paste", "shell", "sleep", "hold")


class Step:
    def __init__(self, kind, text, notes):
        self.kind, self.text, self.notes = kind, text, notes
        self.snap = self.expect = self.also = self.gate = None


def read_steps(path):
    intro, steps, notes, seen = [], [], [], False
    with open(path, encoding="utf-8") as fh:
        for raw in fh.read().split("\n"):
            line = raw.strip()
            if not line:
                continue
            if line.startswith("#"):
                (notes if seen else intro).append(line[1:].lstrip() if line != "#" else "")
                continue
            if not seen:
                seen = True
            gate = None
            words_ = line.split(None, 2)
            if words_ and words_[0] in ("wide", "narrow") and len(words_) >= 3:
                gate = ("only at %s columns or wider" if words_[0] == "wide" else "only below %s columns") % words_[1]
                line = words_[2]
            kind = line.split(None, 1)[0]
            s = Step(kind, line, notes)
            s.gate = gate
            notes = []
            if kind == "snap":
                try:
                    toks = shlex.split(line)
                except ValueError:
                    toks = line.split()
                s.snap = toks[1] if len(toks) > 1 else ""
                s.expect = toks[2] if len(toks) > 2 else ""
                s.also = toks[3:]
            steps.append(s)
    return intro, steps


def scene_page(root, scene, out_img):
    sdir = os.path.join(root, SCENES, scene)
    intro, steps = read_steps(os.path.join(sdir, "steps.txt"))
    cdir = os.path.join(root, CAPTURES, scene)
    used = set()
    body = [top("../"), "<main>", "<h1>%s</h1>" % html.escape(scene), '<p class="meta">scene · scripts/tui/scenes/%s</p>' % html.escape(scene)]
    if intro:
        body.append('<p class="note">%s</p>' % html.escape("\n".join(intro).strip()))
    captured = 0
    for s in steps:
        if s.kind != "snap":
            body.append('<div class="step">%s</div>' % html.escape(_short(s.text)))
            continue
        body.append('<div class="snap"><h3>%s%s</h3>' % (html.escape(s.snap), " · " + html.escape(s.gate) if s.gate else ""))
        cap = []
        if s.expect:
            cap.append("waits for <code>%s</code>" % html.escape(s.expect))
        if s.also:
            cap.append("and holds " + ", ".join("<code>%s</code>" % html.escape(a) for a in s.also))
        body.append('<div class="caption">%s</div>' % (" ".join(cap) or "captures the screen"))
        shown = False
        for ext in ("ansi", "txt"):
            p = os.path.join(cdir, s.snap + "." + ext)
            if os.path.isfile(p):
                # Read the way still.py reads a capture: bytes that are not UTF-8
                # are a glyph that went wrong, not a reason to draw nothing.
                with open(p, encoding="utf-8", errors="replace") as fh:
                    text = fh.read()
                body.append('<pre class="cells">%s</pre>' % render_ansi(text.rstrip("\n"), used))
                shown = captured = True
                break
        pic = os.path.join(root, PICTURES, "%s-%s.gif" % (scene, s.snap))
        if os.path.isfile(pic):
            os.makedirs(out_img, exist_ok=True)
            shutil.copy(pic, out_img)
            body.append('<img src="../img/%s" alt="%s">' % (os.path.basename(pic), html.escape(s.snap)))
            shown = captured = True
        if not shown:
            body.append('<div class="meta">no capture yet; the steps stand alone</div>')
        body.append("</div>")
    body.append("</main>")
    return page(scene, "".join(body), used), steps, bool(captured)


def _short(text, n=160):
    return text if len(text) <= n else text[:n] + " ..."


# --- index -----------------------------------------------------------------


def index_page(entries, scenes, unmapped, sections):
    items = []
    placed = {}
    for g, sec in entries:
        if sec:
            placed.setdefault(sec.anchor, []).append(g)
    body = [
        '<header class="top">design book</header><main><h1>Design book</h1>',
        '<p class="meta">Built from the goldens and the scenes. A surface page shows every width in colour, the mono render on a toggle, and the section of docs/interface/surfaces.md that speaks for it.</p>',
        '<input id="filter" type="search" placeholder="filter by name" autofocus>',
    ]
    for s in sections:
        gs = placed.get(s.anchor)
        if not gs:
            continue
        items.append('<h2 class="grp">%s</h2><ul class="index">' % html.escape(s.title))
        for g in gs:
            items.append('<li data-name="%s"><a href="surfaces/%s.html">%s</a></li>' % (html.escape(g.name), g.slug, html.escape(g.name)))
        items.append("</ul>")
    body.extend(items)
    body.append('<h2 class="grp">Scenes</h2><ul class="index">')
    for name, has in scenes:
        body.append('<li data-name="%s"><a href="scenes/%s.html">%s</a>%s</li>' % (
            html.escape(name), html.escape(name), html.escape(name), "" if has else ' <span class="meta">steps only</span>'))
    body.append("</ul>")
    body.append('<h2 class="grp nowords">no words yet</h2><ul class="index">')
    for g in unmapped:
        body.append('<li data-name="%s"><a href="surfaces/%s.html">%s</a></li>' % (html.escape(g.name), g.slug, html.escape(g.name)))
    body.append("</ul></main>")
    body.append(
        "<script>var f=document.getElementById('filter');f.addEventListener('input',function(){"
        "var q=f.value.toLowerCase();document.querySelectorAll('li[data-name]').forEach(function(li){"
        "li.style.display=li.dataset.name.toLowerCase().indexOf(q)<0?'none':''});"
        "document.querySelectorAll('h2.grp').forEach(function(h){var u=h.nextElementSibling,any=false;"
        "u.querySelectorAll('li').forEach(function(li){if(li.style.display!=='none')any=true});"
        "h.style.display=any||!q?'':'none'})})</script>"
    )
    return page("Design book", "".join(body))


def build(root, out):
    goldens = read_goldens(root)
    sections = read_sections(root)
    if os.path.isdir(out):
        if root == out or root.startswith(out + os.sep):
            sys.exit("book.py: --out %s would remove the tree being read" % out)
        shutil.rmtree(out)
    os.makedirs(os.path.join(out, "surfaces"))
    entries, unmapped = [], []
    for g in goldens:
        sec = section_for(g.name, sections)
        entries.append((g, sec))
        if not sec:
            unmapped.append(g)
        with open(os.path.join(out, "surfaces", g.slug + ".html"), "w", encoding="utf-8") as fh:
            fh.write(golden_page(g, sec))
    wrote = set()
    for _, sec in entries:
        if sec and sec.anchor not in wrote:
            wrote.add(sec.anchor)
            os.makedirs(os.path.join(out, "words"), exist_ok=True)
            with open(os.path.join(out, "words", sec.anchor + ".html"), "w", encoding="utf-8") as fh:
                fh.write(page(sec.title, '<main class="words"><h1>%s</h1>%s</main>' % (html.escape(sec.title), render_md(sec.lines))))
    scenes = []
    sdir = os.path.join(root, SCENES)
    if os.path.isdir(sdir):
        os.makedirs(os.path.join(out, "scenes"))
        for name in sorted(os.listdir(sdir)):
            if not os.path.isfile(os.path.join(sdir, name, "steps.txt")):
                continue
            text, _, has = scene_page(root, name, os.path.join(out, "img"))
            scenes.append((name, has))
            with open(os.path.join(out, "scenes", name + ".html"), "w", encoding="utf-8") as fh:
                fh.write(text)
    with open(os.path.join(out, "index.html"), "w", encoding="utf-8") as fh:
        fh.write(index_page(entries, scenes, unmapped, sections))
    size = sum(os.path.getsize(os.path.join(d, f)) for d, _, fs in os.walk(out) for f in fs)
    print("design book: %d surfaces, %d scenes, %d without words, %.1f MB in %s" % (
        len(goldens), len(scenes), len(unmapped), size / 1048576, out))
    if size > BUDGET:
        sys.exit("book.py: the book is %d bytes, over the %d budget" % (size, BUDGET))


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--root", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
    ap.add_argument("--out", default=None)
    ap.add_argument("--ansi", action="store_true", help="render stdin to an HTML fragment on stdout")
    a = ap.parse_args()
    if a.ansi:
        sys.stdout.write(render_ansi(sys.stdin.read()))
        return
    root = os.path.abspath(a.root)
    build(root, os.path.abspath(a.out or os.path.join(root, "docs", "design")))


if __name__ == "__main__":
    main()
