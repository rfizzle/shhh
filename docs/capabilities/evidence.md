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

A command's output has no natural size. Neither does a remote server's. That
is the whole reason the pipeline exists, and it is the only place it earns its
cost.

**A tool that already bounds its own output is exempt.** Reading a file,
listing a directory, searching, globbing, fetching a page, outlining a file
with the language server, attributing its lines to the commits that wrote
them — each of these returns a shape it chose, inside a cap chosen for that
shape, and says how to continue past it when it runs out. Cutting a head and
a tail through that result is not a saving. It is a second, shape-blind edit
on top of a deliberate one, and what it destroys is the middle.

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

Which tools those are is answered where they are registered, not by a list in
the pipeline. Most of them are optional — a language server that was
detected, an external tool that happened to be installed — so the only place
that can name them is the one that decided this session has them, and a list
kept anywhere else names the tools that existed on the day it was written.

Sometimes the tool is not the unit the bound was chosen at. The history tool
bounds the verbs with a narrower question to offer — a status by paths, a log
by count, a blame by its line window — and deliberately leaves showing a
commit and diffing two of them unbounded, because the content is however
large the commit is and there is no argument that would return less. So that
one is declared per call: the window the reader narrowed to arrives whole,
and the patch that could be anything is what the pipeline is for.

## The reader can always get the whole thing back

The store is not an archive; it is the other half of the reduced view. Every
reduction names the entry that holds its original, and the model can ask that
entry for its metadata, page through its bytes, or search it for a literal
string — which is usually what it wanted from the elided middle in the first
place.

The ids are opaque session-scoped tokens rather than paths. A retrieval
mechanism that took a filename would be a file read with no scope check
wearing a different name.

## A page is kept whole

Not everything in the store arrived as a tool result that was too big. A
fetched page goes in whole, before anything is cut, and the slice the
conversation carries is the opening of exactly those stored bytes. What the
reader gets back is that slice and one notice: the entry that holds the page,
the offset the cut fell at, and the two things that can be done with an entry
— read on from there, or search it for a literal.

The ordering is the point, and it was wrong first. The fetch cut the page to
what a conversation could carry, and the reduction pipeline then kept *that*
as the original. Everything past the cut was gone before anything could store
it, and nothing said so: a documentation page whose answer sat two thirds of
the way down came back as a page that did not answer the question.

A page is also the one read whose size nobody chose — not the reader, not
shhh, only whoever published it. So the fetch bounds itself, the way a file
read does, and the pipeline leaves the result alone. Reducing a view that was
already cut on purpose would cost the middle of it and write a second copy of
a page the store already holds.

The cache keeps what was fetched rather than what was extracted, so the second
read of a long page costs no request and still lands whole in the store. A
page paged back from a cached fetch reads the same as one paged back from a
fresh one.

**The slice answers a different question once the page is retrievable.** A
session with nowhere to keep the rest has to carry as much of the page as a
result reasonably can, because the cut is the end of it. A session with a
store does not: the whole page is one retrieval away, so the first slice is
for deciding whether this is the right page, not for answering from — and a
research turn reading six pages spends a third of what it otherwise would
before it can narrow. One setting names the slice, and setting it fixes the
number whatever the session keeps.

**A page's text keeps what makes it navigable and what makes it a table.** A
link is written as its text and its destination together, resolved against
the address the page was read from, so a documentation index is a set of
addresses rather than a list of labels the next fetch has to guess a URL
from; a fragment or a `javascript:` href is dropped, because neither is
somewhere a fetch can go. Table cells keep their boundaries, so a parameter
table reads as three columns rather than as `namestringthe thing`. An image
contributes its alt text and a preformatted block is fenced, which is what
tells a reader whose whitespace it is looking at.

**Two shapes of page are not text, and both say so rather than failing
quietly.** A PDF is turned into text by the reader this machine has installed;
where it has none the result names the program that would have read it, so
the answer is "install this" rather than a retry of the same URL. And a page
that is a shell for a script — a large document that yields a title and four
words — is reported as exactly that, with the byte counts, because a fetch
that returns four words looks like a page that says four things, and the next
move is otherwise the same URL with a different guess.

## A site is read at the pace it answers

A fan-out is three researchers, and to a documentation site those three are
one session behaving like a crowd. So requests to one host go out one at a
time across the session and its children, with a quarter of a second between
them, and requests to different hosts never wait for each other — pacing one
site says nothing about another. The cache is asked before any of that: a
page two children both want costs one request, and the second child is
answered out of the store rather than queued behind the first to be told
what was already there.

A host that says *slow down* — 429, or the 503 a host under load sends
instead — is believed once. The wait is what it named in `Retry-After`,
floored at a second so its window has time to actually turn and capped at
twenty because a longer wait is a decision for the person at the keyboard
rather than a countdown; a host that named nothing gets two seconds, which
is the first wait of the schedule a stalled provider is waited out on,
because one session should not hold two answers to "how long is a short
wait". The wait is served with the host's turn still held, which is what
keeps the other two researchers from walking into the same refusal, and it
is not charged to the fetch timeout: that ceiling is how long one request may
take, and time a host explicitly asked for is not the request running long.

The second refusal is the answer. A page refused twice comes back as an
error naming the host, the status and the wait already spent, so the model
reads a different source instead of asking a third time — which is exactly
what the rate limit was asking for. Everything else is final on the first
answer: a 5xx that is not a 503 is a server that broke, a timeout is the
ceiling the person set, and a 4xx other than the 429 is this host's settled
answer about this request — the page is missing, or it is not ours to read.
Nothing about any of them says a second identical request would go better,
and each comes back with its status on it so the model can tell which it
was.

The wait is visible while it happens. The fetch's row says how many seconds
are left and which host asked for them, counting down, because a session
that has gone quiet for twenty seconds is otherwise indistinguishable from
one that has hung — and cancelling the turn gives the wait up with it.

There is no robots.txt in any of this, and that is a decision rather than an
omission. The fetcher reads the page a person asked for, one URL at a time,
the way the browser on their desk does; it does not crawl, and a browser
does not ask either. At this scale the courtesy that matters is pacing.

## A trim makes the same promise

The store is not only for output that arrived too big. A long session fills
the window whatever each result cost, and when it does the oldest tool
results are elided to make room for the next request. That used to be the end
of them: the model would notice a finding missing, run the tool again, and
get charged for the same bytes twice — and the loop that watches for a
session going in circles would report it, correctly, as going in circles.

So an elided result goes into the store on its way out, and what replaces it
in the conversation is a notice carrying the id, worded the way a reduction's
notice is. One wording, because the model is told once how to page an id back
and that instruction has to cover both. It is short enough that eliding still
recovers most of what it elided for, and a result shorter than the notice
that would replace it is left where it is: rewriting it would cost the
provider's cached prefix from that message on and give back nothing.

The transcript's own copy goes with it, and that is the larger half of what
a trim recovers. What the model was sent and what the screen shows are two
copies of the same result, and eliding one left the other holding the bytes
for the life of the process — a session that had trimmed twice was still
carrying everything it had ever read. So the row's body is replaced by the
same placeholder the model got. The row stays, because it is the account of
what ran, and so do its counts, which are what a reader scanning the feed was
reading anyway; opening the row whole pages the original back out of the
store, which is the offer the model has, made to the person.

Recovery is an offer, never a condition. A store that cannot take the
result — full, gone, never opened — leaves the plain placeholder behind and
the trim goes ahead, because the request that provoked it still has to fit,
and a session whose store is failing is exactly the session that most needs
the window back.

The offer holds wherever the trim runs: a person's session, an unattended
run, a served turn, and a sub-agent. It is worth most on the last three. A
session trims only in front of a person's request and has somebody sitting
there to notice a finding gone and ask for it again; the others recover the
window at every round boundary, so they trim far more often and there is
nobody watching. The same is true of the results that are never elided at
all — a skill's instructions are guidance for the rest of the work, and an
unattended run that quietly stops following them is a run whose only symptom
is the answer it gives.

## Search has more than one backend

Search was one product's defaults, which meant a machine without a paid key
had no search at all. There are two backends now, and they answer the same
tool in the same three fields — a title, a URL and a snippet — so what the
model reads does not depend on which one a machine happens to have.

Brave is a paid API with a key. A SearXNG instance is a metasearch front end
somebody runs themselves; it takes no key, and it is named by its URL because
it is their own machine and there is nowhere to default it to. An instance
serves its results as JSON only when it has been told to, and one that has
not answers with the page it serves a browser — the search then holds
nothing, which reads as a web with no answer on it rather than as a setting
one line away. That is a check `shhh doctor` makes, against the instance
itself: whether it will answer in JSON is not something this side can know
without asking.

A scraped results page would need no instance at all, and is refused on two
grounds that outlast any one engine: the markup changes with no notice, and
the terms of every engine forbid it. Another paid API is a small addition
once the parameters below are mapped, and can be made when somebody has a
key for one.

## A search is refused rather than widened

A search takes three parameters beyond the words: an age to stay within, one
site to stay on, and which page of results to return. Each backend maps them
onto its own spelling — a two-letter code here, a `time_range` there, an
operator inside the query where neither has a field for it — and the model
writes the same call whichever backend is configured, because a parameter
that changed spelling with a setting would make the call depend on something
the model cannot see.

Where a backend has no equivalent, the search is refused and the refusal
names the parameter. The alternative is sending the search without it, and
that failure is silent in the worst way: a question narrowed to last week
comes back unnarrowed, the model reads year-old results as this week's, and
nothing anywhere in the answer says otherwise. A refusal costs a round and is
recoverable — the model drops the parameter, or narrows the words instead.

## A session is told which half of the web it has

A session may have both web tools, fetch alone, or neither, and the prompt
says which. It used to hedge — web tools, "when registered" — and the hedge
cost the sessions with no search the most: told it might have a search, a
model spends a round calling one that is not there, reads the unknown-tool
error, and then guesses at the URL the search would have found. Told plainly
that fetch is all it has, it asks for a URL or reads the workspace, which are
the two things that actually work.

## Related

- [`coding-agent.md`](coding-agent.md) — the rounds this is spent on, and the
  rules that keep a session from wasting them
- [`sessions-and-memory.md`](sessions-and-memory.md) — what else outlives a
  turn, and what deliberately does not
