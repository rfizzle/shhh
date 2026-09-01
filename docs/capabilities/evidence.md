# Evidence

## Output outlives the question it answered

A test suite prints nine thousand lines and three of them matter. A server's
tool returns a page of JSON to answer one field. The reader needed the answer
once; the conversation carries the whole thing for the rest of the session,
and pays for it on every subsequent request.

So bulky output is reduced before the model sees it: a verbatim head, a
verbatim tail, and the lines from the middle that no reduction may drop
silently — errors, panics, test failures. What was cut is said plainly, in
line counts, where the cut happened. The original is kept whole in a
session-scoped store, and the reduced view carries the token that retrieves
it, so nothing is lost — it is only moved somewhere that costs nothing until
it is asked for.

The pipeline fails open in both directions. Output small enough not to be
worth reducing passes through. So does output the reduction would barely
shrink, and output whose original could not be stored — because a reduction
the reader cannot undo is worse than no reduction at all.

## Reduction is for unbounded output

A command's output has no natural size. Neither does a page's, or a remote
server's. That is the whole reason the pipeline exists, and it is the only
place it earns its cost.

**A tool that already bounds its own output is exempt.** Reading a file,
listing a directory, searching, globbing — each of these returns a shape it
chose, inside a cap chosen for that shape, and says how to continue past it
when it runs out. Cutting a head and a tail through that result is not a
saving. It is a second, shape-blind edit on top of a deliberate one, and what
it destroys is the middle.

The failure this rule exists for was specific and expensive. The file read is
told, in the instruction the model actually acts on, to return a whole file in
one call, because reading a file in small windows costs a round per window and
tells the reader less each time. The reduction then took the four hundred
lines it returned and handed back sixty. The reader's only way to the rest was
to page the store a few thousand bytes at a time — which is the paging loop
that instruction was written to stop, reintroduced underneath it, in a
mechanism the instruction knew nothing about.

Two rules that disagree do not average out. The one nearer the machine wins,
and the reader never finds out why the other one did not work.

## The reader can always get the whole thing back

The store is not an archive; it is the other half of the reduced view. Every
reduction names the entry that holds its original, and the model can ask that
entry for its metadata, page through its bytes, or search it for a literal
string — which is usually what it wanted from the elided middle in the first
place.

The ids are opaque session-scoped tokens rather than paths. A retrieval
mechanism that took a filename would be a file read with no scope check
wearing a different name.

## Related

- [`coding-agent.md`](coding-agent.md) — the rounds this is spent on, and the
  rules that keep a session from wasting them
- [`sessions-and-memory.md`](sessions-and-memory.md) — what else outlives a
  turn, and what deliberately does not
