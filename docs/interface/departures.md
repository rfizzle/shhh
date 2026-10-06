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

Each section opens on one line saying when it was filed and what closes it:
the artboard for a gap, a decision for a disagreement, or the binary for a
disagreement already settled in the artboard's favour. A section that has been
closed or withdrawn says so there instead, so an entry ages where it can be
seen.

<!-- BEGIN generated departure counts — written by `make docs` from each section's filed line; edit those, not this. -->

**47 open gaps · 24 open disagreements · 11 closed or withdrawn**

<!-- END generated departure counts -->

## The transcript search has no filter view

_Filed 2026-09-09 · closes by a decision_

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

_Filed 2026-08-29 · closes by a decision_

The design fixes the gutter width. Padding a short file out to that width
takes columns from the code the row exists to show, so the gutter is as wide
as the largest line number actually in the hunk.

## A deletion's gutter marker is a hyphen

_Filed 2026-08-29 · closes by a decision_

The design uses a true minus sign, which is the right character for a *count*
— and it is used for counts. In the gutter the marker is part of a unified
diff, which is a format other tools parse, so it stays a hyphen.

## The inspector rail is drawn at one width and rendered at several

_Filed 2026-09-04 · closes by the artboard_

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

_Filed 2026-09-09 · closes by a decision_

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

_Filed 2026-09-04 · closes by a decision_

The Backlog artboard draws the block as a heading and a list of items. A set
being worked has a name and a size, and both are what tell the reader whether
the rows below are the whole backlog or the eight things chosen for this week
— which is the difference between a list to scan and a list to work. The row
sits above the items, states the set's name and how many of them are done, and
is absent altogether where no set is open, so a backlog worked without one
looks exactly as the artboard draws it.

## The backlog run's row has no artboard

_Filed 2026-09-04 · closes by the artboard_

There is no artboard for a run's row. What the binary draws is the step
header's grammar — the same fold state, the same lead columns, the same faint
rule, the same right-aligned duration field — with the stages under it as a
strip and each stage's own note under that. A run that blocked ends the row
on the command that reopens its item — `/todo open` and the slug — rather
than on a key.

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

_Filed 2026-09-04 · closes by the artboard_

`tokens/colors.css` has one column. It states a hex and the 256 index that hex
stands for, for every token, chosen against a dark ground — and every one of
those choices is a choice about the ground as much as about the hue, which is
why a light terminal cannot be served by lightening them
([the rule](principles.md#a-colour-is-three-values-and-a-ground)).

The second column belongs there and is not there yet, so the light table was
chosen here instead, on the first column's own reasons: three rungs per token
and nothing derived, the same five tokens deferring to the terminal's theme,
every other hex exactly the 256 index beside it, and the two chrome greys
keeping their jobs by swapping their weight — the faint one is the one nearer
the ground, which is the lighter grey on black and the darker grey on white.

This is a gap and not a disagreement. When the column is drawn, these rows
are what it has to reconcile with, and where the two differ the artboard
wins.

## A theme is a table, and CharmTone is one of them

_Filed 2026-09-04 · closes by the artboard_

The design system describes one palette. A second table of the same jobs drawn in CharmTone is not a divergence from it — nothing about the
product's colours changes for anyone who does not ask for it — but it is a set
of colours that no artboard states, so it is written down here.

It exists because the interface is built on libraries drawn in that palette and
the pairing is worth offering, and it is the one table where the five tokens
that normally defer to the terminal's own theme do not: a named palette that
handed its green back to whatever the user's config says would not be that
palette.

## The band's light and CharmTone values were chosen in the binary

_Filed 2026-10-01 · closes by the artboard_

*A gap.* The design system draws the band a step card rests on for the dark
ground only: `#1c1c1c`, 234 at 256 colours, on its screen `#0f1117`, 233,
which is the dark theme's painted ground. It also answers the two profiles
where a band would mislead, and the binary takes that answer — at sixteen
colours and under mono the band is no ground at all and a card is its padding
rows alone, because sixteen colours has only bright-black between the ground
and the chrome grey, so a band there reads as chrome, and mono collapses every
background onto the selection ground, so a band there reads as a selected row.

The light and CharmTone tables have no band on the artboard, so they were
chosen here, each one step off its own ground the way the dark band is one
step off its screen. The light band is `#e4e4e4`, 254: the dark band stands off black by
1.23:1, and 254 is the named grey nearest that against white (1.27:1, where
255 is 1.16:1) — the same mirroring that set the light table's chrome grey,
and the same one step the light table's selection and intraline tints take
off white. The CharmTone band is Charcoal, `#3a3943`, 237: the published grey
next to Pepper, the ground that set is chosen against, rather than a grey of
the dark table's that the set does not have. Neither draws anything at sixteen
colours, for the dark band's reason.

When the artboard draws a band on either ground, where the two differ the
artboard wins.

## A card on a painted ground steps its band up

_Filed 2026-10-02 · withdrawn_

*Withdrawn.* The band no longer steps. The dark theme's ground was once
`#1c1c1c`, 234, the band's own grey, and with that ground painted the band
stepped one rung up to 235, `#262626`, so a card would not vanish into it. The
ground is now the `Rows` catalogue's screen, `#0f1117`, 233, and it is
painted by default (`/ui ground off` keeps the terminal's own), so the band is
the catalogue's `#1c1c1c`, 234, on it: at truecolour the two reach the
terminal as `48;2;28;28;28` on `48;2;15;17;23`, at 256 colours as `48;5;234`
on `48;5;233`. The ground is written into every cell that has no ground of its
own as well as set as the terminal's default background, so the band and the
screen arrive as colours of the same kind. At sixteen colours the dark ground,
like the band, has no rung, and the terminal's own stands; mono paints
neither. The light ground stays `#ffffff`, 231, and the CharmTone ground
Pepper, `#201f26`, 235, both left to the terminal until asked, under their
bands of `#e4e4e4`, 254, and Charcoal, `#3a3943`, 237.

Why the step went. A terminal measured with its own background at `#1c1c1c`
showed the card as `#292c33`, a blue-grey that is in no table. The binary
cannot have sent that colour. On that terminal, which names itself Ghostty in
`TERM`, the profile is truecolour, so the band went out as `48;2;28;28;28`,
the terminal's own background grey. It was not a sixteen-colour downsample to
bright black, because the band has no sixteen-colour rung and a terminal of
sixteen colours draws no band at all. It was not a 256 index that a theme
remapped, because a truecolour profile sends no index. And it was not the
step to 235, which only happened with the ground painted, and the ground was
off by default. What the terminal showed was its own treatment of a cell's
background next to its default one. A terminal draws the two through
different paths, and settings such as opacity, blur or a generated palette
reach one of them and not the other. The binary sent the right band, but it
sent it onto a ground it did not control, so the card was whatever that
terminal made of the difference: nothing, if the two were drawn alike, and a
grey from no table if they were not. Painting the ground in the cells sends
both halves of the pair the same way.

## Enter walks a card through three depths

_Filed 2026-10-02 · withdrawn_

*Withdrawn: a card has two depths, not three.* The `Rows` catalogue draws a
large step "after [enter]" as the card open on its calls; the `Cards`
artboard once drew a card "folded by the reader", with `▸` in the pointer
column, and the binary took both, walking enter through open, folded to its
header, and the card again. Readers expected the pattern rows had before it:
a press opens the card, the same press closes it, and the header-alone shape
is something the `low` rung draws rather than a place a press lands. So enter
on a closed card opens it onto its groups and enter on an open one closes it
to the card the rung draws; a click on the header does the same, and the
`▸` fold mark is gone from a step's card. The heading keeps its name for the
citations that point here.

An open card's stops are its group lines and the rows of its groups of one
call. A run of calls nothing titled is kept on its first call, so once it is
open the stop there is the first group's line, or that call's row, and enter
acts on it: `-` folds that group and then closes the card, and a click on the
card's own header always means the card.

## A run nothing titled, and a refused call, are cards

_Filed 2026-10-02 · closes by the artboard_

*A gap.* The catalogue draws a card titled by the model's prose and a card
whose body is a reading; it does not draw a run of calls the model said
nothing over and no reading followed. The binary draws that run as a card
with no body, the footer under the header, and the step outline does not
number it, since it was never a step anyone named. A reading that lands
straight after such a run is taken into its card as the body, its verdict on
the header.

A refused call is a card of its own where nothing titled it, as the
catalogue's refused cards are. Inside a step the model titled, a refusal does
not split the step: it is the card's footer, the call as it was asked for and
who refused it, because the header counts only what ran.

## The strip starts at eight calls

_Filed 2026-10-02 · closes by the artboard_

*A gap.* The catalogue ends a large step's footer in the strip — one glyph per
call — and draws a step of seven calls without one and a step of twenty-two
with one, without saying where large begins. The binary draws it from eight:
past that, the counts on the header stop being enough to hold the order the
calls came in.

## The open card's strip starts at two calls, and a group's line at two

_Filed 2026-10-02 · closes by the artboard_

*A gap.* The catalogue draws the open strip under a step of twenty-two calls
and says a group line appears only from two calls; it does not say where the
open strip begins. The footer's strip starts at eight, where the header's
counts stop holding the order; the open card's starts at two, because there
it is not a summary but the way to one call, and two calls in a card are
already two places to go. A group of one call draws that call's row with no
line over it, so its row is the stop the group's line would have been.

## The strip's cursor starts on its last call and lets go on a move

_Filed 2026-10-02 · closes by the artboard_

*A gap.* The catalogue says the cursor starts on the last glyph and the
arrows move it a call at a time. The binary puts the cursor on the strip with
the first arrow pressed on an open card, at its last call, and moves it from
the second; a key that moves the reading cursor takes it off the strip, which
stays where it was drawn. The way back on the strip closes the card to its
card rung, with the reading cursor on it.

## An open card's cursor keeps the pointer column

_Filed 2026-10-02 · closes by a decision_

*A disagreement.* The catalogue draws the open card's lit call row with its
`❯` two columns in, beside the call. The binary keeps the `❯` in the
transcript's one pointer column, where it stands on the card's header and on
every row outside a card, so the eye finds the cursor in one column whatever
it is on; the band runs to where the line's own marks begin and the highlight
starts there, as the catalogue lights it.

## A failed call in an open card keeps its mark and its body

_Filed 2026-10-02 · closes by a decision_

*A disagreement.* The catalogue draws the failed command inside an open card
as `$` in red, its error line beside the command and no body under it. The
binary draws `✗`, in the row and in the strip, as the footer's strip and every
failed row elsewhere do: red alone is lost in mono and at sixteen colours,
and the mark is what says the call broke. Its body stays open under the row,
as a failed row's does anywhere, so the explanation of a command that never
started is not one more press away; the line beside the subject is drawn only
while the body is shut.

## A call in an open card names its verb where its group does not

_Filed 2026-10-02 · closes by the artboard_

*A gap.* The catalogue's rows inside an open card name the call's subject
alone, the group's line carrying the verb. The binary does the same where the
group's verb is the call's — a command run, a file read, an edit made, a
search — and keeps the call's own verb where it says more: staging and
committing are commands, and `add` and `commit` are what tell their rows apart.

## A card's header keeps a column between its sides

_Filed 2026-10-02 · closes by a decision_

*A disagreement.* The `Cards` artboard's drop table gives, for each header,
the narrowest pane that still draws a field, measured with the receipt and
the right-hand run touching. The binary keeps at least one blank column
between them, so each field goes one column earlier than the table says; a
receipt that ran into its outcome would read as one word.

## What the turn's total says where the catalogue left it open

_Filed 2026-10-02 · closes by the artboard_

*A gap.* The Rows catalogue draws the total done and failed, and the
changed-files line under a done one. The binary decided the rest.

A turn the reader stopped draws `⊘ cancelled`, counting as a failed one
does: what it got through, how long, what it cost. A turn that broke or was
stopped and changed nothing says `no files changed` on its total, and the
`changed no files` line a command would otherwise earn under it is not drawn,
since it would say the same thing twice; where it did change files, the
changed-files line says so under the total as it does under a done one.

A turn still running draws no total at all. The frame's status states the
phase and counts the turn's clock, and the cockpit counts the calls and the
spend, so a running total under the transcript was the same facts told a
second time, with a spinner of its own beside the frame's. The frame's
status is what moves for the turn, and the transcript ends on the running
card, which says `running` where its outcome will go, and its command's last
line. The total appears once, when the turn ends, in the state it ended in.

## Only the frame's status moves

_Filed 2026-10-02 · closes by a decision_

*A disagreement, and a gap.* The catalogue's running fan-out lane reads
`◇ writer-1 docs/loop.md · ✎ 1 file writing · 41s`. The binary draws the
lane's `◇`, the task and the still word, with the costs and the clock on the
right, and no `✎ 1 file`: the session has no count of the files a child has
written until its patch lands, and a count drawn from what it was asked to
touch would be a claim about work it has not done. The word is `writing` for
a child whose role changes files and `running` for any other, in the spin
colour, and the manager's row says the same word, since the two are one
renderer.

The rest is a gap the binary closed. A plan's step in flight and the rail's
running agents draw the kit's still `▸`; nothing on a card, a lane or the
rail animates. A running step's card says `running` in the outcome slot, in
the spin colour, with its duration beside it, and its kind's own glyph held
still: the lane's word, on the card that holds the work. It is `running` and
not the `running…` a row under the transcript says, because the slot is
where the step's answer will stand, and an answer carries no ellipsis; and
an earlier call's `ok` or a write's line count waits for the step to end
rather than standing as the answer of a step still going. Where a call in
flight has more to say than that it runs — a host being waited out — the
slot says that instead. The lines the session drew under the transcript
while it waited are still notice lines, `·` and the words — `Applying changes…`, `Listing
models…`, `Running the quality gate…` — or are not drawn where something on
screen already says the same: a permission check is the frame's `deciding…`,
and a compaction is the transcript's own `· Compacting conversation…` row
directly above where the line stood.

## A failure card's header carries the class

_Filed 2026-10-02 · closes by the artboard_

*A gap.* The catalogue's error card names the class on the header's right
(`stream error`) and gives the body to what the failure means. The binary
puts the class as the provider's status and class word there, and writes the
body as one sentence per class — the fact that decides what to do next, which
a failure row used to state on its right, and what the failure cost — so the
key a rejected key needs, the wait a rate limit named and the window a long
conversation is over are each said where the catalogue says what happened.

The catalogue draws a break. A stall the session will come back from keeps
`⚠` and the accent on its card, and a request the reader stopped keeps `⊘`,
as their rows did: which of the three it is was always the reason all three
marks exist. The provider's words are up to three lines of the footer. At a
hundred and twenty columns and over, the ways out share the last of them
where they fit beside it, and take their own line where they do not.

A retry on another model than the failure says so in the same words: `↻ try
again · same prompt, now on gpt-4.1`.

## A reading's row opens to its argument

_Filed 2026-10-02 · closes by the artboard_

*A gap.* The catalogue draws a reading's row closed. Opened, the row adds the
reason behind a departure and the instruction the verdict was reached
against, at the body column under the sentence; the verdict is already on the
row's right, so it is not drawn again under it. `[ctrl+o] reading mode` is on
an `unclear` row only while the draft holds the keyboard, since it is the way
into reading mode and is not offered from inside it.

A run of calls nothing titled takes the reading after it as its body even
where a passage of prose stood above the run, because the passage did not
title the calls; the reading is a row of its own after a card a sentence
titled.

## The turn's total and the retry draw two marks the pages do not list

_Filed 2026-10-02 · closes by the artboard_

*A gap.* `∗` is the total of a turn that is done and `↻` the retry's line.
The Rows catalogue and the Cards artboard draw both, and no glyph page lists
either. Each is a mark of one line: `∗` is the done state of the one slot
whose other states are `✗` and `⊘`, dim because the line is an
account and never an alarm; `↻` stands in the prompt's column because the
retry stands where the reader's words would.

## Where the verdict, a search's count and the window go

_Filed 2026-10-02 · closes by the artboard_

*A gap.* The catalogue puts the reading's verdict on the header of a card
with no footer and at the right of the footer of one that has one, without
stating it as a rule; the binary takes it as one. Two facts the catalogue
does not place: what a live search finds behind a card stands after the
card's outcome and never drops, since a fold that hid the answer to the
reader's question would be hiding rather than folding; and that the model no
longer remembers a card's calls firsthand — `out of the window` — stands in
the footer's right-hand run, which keeps its place at every width.

## The narrow rollup counts what the wide one names

_Filed 2026-10-02 · closes by the artboard_

*A gap.* A kind with one call names what it was about in the receipt (`ran
git status --short .plan/`). Behind the header's verb, a name like that is
what makes a rollup too long to keep, and the artboard's drop order would
then give up the whole rollup for one command line. Once the directory clause
has gone, the binary counts those calls instead (`ran 1 command`) and keeps
the rest of the receipt; the call that leads keeps its name and is cut, as the
artboard draws.

## Thinking is prose and the rung bounds it

_Filed 2026-10-02 · closes by the artboard_

The think row folded what the model thought into one line counting what it
held and opened it through three depths. The catalogue draws thinking as
dimmer italic prose that never folds, and the binary follows it: the thought
is the passage that says why the next card exists, and a fold the reader had
to open to learn why the work changed direction was the burial the cards were
made to end. What bounds a forty-line thought is the density rung instead —
`low` drops it, `normal` and `high` draw it whole — and a reader who finds it
too long turns the screen down rather than opening and closing passages one
at a time.

The catalogue left open where a thought between two rounds of one step goes,
since it gives prose no place on a card's band but the body. The binary draws
it where it was thought: the card ends there, and the calls after it are a
card of their own. The old row stood inside the step and was hidden with the
calls whenever the card was closed. At `low`, where the thought is dropped,
nothing stands there to end the card, and the step stays one card.

## Thinking and the checkpoint take the body column, the answer keeps its own

_Filed 2026-10-02 · withdrawn_

*Withdrawn: every block of prose is at the body column.* The catalogue sets
every paragraph of prose at the body column, and the binary once moved only
thinking and the progress checkpoint there, leaving the answer and a mid-turn
paragraph on the content column. The answer and the paragraph are at the body
column now too, with the code blocks inside them, so nothing is left of the
departure. The heading keeps its name for the citations that point here.

## A notice's words where the catalogue drew none

_Filed 2026-10-02 · closes by the artboard_

The catalogue draws a notice as one sentence. The session's notices that sat
on the grid — a conversation reopened, a session saved, a steer, a queued
message cancelled — keep their verb first and join what they are about and
what came of it with ` · `: `· resumed master · 3 changed`. A notice that
leads with a mark of its own — a failure's `✗`, a child's verdict — keeps that
mark in the slot rather than standing behind a `·`, and a notice that wraps
ends a line on its separator rather than starting the next with one, which
would read as a notice of its own.

The catalogue's `context 71% · the oldest 12 rounds will be compacted at 80%`
has no line to become: the session says nothing at its warning threshold but
the rail's colour, and the alert threshold is a card with a decision. The
context notice it has is the trim's, `· context trimmed: 3 older tool results
elided`, and it is drawn as a notice, with no key.

The catalogue counts a compaction in rounds and puts its summary on a row
above the notice. The session counts it in turns, as every other surface
does, and opens the summary under the line the way every body opens under
what it belongs to; the line says how long the summary is, in lines, so the
fold states what it holds without a key.

## A card's footer says the tree moved in the step's own words

_Filed 2026-10-02 · closes by the artboard_

The catalogue words one case, `tree moved under me · 1 file I'd read
changed`. The binary keeps that lead and says after it everything the notice
said — a head or a branch that moved, how many paths and whose, the files the
step had read, what the ignore rules held back — since the card is the only
place the notice is drawn while it is closed. The line is a footer row of its
own, wrapped rather than cut, under any evidence the step has, so the verdict
stays where the step's evidence puts it rather than right of the note.
Open, the card draws the notice itself among its calls and the footer goes
with the rest of the footer.

## A returned picture is a stop of its own

_Filed 2026-10-02 · closes by the artboard_

The catalogue says the picture row opens like an attachment and leaves open
how a cursor reaches a row inside a card whose enter walks its depths. The
binary makes the row a reading-mode stop of its own beside the card's header:
enter on it, or a click, opens the attachment card; enter on the header still
walks the card. The card opens it with no `sent with turn` line, since nobody
sent it, and on a narrow pane the right-hand words go first and then the
picture's facts, never its name.

## A foreign colour arrives as a token and a foreign ground does not arrive

_Filed 2026-09-09 · closes by the artboard_

*A gap.* No artboard draws a detail body a program painted itself. What the
design system states is the rule — output a program painted is re-painted into
the palette on the way in
([the rule](principles.md#one-grid)) — the palette's tokens and their one job
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

_Filed 2026-09-09 · closes by the artboard_

*A gap.* No artboard draws a reply containing `*emphasis*`, and the type rule
it would be read under — italic is reserved for quoted model output — settles
the question rather than raising it: an emphasis the model wrote is the model's
own markup, so the transcript renders it italic and every other italic on the
screen belongs to the summary a compaction quotes.

The gap is closed by drawing a reply that emphasises something. Where that
artboard and this differ, the artboard wins.

## The backlog screen's layout was decided in the binary

_Filed 2026-09-04 · closes by the artboard_

*A gap, mostly closed.* The Backlog artboard now draws `/todo` as the screen
the binary draws, and it draws the first four decisions below as they are
written here: the one-row foot with `[?]` holding the rest, the row's field
order, the pointer on the arrows alone and both ends of a dependency. What it
does not draw is the rest of the first — which offers give ground where even
the foot does not fit, and the keys drawn grey while a turn runs — and the
fifth, where the two panes stop fitting. The five were decided here, before
there was an artboard:

**The foot is one row, and `[?]` holds the rest.** The picker's own key row
is one row — `[↑↓] choose · [enter] read it · [esc] close` — and a key hint is
one row of at most six. The screen has more than twenty keys, and a foot that
printed them all ran to three rows under the list while the header offered
`[?] keys` beside it: the register twice. So the foot offers the keys pressed
every time the screen is open — move, read, filter, edit, a new item and the
way out — and the filters, the tabs and the other verbs are behind `[?]`.
Where even those do not fit, whole offers give ground: the way out first,
because the header's `[q] back` states it at every width, then the new item
and the editor. The pointer's keys never go, since no other list teaches that
this one moves on the arrows alone. While a turn runs, the keys that change a
file leave the row and are drawn grey under the sentence saying why — that
block is a reading of the screen's state, not the foot's offers, so it keeps
every such key. `[q] back` is drawn once, on the header: the frame's row under
the screen says `backlog` and offers nothing, and reading an item offers
`[esc]` rather than a second `[q]`.

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

The gap is closed by the artboard drawing the foot at its narrowest, the turn
running and the panes stacked; where that artboard and this differ, the
artboard wins.

## The item draft card's layout was decided in the binary

_Filed 2026-09-04 · closes by the artboard_

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

_Filed 2026-09-04 · closes by the artboard_

There is no artboard for it, and one is owed. Most of the tab needed no
decision — the two panes, the windowed list, the header, the rule and the key
row are the backlog screen's, drawn already, and the progress meter is the
step meter with the set's own noun. Six things it could not take from
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

**The head counts the items being worked and names none of them.** A sprint
working several items at once says so above the list — `working · 3 at once`
— and stops there. Each of those items is already a row of the list, with the
stage it is at as that row's state, and the list is where a new reader looks:
it is where the pointer moves, and the pane beside it answers for the row the
pointer is on. A head that also listed each slug with its stage stated every
lane twice, one screen apart, and a sprint working one item named it twice as
well. The count stays because it is the one fact no row states: how many the
sprint is working at once.

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

When there is an artboard, these six decisions are what it has to reconcile
with, and where the two differ the artboard wins.

## The safety reading's layout was decided in the binary

_Filed 2026-09-27 · closes by the artboard_

*A gap.* No artboard draws `/safety`; it is the family's chrome over sections
that wrap rather than clip, read through a pager whose two ends name the
sections they fold ([the safety reading](surfaces.md#the-safety-reading)).
Most of it needed no decision — the header and its rule, the key row, the
`[?]` behind it and the way back to the prompt are the supporting screens'
own, drawn already. Three things it could not take from anywhere, and they
were decided here:

**A section wraps rather than clips.** Its body is lists of paths, hosts and
names, and a path cut at the pane's edge is a different path. So a line that
does not fit continues on the next row, and the screen is as tall as what it
has to say.

**The pane scrolls, and its ends say what they fold.** The diagnostic and
the metrics screen drop whole blocks until the rest fits, which would
misstate a boundary — a section folded away reads as a section that is not
there. This screen keeps every section and is read through a pager instead,
and the marker at each end names the sections whose headings it hides,
counting rows only where it hides none, so the part not showing is an answer
before the reader moves to it.

**A missing capability is a section, drawn dim.** Containment that is not
available, a checkout whose trust was withheld and no servers are each a
section that says so rather than a section left out, because a boundary
drawn without them would look wider than it is.

When there is an artboard, these three decisions are what it has to
reconcile with, and where the two differ the artboard wins.

## The alerts screen's layout was decided in the binary

_Filed 2026-09-30 · closes by the artboard_

*A gap.* No artboard draws `/alerts`, and the InspectorRail component draws
the ALERTS block's heading as a label rather than as a door to anything. The
binary draws a screen behind that heading and its marker ([the supporting
screens](surfaces.md#the-supporting-screens)): the family's chrome over a
list and a preview, the list every episode the block reads and the preview
the one under the pointer. Most of it needed no decision — the header and its
rule, the two panes and the divider, the windowed list and the key row are
the supporting screens' own, and a list row is the block's own row with the
standing word added. Three things it could not take from anywhere, and they
were decided here:

**A superseded episode is `✓`.** The block never draws one as a row, so it
has no mark for one; the list takes the close row's pass mark, because what
answered it was a clean run or a passing suite, and the word `superseded`
beside it says the same thing.

**The account is four labelled lines.** `last run`, `runs`, `broke` and
`answered`, in a label column, with `not yet` in the failure tone for one
still standing — the rail row's three facts spelled out and the one it has
no room for.

**`[enter]` opens the runs in place.** Each run is two rows under the
account — its mark, turn, ending and time with the evidence id at the far
end, and the command line under it. An id that will not fit beside its run
takes a row of its own. With them out the pointer walks the runs, the `❯` in
the columns the other runs leave blank, and moving past either end puts them
away, since they are one episode's; `[enter]` on a run is then the run's
kept output, so there is no key that only hides them.

When there is an artboard, these three decisions are what it has to
reconcile with, and where the two differ the artboard wins.

## The spend screen's layout was decided in the binary

_Filed 2026-09-30 · closes by the artboard_

*A gap.* No artboard draws `/stats` as a screen, and the InspectorRail
component draws the SPEND block's heading as a label rather than as a door to
anything. The binary draws a screen behind that heading and its marker ([the
supporting screens](surfaces.md#the-supporting-screens)): the family's chrome
over a list and a preview. The chrome, the panes, the windowed list and the
key row are the supporting screens' own, a model row is the block's own row,
and a turn row is the turns screen's. Three things it could not take from
anywhere, and they were decided here:

**The list is headed by the total and cut under three group rails.**
`session total` is the first row and the pointer's first stop; `by model`,
`by child` and `by turn` are rails the pointer steps over, the way the
sources ledger groups its hosts.

**The header counts the cuts and leaves the adding to the total.** `2 models
· 2 children · 3 turns` on the left and `$0.4210 spent` as the tally,
because the three cuts overlap and a sum of them would be no figure at all.

**The account is labelled lines, then the kinds.** `spent`, `model`,
`tokens` and `requests` in a label column, then `by kind of request` with one
line per kind — its cost, its tokens and its requests — and `children` after
their `◇` on a model.

When there is an artboard, these three decisions are what it has to
reconcile with, and where the two differ the artboard wins.

## The tools screen's layout was decided in the binary

_Filed 2026-09-30 · closes by the artboard_

*A gap.* No artboard draws a screen behind the TOOLS block, and the
InspectorRail component draws its heading as a label rather than as a door.
The `Servers` artboard draws `shhh mcp` — the doctor screen over servers —
and not a session's screen. The binary draws one behind the heading, its
marker and bare `/mcp` ([the supporting
screens](surfaces.md#the-supporting-screens)): the family's chrome over a
list and a preview, each row the block's own. Three things it could not take
from anywhere, and they were decided here:

**The list is every source under group rails, built-in first.** `mcp
servers`, `definitions not loaded`, `language servers`, `binaries on PATH`
and `web` are rails the pointer steps over, the way the spend screen's cuts
are; the missing optional binaries are one row rather than one each.

**The header counts the servers and what is up.** `/mcp · 3 servers · 2 of
4 up`, the second field the block heading's own ratio — the built-in toolset
and the servers, the sources the block draws — so the two never state
different figures; a session with no servers has neither field.

**The account is labelled lines, then the tools, then the fix.** `state`,
`reaches` and `costs` in a label column and wrapped under themselves, then
the count of tools with their names wrapped, then `what would move it` with
the listing's own lines; `[a]` rides the key row only on a row that carries
the checkout's answer, and its confirm takes the foot row as the doctor's
does.

When there is an artboard, these three decisions are what it has to
reconcile with, and where the two differ the artboard wins.

## The explanation's screen wears the full view's title, not a rail label

_Filed 2026-09-08 · closes by a decision_

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
artboard settles and the binary keeps is the footer: the model, what asking
took, and that nothing here reached the model waiting on the answer.

The paragraph's tone follows the same reason. The artboard draws it in dim;
the binary draws it in dimmer, which is the body of that viewer for all three
of its uses. A paragraph a rung fainter than the dry run's output on the same
screen would be the one difference between them, and dim is the faintest
rung the ladder has — the tone of chrome, where this paragraph is the thing the
screen was opened to read.

## The grant list's narrow row is drawn only where there is one

_Filed 2026-09-08 · closes by the artboard_

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

_Filed 2026-09-08 · closes by the artboard_

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

_Filed 2026-09-09 · closes by a decision_

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

_Filed 2026-09-09 · closes by a decision_

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

_Filed 2026-09-09 · closes by a decision_

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

## The frame's border is chrome, and the mode segment carries the mode

_Filed 2026-09-27 · closed_

*Closed.* The `Frame` and `Main` artboards, and every other artboard that
draws the input frame, now draw its border in chrome in every mode; the mode
segment on the rail keeps its tone, which is where the binary says the mode.

## A read's glyph is chrome

_Filed 2026-09-27 · closes by a decision_

*A disagreement.* The `Main`, `Approvals` and `Agents` artboards, like every
artboard that draws a read's row but `WorkList`, draw a read's `⚙` in the
accent, and the state-colour guideline gives the accent to tool glyphs alike. The binary draws `⚙` dim wherever it marks a
read — the activity row, a folded run of reads, and the act row of a card
asking about one — while `$`, `✎` and `⇄` keep the accent beside the rail
they carry, and an offer that uses `⚙` to mean "start something" keeps it
too, since an offer is a place to decide.

A read is chrome ([weight tracks risk](principles.md#weight-tracks-risk)).
The accent on every read's glyph put it on a screen of twelve reads twelve
times, which taught the eye to skip the one colour that is meant to say
something is changing or waiting on the reader. The verb beside the glyph
still says which read it was, so nothing is lost but the colour, and the
card's own border is what carries a decision about a read. The departure is
closed by the artboards drawing a read's glyph dim and the guideline
giving the accent to the rail, a card's weight and the context meter.

## A row with no kind of its own carries its outcome in the glyph column

_Filed 2026-09-09 · closes by the artboard_

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

## The rewind picker's frame is a card's

_Filed 2026-09-13 · closes by a decision_

*A disagreement.* The `Rewind` artboard's timeline is drawn otherwise in three
places, each decided by a guideline or by what the binary can see.

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

## A fan-out lane keeps its kind glyph, and a manager row does not

_Filed 2026-09-13 · closes by the artboard_

*A gap.* The rule in the heading is not itself a departure. The states
guideline names a fan-out lane among the things its overriding rule is not
about, the `Agents` artboard draws a lane keeping `◇` and a manager row giving
its lead column to `⚠`, and the binary draws both the way the artboard does.
Why each surface takes the reading it does is stated with the manager
([the agent manager](surfaces.md#the-agent-manager)).

What no artboard draws is two of a lane's states. A failed lane is `◇` in del
with `✗ failed` in the outcome field, and a queued or idle one is `◇` in grey
with its word. Both are extended from the lane's rule rather than the row's: a
lane that changed shape on the one state that went wrong would be the reader
learning a second grammar for the case they are least able to spend attention
on. The gap is closed by drawing a lane in those two states, and where that
artboard and this differ, the artboard wins.

## The current one is marked, and four other marks the pages do not list

_Filed 2026-09-13 · closes by the artboard_

*A gap.* Five marks the binary draws are on no guideline page. Three of them
are on artboards; two had nowhere to come from.

`●` is the current one of a run — the step the drafter's rail is standing on,
the agent whose surface is showing in the manager, the decision at the head of
the approval queue. The `Drafter` and `Agents` artboards both draw it and no
glyph page lists it. It is not a state: `✓ brief   ● questions   · draft` says
where you are in something, which is a different question from how any of it
turned out, and answering it with a state mark would be the rail claiming an
outcome for a step nobody has finished. `○` is its pair, and only the queue
strip draws it, as the `Approvals` and `Attention` artboards do — a run of dots is a shape rather than a list of rows, and a
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

_Filed 2026-09-13 · closes by the artboard_

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

_Filed 2026-09-13 · closes by the artboard_

*A gap.* The `Paste` artboard draws the staged strip's chips with `≡` and
`▣`, as the binary does, and no glyph page lists either there: the kit has no
mark for *this is a picture*. The tool-kind glyphs answer a different question —
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

Once sent, they do meet: the tray under a message is read in the transcript,
beside the summary row that wears `≡`. So a tray row marks text `¶` instead —
the pilcrow, a mark for *written lines* that no row in the transcript uses —
as the `Tray` artboard draws it, and keeps `▣` and `▤` as the chips have them.

Colour reinforces nothing here. The chips are body text and the mark carries
the whole distinction, which is what makes the strip read the same in mono
([invariant 1](principles.md#colour-never-carries-meaning-alone)).

## A sent attachment's tray keeps the sentence's handle and the session's facts

_Filed 2026-10-01 · closes by a decision_

*A disagreement.* The `Tray` artboard draws a PDF's row as `⟨▤ Doc#1⟩`. The
binary spells it `⟨▤ File#1⟩`, because the rule the artboard states is that
the row spells the handle the way the sentence spells it, and the sentence,
the strip and `/paste show` all call a PDF `File#1`; a row with a word of its
own would be a fourth name for one file.

The artboard draws a picture's source phrase on every row. The binary knows
where a picture came from only in the session that staged it: the store keeps
the bytes and the name, never the folder, so a reopened session's rows leave
the phrase out — the same way a row too narrow for it does — rather than
guessing it.

The artboard puts reading mode's cursor on the message as well as on each
row. The binary stops on the rows that open something and not on the message,
which has nothing for enter to open; a message carrying only a PDF is
therefore no stop at all, as it was before the tray.

On the dark table the tray's band is the card's, `#1c1c1c` on the painted
`#0f1117`; in mono and in sixteen colours it is its rows alone.

## Two surfaces draw with blocks rather than in them

_Filed 2026-09-13 · closes by the artboard_

*A gap.* The drawing kit's eight sparkline blocks are cells of a bar. Two
surfaces use block characters as ink instead, for pictures rather than
measurements, and the glyph pages list the blocks only as a bar's cells.

The start screen's wordmark is drawn in `▀ ▄ █` — the letters are the blocks,
three rows tall, and mono drops the whole thing rather than drawing it in one
grey, which is what the `brand-wordmark` guideline draws. The image rasteriser
is the other: a picture arrives as half-block cells (`▀`, with the lower half
as the cell's background), which the `Paste` artboard draws, and a terminal
that will not take colour gets `░ ▒ ▓ █` as a four-step ramp, which is the
only way left to say *this pixel is darker than that one*.

Neither is a glyph in the sense the kit means. Nothing here is a mark a reader
learns and then recognises somewhere else; they are pixels, and the test that
keeps the set closed lists them so that the next block character to arrive has
to say which of the two it is.

## The summary's fourth verdict shares the third's mark

_Filed 2026-09-13 · closes by the artboard_

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
is not a warning, it is news, so it takes the reading weight, and the two `▸`
verdicts differ in the weight of their words as well as in the words. Their
mark is chrome in both: a verdict is settled, and the spinner's colour means a
thing in motion and nothing else.

## The drafter's rail marks a step nothing was asked at

_Filed 2026-09-13 · closes by the artboard_

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

_Filed 2026-09-23 · closes by the artboard_

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

## An unattended run's activity lines lead with a chevron

_Filed 2026-09-24 · closes by the artboard_

*A gap.* No artboard draws what an unattended run writes to stderr, and no
guideline page lists `»`. The binary leads every line of a run's activity
there with it — a call being made, `» read_file internal/cli/print.go`; a
notice the loop delivered, a steer, a compaction, a moved tree, a retry; the
on-close suite's verdict; and a setting the run is working under or could not
honour, `» hooks: …`, `» delegation: …` — so a person reading the terminal can
tell the run's own account of itself from the answer written to stdout
beside it, and a script can take one or the other with a single pattern.

The kit's marks are each a state or a kind of act, and this is neither: the
lines under it are a call, a notice, a verdict and a setting, and one prefix
has to lead all four. `▸` was the alternative and is the kit's *running* — true
of the call line and false of the rest, and a reader who learned it in the
transcript would read every notice as something still going on. A per-kind
mark (`⚙` for a read, `✎` for a write) would need the stderr line to say what
the transcript row says, which is a second rendering of the row for a surface
with no grid to put it in. So the chevron is added once, with the one meaning
every site already gives it — *this line is the run speaking, not the
answer* — and the failure that answers a call hangs from its line with `↳`,
as [above](#a-line-that-answers-the-one-above-hangs-from-it).

## A profile in the older shape is marked with a diamond

_Filed 2026-10-01 · closes by the artboard_

*A gap in the guideline pages.* The `Drafter` artboard's section on moving a
profile into sections draws `◆ older shape` after where a role's file lives,
and again at the head of the drafter opened on such a file; no guideline page
lists `◆`. It marks a file written before the prompt's five sections: nothing
is wrong with it and nothing is waiting on it — it loads and runs as it always
did — but there is something to do about it, and one key does it.

The kit's marks each say something this is not. `⚠` is a stall or a gap that
needs you now, and on a role that runs as written it would read as a profile
that is broken. `✗` is a failure and `⊘` a stop. `○` is the empty half of a
choice. So the diamond is added once, with the one meaning both sites give
it, always beside the words `older shape` so colour and the glyph never carry
it alone ([an older profile is moved into sections, not
rewritten](../capabilities/subagents.md#an-older-profile-is-moved-into-sections-not-rewritten)).

## The start screen's trust row has no artboard

_Filed 2026-09-23 · closes by the artboard_

*A gap.* The start screen's artboard draws a checkout shhh has never seen and
no trust row at all. The binary draws one in two states, as one of the
labelled notes under the facts and last among them: `withheld`, with the
kinds the checkout declared and `shhh trust loads them`, where nobody has
answered for it; and the kinds that changed, with `changed since you trusted
it · shhh trust off withdraws it`, in the one session after a trusted
checkout's declared files moved
([approvals-and-safety.md](../capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs)).

It is written in the notes' own voice — lower case, the value in body and the
rest dim, joined with ` · `, no full stop — and a trusted, unchanged checkout
draws nothing, so the screen the artboard draws is the one most sessions see.

## A picture is drawn in its own colours

_Filed 2026-09-23 · closes by a decision_

*A disagreement.* No surface may reach for a colour outside the palette
([the rule](principles.md#a-colour-is-three-values-and-a-ground)), and output
that arrives painted is re-painted into the palette on the way in
([one grid](principles.md#one-grid)). The staged attachment's picture is the
one thing on screen drawn in colours none of the palette's tokens name: each half-block
cell takes the two colours the rasteriser read out of the image, converted to
the rung the terminal can carry.

The reason is what the surface is for. It answers which of two screenshots is
the one with the stack trace in it
([a staged attachment](surfaces.md#a-staged-attachment)), and a photograph
folded onto the palette's tokens stops being recognisable as the one somebody took.
A program's colours are folded because they are markup about words, and the
palette is what keeps the interface's words meaning one thing each. A
picture's colours are the picture — content, like the text of a message — and
they say nothing the interface has a word for.

What the rule protects is kept everywhere around it. The card holding the
picture — its frame, the name and the size — is drawn in tokens. A mono
palette draws the picture in no colour at all, and so does a terminal below
sixteen colours: both get the drawing kit's density ramp
([the blocks](#two-surfaces-draw-with-blocks-rather-than-in-them)).

## A turn with no round limit still draws a bound

_Filed 2026-09-23 · closes by the artboard_

*A gap.* The `Frame` artboard draws the round segment with its bound —
`round 7 of 25` — and nothing draws a
turn that has none: a sub-agent, a session started with the limit off, or a
turn the reader told to run on at the checkpoint. The binary keeps the
counter's shape and puts `∞` where the bound would be, `round 7 of ∞`.

Each other way of saying it says something else. `round 7` alone reads as a
bound nobody has stated, which is not the same as a bound that does not exist;
a number would be one nobody measured
([the rule](principles.md#a-stat-that-cannot-be-reported-is-left-out)); and
words cannot go on a rail whose segments are joined by the separator, where
`round 7 · no bound` reads as two facts rather than one. `∞` is typography
rather than a mark — it stands for a number, in the place a number goes — so
the glyph set does not grow for it.

The gap is closed by drawing the unbounded segment, and where that artboard
and this differ, the artboard wins.

## The round is counted on the rail and not on the close row

_Filed 2026-09-27 · closed_

*Closed.* `Changeset` and `Scroll` now draw the Done row without the round;
it is stated once, on the rail, as the binary draws it.

## The working label arrives, and a light runs along it

_Filed 2026-09-23 · closes by a decision_

*A disagreement.* The readme knows one animation: the spinner. The binary
moves the word beside it as well, in two ways. When a turn starts the label
arrives a cell at a time, each cell still to come drawn as `·` in dim, over
twelve ticks — a little under a second. While the turn lasts a three-cell
crest in bright runs along the label, which is otherwise the spinner's colour,
and rests between passes.

Both are held to what the readme's rule is for. There is still one clock and
one frame counter: the label reads the frame the spinner is on and keeps no
state of its own, so nothing starts, stops or falls out of step with the
glyph beside it. The entrance is a shape rather than a hue — the `·` is one
cell wide, so the label never reflows, and it reads in mono exactly as in
colour — and it is the half that says something a spinner cannot: that this
turn has just started, rather than that one is still going. The crest costs
the palette nothing. It is two tokens rather than a gradient, and under mono
they are the same grey, so the swept label is byte for byte the still one and
the motion is declined the way mono declines every colour it cannot carry.

What departs is the count. A reader who has learned that one thing moves on
this screen sees a second thing move, beside the first. The departure is
closed by the readme naming the label's motion beside the spinner's.

## A turn that changed no files says so

_Filed 2026-09-23 · closed_

*Closed.* The `rail-session-scope` guideline now draws `0 files this turn`
for a turn that changed nothing, as the binary does: that zero was measured.

## The AGENTS block's meter is on the detail row

_Filed 2026-09-23 · closed_

*Closed.* The `Main` artboard now draws a child's meter at the head of the
line under its name, the one slot saying how a child is moving, and the
binary draws it there too.

## The children's tally says who needs you first

_Filed 2026-09-23 · closed_

*Closed.* The `Agents` artboard now heads the manager `Agents ─ 1 needs you ·
3 running`, the ask in del and the count dim, and draws every count on a
lane and on the rail dim, as the binary does.

## A fan-out offers the manager, not the answer

_Filed 2026-09-27 · closed_

*Closed.* The `Agents` artboard now draws the line under a waiting lane as
the manager's key and no answer key, as the binary does: the routed card is
the one surface that owns an answer.

## A confirm prompt and a card title keep their capital

_Filed 2026-09-26 · closes by a decision_

*A disagreement.* The voice every notice is written in is lower case almost
everywhere, with no closing full stop and a failure named after a `✗`. Four
surfaces keep a sentence's capital instead: the backlog screen's confirms
(`Block <slug>?`, `Archive <slug>?`, `Drop <slug>? The file is deleted, not
archived.`), the saved-chat browser's `Delete …? Files on disk are
untouched.`, the config screen's `Write 2 changes to <path>?`, `Discard 2
changes?` and `Paste a value for <field>`, and the commit card's `Commit this
turn`.

The reason is what each of them is. A notice is the product reporting what
happened, and the reporting voice is the shape of a receipt. A confirm prompt
is a question put to the reader that waits for their answer, and a card title
is the heading of a decision; neither is a report, and writing either as one
would make the one line the reader has to answer look like the lines they may
skip. It is also what every other card already does — `Approve command`,
`Apply patch`, `Agents` — so a lower-case prompt on these four would be the
exception rather than the voice.

## A card row's gloss is a fact about the call, or nothing

_Filed 2026-09-27 · closes by a decision_

*A disagreement.* The `Approvals` and `Commit` artboards gloss every row of a
card's body, and every gloss they draw is a fact about the call in front of
the reader — *reads 41 packages, writes only /tmp test binaries*, *README.md
was changed by you, never staged*, *will be 2 ahead of origin/main*. The
binary's glosses on the same rows were mostly the same sentence on every card
— *the command resolved to reads only*, *no workspace file is modified*, *the
workspace profile allows network access*, *shhh never pushes; the remote is
yours* — so a read-only command's card was four sentences of which none said
anything about the command.

The binary draws a gloss where it states something the value alone does not
say about this call: a path and its size, the hosts on a list, a file changed
by you, `unknown` with what could not be read — the card stays honest about
its limits ([the approval card](surfaces.md#the-approval-card)) — `skipped`
with the `/trust` door, and the reason beside `HIGH`. A row whose sentence
would read the same on every card with that value is drawn as the value
alone, on the command card (touches, undo, network, the containment row), the
commit card (leaves, branch, hooks, push), the card a git write asks
through (stages, push, hooks), and the fetch, spawn and MCP cards (domain,
sends, receives, budget). On those last three the value is the call's own —
the host, what goes out, what comes back, the budget — and the sentence
under it is the card's, so the row keeps the value and drops the sentence;
a gloss there that names something about this call, a server's address or a
child's undo, stays. Which sentences are fixed is one table beside the rows
rather than a judgement at each site that words them. The words stay where
they are written, and a command card's full view draws every row with its
sentence. The commit card, a git write's card and the fetch, spawn and MCP
cards have no full view; what their fixed sentences said — nothing is
pushed, a failing hook changes nothing, only this session's files are
staged, a fetch sends no file contents, what comes back is counted against
the window and the child's spend against the session — is what the values
there already say, or what every card of that kind says alike. The
departure is closed by the artboards
agreeing that a gloss is drawn only where it is about the call — which every
gloss they draw already is but one: `Commit` still glosses `push` with *shhh
never pushes; the remote is yours*.

## A code block's heading where the artboard left it open

_Filed 2026-10-01 · closes by a decision_

*A gap, and one disagreement.* The `Blocks` artboard and the Rows catalogue
draw a heading row over every fenced block in a reply: the language word in
dim, `code` for a bare fence, no key, the row itself the click target. They
leave five things undrawn, and the binary decided them.

An indented block — four spaces in, no fence — gets no heading and is not a
target. It has no language to name, and the copy by number never counted it,
so a heading over it would offer a block no key can reach.

A fence that never closes is not headed and is not counted, in a stream or
in a reply cut off before its closing line. The heading lands with the line
that closes the fence. The copy by number counts exactly the blocks that are
headed, read by the parser that draws them, so the n-th heading is always
block n — which also means a block inside a quote, drawn and headed by the
artboard, is one the copy reaches.

A message the reader sent heads its blocks the same way, since the renderer
draws a block one way wherever it is, but its headings are not targets: no
key copies a block from a sent message, and a target only the pointer reaches
is half the readers'. A backlog item's sections are drawn by the same
renderer, so their blocks are headed too.

There is no hover. The artboard has the hint bar name the act under the
pointer; the terminal reports motion only while a button is down, so the bar
that names it is reading mode's, which offers the block copy on the reply
the click has just put the cursor on.

The disagreement: the artboard draws the mono heading in the mono dim. Mono
markdown carries no escapes at all, so the heading there is the word in the
one grey the rest of the reply is in; the word is what carries it, which is
the artboard's own rule for mono.

## A card the reader gave back at low keeps that shape

_Filed 2026-10-02 · withdrawn_

*Withdrawn: at `low` a card opens like any card.* The `Cards` artboard draws
the ladder at `low`, `normal` and `high` and says a card the reader opened
stays as they left it at any rung. The binary once gave a card back at `low`
as header, body and footer, a shape between the rung's and the open card.
It no longer does: at `low` a closed card is its header alone on one band
row, with the pointer column, the rail column and the glyph before its words
so the text is not set against the band's edge, and enter or a click on it
opens it onto its groups as at any rung; closing it gives back the one row.
An open made at `low` is kept when the rung moves, and a card closed at
`high` is the padded card there and the one row at `low`. No rung draws a
folded card. The heading keeps its name for the citations that point here.

## Only a card's header answers a click

_Filed 2026-10-02 · closes by the artboard_

*A gap.* The artboards draw no pointer on a card. The binary makes the
header the card's one control and every other line of it text: a click on
the padding, the sentence, the evidence or a running command's tail does
nothing, and neither does one on the strip's words or its count. A sentence
is what a reader drags across to copy, and a press that opened or closed the
card under it would be a gamble on where the drag began; the header names
one step and enter already acts on it, so it passes the rule every target
passes. A click on the header opens a closed card and closes an open one.
Inside an open card a call's row is a target of its own: a click there opens
that call's view, the one enter on the strip opens, rather than opening the
row in place under its group, because the group already says what the call
did and what is left to open is the call itself; esc gives the card back.

## The key list names a card's acts beside the register

_Filed 2026-10-02 · closes by the artboard_

*A gap.* The `Cards` artboard puts the strip's keys on the hint bar and draws
no key list over a card. Reading mode's `?` with the cursor on a card, a
group's line or the strip lists what the mode's keys do there — `[enter]
open it` or `close it` on a card, `fold it` or `unfold it` on a group's
line, `[←→] along the strip`, `[enter] open that tool` — in the words the bar uses, railed as the row's own offers are,
under the mode's register; the register's own words for enter and the arrows
stay the general ones every row shares. On the strip `?` keeps the strip's
cursor, so closing the list comes back to the call it was on.

## A fan-out lane sets its name in a slot

_Filed 2026-10-02 · closes by a decision_

*A disagreement, with the manager.* The `Rows` artboard draws a fan-out's
children as a card's footer rows, the name in a ten-column slot and what the
child is doing after it, and the binary draws them so. The manager and the
rail's map keep the separator between a name and its task, because a list
of rows that is the whole surface has no card's columns to line up with. A
name longer than the slot is never cut: it takes what it needs and one more
column, and the facts after it start there, because two children of one
profile cut to the slot are one name.

## A fan-out child's row answers the click its lane did

_Filed 2026-10-02 · closes by the artboard_

*A gap.* The artboard draws no pointer on a fan-out's card. Its header opens
it and closes it, as every card's header does. A child's row is the lane, and
it does what a click on the block's header did before the block was a card:
it opens the children's reports and closes them again, the same two depths
the header and enter walk. A line of an open report is the report, and a
click there opens the whole of it full screen where the bound held some back.
The padding and the sentence are text, and a click there does nothing. The
plan's card has a header that opens it where it has steps past its ceiling,
and rows that are text: a plan's step is not a thing to open, and enter has
no act on one.

## A fan-out's rows keep their words and their costs

_Filed 2026-10-02 · closes by a decision_

*A disagreement.* The artboard draws a returned child's state as its bare
mark, `✓ · 1m 12s`, a running one as the role's own verb, `⠋ writing`, and
leaves out what each child cost. The binary keeps the state in words beside
its mark — `✓ done`, `✗ failed`, `⠹ working`, `⏸ held` — since a glyph never
carries a state alone, and keeps the calls and the spend after it, giving
them up first as the pane narrows, as the manager's row does. The lane's
colour is its state's tone, the one the lane always wore: the palette has no
colour per child, and the kit's lanes in theirs would be colour carrying a
name. The line that said how many of the others keep running is gone; the
header says they work in parallel, and each row says how it stands.

## A fan-out's card and a plan's card are drawn whole at every rung

_Filed 2026-10-02 · withdrawn_

*Withdrawn: the two cards have the step card's two depths.* The binary once
drew both whole at every rung and let a click on the header fold either to
that header with `▸` in the pointer column. Readers expected the three card
kinds to behave as one, so neither folds any more. Closed is the padded card
at `normal` and `high` and the header alone on one inset band row at `low`;
open draws what the card has more of — the fan-out's children's reports, the
plan's steps past its ceiling — and at `low` everything under the header. Enter
and a click on the header take a card from one to the other where there is
more to draw, and do nothing where there is not. `high` does not open them:
it opens a step onto its calls, and a fan-out's reports and a plan's whole
list are not calls. At `low` a child waiting on you would hide behind the
rung, so the header alone says `1 needs you` first, in del, and the chord that
reaches the manager answers it from anywhere. Enter on an open fan-out whose
report the bound cut still opens the whole of it full screen, as it did, and
`-` or a click on the header closes the card; without that the lines the
bound held back would be the mouse's alone. The plan's header keeps the plan's
own dim `▸` in its glyph column, which is the plan's mark and not a fold. The
heading keeps its name for the citations that point here.

## A plan's card says what can be put back on the right

_Filed 2026-10-02 · closes by a decision_

*A disagreement.* The artboard draws `reversible` at the end of the plan's
receipt and the count of writes on the right. The binary draws both on the
right, `reversible · 3 writes`, the answer where a step's card puts its
outcome, in the tone the approval card gives it: it is the answer the plan
was approved on, and the receipt is the plan's size. The body is the plan's
own sentence, its title line, since an approved plan carries no other. Past
the ceiling the card draws its four rows around the step in flight, with `…
N earlier` above them where the window has moved down. A finished step's line
names the step by the plan's number; `writes left` counts the steps that
write which the run had not reached when it took this one, and a step that
broke ticks with `✗`.
