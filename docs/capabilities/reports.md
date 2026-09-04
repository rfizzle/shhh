# Reports

## What a report is

Some answers are pages. Why the test suite got slow is forty packages sorted
by wall time with three pulled out above them and six runs of history
underneath — none of which fits in a terminal, and all of which fits on a
page. A report is that page: the model builds it when the answer earns one,
the transcript row carries a link to it, and it opens in the browser beside
the terminal, drawn in the same palette the terminal is.

The failure mode this surface is designed against is not that no report is
ever made. It is a report for a three-row table, which teaches the reader to
ignore the link. The tool that makes one is described to the model with that
rule in it: plain text stays right for anything a sentence or a short table
answers.

## Typed blocks and freehand

Most reports are a stat band over a table with a chart under it, so those
shapes — stats, table, bar and line charts, a diff, a tree, prose — are typed
blocks: the model supplies data and the page draws it. Typed blocks are
stored as data and re-drawn under the current template every time the page is
served, so an old report quietly adopts whatever the page chrome has since
become.

The block set is the default vocabulary, not the boundary. When the answer is
a picture no block holds — a dependency graph, a timeline, a state machine
with the dead branch drawn in the deletion colour — the model draws it
freehand, in static markup and inline SVG. Freehand is validated once, when
the report is made, and frozen: it replays exactly as it was drawn, because
the drawing is the artifact and silently redrawing it later would be a
different report wearing the same id.

What holds the two halves together is not the block set; it is the tokens.
Every colour on the page is a named variable from one stylesheet, aliased
from the terminal palette, so a deleted line is red on the page for the same
reason it is red in the terminal. Freehand markup may name colours only
through those variables — a literal colour is rejected with the violation
named — and the stylesheet's normative home is the design system, beside the
terminal tokens, because it is the same system. The one register the terminal
never needed is categorical series — spend by model, wall time by package,
where the categories mean nothing — and the stylesheet carries a ramp for
exactly that, deliberately duller than the state hues so that series three
never reads as success.

## A page shhh writes for you

Every page above is one the model chose to build. A sprint's report is the
first that shhh builds itself: when a set of work closes, the goal, the items
with what each produced, what stopped the rest and what the set cost go onto
a page, and the board offers the link
([`the sprint board`](../interface/surfaces.md#the-sprint-board)).

It is the same blocks — a stat band, a table, prose — because a second
vocabulary for pages shhh writes would be a second design to keep in step
with the first, and nothing about who asked for a page changes what a page
is. Two things do change. Nothing here came off a tool call, so there is no
freehand markup to validate: the whole document is typed blocks the product
built. And it does not open a browser. A page written while you were away
must not steal the window you are in; the link is on the board and in the
line that says the sprint closed.

## A report outlives its session

A report you cannot come back to is a screenshot you forgot to take. Reports
are state, so they live where state lives — the state directory, keyed by
project, never in the checkout: a report is a thing you made, not a thing the
project ships, so it never appears in `git status` and never needs an ignore
line.

The listing command shows what a project has produced, in the shape every
other listing has, and reopens one by id. The serving link itself is
deliberately short-lived — an ephemeral loopback port that lasts as long as
the process serving it — so the id, not the URL, is the durable name, and the
tool's own result says how to reopen one.

## Findable and prunable

Permanence has a cost, and it is the one an ephemeral design would be
dodging: things that last must be findable and prunable. The listing is how
they are found; retention is a setting with the same default history keeps,
enforced whenever the store is opened; and the doctor has a row that names
the directory and what it is costing in disk, the same row the log gets.

## The page cannot phone home

A report can quote source, test output and timings from a private repository,
so the store gets the treatment credentials get, not the treatment a cache
gets: user-only permissions, under the same containment deny mask as the
database beside it — an approved command cannot read the store, and the
model's own web tool cannot fetch from the serving address, both on purpose.

Serving is loopback only, on an ephemeral port, at a path built from an
unguessable id — and a wrong guess learns nothing, not even whether it was
close. The page itself is self-contained: no scripts, no event handlers, no
external stylesheets, fonts or fetches, embedded data only. The validator
refuses what the grammar does not allow, and the serving policy tells the
browser the same thing, so nothing the page holds can be sent anywhere.

This is a local render surface, not an export or sharing feature — nothing
leaves the machine, which is what keeps it inside the boundary the project
drew when it declined page hosting and gist sharing as goals.

## Related

- [Configuration](configuration.md) — the state directory reports live under,
  and the retention setting.
- [Approvals and safety](approvals-and-safety.md) — why the state directory
  sits behind the containment deny mask.
