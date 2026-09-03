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
