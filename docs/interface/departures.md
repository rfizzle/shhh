# Departures from the design system

The `shhh Design System` project is normative
([why](../architecture.md#design-lives-outside-the-repository)). These are the
places the binary deliberately does something else, with the reason, so that
the next reader finds a decision rather than a discrepancy.

Two positions are allowed and there is no third: a divergence is either fixed
in the implementation, or it is recorded here. What counts as a divergence is
what a reader sees on the row — a field named one thing in the design and
another in Go is not one.

## An unavailable option carries a glyph, not just a shade

The design paints an unusable row grey and stops there.
[Colour never carries meaning alone](principles.md#colour-never-carries-meaning-alone)
does not allow a shade to be the only difference between two states, and the
monochrome palette has one grey to spend. The glyph stays, and so do the words
beside it.

Where a guideline and an artboard disagree about a rule, the guideline wins.

## An unavailable option is still selectable

The design says such a row is shown but not selectable. Choosing it is how the
surface explains *why* it cannot be used — the reason is stated on the row
rather than swallowed. Nothing acts on the choice, so no key does anything it
did not offer.

It is a row that answers rather than a row that refuses.

## The multi-select keeps its pointer

The design draws a focused row as a checkbox inside a focus background, with
no pointer. A background is the one focus treatment a monochrome terminal
cannot carry, so the pointer stays.

## The note field is a labelled row, not a nested frame

The design draws a border around the note. A card's height comes out of the
bottom panel's budget, and a nested frame spends two of its rows on that
border — taken from the options. A label above the text names the field just
as well and costs nothing, and it turns red when the note is required exactly
as the border would have.

## The plan card numbers its options

The design draws them unnumbered. The card offers jump-to-number keys, and a
number you can read has to be a number you can type.

## The plan checklist has a fourth state

The design declares done, running and to-do. A step that finished and
contained a failure is none of the three, and the checklist is the answer to
"where are we".

## The plan checklist names a command, not keys

The design offers keys on the rail. The rail has no way to hold the keyboard,
so a key printed there would be an offer nothing accepts
([invariant 5](principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard)).
It names the command that shows the whole list instead.

## Diff line numbers are as wide as the file

The design fixes the gutter width. Padding a short file out to that width
takes columns from the code the row exists to show, so the gutter is as wide
as the largest line number actually in the hunk.

## A deletion's gutter marker is a hyphen

The design uses a true minus sign, which is the right character for a *count*
— and it is used for counts. In the gutter the marker is part of a unified
diff, which is a format other tools parse, so it stays a hyphen.

## A fan-out lane's verb is the kind, not the child's name

The design puts the child's name in the verb field. The verb vocabulary is
[closed](principles.md#closed-vocabularies) and its field never grows, and two
children's names clip to the same eight columns. The verb says what kind of
row it is; the name leads the target, which is the only field allowed to grow,
and the lane still lines up with the rows around it.

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

## The agent manager kills on a capital

The design offers a lower-case key. That key is move-up on every list in the
product, including this one, and a movement key that also kills a process is
the worst kind of false offer. Cancelling and killing are the same letter in
two cases, and the pair reads as one escalation.

The rule outlives this surface. A
[keymap file](../capabilities/configuration.md#the-keymap-file) can move keys,
and it is refused where it would put a destructive act — killing, deleting,
forcing an undo past its confirm — under a key that moves the cursor. A
decision taken here to keep a reflex safe is not one a file gets to reverse.

## The scroll gutter is a dashed track under a block thumb

The design gives the gutter the same rule the frame and the pane divider draw,
one shade apart. The gutter sits one column from that divider, so two rules a
column apart read as a double border, and the rows the thumb covers read as a
third. The shade that was meant to separate them is the first thing a
monochrome terminal spends, and a sixteen-colour one spends it too.

The gutter is the one column on the screen that reports a position rather than
bounding a region, so it is the one that changes: a dashed track under a block
thumb, told from the divider by shape and not by shade
([invariant 1](principles.md#colour-never-carries-meaning-alone)).

## Stopping a run is the cancel chord, not Esc

The design offers Esc on the rail as the key that stops the run. Esc here
means [go back](principles.md#esc-is-always-the-safe-answer) and nothing else:
it clears the draft, drops a selection, dismisses a menu, detaches a level,
leaves a waiting decision waiting. A key that backs out of that many surfaces
cannot also be the one that abandons minutes of work, because the hand
presses it before the eye has read which of them it is about to do. The
cancel chord stops the turn, and every rail that offered the artboard's Esc
names the chord instead.

## The turn's account opens the top rail

The frame's artboard hangs the session identity at the rail's left corner and
the running turn's account against its right. The account is the only thing on
the frame that changes while a turn runs, and the eye that is watching it is on
the prompt glyph two rows below the left corner; on a three-thousand-pixel
window the artboard's arrangement puts the moving figures a hundred and fifty
columns from there, at the one edge of the screen nobody is looking at.

So the two sides trade places: the account opens the rail and the identity —
the breadcrumb while attached, nothing at the root — closes it. Where the rail
has room for only one, the account is the one that stays.

## The backlog block opens with a sprint row

The Backlog artboard draws the block as a heading and a list of items. A set
being worked has a name and a size, and both are what tell the reader whether
the rows below are the whole backlog or the eight things chosen for this week
— which is the difference between a list to scan and a list to work. The row
sits above the items, states the set's name and how many of them are done, and
is absent altogether where no set is open, so a backlog worked without one
looks exactly as the artboard draws it.

## The backlog run's row is drawn with the step's grammar

The RunRow artboard states the row's own layout. What the binary draws is the
step header's grammar instead — the same fold state, the same lead columns,
the same faint rule, the same right-aligned duration field — with the stages
under it as a strip and each stage's own note under that.

The reason is the neighbours. A run's row sits in a transcript of steps, and
a run *is* a step of steps: a second header shape a column out of alignment
would read as a different kind of thing at exactly the moment the reader is
being told it is the same kind. Column widths are the design system's to
settle, and the artboard is the place to settle them; until the two are read
side by side this is a row drawn from the grammar its neighbours keep rather
than a disagreement with the artboard about any of them.

## The chrome's empty runs are a diagonal texture

The design draws a screen's title rule and a card's top edge as flat lines,
which is what they were. Two families that share no material read as two
products, and the shade that would otherwise be spent making them look
related is the shade a monochrome terminal has already spent.

So the run that carries nothing — the rule under a header, the part of a top
edge between the title and the chips — is filled with a diagonal instead. It
is one glyph and no colour, which is the point: the alternative that gives
the same read is a gradient, and a gradient is a run of colours no token
names.

The glyph is a choice the artboards have not made, and it belongs to them as
soon as there is one to make it in. Until then this is a material picked to
be checkable in two greys rather than a disagreement about any drawing.

## The product's face is three rows of half blocks

There is no wordmark artboard. The start screen is the one surface with rows
to spare and the only place the product is looked at rather than read, so it
is where the name is drawn: three rows of the half-block set, with the mark
the working label stands an unarrived cell in for trailing off the end of it.

Two rules were decided here that an artboard would otherwise decide. The
letterforms are drawn from the drawing kit rather than from a font, because a
font is a dependency and four letters are three lines of table. And the face
is spent out of the pane's rows rather than the window's, so a short pane gets
the name in one row of the texture and a monochrome terminal gets neither —
the face states nothing the line under it does not, and what states nothing is
what a palette with two greys gives up first.

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

There is no artboard for it. The screen is the supporting screens' own shape
over backlog items, so most of it needed no decision — the header and its
rule, the two panes and the divider, the key row, the windowed list and the
counted overflow markers were all drawn already. Four things it could not
take from anywhere, and they were decided here:

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
