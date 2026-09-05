# Departures from the design system

The `shhh Design System` project is normative
([why](../architecture.md#design-lives-outside-the-repository)). These are the
places the binary deliberately does something else, with the reason, so that
the next reader finds a decision rather than a discrepancy.

Two positions are allowed and there is no third: a divergence is either fixed
in the implementation, or it is recorded here. What counts as a divergence is
what a reader sees on the row — a field named one thing in the design and
another in Go is not one. Where a guideline and an artboard disagree about a
rule, the guideline wins, and an artboard that breaks one is a bug in the
artboard rather than a departure.

A section here is one of two things, and says which: a *disagreement*, where
an artboard draws one thing and the binary draws another for a stated reason;
or a *gap*, where no artboard draws the surface at all and the binary had to
decide. A gap is closed by drawing the artboard, and where the two then differ
the artboard wins.

## Diff line numbers are as wide as the file

The design fixes the gutter width. Padding a short file out to that width
takes columns from the code the row exists to show, so the gutter is as wide
as the largest line number actually in the hunk.

## A deletion's gutter marker is a hyphen

The design uses a true minus sign, which is the right character for a *count*
— and it is used for counts. In the gutter the marker is part of a unified
diff, which is a format other tools parse, so it stays a hyphen.

## The inspector rail is drawn at one width and rendered at several

The rail's artboard is drawn at its narrowest, which is the width the surface
splits at and the one most terminals show. It is not the only width the binary
draws: past the threshold the rail grows with the terminal to a ceiling, and
the reader can fix it anywhere in that range
([the rule](surfaces.md#the-inspector-rail)). Column widths are the design
system's to settle, so the range and its ends are the artboard's business as
soon as there is a wide variant of it to state them; until then the artboard
is the narrow end of a range rather than the whole of it, which is a gap and
not a disagreement.

## The scroll gutter has no artboard

No artboard draws a scroll gutter, and the drawing kit has no glyph for one.
The binary draws a dashed track under a block thumb in the column beside the
pane divider, told from the divider by shape and not by shade
([invariant 1](principles.md#colour-never-carries-meaning-alone)), because two
rules a column apart in the same material read as a double border.

The drawing is provisional. The gutter is the one column on the screen that
reports a position rather than bounding a region, and what that column should
look like is a decision the design system has not taken. This is a gap: when
the gutter is drawn, the artboard wins, and the kit gains whatever glyphs it
needs.

## The backlog block opens with a sprint row

The Backlog artboard draws the block as a heading and a list of items. A set
being worked has a name and a size, and both are what tell the reader whether
the rows below are the whole backlog or the eight things chosen for this week
— which is the difference between a list to scan and a list to work. The row
sits above the items, states the set's name and how many of them are done, and
is absent altogether where no set is open, so a backlog worked without one
looks exactly as the artboard draws it.

## The backlog run's row has no artboard

There is no artboard for a run's row. What the binary draws is the step
header's grammar — the same fold state, the same lead columns, the same faint
rule, the same right-aligned duration field — with the stages under it as a
strip and each stage's own note under that.

The reason is the neighbours. A run's row sits in a transcript of steps, and
a run *is* a step of steps: a second header shape a column out of alignment
would read as a different kind of thing at exactly the moment the reader is
being told it is the same kind. Column widths are the design system's to
settle, and the artboard is the place to settle them. This is a gap: when the
row is drawn, this is what the artboard has to reconcile with, and where the
two differ the artboard wins.

## The light table's rungs were chosen in the binary

`tokens/colors.css` has one column. It states a hex and the 256 index that hex
stands for, for fifteen tokens, chosen against a dark ground — and every one of
those choices is a choice about the ground as much as about the hue, which is
why a light terminal cannot be served by lightening them
([the rule](principles.md#a-colour-is-three-values-and-a-ground)).

The second column belongs there and is not there yet, so the light table was
chosen here instead, on the first column's own reasons: three rungs per token
and nothing derived, the same five tokens deferring to the terminal's theme,
ten hexes that are exactly the 256 index beside them, and the two chrome greys
keeping their jobs by swapping their weight — the faint one is the one nearer
the ground, which is the lighter grey on black and the darker grey on white.

This is a gap and not a disagreement. When the column is drawn, these fifteen
rows are what it has to reconcile with, and where the two differ the artboard
wins.

## A theme is a table, and CharmTone is one of them

The design system describes one palette. A second table of the same fifteen
jobs drawn in CharmTone is not a divergence from it — nothing about the
product's colours changes for anyone who does not ask for it — but it is a set
of colours that no artboard states, so it is written down here.

It exists because the interface is built on libraries drawn in that palette and
the pairing is worth offering, and it is the one table where the five tokens
that normally defer to the terminal's own theme do not: a named palette that
handed its green back to whatever the user's config says would not be that
palette.

## The backlog screen's layout was decided in the binary

The Backlog artboard draws `/todo` as a picker: a card in the panel, one slug
per row with its state beside it, and enter to read one. The binary draws a
screen. Reading an item, walking both ends of its dependencies and acting on
it are what the command is opened for, and a card cannot hold them, so the
picker was superseded rather than disagreed with — the screen is the
supporting screens' own shape over backlog items, and an artboard for it is
owed. Most of it needed no decision: the header and its rule, the two panes
and the divider, the key row, the windowed list and the counted overflow
markers were all drawn already. Four things it could not take from anywhere,
and they were decided here:

**The row's field order, and which field gives ground.** The name and the two
grade letters are kept, the state clips, and the title goes first. The pane
beside the list carries the title in full, so losing it there is a fold; the
state is why the list is on screen at all.

**The pointer moves on the arrows alone.** Every other list in the product
moves on `↑↓` and `j/k`. Four letters select on this screen — status,
priority, kind and ready-only — and one of them is `k`. A key is answered
once, so the pair is broken here and nowhere else.

**Both ends of a dependency.** What an item waits on is on the row, and what
waits on it is on the pane's header line. The design system has no drawing of
the second, because no surface has ever shown it.

**Where the two panes stop fitting.** The fold is at the history browser's
own threshold, arrived at the same way: below it the pane beside the list is
prose in a column too narrow to read a sentence in.

When there is an artboard, these four decisions are what it has to reconcile
with, and where the two differ the artboard wins.

## The item draft card's layout was decided in the binary

There is no artboard for it. Most of the card needed no decision — the frame,
the title rail and its chip, the pointer, the windowed rows and the counted
overflow marker are the selector's, drawn already. Four things it could not
take from anywhere, and they were decided here:

**A header field is a row, and the checkbox key steps it.** The alternative
was a key per field, and the register has no letters left that mean "kind" or
"size" on a card that also has to offer the editor and the writing. A row that
carries its own answers reads as a field, the pointer already lands on it, and
the key that toggles a checkbox where a row has two answers steps a scale
where it has three. A value the model gave that is off the scale steps back
onto it rather than needing a key of its own.

**What it waits on is a row that opens a list, not a scale.** Dependencies
are the backlog rather than a closed set, so the same key opens the backlog on
that row. Checking nothing there is an answer — it is how dependencies are
cleared — where on every other multi-select an empty answer is the slip it
looks like.

**The reading folds before the rows do.** The body is drawn under the header
in the renderer the transcript uses, and the card is bounded by the panel. The
rows a key can land on are kept and the prose folds with the count of what it
hid, because a card that dropped its rows to keep its reading cannot be
answered. It keeps a row wherever the card has one to spare, down to the
marker alone: a reading that vanished leaves a frame around four fields, which
says nothing about what the fields are for.

**The warning is pinned above the key row and wraps.** What will not survive
being taken — a dependency naming nothing — is stated where it cannot scroll
away, and it wraps rather than clips, because half a sentence about what is
about to be dropped is worse than no room for it at all.

When there is an artboard, these four decisions are what it has to reconcile
with, and where the two differ the artboard wins.

## The sprint board's layout was decided in the binary

There is no artboard for it, and one is owed. Most of the tab needed no
decision — the two panes, the windowed list, the header, the rule and the key
row are the backlog screen's, drawn already, and the progress meter is the
step meter with the set's own noun. Five things it could not take from
anywhere, and they were decided here:

**The head is pinned above both panes, and it gives ground first.** What the
set is for and how far through it is are the two facts the tab exists to
state, so they do not scroll with the list. But a head taller than the tab
would leave no set on screen at all, and a board with no set on it is a
paragraph — so when the terminal is short the head is what clips, down to the
rows the list needs.

**The row's state field is the set's reading, not the item's.** Everywhere
else on this screen a row states where an item stands in the backlog. Here it
states where the slug stands in the *set*: the one being worked says which
stage it is at, a slug the backlog no longer holds says so, and the two are
different sentences about the same file. A row that said "in progress" on a
board would answer the question the board was opened to ask with the word it
already had.

**The plan is a card on the tab, not a card over the transcript.** Choosing
the set and watching it are the same two questions about the same thing, and
a proposal drawn somewhere else would be a second place a sprint is looked
at. The card holds the keyboard while it is up, so the tab's own list is
drawn and not live — which is what lets the card keep `j/k`, the one pair
this screen had to break.

**The plan's card carries a reading, and folds what it left out.** The
proposal is a reading of the ready items — grouped by what makes a set ship
as one change — so every row's reason is a sentence a model wrote, and the
card has a second half: the candidates the reading did not take, each with
one word for why. That list is folded under the set behind its own key rather
than drawn beside it. What the reader is answering is the set; what was left
out is the evidence behind the answer, and a recommendation that showed only
what it took could not be argued with — which is the whole of what a reading
is for. Folded, the row states how many went and which words they took, so
the count is never the only thing on screen
([fold, never hide](principles.md#fold-never-hide)).

**A dropped row keeps its place.** The alternative was removing it, and the
card is the only record of what was proposed: a row that left could not be put
back without planning again. So the box empties and the row stays where the
order put it.

When there is an artboard, these five decisions are what it has to reconcile
with, and where the two differ the artboard wins.
