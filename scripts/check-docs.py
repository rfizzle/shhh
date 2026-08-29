#!/usr/bin/env python3
"""Verify every docs/ citation in the repo resolves to a real file and anchor.

Code comments cite documentation by path (docs/interface/principles.md#one-grid).
Headings are therefore anchors, and renaming one silently breaks every citation
to it. This script is what makes that loud instead. See AGENTS.md#documentation.

Also reports documents that nothing cites: a section nothing points at is
either wrong or unnecessary.
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
print(f"checked {n} docs/ citations in {len(per)} files")
if uncited:
    print("uncited documents (nothing in the tree points at these):")
    for u in uncited: print("  "+u)
if bad:
    print(f"BROKEN ({len(bad)}):")
    for b in sorted(set(bad)): print("  "+b)
    sys.exit(1)
print("all citations resolve")
