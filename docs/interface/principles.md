# Interface principles

Five invariants and the grammar that follows from them. A surface that breaks
an invariant is wrong even when it looks right, and these are checked before
anything else about it.

Exact measurements — column widths, colour rungs, glyph assignments, the
artboards themselves — are normative in the `shhh Design System` project in
Claude Design, not here. This page is what the rules are and why they hold.

## The five invariants

### Colour never carries meaning alone

Every state pairs its colour with a glyph or a word. A monochrome terminal, a
`NO_COLOR` environment, or a colour-blind reader loses decoration and never
loses information.

The rule is checkable rather than aspirational: cover the colours, and if two
states have become indistinguishable, the surface is wrong. It holds outside
the TUI too — help output and error blocks label themselves in words for the
same reason.

Where a surface leads with severity, it says the severity three ways at once —
the border, the title, and the word. That looks redundant and is the reason
the surface survives contact with a terminal we did not anticipate.

### Weight tracks risk

A read is chrome. A mutation carries a rail. A decision gets a card.

Never the reverse: a card for a read is exactly as wrong as a bare row for a
recursive delete. The reader learns the weight of an event from how much of
the screen it takes, and that lesson is only useful if it is never violated —
one over-weighted read teaches them to ignore the signal.

The visible form of this is a single gutter column marking every row that
changed the machine. Scrolling back through a long session, the eye follows
the rail rather than reading rows left to right, and finds the moments that
mattered.

Commands always carry it. shhh cannot know whether a command wrote something,
so it assumes it did — a build and a test are marked alike, and the
conservative reading is the one that holds. Denials carry it too, because a
denial is a decision you took, and the rail is for finding decisions.

### Esc is always the safe answer

Wherever the safe answer is not obvious, the surface says what Esc will do —
`[esc] leave review, change nothing`.

This is what makes the product safe to explore. A user who knows one key
always backs out without consequence will try things; one who has to read
carefully before every keystroke will not, and will eventually approve
something they did not read because reading everything is exhausting.

It is checkable in one sentence: Escape never abandons work. Find a screen
where a single Esc ends a running turn, throws away what was typed without
putting it back, or destroys something that cannot be got again, and the
invariant is broken there whatever the rail beside it promises. Ending a turn
belongs to a chord no reflex produces, and takes two presses of it.

### Fold, never hide

A collapsed group still counts what it swallowed — `▸ 6 reads · 2 searches`.
Nothing is dropped to save space.

The distinction is between *summarising* and *omitting*. A reader who knows
six reads happened can decide whether they matter; one who was shown nothing
does not know there was anything to decide. Density is achieved by folding,
and folding always leaves a count behind.

### A key is inert until its surface holds the keyboard

There is one keyboard and there are several things that would like it, so
every screen says which one has it. The surface holding the keyboard names
itself in a labelled rail. The surfaces that do not render their keys greyed,
beside the one key that hands the keyboard over.

Invariant 3 depends on this one: Esc can only be the safe answer if it reaches
the surface you believe you are answering. The same rule is what stops a
transcript key from firing while you are typing that letter into a sentence.

It is checkable the same way: cover the colours, and if two surfaces are on
screen and neither names the keyboard's owner *in words*, the screen is
under-specified.

Every key in the product is declared once, in a single register, so a hint and
its handler cannot disagree. A pointer is exempt — clicking a row opens it the
way Enter would, without taking the keyboard from anyone. It opens it by a
different route, because the two inputs have different things to spend: the
key has one row under its cursor and reaches a row's depths by being pressed
again, while a click names a cell and so reads which half of the row it landed
in — the row line toggles, the body under it opens whole. What a click must
never do is arrive somewhere the same click cannot leave, which is what a
pointer walking the key's cycle would do the moment the third press took the
screen. No modifier stands in for the distinction: shift-click belongs to the
terminal, which keeps it for its own selection and hands the application
nothing.

A row is a target only where the pointer names exactly one thing and that
thing already has a key. That is what makes the rail's list of changed files
and its map of sessions clickable — each row names one path, one session, and
the keyboard reaches both by name — and it is what leaves the rest of the rail
inert. A heading names a block rather than anything in it; a summary, a plan,
a backlog and a list of tool sources are readings whose rows have no act of
their own; a meter has nothing to open. Each of those does have a surface a
command opens, and that is the point: a row that took the whole screen would
be somewhere the same click could not leave.

## The grammar

What follows from the invariants, as mechanics.

### One interaction panel

Anything that needs keys renders in the bottom panel, replacing the input
while it is active. The transcript stays visible above it. The panel may take
at most 40% of the terminal, and the viewport shrinks to make room and
restores afterwards.

One panel is what makes invariant 5 tractable. With two places a decision
could appear, "which surface has the keyboard" becomes a question the reader
has to answer from context rather than from position.

### The transcript is passive

Anything rendered into history is stored as data and re-rendered on resize.
Only the selected row responds to keys, and selecting is an explicit mode.

This is why the input keeps every letter. A transcript that answered keys
directly would take `v`, `u`, `r` and `e` away from the sentence being typed —
and the sentence is the primary thing the user is doing.

### One grid

Every line of activity — a tool call, a command, a sub-agent, a folded group,
a failure — is the same row with the same fields in the same columns: what
kind of act it was, which act, what it touched, what came of it, how long it
took.

Fixing the fields is what lets a reader scan one column instead of parsing
sentences. Three rules follow from fixing them:

- **Only the target grows.** When the row is too narrow, the target clips; the
  outcome never does, because the outcome is the reason to read the row.
- **Duration is a field, not a suffix.** Below the threshold it renders blank
  rather than as a zero, because a column of zeroes is noise. Something that
  never ran renders as a dash, which is not the same claim as zero.
- **Detail bodies indent; they do not re-grid.** Output that a program painted
  itself is re-painted into the palette on the way in, so nothing arrives with
  colours of its own.

A surface that needs a field the grid does not have is a card, not a row. That
is the escape hatch, and it is deliberately expensive.

### Closed vocabularies

The verbs a row can use, the outcomes it can report, and the classes a failure
can be are each a fixed list. Something that maps onto none of them is a bug
in the list, not a new entry invented at the call site.

Closed vocabularies are what make the transcript scannable at all: a reader
learns a dozen words once, rather than re-reading each row to find out what
this one decided to call itself. An unmapped value renders as itself and is a
signal the list has gone stale.

### Two denials are not one denial

"You said no" and "a rule said no" are different facts and are reported
differently — different colour, different word, same glyph. Neither is
confusable with "it failed", which is a third thing.

Collapsing them loses the only information the reader needs next: whether to
change their mind or change their configuration.

### A stat that cannot be reported is left out

A turn that called no tools reports no tool count. A session that cannot price
its tokens reports its tokens. Nothing is reported as a zero it did not
measure.

A fabricated zero is worse than a gap, because a gap is legible as a gap.
