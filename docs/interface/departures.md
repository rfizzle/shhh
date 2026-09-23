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

## The transcript search has no filter view

*A disagreement.* The `Search` artboard draws a third window: the search as a
filter, where only the matching rows are drawn and every run without one is
replaced in place by a count of it — `· 203 rows without a match, folded, in
place ·` — under a heading per turn. The binary searches the whole session,
counts what every fold is covering, says so on the fold's row and opens it
onto the match; it draws no filter.

The reason is what a filter would be a view of. The transcript is rendered
from the entries on every frame, and each of those renders is the whole pane:
a streaming turn rebuilds its tail, an approval lands a row, a fold changes
what a step shows. A filter is a second render of the same entries that has to
survive all of them — a competing content path through the pane rather than a
treatment on top of the one already there — and a second path is where the two
drift into disagreeing about what the session contains, which is the exact
failure this surface was fixed to stop making.

What the filter was for is delivered without it: every match is counted
whether or not a fold is showing it, and every match is reachable, `n` and `N`
through what is on screen and `[enter]` through the fold rows that say what
they are covering. The letter `f` is left unspent on the reading surface for
the view when it is built.

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

## The wide frame's vitals are drawn on the rule, not in a row under it

This is a disagreement. The Frame artboard gives the vitals a row of their
own inside the box, with a `├──┤` rule above it separating them from the
draft; the binary draws the same segments on that rule, so the rule and the
vitals are one row rather than two.

What the artboard is saying is where the vitals stand — between the sentence
being typed and the keys under it, inside the box rather than on its edge —
and drawing them on the rule says it in the same place. What it costs to say
it in two rows is a row of transcript at every width from the rung up: the
bottom panel takes at most 40% of the terminal
([the grammar](principles.md#the-grammar)), it is the transcript that pays for
every row the panel keeps, and this row would carry no character the rule does
not already carry.

The rule is a border and the borders on this surface carry information
([the input frame](surfaces.md#the-input-frame)) — the top one carries the
running turn's account and the bottom one the keys — so a third border
carrying the session's counters is the frame's own grammar rather than an
exception to it. The narrower rungs fold the same segments onto the bottom
border for the same reason, which the artboard draws and agrees with.

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

The strip under it carries one mark the glyph pages do not: `↺`, for a stage
that was finished in an earlier session and that this row therefore never
watched. The four the kit supplies are all this row could otherwise say, and
each of them would be a claim it cannot make — `✓` says *I saw this pass*, `·`
says *nothing has happened here*, and both are wrong about a stage whose
record came out of the store. What the reader is deciding is whether to trust
the strip, and the difference between a stage this session watched and one it
read about is the whole of that decision. The mark is dim, behind the ticks,
because a stage that is already done is not what the row is about.

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

## A foreign colour arrives as a token and a foreign ground does not arrive

*A gap.* No artboard draws a detail body a program painted itself. What the
design system states is the rule — output a program painted is re-painted into
the palette on the way in
([the rule](principles.md#one-grid)) — the fifteen tokens and their one job
each, and a type system of a foreground colour, one background tint, bold and
italic. It does not say what happens to the colours that rule has no token
for, and there are two hundred and forty of them in the 256-colour table alone,
plus every triple a truecolor terminal will take.

The binary folds them rather than dropping them. A run keeps its distinction
and the palette keeps its vocabulary: a colour with hue in it arrives as the
token whose hue is nearest, and a colour with none joins the grey ramp at the
rung nearest its lightness. The six hues and five greys it can land on are the
ones the sixteen already land on, so naming a colour the long way reaches
nothing naming it the short way cannot — a program's red is the failure red
whether it wrote the theme colour, the index or the triple.

Dropping every extended colour to Dimmer was the other option and is the worse
one. A linter that spends orange on a warning and red on an error is drawing
the distinction the row exists to carry, and dropping both leaves two lines
that differ only in a word the reader has to find. The fold can be wrong about
what a program meant; dropping is wrong about whether it meant anything.

Three things a program can ask for do not arrive at all, and each is dropped
for a reason the fold cannot answer:

- **A background, and reverse video with it.** There are three background
  tints, all three collapse onto the mono ground, and that ground means
  selection. A program painting a block of a body — or asking for reverse
  video, which paints the ground with the foreground — would be drawing the
  reading cursor somewhere the reader did not put it.
- **Italic**, which is the mark on quoted model output. A detail body is the
  one place on the screen that is nobody's words but a program's, and a
  linter emphasising a rule name would be quoting the model.
- **Blink**, which is in no part of the type system.

Bold, faint, underline and strikethrough pass through: they are emphasis
rather than colour, they cost the palette nothing, and bold is half of how
invariant 1 is met once mono has taken the hue away.

The gap is closed by drawing a foreign-coloured body. Where that artboard and
this differ, the artboard wins.

## Markdown emphasis in model prose is drawn italic

*A gap.* No artboard draws a reply containing `*emphasis*`, and the type rule
it would be read under — italic is reserved for quoted model output — settles
the question rather than raising it: an emphasis the model wrote is the model's
own markup, so the transcript renders it italic and every other italic on the
screen belongs to the summary a compaction quotes.

The gap is closed by drawing a reply that emphasises something. Where that
artboard and this differ, the artboard wins.

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

## The explanation's screen wears the full view's title, not a rail label

*A disagreement.* The artboard draws the command explanation with a rail
label reading `EXPLAIN` above the paragraph. The binary draws it on the same
full-screen viewer the dry run and the card's own facts already open on,
whose title rail is one line — the act, an em dash, and what it was about.

The reason is the surface it borrows. The whole point of this screen is that
it is the one the reader already knows how to leave: it takes the screen, it
gives it back with the decision still waiting, and nothing on it enters the
conversation. Giving one of its three uses a label the other two do not have
would say the screens are different when the promise they make is the same,
and the second half of the title says which use it is anyway. What the
artboard settles and the binary keeps is the rest of it: the paragraph in
dim, and a footer naming the model, what asking took, and that nothing here
reached the model waiting on the answer.

## The grant list's narrow row is drawn only where there is one

*A gap.* The artboard draws the `Allow without asking` list over a command:
three rows at seventy-two columns, the pattern each grant would match on the
row and its end in the short field. Nothing is drawn for the other two cards
that offer a grant, and the third row is a width rather than a length — so
the binary decided what it means on each.

An edit card offers it as *this file only*, since the directory the card
prints is wider than the file the reader read. A fetch card does not offer it
at all: a host grant is already exactly the host and never a suffix of it, so
a third row there would grant the same thing under a second name. The two
lengths are on every one of the three, because how long a grant lasts is not
a fact about what kind of thing it covers.

One more thing the artboard leaves open, because it draws one row's pattern
and not two side by side: the prefix rows print their pattern with a trailing
ellipsis and the narrow row prints its own bare. At sixty columns the
description column is the width of a short command, and a clause saying which
of the two a row is would be the first thing the terminal dropped.

## The tab strip is the question card's, and the backlog screen keeps its rail

*A gap.* The `Questions` artboard draws a tab strip over a card carrying
several questions, and the question card draws exactly that. What the artboard
does not settle is where the strip lives, and the only other tabbed surface in
the product is the backlog screen — which draws no strip at all. Its tab is a
field on the screen header's rail, and its three tabs are filters over one list
rather than parts of one answer: stepping between them changes which rows are
shown, not which part of a decision you are holding.

So the strip is a component with one caller rather than two, and the backlog
screen stays where it is. There is no second renderer to drift from it, which
is the thing having a component protects; putting a strip on the backlog header
would be a redesign of a takeover screen with its own artboard and its own
goldens, and nothing in the question card asks for one. The gap is closed by
drawing the backlog screen's own tabs, and if that artboard puts them on this
strip, this is where they will already be.

## The frame's phase is a lower-case word, and there are four of them

*A disagreement.* The `Sheet` artboard draws the frame's top rail as
`⠹ WORKING · 12.4s`; the `Frame` artboard draws the same corner as
`⠋ running go test · 3s` and `⠹ writing · 8s`. The binary draws the lower-case
form, because the readme's casing rule reserves upper case for rail block
headings and a phase is the product reporting rather than a heading. `WORKING`
is also true of every moment of every turn, so it answers nothing the rail is
being asked.

Of the two artboards' words, four are the vocabulary — `thinking…`,
`deciding…`, `acting…`, `streaming…` — and `writing` is not among them. It is
the nearest to `streaming…`, which is what a phase outside a closed
vocabulary becomes ([closed vocabularies](principles.md#closed-vocabularies)):
a fifth word would be a fifth state to read.

The artboard's `running go test` is that third phase, and the binary draws
neither half of it: the tool's name is the feed's row rather than a second
clipped copy on the rail, and `running` is that row's own outcome word, so the
rail says `acting…` instead of putting one word with two subjects a few rows
apart ([the input frame](surfaces.md#the-input-frame)).

## The attached rail states no elapsed beside the child's phase

*A disagreement.* Both the `Frame` and the `Agents` artboards put a duration
on the top rail of a frame attached to a child — `⠹ writing · 8s`, `⠹ 8s`. The
binary draws the phase alone there.

The number in that slot on every other frame is how long the running turn has
been in its phase. What a supervisor reports of a child is how long the child
has been alive, frozen when it finishes, which is a different span: a child
five minutes old that started its third turn a moment ago would read as five
minutes of one phase. Reporting the span it has under the label of the span it
has not is worse than reporting neither
([a stat that cannot be reported is left out](principles.md#a-stat-that-cannot-be-reported-is-left-out)),
and the child's lifetime is already on its lane in the agent map, where it is
what the column means. The gap is closed by the supervisor reporting a child's
turn, at which point the rail can draw what the artboard draws.

## The mode segment names the mode, not its class

*A disagreement.* The palette row offers three words for the mode — `gated,
auto or read-only` — and the artboards draw two of them: `⏵⏵ auto` in add on
the frame, the main window and the interrupt, `⏸ gated` in accent on the frame
and on an attached child's rail. The binary draws neither word. It draws the
mode's own name under the class's mark: `⏸ manual`, `⏵⏵ accept edits`,
`⏵⏵ auto`, `⏸ read-only`, `⏸ plan`
([the input frame](surfaces.md#the-input-frame)).

The three-word palette was a closed vocabulary
([closed vocabularies](principles.md#closed-vocabularies)) and this replaces
it with another one, of five, rather than opening it. What made the shorter
list wrong is that the marks already carry it: `⏵⏵` is every class that lets
work through and `⏸` every class that does not, so the word beside the mark
was the mark spelled out, and the three modes it could not tell apart had to
borrow. Manual read as `gated`, which is the class and not the mode.
Accept-edits read as `⏵⏵ auto · accept edits`, which names the mode it is not
before the mode it is. Read-only and plan read as one word for two modes that
now differ in what a turn ends on. A reader checking the segment before a
keystroke was being told the class twice and the mode never, and the two
complaints that produced this — that auto and accept-edits are called the same
thing, and that read-only and plan are not the same mode — are the same
complaint.

The marks are unchanged, and they are what keeps the count of *states* at
three: work goes through, work is asked about, nothing is written. The
departure is closed by the palette row taking the five names, at which point
the artboard and the binary say the same thing.

## A row with no kind of its own carries its outcome in the glyph column

*A gap.* The glyph guideline closes the outcome table with a rule and a list:
five state glyphs override the kind glyph, `✓` is not one of them, and `✓`
marks "the things that are not rows" — a step header, a turn close, a fan-out
lane, a check on the doctor screen. Every row the list was written against is
an act on the machine, and every one of those has a kind: a read, a command,
an edit, a call to a server, a child.

The compaction receipt is the first row that has none. It is an act the
session took on its own conversation — no tool was called, so there is no kind
of act to name — and the column that would carry the kind is empty. So the
rule is not in play: `✓` there overrides nothing, which is the whole of what
the rule forbids, and the list is an enumeration of where `✓` had appeared
rather than a bound on where it may. The row draws `✓ compact` when the window
came back and `✗ compact` when it did not, which is what the `Compaction`
artboard draws.

It carries no mutation rail in either state, including the failed one, where
every other row keeps its rail so a break can be found by scrolling. The rail
is a claim about the machine — this row wrote to it, this row broke against it
— and a compaction that could not recover the window has done neither; what it
did is in the glyph column, in del, where the scan finds it anyway.

The gap is closed by a guideline that names the case: a row whose glyph column
has no kind in it states its outcome there. Where that guideline and this
differ, the guideline wins.

## A rewind returns to before a turn, and the picker's frame is a card's

*A disagreement.* The `Rewind` artboard draws three things the binary does
otherwise, and all three come from one place: what the product's own `/rewind`
has always meant.

**Picking a turn returns to before it, not to the end of it.** The artboard's
caption reads *return to how things stood when this turn ended*, so its turn 5
keeps turns 1–5 and folds 6–7. `/rewind 5` cuts the conversation at the start
of turn 5 and keeps 1–4, which is what the numbered command has meant since it
was written and what the surfaces around it say. Every figure the artboard
draws is therefore one turn along from the binary's: the card is titled
`Rewind to before turn 5`, the turns leaving the window are 5–7, and the
frame's rail reads `at turn 4`. Changing the meaning would change the command
as well as the picker, which is a product decision rather than a wording one;
until it is taken, the two must agree, and it is the picker that was drawn
against a different one.

**The picker is a card and not a bare rail.** The artboard draws the timeline
frameless, rows under a labelled rule. Selectors are cards
([surfaces.md](surfaces.md#selectors) is normative on that), and a catalog
opens as a search card, so the picker keeps its frame and takes the artboard's
rule above it — which is the rule that names the keyboard's owner and belongs
to the session rather than to the card. The frame carries no title of its own:
the rail has already said what the surface is, and the count on the frame's
own rail says how big the catalog is, which is the other question.

**The unreachable row's `⊘` leads the row rather than the reason.** The
artboard puts the mark where the diffstat would be, after the turn's words.
The selector's rule is that an option that cannot be taken says so behind `⊘`
at the head of the row, in one glyph, once — the same shape on every list in
the product — so the mark leads and the reason follows the separator. The
guideline wins.

**The reason itself is the binary's to know.** The artboard's turn ran `rm -rf
tmp/`, and shhh cannot tell that from any other command: what a command
changed was never recorded, which the scope card says outright on the row that
would write. What the binary *can* see is a run of turns whose records were
dropped to stay inside the changeset store's limit, and a conversation that
came back from the store without records at all. Those are the two boundaries
the row states.

## The rewound turns are branched, not folded

*A disagreement.* The `Rewind` artboard's third window keeps the rewound turns
in the transcript as a `▸` group row — `turns 6–7 · rewound · 2 turns, 3
files' worth of work` — with a key that reads them and a key that reapplies
them. The binary truncates the conversation and preserves the abandoned tail
as a branch of the session, reachable with `/branches`, which is the mechanism
`/rewind` has always used and the one the transcript is rebuilt from.

So nothing is hidden — the turns are a switch away, and the notice under the
row names the branch and the command — but they are not on this transcript,
and the row says `out of the window` rather than `folded` because folded is a
claim about rows that are still here. The `[r] reapply` key has nothing behind
it yet for the same reason: reapplying is switching back to the branch, which
is a key on a surface this story did not build. Closing the gap means holding
the rewound turns as fold entries on the transcript beside the branch, which
is a change to how a rewind stores what it took back rather than to how the
row is worded.

## A fan-out lane keeps its kind glyph, and a manager row does not

*A gap the artboard fills two ways, and both are kept.* The states guideline
closes its table with a rule — five of the state marks override the kind
glyph, `✓` never does — and then says `✓` marks "the things that are not rows:
a step header, a turn close, a fan-out lane, a check on the doctor screen". So
a lane is named as one of the things the rule is not about, and the `Agents`
artboard draws it that way: `◇` stands in every lane it draws and its colour
carries the state, with the state itself in words in the outcome field —
`▰▰▰▱▱ 2/5` running, `▰▰▰▰▰ ✓ 5/5` done, `⚠ needs you` blocked.

The same artboard's manager window draws the same three children the other
way, and that is the rule rather than an inconsistency: a manager row is a
row. Its blocked child leads with `⚠` and its done child keeps `◇` with `✓
done` on the right, which is exactly "five override, `✓` does not".

Both are implemented. The difference is what the two surfaces are lists of. A
lane is a child, from the moment it is queued until it stops being one, and it
will be many acts before it is anything — so the column that says *what this
is* keeps saying it, and how the child is doing is the field that changes. A
manager row is the child as one thing you are about to act on, in a list where
`⚠` is what sorts to the top and what the key row is offering to answer; there
the state is the reason the row is in front of you, and it takes the lead
column the way it does on every other row in the product.

Two states no artboard draws are extended from the lane's rule rather than the
row's: a failed lane is `◇` in del with `✗ failed` in the outcome field, and a
queued or idle one is `◇` in grey with its word. A lane that changed shape on
the one state that went wrong would be the reader learning a second grammar
for the case they are least able to spend attention on.

## The current one is marked, and four other marks the pages do not list

*A gap.* Five marks the binary draws are on no guideline page. Two of them are
on artboards; three had nowhere to come from.

`●` is the current one of a run — the step the drafter's rail is standing on,
the agent whose surface is showing in the manager, the decision at the head of
the approval queue. The `Drafter` and `Agents` artboards both draw it and no
glyph page lists it. It is not a state: `✓ brief   ● questions   · draft` says
where you are in something, which is a different question from how any of it
turned out, and answering it with a state mark would be the rail claiming an
outcome for a step nobody has finished. `○` is its pair, and only the queue
strip draws it — a run of dots is a shape rather than a list of rows, and a
dot that is not the current one has to be a dot.

`⋮` is the drafter's profile pane counting what did not fit: `⋮ 2 more lines ·
shift+↓ read on`. The `Drafter` artboard draws it. Everywhere else a fold ends
on `…`, and the difference is the axis — `…` says a line was cut, and this
says the rows continue below.

`↵` is a line break inside a one-line preview, where a code block is being
shown on a description row. It is not a mark about the row; it is a character
standing in for one that cannot be drawn, the way `␛` stands in for an escape
in a golden fixture.

`↺` is the run strip's unwatched stage, argued beside [the run
row](#the-backlog-runs-row-has-no-artboard).

## The paste fold is written in angle quotes

*A gap.* The guideline pages carry no mark for a fold standing inside a
sentence, and the `Paste` artboard needed one: a log too big for the draft
leaves a token where it was pasted, and the token has to be legible as *this
stands for something bigger* in the middle of a line somebody is still
typing.

Nothing in the kit says that. `▸` is a folded entry and it means it at the
head of a row, in the glyph column, where the row it folds is the whole line;
wrapped around a run of words inside a sentence it would be a second meaning
for the mark the transcript's every fold already uses. `…` is what a clip
ends on — it says text was lost, and a paste token loses nothing, it stands
for something staged and counted.

So the draft adds `⟨` and `⟩`, and the artboard says why in one line: square
brackets are keys. Every offer in this product is written `[enter]`, and a
fold written the same way in the one place a reader is typing rather than
choosing would be an offer nothing accepts, three inches from a rail full of
real ones. The two notations never meet — `⟨ ⟩` never brackets a key and
`[ ]` never brackets a fold — which is the whole reason a second pair was
worth two new marks.

They come as a pair and they are drawn in one place: the token the draft, the
sentence in the transcript and the fold row under it all spell it through the
one renderer, so there is no second spelling to drift.

## The attachment chips mark what kind of file is staged

*A gap.* No artboard draws the staged strip's chips, and the kit has no mark
for *this is a picture*. The tool-kind glyphs answer a different question —
they say what the session did, and a chip is a thing the reader attached
before the session does anything at all.

So the strip adds two and reuses one. `▣` is a raster image: a frame with a
subject inside it. `▤` is a document the model reads whole — a PDF: the same
lines as text, inside the boundary that makes it one artifact. `≡` is text
bound for the prompt itself, and it is the kit's own mark for a reading,
borrowed rather than invented — the shape already means *lines of it*, and a
third new mark on a strip that has to survive at 60 columns would be three
things to learn where two and a familiar one will do. The two meanings never
meet: `≡` on a row is an act the session took, `≡` on a chip is a file waiting
above the draft, and no surface draws both at once.

Colour reinforces nothing here. The chips are body text and the mark carries
the whole distinction, which is what makes the strip read the same in mono
([invariant 1](principles.md#colour-never-carries-meaning-alone)).

## Two surfaces draw with blocks rather than in them

*A gap.* The drawing kit's eight sparkline blocks are cells of a bar. Two
surfaces use block characters as ink instead, for pictures rather than
measurements, and neither has an artboard.

The start screen's wordmark is drawn in `▀ ▄ █` — the letters are the blocks,
five rows tall, and mono drops the whole thing rather than drawing it in one
grey. The image rasteriser is the other: a picture arrives as half-block cells
(`▀`, with the lower half as the cell's background), and a terminal that will
not take colour gets `░ ▒ ▓ █` as a four-step ramp, which is the only way left
to say *this pixel is darker than that one*.

Neither is a glyph in the sense the kit means. Nothing here is a mark a reader
learns and then recognises somewhere else; they are pixels, and the test that
keeps the set closed lists them so that the next block character to arrive has
to say which of the two it is.

## The summary's fourth verdict shares the third's mark

*A gap.* The tools guideline says a reading of the session "carries its
verdict in the outcome field, in the marks the rail's SUMMARY block uses", and
gives one example: `⚠ off target`. It gives no mark for a run that is still on
its instruction but has found what it needs and has not started acting on it,
which is the fourth thing the reader's summariser can say.

That verdict draws `▸ has enough`. A run that has what it needs is a run going
where it was sent, so it takes the running mark and is told apart from `▸ on
target` by the word — which is the half a monochrome terminal reads anyway,
and the half that says what the difference actually is. What it is not is a
departure or a state that could not tell, and those are the only other marks
the vocabulary has. A mark of its own for it would be one a reader meets once
a session, on a rail whose whole job is to be read at a glance
([closed vocabularies](principles.md#closed-vocabularies)).

Only the departure is drawn in the accent. A run that has found what it needs
is not a warning, it is news, so it takes the reading weight and the healthy
glyph colour, and the two `▸` verdicts differ in the weight of their words as
well as in the words.

## The drafter's rail marks a step nothing was asked at

*A gap.* The `Drafter` artboard draws its rail in three states — `● brief`,
`● questions`, `● draft`, with `✓` behind and `·` ahead — and never draws a
step that was skipped. Its own caption says why: "the rail keeps the step it
was started from: this turn may still come back with questions."

The binary has the case the artboard does not. A brief that was already a
specification gets a draft and no questions at all, and the rail then has a
middle step that is behind you and that never happened. It draws `⊘`, which is
the states page's own mark: it reads "denied / skipped", and the page has it
leading an option a picker cannot take here — skipped, with the reason on the
row in words. So this is the kit's mark used for the meaning the kit gives it,
not a fourth one. `✓ questions` was the alternative and is a lie: it would be
the rail claiming an exchange that never happened.

## A line that answers the one above hangs from it

*A gap.* No guideline page lists `↳` and no artboard draws the two places it
appears. Both are a line whose whole meaning is that it answers the line
directly above it: the receipt a person's steer leaves on a child's lane,
`↳ steer delivered · round 3`, under the message it acknowledges; and an
unattended run's failure line on stderr, `↳ read_file: no such file`, under
the call it is the result of.

The kit has nothing that says *this belongs to the row above*. `✓` was the
alternative for the receipt and claims too much: a delivered steer has reached
the child's conversation, not been acted on, and a done mark on it would be
the lane reporting an outcome the next rounds have not produced yet. `→` is
typography for a link and a value becoming another, and `▸` is a running or
folded entry — each would give an existing mark a second meaning, which is the
cost the closed set exists to refuse. So the hook is added once, with the one
meaning both sites already give it, and a third line that hangs from its
parent takes this mark rather than inventing another.
