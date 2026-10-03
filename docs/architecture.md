# Architecture

The structural decisions, and what each one bought. Where these shapes live in
the tree is [`AGENTS.md`](../AGENTS.md) — this page is the reasoning, that page
is the map.

## One agent, several front-ends

The agentic loop is a **passive state machine**. It does not own a screen, a
goroutine, or a main loop; something else advances it one step at a time and
decides what to do with what comes back.

That inversion is what lets the interactive session and the scripted runner be
the same agent rather than two implementations that drift. A child agent
spawned by a parent is the same object again, driven synchronously. Every
behaviour that matters — the approval queue, the round accounting, the
repeat-call detection — is therefore written once and observed identically
everywhere, including in tests, which drive it the same way a front-end does.

The cost is that the loop cannot decide anything for itself. It cannot show a
prompt, cannot block on an answer, cannot retry on a timer. Each of those
becomes a state the caller has to handle. That is the trade, and it is worth
it: the alternative is an agent that behaves differently depending on who is
watching.

What that buys, eventually, is a front-end in another process. A loop that is
advanced one step at a time by whatever is holding it does not care whether
that thing is on this side of a socket, so putting a protocol in front of it
is wiring rather than a redesign — and the shape was already there, which is
why the surface could be added without moving the screen behind it. The rule
that makes it safe to have is that the protocol contributes exactly two
things, where the events go and who is asked, and contributes nothing to what
a session may do. Everything else is assembled the way the unattended run
assembles it, because a second assembly is a second set of answers to every
question about containment, trust and refusals, and the two would agree only
on the day the second was written.

What a call did — the kind of act, its verb, what it was about and how it
came out — is one vocabulary, read from the call and its result once, below
every front-end, so that each draws the same account of a call and none keeps
a list of tools of its own.

See [`capabilities/headless.md`](capabilities/headless.md).

## Tiers, not permissions

Tools are separated by what they can do to your machine, and the separation is
structural rather than a flag checked at call time:

- **Read-only** tools change nothing and run without asking.
- **Execute** runs a command, and needs an answer — from the user, or from a
  policy that has already decided.
- **Mutating** tools write to disk, and need an answer unless the session's
  mode has granted it in advance.

The dispatch paths for these are *different functions*, not one function with
a branch. A read-only dispatcher that has no case for a mutating tool cannot
be talked into running one — not by a bug, not by a malformed tool name, not
by a model that has learned to ask nicely. A boolean can be wrong; a function
that does not contain the code cannot be.

This is the invariant most worth protecting in the codebase, and the easiest
to erode by accident, because merging the two paths always looks like a
simplification.

See [`capabilities/approvals-and-safety.md`](capabilities/approvals-and-safety.md).

## Providers are interchangeable and are not equivalent

Every backend exposes one operation — stream a completion — and registers
itself under a name the rest of the system resolves against. Nothing outside
that layer knows which vendor is answering.

What the interface deliberately does *not* do is pretend the vendors agree.
Dialects differ in ways that are not cosmetic: how a tool result is addressed
back to the call it answers, whether reasoning state has to be handed back
untouched, what a failure looks like on the wire. Those differences are
absorbed inside each implementation, and where one has a rule that reads like
a quirk, it is load-bearing — the ones that have bitten us are explained, with
the symptom, in a comment at the code that has them, because the symptom (the
model silently calls the same tool again) does not point at the cause.

A failure is classified into a closed set before it reaches any surface. The
classes belong to the provider layer; what to *offer* the user about each one
belongs to the interface. Splitting it there is why a new provider inherits
every recovery path already built.

See [`capabilities/providers.md`](capabilities/providers.md).

## Configuration resolves in one direction

Everything configurable resolves the same way, most specific first: an
explicit flag, then the environment, then the config file, then a default.
There is no setting that reverses this and none that can only be set in one
place.

The reason is debuggability. A user who can predict where a value came from
can fix it; one who has to know which of four mechanisms wins for *this*
particular setting cannot. Uniformity is worth more here than the flexibility
of special-casing.

See [`capabilities/configuration.md`](capabilities/configuration.md).

## State is local, single-connection, and boring

Sessions, history, memories and metrics live in one embedded SQLite file with
exactly one connection open to it. Concurrency is handled by the journal mode
and a busy timeout rather than by a pool.

Nothing here needs a pool — the workload is one interactive process — and a
second connection to the same file buys nothing while introducing a class of
lock contention that is miserable to reproduce. The constraint is deliberate
and should be left alone.

The binary is pure Go with no cgo, which is what makes a single static
cross-compiled binary possible. That rules out the conventional SQLite
bindings, and that trade has already been made.

## The screen is a rectangle, and so is everything in it

Terminal layout resolves once, into rectangles, and each renderer is handed
the rectangle it may draw into. A renderer that needs to know how wide it is
reads its rectangle; it does not subtract a width from another width.

Before this, geometry was a scatter of arithmetic across every surface, and
every new pane meant finding all of it. The failure mode of the old approach
is a surface that is correct at 100 columns and one character wrong at 132 —
which is exactly the bug nobody catches, because nobody has that terminal.

Clipping is a property of the rectangle rather than a thing each renderer
remembers to do, so a block that is too big is cut off instead of corrupting
the frame around it.

## Colour is resolved once, at the top

The terminal's actual capability is decided in one place, and every style is
built against that decision. Changing the palette or the profile rebuilds
every derived style rather than patching some of them.

The bug this prevents is the one where a surface was constructed before the
profile was known, keeps its original colours, and looks subtly wrong on a
16-colour terminal that nobody on the team is using.

See [`interface/principles.md`](interface/principles.md).

## Only one place speaks to the terminal

Asking a terminal what it can do means writing escape sequences and reading
replies. Exactly one component does that; everything else holds the answer as
a value.

Terminal capability probing is the kind of code that spreads — one more
question asked from one more place, each with its own timeout and its own idea
of what a non-answer means — and the result is a program that hangs on one
emulator in a way no one can reproduce. Keeping the wire in one component
means there is one thing to reason about and one thing to fix.

## A list screen is one shape with its own rows

Most take-over screens are a list on the left, the item under the pointer
laid out on the right, and the shared chrome around both. Each of them had
written the list half for itself — the pointer, the walk over the rows, the
row under the pointer, the window that draws them, the key that shows the
whole register, the pair of keys the header ends with, and the frame — and
the copies differed only in their names. Two of them filtered as well, and so
did three other screens, and each wrote the query line's handling again. A
rule moved in one copy and not the next is a screen that walks or filters
differently from its neighbour for no reason a reader could name.

So the list half is one embeddable shape, and a screen supplies its rows. It
holds:

- **the pointer**, an index into the screen's own rows — all of them, never
  only the ones showing, so it survives a filter and a host that hands the
  rows back rebuilt;
- **the selector window** the list is drawn in, which remembers where it was
  between keystrokes;
- **whether the register is open**, the state behind the header's first key;
- **the rows showing**, for a screen that filters its list or draws it in an
  order of its own: their positions in the screen's rows, in the order they
  are drawn.

What it does with them is the part that was copied: it walks the pointer on
the screen's own movement binding (over the rows showing where there are
any, and on only the half of the binding no sentence produces while the query
line is open); it answers which row is under the pointer; it draws the list
pane, or the one sentence a screen with nothing to list says instead; it
writes the header's key pair and the footer from the screen's offers; and it
draws the frame around the two panes.

The screens adopt it one at a time, in order of risk, each change proving
that every capture of every screen is byte-for-byte what it was: first the
seven that do not filter — the bill, the turns, the tools, the steps, the
readings, the sources ledger and the alerts — and then the two that do, the
saved-chat browser and the snippet browser. What a screen keeps for itself is
everything that is a fact about that screen: what its rows are and how one is
drawn, the headings it groups them under, its preview, its header's fields
and tally, which keys it offers and what they do, the sub-surfaces it opens
(a confirm, a rename row, an episode's runs, a plan's drift) and the numbers
its panes are split by.

The filter is the shape's own part rather than a screen's, and it replaces
the five that were written by hand. It owns the rows showing, the match —
the query, folded and trimmed, found inside any of the fields a screen names
for a row — and what a changed query does: the pointer goes to the first row
that survived it, because the rows under it are not the ones that were there
a moment ago. The query line's keys are its too: a clear on an empty query
closes the line, which is how the row keys come back without leaving the
screen. The screen names its fields, says what else a changed query puts
away, and words the line saying how many rows the filter hid. The history
browser takes the whole shape after the two browsers. The settings screen
takes the filter and keeps its rails, its sub-surfaces and its own query
keys, which never closed the line on an empty clear. The backlog screen
takes the rows showing and the match, and keeps on its side what only it
has: the cycles by status, priority, a field and readiness, which narrow what
the query left, and its own query keys, where the way back closes the line.

## Spend is counted at the provider

Every request shhh makes is a call to a provider: the agent's own rounds, the
permission classifier's judgements, the session summary's readings, each
sub-agent's turns. The provider is therefore the one thing a feature cannot
route around, and it is where spend is counted. Features are handed a gated
provider and know nothing about accounting.

The alternative is each feature reporting what it spent, and it fails in one
direction only. A feature added later counts nothing, and nothing breaks —
no test goes red, no screen shows an error, the session simply reports a
smaller number than the invoice will. Under-reporting is the one kind of
wrong a spend meter must not be, because it is invisible exactly when it
matters. Counting at the choke point makes the default correct: a new caller
is billed because it made a request, not because someone remembered.

Spend is attributed to whoever incurred it, down to the individual requester.
"Sub-agents cost $2.40" is not an answer to "which of them cost that", and a
fan-out that ran away with the budget is only actionable if the child can be
named. A request that arrives with no declared origin is not dropped and not
quietly filed under the agent — it is counted under an unattributed heading
that the breakdown prints, so a gap in the wiring shows up as a visible row
rather than as a total that is slightly too small.

Each request is priced against the model that answered it. The classifier and
the summary routinely run somewhere cheaper than the session, a fan-out bills
several models at once, and `/model` can change the rate mid-session — so a
single total priced against whichever model happened to be current is a
number that cannot be reconciled with anything.

What the agent's own turns cost stays a separate figure from what the session
cost, and both are shown. They answer different questions: one is what the
work in front of you is costing, the other is the bill.

## A session is assembled in one place

Every surface that runs a conversation — the terminal session, a scripted
run, a served session — stands on the same assembly: the working scope, the
toolset and everything it opened, the local store, the roles the session may
spawn, the memories it recalled, the sentence that tells the model where the
work is, the price table and the spend ledger, and the session's environment
(its provider, its model, its built prompt and its stream). One function
builds that value, and it keeps what it opened on one stack that is closed in
the reverse order it was opened, whichever surface holds it and however the
assembly ends.

The order is load-bearing, and it is the builder's rather than any caller's:

1. **The scope comes first**, because everything that runs a command — the
   gate, the sub-agents, the session's own runner — is built over it.
2. **Then whatever the git stager reads**, named before the toolset that
   holds the stager: what may be staged is what the session has written by
   the time the call is made.
3. **The toolset**, then **the surface's registrations**: the store, the
   servers, the skills, the memories, the roles.
4. **The scope sentence and the toolbox, after the last registration.** Every
   optional tool joins on a condition, so this is the first point at which
   the whole set is known, and a toolbox written earlier describes a toolset
   the model does not have.
5. **The ledger before the provider**, because the provider is handed out
   through it (see [spend](#spend-is-counted-at-the-provider)).
6. **The environment**, which builds the prompt from everything above, and
   last the models a spawn may name, which are known only once the provider
   is.

The registrations are the one step whose order is the surface's own. The
terminal session registers its roles before it opens the store and recalls
memories before skills; the unattended surfaces register skills before
memories and roles last. Both orders are what the model reads — the order of
its tools and of the paragraphs that describe them — so the builder runs
whichever order it is handed, at one point, rather than settling on one and
changing what a surface says.

The assembly ends at the environment rather than at a finished session,
because past it the surfaces differ in order and not only in values: a
scripted run reads its prompt before it builds its containment, so an empty
prompt never starts a disposable container; the terminal session builds its
classifier ahead of its containment and is the only one that offers to
install a missing tool; the hooks' notes are written in each surface's own
shape. What each keeps for itself:

- **The terminal session**: its registrations, which add the remember tool,
  the question tool, the working steps and the notebook; and everything after
  the environment — containment, hooks, the record, the session boundary and
  the screen.
- **A scripted run**: the store it opens for itself, the delegation policy it
  states on stderr, its prompt, the disposable container, the approver and
  the loop.
- **A served session**: the store it is handed, because one server serves
  several sessions from it; a read record of its own; and the release that
  closes the assembly with everything else the session opened.
- **The sub-agent supervisor**, which is built from the assembly and from
  what each surface makes after it: the record, the classifier and the hooks.
  A child's own environment stays the supervisor's, because a child is not a
  copy of its parent — its role decides its tools and its prompt, its spawn
  decides its model, and its mode is read at every request.

## The unattended surfaces share one tail

A scripted run and a served session agree about more than the assembly. Past
the environment, both go on through the same steps in the same order, and
those steps are written once and handed what differs between the two:

1. **The containment**: what runs the session's commands, the refusal every
   command gets where containment is required and the host has none, and
   what contains a hook.
2. **The prompt blocks**: what the model is told the containment is, and the
   declared tools its commands will not find. They are joined to a prompt
   already built, because the containment is resolved after the provider,
   and the provider needed the prompt.
3. **The runner's wraps**: the secret scrub on every result, then the command
   ceiling, because nobody is at a keyboard to cancel a command that will not
   finish.
4. **The hooks**, after the containment because what contains a command is
   what contains a hook; their notes, and the first seam, whose context is
   joined to the built prompt.
5. **The conversation** the session carries on, and the slot it will be left
   in, claimed here so two sessions started together settle the name before
   either writes to it.
6. **The record**, opened before anything that links to it, with the hooks
   told its identity; then **the stamp**, the gate's verdicts and the
   searches.

The order is load-bearing. A hook built before its containment runs outside
it; a prompt block written before the containment is settled describes a
runner the commands do not get; a record opened before the conversation it
names is a row with no slot behind it; and what the session opened is closed
in the reverse of that order — the record, then the slot, then whatever the
surface opened before the tail — so nothing is torn down under something
still holding it.

What a scripted run keeps for itself, around the tail:

- **Its prompt, before the tail starts.** It reads the prompt from its
  argument or stdin and refuses an empty one before any containment is built,
  so a run with nothing to do never starts a disposable container.
- **The disposable container**, which stands in for the first step: started
  for the run, torn down after the tail's own steps are closed, and the
  reason no hook runs in that run — a hook cannot follow the commands into
  the container, and running it on the host would put the person's own
  command outside the strongest containment the run has. That is said on
  stderr in place of building the hooks.
- **Saying the containment only where it offers commands**, because a
  conversation that cannot run one is not told what one would run under.
- **Its stderr**: every line a run prints is a `»` line beside its activity,
  because stdout is the answer.

What a served session keeps:

- **The store it is handed**, which it neither opens nor closes.
- **Its closers**: the tail's are added to the session's own stack and
  released with everything else it opened, when the client goes or the
  assembly fails, rather than on the way out of a function.
- **A conversation it may begin from**: a fork's copy of its parent's takes
  the place of the fresh one, and of anything asked to resume.
- **Its stderr**, the same `»` notes a scripted run writes. The event stream
  a client reads does not exist yet when the tail runs, so what the first
  seam says goes to stderr alone.

The terminal session does not take the tail. It builds its classifier before
its containment, offers to install a missing tool, and writes its notes as
rows on the screen; taking the shared order would change what it does, which
is a decision about the product and not a refactor.

## A busy screen gives each of its modes one owner

Two screens hold the keyboard through more than one mode: the profile
drafter, which is a brief, its questions, a draft revised section by section
and a migration reviewed against its file; and the backlog screen, which is a
list, a filter being typed, an item being read and a sprint being planned.
Each mode was a flag on one value, set in one method and read in another, and
the bug that shape breeds is a key routed by a flag nobody it reaches knows
about: a mode left on by the step that should have cleared it, answering keys
on a screen that no longer draws it.

So each screen is an owner over pieces. A piece holds the flags of its own
mode, draws its own rows and answers its own keys; the owner keeps the step,
the chrome every supporting screen shares, and whatever the host sets. Where
the owner has to read or clear a piece's flag on a transition, that crossing
is named here, because it is the one place the next change can break the
routing without any single piece looking wrong. Splitting draws nothing new:
every frame is the frame it was.

### The profile drafter's widgets

The drafter is a wizard over three widgets, and the step selects which one is
drawn and handed the key.

- **The brief and the questions** own what is being asked, the starting
  points and the pointer between them and the field, and the exchanges
  already answered and the count of questions.
- **The section editor** owns the draft's sections and the pointer on them,
  the note open under a section, whether that note is about the whole draft
  and whether it includes the sections the person wrote, the note as it was
  sent, and the sections' scroll and the request to bring the selected
  section into view. It answers the keys while the sections, or a note under
  one, hold the keyboard.
- **The diff and migrate viewer** owns the diff's scroll and whether the wait
  is a migration, and draws the older-shape offer at the head of the draft,
  the file's own prompt beside the sections, and the diff that stands where
  the sections stood while the card holds the keyboard.

The wizard keeps the step and the step a wait was entered from, the header,
the rail and the key register, the decision card and whether it holds the
keyboard, the selector a field block opened, the warning, the wait's label,
and every fact the host sets — the subject, whether the profile came from its
file, whether it is in the older shape, its original prompt and the diff. It
keeps the one text field too, and lends it to the brief and to the section
editor: the field carries the width the last draw gave it, and a note typed
before the next draw is laid out in that width, so two fields would draw the
same keystrokes differently.

The flags that cross:

- **Whether the card holds the keyboard** is the wizard's. The section editor
  reads it to decide whether a heading is lit and whether its key row is
  drawn, and the viewer's diff is up only while it holds.
- **Whether a note is open** is the section editor's. The wizard reads it for
  the way out the header states and for whether the register's key is a key,
  and closes it when a wait starts or a draft lands.
- **Whether the note is about the whole draft, and whether it includes the
  person's own sections**, are the section editor's. The wizard reads the
  first for the key row of the wait that follows the note, and a landed draft
  clears both.
- **Whether the selected section is to be brought into view** is the section
  editor's. The wizard sets it when a draft lands, when the host puts the
  pointer on a section, and when a selector opens.
- **Whether the wait is a migration** is the viewer's. The wizard sets it when
  a migration starts, clears it when any other wait starts or a draft lands,
  and reads it for the way out and the wait's rows.
- **Whether the register is showing** is the wizard's alone.
- **Whether the profile came from its file, and whether it is in the older
  shape**, are the host's. The wizard holds both; the section editor is
  handed the second to make the migrate key live, and the viewer to draw the
  offer.

### The backlog screen's pieces

The backlog screen keeps its list — the window of items, the pointer each
tab remembers, the tab itself and the confirm in front of a key that changes
a file — and hands the rest to four pieces.

- **The sprint and plan tabs**: the board draws its own head over the
  sprint's list, and the plan card draws itself and answers every key while
  it is up.
- **The reader** owns the item pane and the mode that gives it the whole
  surface: whether it is open, its scroll, and the last render of an item's
  body.
- **The filter and field editor** owns the query row and the cycles: the
  query, whether the row is open, the status, priority and field stops, and
  the ready toggle, with what the header says about them and the rule a row
  is matched by.
- **The foot** is the key row, the register and the grey run of keys a turn
  holds inert, drawn from a reading of the others taken when the frame is
  drawn; it holds nothing of its own.

Every flag the screen held for its modes, with its owner:

- **Whether a turn holds the files read-only** is the host's, kept by the
  screen. The screen's file keys go inert under it, and the foot reads it to
  grey them and say why.
- **Whether the filter row is open** is the filter's. The screen reads it to
  route every keystroke to the row before anything else but the confirm and
  the plan card, and the foot reads it to offer the row's keys instead of the
  list's. A dependency jump to a row the filters hide clears every filter,
  which closes it.
- **The ready toggle** is the filter's. Its key flips it through the filter;
  stepping onto the archive clears it, and so does a dependency jump to a row
  the filters hide.
- **Whether the reader is open** is the reader's. The read key opens it
  through the reader, the reader's own back key closes it, and the screen
  closes it whenever a filter, a tab or a dependency jump moves the row under
  it. The screen reads it to route keys and to give the reader the surface,
  and the foot to offer the reader's keys.
- **Whether the register is showing** is the screen's. The reader hands the
  register's key back to the screen rather than flipping it, and the foot
  reads it.

## A surface declares itself once, in the key register

The register of keyed surfaces says, for every surface that answers a key,
what it is called, which document is normative for it, where it stands
relative to the keyboard, how it gets the keyboard, and the keys it offers.
The chat session's table of modes knows which of those surfaces a mode is
standing in, because `?` over a card lists that surface's keys. The two had
to agree by spelling: a mode named its surface by repeating the name the
register gave it, and a misspelt name was not an error, it was a `?` that
listed nothing.

The obvious fix runs the wrong way. The register is read by things that never
start a chat session — the keymap loader refusing a file that would make one
keystroke mean two things, the reserved-key check, the doctor's report on the
keymap, and the test that keeps the generated keymap and reserved-key
references current — and the chat package imports the key package, not the
reverse. A register assembled from the chat session's table would be empty,
or a cycle, in every one of those programs; one that each mode filled in when
the chat package loaded would be empty in the same ones, silently, which is
worse.

So the metadata lives in the key package, beside the bindings it lists, and
nowhere else. That is also the only place it can live: the keys a surface
offers are the key package's bindings, which a keymap file moves after the
program starts, so a row is built from them each time it is read rather than
kept; a package beneath the key package could hold the shape of a row but not
one row, and a package above it could not be read by the loader. Who writes
what:

- **A surface a mode of the chat session stands in** is a row of the
  register, keyed by a handle the register declares. The mode's row in the
  chat table names the surface by that handle and says nothing else about it,
  so the name, the section, the position and the way in are written once.
- **The input** is the one row read off another declaration: its keys are the
  input's offers, each declared with its help paragraph, and the row lists
  them in that order.
- **The surfaces no mode stands in** — the input's history search, the
  transcript search, the staged strip, the fields typed into on a card, the
  lists a card opens under it, a selector being typed into — are rows of the
  same register with the same handles, because nothing else owns them.
- **The programs outside a session** — the supporting screens, the
  one-shot's action bar, the saved-chat browser — are a second list in the
  same place, read alongside the first wherever a key is checked against
  every surface.

The register's order is the order of its handles, which is the order a reader
meets the surfaces in. Because it is data built from the key package's own
values, every reader sees the full list from the moment the package has
initialised, including the checks that run during initialisation, and none of
them needs the chat session to be linked in.

## Design lives outside the repository

The visual specification — tokens, components, artboards, and the guidelines
that constrain them — is a design-system project in Claude Design, and it is
normative. This repository implements it.

That direction is deliberate. Markdown re-drawings of an artboard become a
second source of truth that disagrees with the first, and the disagreement is
discovered by a reader who cannot tell which one is stale. What lives here is
what the rules *are* and why they hold; what the design system holds is what
they measure.

See [`interface/`](interface/).
