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

## The agent manager kills on a capital

The design offers a lower-case key. That key is move-up on every list in the
product, including this one, and a movement key that also kills a process is
the worst kind of false offer. Cancelling and killing are the same letter in
two cases, and the pair reads as one escalation.
