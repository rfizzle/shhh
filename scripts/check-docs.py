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

And it fails on an os.Chdir or t.Chdir in a _test.go file. cmd/go records
every chdir target as one of the test's inputs, and a t.TempDir() path is new
on every run, so one such call makes its whole package uncacheable — `go test
./...` re-runs it in full against a tree nothing has touched. The rule is the
test file rather than the call: a chdir anywhere in production code is a
different question, and shhh's answer to it is that it never chdirs at all.

Only § is checked, not docs/ paths: a path like docs/loop.md is a perfectly
ordinary test fixture filename, and internal/ui/keys deliberately stores doc
paths as data so each keyed surface names what is normative for it.

And it fails on a section of docs/interface/departures.md that does not open
on its filed line — the date and what closes it — so a new one cannot land
undated, and on a header count of open gaps, open disagreements and closed
entries that no longer matches those lines. `make docs` runs this with
--write, which rewrites that count and nothing else.
"""
import re, sys, pathlib, collections


def visible(path):
    """Report whether path has no hidden component below the repository root."""
    return not any(part.startswith(".") for part in path.parts)

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

# Only visible documents count as citations; docs/loop.md etc. are test
# fixture filenames. Hidden directories are editor or tool state, not the
# checkout the documentation contract describes.
REAL={str(q) for q in pathlib.Path('docs').rglob('*.md') if visible(q)}
CITE=re.compile(r'\b(docs/[A-Za-z0-9_./-]*?\.md)(#[A-Za-z0-9-]+)?')
bad=[]; n=0; per=collections.Counter(); CITED=set()
for f in pathlib.Path('.').rglob('*'):
    if not f.is_file() or not visible(f): continue
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
CHDIR=re.compile(r"\b(?:os|t)\.Chdir\(")
for f in pathlib.Path(".").rglob("*.go"):
    if not visible(f): continue
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
        if f.name.endswith("_test.go") and CHDIR.search(code):
            bad.append(f"{f}:{ln}: a test that chdirs makes its package uncacheable — pass the directory to the code under test as an argument")
for f in pathlib.Path(".").rglob("testdata/golden/*.txt"):
    if not visible(f): continue
    for ln,l in enumerate(f.read_text().split("\n"),1):
        if REF.search(l) or STORY.search(l):
            bad.append(f"{f}:{ln}: spec or story reference baked into a golden fixture")

# Every departure says when it was filed and what closes it, so a reader can see
# which entries have aged; the header counts what is still open from those
# lines. See docs/interface/departures.md.
DEPARTURES=pathlib.Path("docs/interface/departures.md")
FILED=re.compile(r"_Filed \d{4}-\d{2}-\d{2} · (closes by (the artboard|the binary|a decision)|withdrawn|closed)_")
COUNT_BEGIN="<!-- BEGIN generated departure counts — written by `make docs` from each section's filed line; edit those, not this. -->"
COUNT_END="<!-- END generated departure counts -->"

def departures():
    """Return the departures page's problems and its counts region as it should read."""
    errs=[]; tally=collections.Counter()
    lines=DEPARTURES.read_text().split("\n")
    for i,l in enumerate(lines):
        if not l.startswith("## "): continue
        rest=[(j,x) for j,x in enumerate(lines[i+1:],i+2) if x.strip()]
        filed=rest[0][1] if rest else ""
        m=FILED.fullmatch(filed)
        if not m:
            errs.append(f"{DEPARTURES}:{i+1}: section opens without its filed line (_Filed YYYY-MM-DD · closes by the artboard|the binary|a decision_, or · withdrawn_ / · closed_)")
            continue
        word=m.group(2) or m.group(1)
        body=rest[1][1] if len(rest)>1 else ""
        for state,lead in (("withdrawn","*Withdrawn"),("closed","*Closed")):
            if (word==state)!=body.startswith(lead):
                errs.append(f"{DEPARTURES}:{rest[0][0]}: filed line says {word!r} but the section opens {body[:24]!r}")
        tally["gap" if word=="the artboard" else "closed" if word in ("withdrawn","closed") else "disagreement"]+=1
    def count(k,one,many): return f"{tally[k]} {one if tally[k]==1 else many}"
    region="\n".join([COUNT_BEGIN,"",
        f"**{count('gap','open gap','open gaps')} · {count('disagreement','open disagreement','open disagreements')} · {tally['closed']} closed or withdrawn**",
        "",COUNT_END])
    return errs, region

def counts_span(txt):
    a=txt.find(COUNT_BEGIN); b=txt.find(COUNT_END)
    return (a, b+len(COUNT_END)) if a>=0 and b>a else None

derrs, region = departures()
if "--write" in sys.argv[1:]:
    txt=DEPARTURES.read_text(); span=counts_span(txt)
    if derrs or not span:
        for e in derrs or [f"{DEPARTURES}: no generated departure counts region"]: print("  "+e)
        sys.exit(1)
    DEPARTURES.write_text(txt[:span[0]]+region+txt[span[1]:])
    print(f"wrote the departure counts in {DEPARTURES}")
    sys.exit(0)
bad+=derrs
txt=DEPARTURES.read_text(); span=counts_span(txt)
if not span: bad.append(f"{DEPARTURES}: no generated departure counts region")
elif txt[span[0]:span[1]]!=region: bad.append(f"{DEPARTURES}: the departure counts are stale — run make docs")

print(f"checked {n} docs/ citations in {len(per)} files")
if uncited:
    print("uncited documents (nothing in the tree points at these):")
    for u in uncited: print("  "+u)
if bad:
    print(f"BROKEN ({len(bad)}):")
    for b in sorted(set(bad)): print("  "+b)
    sys.exit(1)
print("all citations resolve")
