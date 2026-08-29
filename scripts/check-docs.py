#!/usr/bin/env python3
"""Verify every docs/ citation in the repo resolves to a real file and anchor.

Code comments cite documentation by path (docs/interface/principles.md#one-grid).
Headings are therefore anchors, and renaming one silently breaks every citation
to it. This script is what makes that loud instead. See AGENTS.md#documentation.

Also reports documents that nothing cites: a section nothing points at is
either wrong or unnecessary.

And it fails on a story identifier (S-060, E-018) anywhere in the code or a
golden fixture, and on a spec section reference (a § number) inside a Go
string literal or a golden fixture. Planning is not part of this repository,
so a reference to it points at something the reader cannot open. Such a reference belongs in a comment; in a
string it becomes test output, an error message, or — worst — committed
golden content, which couples a documentation edit to regenerating goldens.

Only § is checked, not docs/ paths: a path like docs/loop.md is a perfectly
ordinary test fixture filename, and internal/ui/keys deliberately stores doc
paths as data so each keyed surface names what is normative for it.
"""
import re, sys, pathlib, collections

def anchors(path):
    out=set()
    for line in path.read_text().splitlines():
        m=re.match(r'^#{1,6}\s+(.*)$', line)
        if m:
            t=re.sub(r'`([^`]*)`',r'\1',m.group(1))
            t=re.sub(r'\*\*?([^*]*)\*\*?',r'\1',t).lower()
            t=re.sub(r'[^\w\s-]','',t)
            out.add(re.sub(r'\s+','-',t.strip()))
    return out

# only real documents count as citations; docs/loop.md etc. are test fixtures
REAL={str(q) for q in pathlib.Path('docs').rglob('*.md')}
CITE=re.compile(r'\b(docs/[A-Za-z0-9_./-]*?\.md)(#[A-Za-z0-9-]+)?')
bad=[]; n=0; per=collections.Counter(); CITED=set()
for f in pathlib.Path('.').rglob('*'):
    if not f.is_file() or '.git' in f.parts: continue
    if f.suffix not in ('.go','.md'): continue
    try: txt=f.read_text()
    except Exception: continue
    for m in CITE.finditer(txt):
        rel, frag = m.group(1), (m.group(2) or '')[1:]
        if rel not in REAL:
            if pathlib.Path(rel).exists(): continue
            bad.append(f"{f}: citation to non-existent doc: {rel}") if rel.startswith(('docs/capabilities/','docs/interface/')) or rel in ('docs/README.md','docs/product.md','docs/architecture.md') else None
            continue
        n+=1; per[str(f)]+=1; CITED.add(rel)
        tp=pathlib.Path(rel)
        if not tp.exists(): bad.append(f"{f}: no such file: {rel}"); continue
        if frag and frag not in anchors(tp): bad.append(f"{f}: no anchor #{frag} in {rel}")
cited={c.split("#")[0] for c in CITED}
uncited=sorted(REAL - cited - {"docs/README.md","docs/capabilities/README.md","docs/interface/README.md"})
# a doc reference must never be data: not in a string literal, not in a golden
def comment_start(line):
    i=0; q=None
    while i < len(line):
        c=line[i]
        if q:
            if c=="\\" and q!="`": i+=2; continue
            if c==q: q=None
            i+=1; continue
        if c in "\"`'": q=c; i+=1; continue
        if c=="/" and i+1<len(line) and line[i+1]=="/": return i
        i+=1
    return -1

REF=re.compile(r"§\d+[a-z]?")
STORY=re.compile(r"\b[SEBT]-\d{3}\b")
for f in pathlib.Path(".").rglob("*.go"):
    if ".git" in f.parts: continue
    raw=False
    for ln,l in enumerate(f.read_text().split("\n"),1):
        if raw:
            raw ^= (l.count("`")%2==1); continue
        c=comment_start(l); raw ^= (l.count("`")%2==1)
        code = l if c<0 else l[:c]
        if REF.search(code):
            bad.append(f"{f}:{ln}: spec section reference in a string literal, not a comment")
        if STORY.search(l):
            bad.append(f"{f}:{ln}: story identifier in code — say what it does and cite docs/")
for f in pathlib.Path(".").rglob("testdata/golden/*.txt"):
    for ln,l in enumerate(f.read_text().split("\n"),1):
        if REF.search(l) or STORY.search(l):
            bad.append(f"{f}:{ln}: spec or story reference baked into a golden fixture")

print(f"checked {n} docs/ citations in {len(per)} files")
if uncited:
    print("uncited documents (nothing in the tree points at these):")
    for u in uncited: print("  "+u)
if bad:
    print(f"BROKEN ({len(bad)}):")
    for b in sorted(set(bad)): print("  "+b)
    sys.exit(1)
print("all citations resolve")
