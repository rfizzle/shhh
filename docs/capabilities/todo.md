# The backlog

A backlog is the work a project still owes: the stories, bugs and chores a
session surfaced but did not do, each written down well enough that a later
session — or the person — can pick it up cold. It belongs to the project, not
to a session, and it outlives both.

It exists because the end of a good session is usually a list. The
conversation has settled what to build, what the edge cases are, what tests
would prove it, and what has to land first — and then the session ends and
the list goes with it, or it goes into a scratch file that nothing else can
read. The backlog is that list kept somewhere shhh can show, order and, in
time, work through.

## An item is a file you can edit

Every item is one Markdown file: a short header of fields shhh reads, then
the sections a person writes — the story, the acceptance criteria, the tasks,
the tests, the notes, the dependencies. The header is what the tool needs to
order and select; the sections are for whoever does the work. Neither is
hidden in a database.

That split is the whole reason the format is a file. A backlog nobody can
open in an editor is a backlog that is maintained through the tool or not at
all, and the tool is the wrong place to reword an acceptance criterion at
eleven at night. Open the file. Change the line. shhh reads the change the
next time it looks.

Which fields the header carries comes from one place, a **profile**: what
one item is called, which fields it has, what each of them may say, and
which of them grades the work a run spends on it. Every surface draws the
same words from it — the list, the rail, the card that proposes an item,
the schema a reading is asked for in, and `--json` — so none of them holds
a vocabulary of its own. Which profile a project runs is a line in its
settings ([below](#a-profile-says-what-the-work-is)); unset is the one a
checkout of code has always been written in: a `kind` of story, bug or chore,
and a `size` of S, M or L as the grade. Priority is on every profile and says the
same three words in every one, because the order of the ready list has to
be one rule a person can recompute by reading the headers.

A value off its field's scale is a warning rather than a refusal, and what
it costs the item depends on what the field is for: the item is ordered as
medium when its priority will not read, reads as ungraded when its grade
will not, and keeps what the file said for anything else.

Two rules keep that trustworthy:

- **A field shhh does not know is kept, never dropped.** Add your own
  headings, your own header fields. They survive every write shhh makes.
- **shhh changes a line, never the file.** Flipping a status or ticking a
  checkbox is an edit to that line. The prose around it is not reflowed,
  reordered or rewritten. A diff of a file shhh touched shows exactly the
  fact that changed.

The file's name is the item's identity — a short slug, lowercase, hyphens —
and it is what one item names when it depends on another. A slug is chosen
once and does not change; renaming one is a deliberate act that rewrites
every dependency that used it. That grammar is the whole rule. A project
that reserves a shape of its own — an identifier its planning already uses,
a prefix its tooling reads — says so in its profile, and the refusal names
the profile it came from, so the rule travels with the project rather than
with the tool.

## Ready means the dependencies are done

An item is *ready* when it is open and everything it depends on is done.
That is the only definition, and it is computed rather than recorded, so a
file that says "open" and names a dependency still being worked on is not
offered as the next thing to do.

Order among ready items is fixed and stated: priority first, then age, then
name. No weights, no decay, nothing the reader cannot recompute by looking at
the headers. The next item is always the first one on that list, which is
what lets "do the next thing" be an instruction rather than a judgement.

An item that cannot be read — a header it lacks, a value it misspells — is
reported alongside the list, naming the file and what was wrong with it.
It is not silently left out, because an item that vanished from the list
is indistinguishable from one that was finished.

## A sprint is a file that names its items

Priority and dependencies can say what comes first and what has to land
before what. Neither can say "these eight, in this order, and nothing else
this week", and neither can state what the set is *for*. A sprint is that
statement, and it is a file.

It lives beside the items, in their header grammar: a name, a status, when
it was made and by whom, then a goal paragraph and a list of slugs. The
same two rules that make an item trustworthy make the sprint trustworthy —
a field shhh does not know is kept, and shhh changes a line rather than the
file — so adding an item, dropping one or rewriting the goal leaves
everything else on disk byte for byte as you left it. There is one sprint at
a time; planning another while one is open is refused rather than allowed to
overwrite a set that may be half worked, and so is one whose name a closed
sprint already occupies — a sprint that could not be filed under its own name
is a sprint that could never close.

**While a sprint is open it is what "ready" means.** The next item is the
first slug in the file that can be started, and nothing outside the file is
offered — that is the boundary a sprint exists to draw. With no sprint file,
or with a closed one, the rule above is unchanged and the whole backlog is
the ready list. What a sprint may not do is *declare* an item ready: a slug
whose dependencies are outstanding is skipped, with what it is waiting on
stated on the row, rather than started or silently dropped. The order is the
file's, in full, which keeps the promise that order is nothing the reader
cannot recompute — here, by reading the file.

The set is proposed and never imposed. Asking for a plan offers a set as a
card on the sprint's own board: everything kept, you drop what you do not
want, you say what the set is for, and only then is the file written. A
budget stated by size bounds what the proposal may hold, and size is its unit
because size is what a run gates on — three large items is a different week
from nine small ones.

The goal earns its place by being read. It rides in every item's research
step, so a run knows what the set around it is for; a session with no
sprint sends nothing rather than sending an empty heading.

**The set is watched on a board.** The sprint is a tab of the backlog screen
([`the sprint board`](../interface/surfaces.md#the-sprint-board)): the goal at
the top, a meter of what is finished over what the backlog still holds, what
the set has cost so far, and its slugs in the file's order with where each
one stands — the one being worked saying which step it is at. A set that
stopped on a block shows the block and the item that wrote it, because a
sprint stops on the first one and attempts nothing after it. From outside the
session the same board prints as a listing, and `--json` carries the file back
with the state of each slug placed against the backlog.

When the last item is archived — by a run or by hand — the sprint stops
being a plan and becomes a record: it moves into the archive under its name
with the set's notes at the top and each item's report copied in under its
slug. Closing one early does the same and carries what was left as deferred.
An item dropped from the backlog outright is accounted for on the sprint's
rows rather than quietly forgotten, because a set that shrank without saying
so is a set nobody can read afterwards.

A closed sprint also writes a page: the goal, the notes, every item with what
it produced, what stopped the rest, and the turns and spend the set took
([`reports`](reports.md#a-page-shhh-writes-for-you)). The archived file is the
record and the page is the readable form of it — the one a person hands to
somebody who was not there.

## A sprint is what ships together

A set is right when what ships together reads as one change. That is not
something priority and dependencies can decide: they order work, and
coherence is about what the work has in common. So the proposal is a
*reading* of the ready items rather than a sort of them.

**What the reading does.** It reads each candidate against the code, then
groups: a dependency chain that lands together, items that touch the same
packages, a theme the titles share, a bug and the story that closes its
cause. It answers with the set in the order it should be worked, one line
per item saying what puts it in that set, a sentence saying what the set is
for, and every candidate it left out with one word for why — `waits`, `too
big`, `unrelated` or `stale`. The words are a closed set, because a reason
free to be a sentence becomes one, and a left-out list of paragraphs is a
list nobody reads. What was left out is folded under the set on the card:
what a recommendation did not take is half of what makes it arguable.

**Reading comes first.** The candidates are read against the tree before
they are grouped, which is the same reading
[an item gets before it is worked](#an-item-is-checked-before-it-is-worked):
a recommendation over items that state what the code did last month
recommends the wrong week. An item whose reading still stands is not read
again — the planner takes the reading you accepted rather than paying for it
twice.

**The proposal says what kind of release the set reads as** — `patch` for a
set of bug fixes, `minor` when a story is in it — because that is the
question you answer when you tag. It is one line of the goal and never a
field: shhh names no version and makes no tag.

**What makes a set is the profile's; the shape of the proposal is not.** The
four things the reading groups by — a dependency chain, a shared package, a
theme, a bug and the story that closes its cause — are what the `code`
profile calls coherence, and they are a file in that profile's directory
([above](#a-profile-says-what-the-work-is)). A reading list groups a question
with the sources that would settle it; an operations backlog groups the work
that can be done in one window. The release words are the `code` profile's
too, and a profile that states none is never offered the line — so a set of
readings has a goal that is a sentence and nothing under it. The goal, the
line per item, the closed words for what was left out and the budget the set
has to fit stay the program's, because that is the shape the card is built
from and the sprint file is written from. A profile that says nothing about
what belongs together has no proposal, and asking for one says so.

**Only the goal is written.** The sentence goes into the sprint file, with
that release line under it. On the card the two are separate rows, so
rewriting the sentence with `/todo sprint goal` keeps the release line
rather than throwing it away. The reasoning lines stay on the card and never
reach the file — the file is yours, and a sentence a model wrote about an
item is shhh's reading of it, not a decision you made.

**What a closed sprint leaves is notes you can paste.** Each item that
landed with its title, what was built and the commit that carries it, and
anything the set did not finish listed as deferred and back in the backlog
unchanged. They are written into the archived sprint file and onto the
report page, in one flat block, because the next act after a set closes is a
tag and a tag message is plain text. Making the tag stays yours: a tool that
offered to make one is a tool that will one day make the wrong one.

From outside the session `shhh todo sprint plan` prints the same reading,
and `--json` hands it over whole for a script that wants the recommendation.
Neither writes anything — not the sprint file, and not a reading of any
item. Choosing what a week is spent on is a decision, and the set is taken on
the card in a session.

## Where the backlog lives

The backlog is a directory inside the project's shhh directory. Every
session opened anywhere under that project sees the same list.

Which directory the project is has one order, read top to bottom:

1. **The nearest shhh directory.** It is the one marker somebody put there
   to say where shhh's state goes, so it is read first. A repository whose
   services each keep their own is as many projects as it has of those.
2. **The repository around it.** A checkout with no shhh directory yet keys
   on its root, so the first item written lands where the rest of the
   checkout will find it.
3. **The root the settings name.** A directory that is part of neither takes
   whatever `todo.root` says.
4. **The global backlog**, beside the settings themselves, when the settings
   say nothing. A conversation opened in a home directory has a reading list
   rather than nothing.

The first two are the project, and nothing below them can override one: a
setting that could move the backlog out of a checkout would give two
terminals in one checkout two different lists. The last two exist because a
reading list is not kept in a checkout, and a session that keeps one is often
opened nowhere in particular.

A shhh directory above a repository root is not read: it belongs to whatever
encloses the checkout, and a clone dropped inside somebody's own
shhh-managed directory would otherwise take that directory's backlog. A
session names the root it chose whenever it is not the directory it was
started in, on the start screen and on the first `/todo`.

The global backlog is its own directory rather than a shhh directory inside
one. Nothing but the backlog is kept there, and a state directory under the
configuration directory would be shhh keeping project state for a project
that is its own settings.

Whether the directory is committed is the project's decision, not shhh's.
Some teams want the backlog in history beside the code it describes; some
want it private to one machine. shhh reads it either way and never assumes
one or the other — in particular, nothing shhh commits on the project's
behalf includes it. The one part that is always ignored is the scratch state
under it, which is per-run and never worth a diff.

The project's context file — the notes appended to every system prompt —
lives in the same directory now, as a file inside it rather than a file of
the same name beside it. A checkout still holding the old single file is
reported by `shhh doctor`, which offers to move it
([configuration.md](configuration.md#a-migration-is-a-doctor-check)).

## A profile says what the work is

Everything above this line is the same whatever the work is: one file per
item, the four statuses, ready as dependencies archived, the sprint, the
archive. Everything below it is not. What an item is called, which fields it
carries and what those may say, which of them grades the effort a run spends,
and which steps a run has at all — those are facts about the project, and a
project whose backlog is a reading list should not have to fork the tool to
say so.

A **profile** says them, and it is a directory: a `profile.toml` holding the
vocabulary and the run, and a `prompts/` directory holding one file per step
of that run. `todo.profile` in the settings names which one, and unset is
`code` — the profile a checkout of code has always been written in.

**Five profiles ship.** `code` is today's: a story, a bug or a chore, graded
S, M or L, worked by research, implement, verify, review, remediate and
commit. `research` is a backlog of questions, readings, interviews and
syntheses, graded quick or deep, worked by scope, gather, review and a
write-up — no verify, no commit. `ops` is tasks, incidents and changes,
ungraded, worked by scope, act and a command that checks the change landed.
`notes` is one turn that writes the note down. `checklist` has no run at all:
asking for one says so and offers the verb that files the item, because what
an item on a checklist needs is a person doing it.

**A profile composes; it does not invent.** A step is one of six kinds the
program carries out, each with the answer it asks for and the reading of that
answer — a turn, a sub-agent, a division into lanes, a command, a gate, a
finish ([above](#a-run-is-turns-with-gates-between-them)). A profile says
which of them a run has, in what order, in which mode and with what the gate
stops for; it cannot say what a step's answer looks like, because a step whose
answer nothing can read is a step nothing can gate. A profile that states a
shape the runner cannot carry out — a run with no ending, a gate after the
tree has already moved, a division with no turn to put the lanes back
together, a commit where nothing writes — is refused when it is read, with
the file, the line and the rule. So is a name reserved by a pattern that will
not compile, which would otherwise reserve nothing at all and look like it
was working.

Priority is on every profile and says the same three words in every one. The
order of the ready list has to be something a person can recompute by reading
the headers, and a profile that could rename or reorder priority would make
two projects' backlogs sort differently under what reads as one rule. The
file says only where priority's column goes.

**Three places hold a profile, most specific first.** The checkout's own, at
`.shhh/todo/profile/`; then a directory of your own, at `todo/<name>/` beside
your settings; then the profiles built into the binary. A name nothing
answers to stops the command and names all three, because the person who
typed the name is deciding where to put the directory.

A checkout's profile is behind its trust answer, for the reason its wordings
are: it is not prose the model chooses to read, it is what shhh itself says
at a step that changes the tree without asking — plus the shape of the run
that says it. Until the person has answered for the checkout, the profile is
among what the session names as withheld, and the run falls back to theirs or
to the built-in one.

The wordings under a profile follow the rules every other wording follows
([below](#the-stage-prompts-are-yours-to-edit)): instruction only, the blocks
placed where the file wants them, the answer shape appended by the program. A
step whose file is not in the directory stops the load rather than falling
back to shhh's own words, because a run that sent the code profile's
implement wording to a reading step would be telling the model to build
something.

Two of the wordings are not a step's. `groom.md` is what a reading of one
item is told to check ([below](#an-item-is-checked-before-it-is-worked)) and
`plan.md` is what makes a set of them one set
([above](#a-sprint-is-what-ships-together)); both are optional, a profile
that ships neither has neither verb, and no step may take either name. The
profile also says how far a reading may fall behind before the surfaces say
so — in commits or in days, with the number — and which words a proposed set
may use for the kind of release it reads as.

A session names the profile beside the root whenever it is not `code`, the
backlog counts in the profile's own noun, and `shhh doctor` says which
profile is in force, where it was read from and which steps and wordings it
has.

## A session proposes; you accept

The end of a session is where a backlog comes from, so a session can read
itself into items. It digests the conversation — what was asked, what the
assistant said, which tools ran, what changed — and a model proposes the
work that was settled but not done, in the shape an item takes: the story,
the criteria, the tasks, the tests, the decisions already made, and what
has to land first.

Nothing is written until you say so. The proposals come back on a card,
all checked; you uncheck what you do not want, or drop the lot, and only
then do files appear. The rule is the one memory follows: a session may
propose, and the person decides. A backlog that filled itself would be a
backlog nobody trusts.

The digest is the boundary. It carries what the two sides said and what
tools were called, never a tool's own output, so a page the session
fetched or a test's stdout cannot write an item into the project's list
of what to do next.

The same door opens from a sentence. Say what the work is in one line and a
model drafts the single item it describes — the title, what sort of work it
is, how soon, how big, the story, the criteria, the tests and what it waits
on — onto the same card, which writes nothing until you accept it. A drafted
dependency that names nothing in the backlog is a warning on the card and is
left off the file, because a dependency on nothing would hold the item back
forever.

The card is also where a header stops needing an editor. An item's header is
a closed set of answers, so each one is a row you step through in place —
what sort of work, how soon, how big — and what it waits on opens the backlog
itself, so a dependency is a slug that exists rather than a name somebody
typed. The proposals a session reads out of itself are set the same way,
which is how an extracted item lands graded and connected instead of on its
defaults. The sections under the header are prose and stay prose: one key
hands the whole item to your own editor, exactly as editing a written item
does, and whatever the file holds when the editor exits is the draft. Nothing
reaches the backlog directory until the card is accepted, and the item that
lands names the session that wrote it.

## The backlog is in view, and the file is still the item

A session shows the backlog three ways, and each answers a different
question. In the inspector rail, a block lists the first few active items in
working order with what each waits on, so "what is next" is on screen beside
"where are we". From the input, one command lists everything, picks an item
to read, starts a new item from a sentence, or changes one item's state —
blocked, reopened, archived — and says what it did. It can also drop an item
outright; that is the one verb here that loses information, and it says so
when it has.

The third is a screen, and it is the answer to "what is in here", which the
listing could only half answer: it named every item and then asked for one
of those names to be typed back before it would do anything to it. The
screen shows every item with the one under the pointer beside it, filters by
text and by each of the header's own fields, and puts the keys where the
names were — a state change is a keystroke on the row rather than a slug
copied back into a command. What shipped is a second tab,
where each archived item's body is the report of what was done rather than
the criteria that were the question, and an item can come back out of the
archive from there. Nothing on it changes a file without asking first, and
while a turn is running the keys that would change one are not live at all —
the model may be working from those files.

None of that replaces the file. Editing an item means opening the file,
and the session hands it to your own editor rather than offering a form:
the sections are prose, and the place to write prose is an editor. When
the editor comes back the session re-reads the backlog and says what the
file now reads as — including why it no longer loads, if that is what the
edit did — so a broken header is a sentence on screen, not an item that
quietly disappeared.

## From outside the session

The same backlog answers a second terminal, a script and a CI job. One
command lists it, two more narrow it to what can be started now and to the
single item a run would take next, one prints an item, one shows the sprint,
one works an item, and blocking, reopening, archiving and dropping are each a
verb of their own. They are the session's verbs with a command wrapped round
them rather than a second implementation: one refusal, one confirmation, and
one answer to what archiving an item means.

Two things follow the destination rather than the verb. Showing an item lays
its prose out for somebody reading it on a terminal, and hands back the file
itself when the output is redirected — what a script asked for is the item,
and a rendering of prose is not one, so what it gets is a file the backlog
would load again. And the listing verbs will answer in JSON: the header
fields, whether each item is ready and what it is waiting on, the sprint
scoping the set, the files that would not load, and the warnings. The
warnings are the part worth stating. A file with a size line off the scale
still loads and still shows that line as a warning on screen, and a reader of
the fields alone would treat the item as ungraded and never learn there is a
line there to fix.

A verb that changes an item refuses while a run has that item in flight, and
names the session the run is in. Every step of a run states the item as the
file stands when the step begins, so blocking or archiving one underneath a
run changes what its next step is working from — and the run says nothing
about it, because it reads the file rather than watching it. The session in
the refusal is where that run can be stopped, and until it is, the item is
being worked in two places at once.

## An item is checked before it is worked

An item written weeks ago is a description of a tree that has since moved.
It names files that were split, functions that were renamed, flags that went,
and it states what the code does today in the present tense of a day that has
passed. A run finds all of that out three steps in, on a plan built against
the wrong file — and that is the best case, because the alternative is a
plan that is wrong and looks right.

So an item can be read against the code as it stands, before anything starts.
The reading is one read-only turn per item. It takes every claim the item
makes — each path and line, each function, flag, config key and command it
names, each sentence about what happens today, each dependency, each
acceptance criterion and the size it is graded at — and answers with a
verdict from a closed set: it *holds*, it *moved* and here is where to, it
*changed* and here is what happens now, it is *gone*, it is *already done*,
or it is *unknown*.

**What is checked is the profile's; how it is answered is not.** The list in
the paragraph above is what the `code` profile asks for, and it is a file in
that profile's directory ([above](#a-profile-says-what-the-work-is)). A
reading list asks instead whether each source is still there and still says
what the item says it says, whether the question has already been answered
somewhere else, and whether it has grown or narrowed; an operations backlog
asks whether the task still applies at all. The verdicts, the markers and the
diff they become stay the program's, because a diff needs a fact per line
whatever the work is, and a claim that a paper exists is *gone* in the same
sense a function is. A profile that says nothing to check has no reading at
all, and asking for one says so.

**The set is closed, and the only free text is one line of evidence under
each verdict.** That is not tidiness. What comes of a reading is a diff, and
a diff needs a fact per line: "moved, and it is in this file now" is a fact
you can accept or decline, and "this item may need updating" is a sentence
that can be said about everything — so a reading that is allowed to say it
will say it about everything, and the reading stops being worth taking.

**You accept it, line by line.** The verdicts come back as a proposed edit
shown as a diff: a reference rewritten to where it now points, a "today"
sentence restated with the old one struck through beside it, a criterion the
tree already satisfies ticked with the commit that did it, a dependency that
is already finished taken out, a size re-graded with the reason. Enter writes
the lines you checked; esc writes nothing. Every accepted change is one line,
and prose the reading did not name is not reflowed, reordered or rewritten —
the same rule every other write to an item follows.

The reason it is yours to accept is the run's own rule. Rewriting a `path:line`
is mechanical, but restating what the code does today is a claim, and a
groomer that made that claim into the item on its own would be the session
writing the backlog rather than working it. A reading that could not be
declined would also be one nobody could disagree with.

**The header records the reading in whatever distance the profile measures.**
An accepted reading stamps the item with when it was taken, and the surfaces
that list items say when it has fallen further behind than the profile allows
— with the count and the distance it is counted in, in the tone a warning
takes. On a checkout of code the distance is commits and the stamp carries
the commit as well as the day: staleness there is how far the tree has moved,
which the repository can compute, where a date only says how long you waited.
Where the work has no tree under it, it is the other way about, the stamp is
the day alone, and a commit in the header would be a fact nothing reads. How
fast a backlog goes stale is a fact about the work, so the threshold is the
profile's too, and the setting overrides it rather than stating it. An item
nobody has read this way says nothing at all, because absence is not
staleness.

Two things follow from the verdicts. An item every acceptance criterion of
which reads *already done* is proposed for archiving, with the evidence as
its report — proposed and never carried out, because an item finished by work
nobody filed under it is exactly the case where somebody has to agree that it
was the same work. And a run started later is handed the reading you accepted
rather than taking it again: its research step is told the item was read and
corrected — on the day the stamp carries, and at the commit as well where
commits are the distance — so it plans the work instead of re-deriving what
you already settled.

From outside the session, the same reading is a command that prints the
verdicts and writes nothing at all — not the corrections and not the stamp.
What a script wants out of a grooming is the reading; accepting it is a
decision, and decisions are made where somebody is looking.

## A run is turns with gates between them

An item can be worked by the session itself, in steps. Each step is one thing
the run does, and between two of them the decision of what happens next is
made by code reading the last one's answer — never by the model deciding
which step it is in. That is what makes a run deterministic: the same answers
produce the same path every time, and the gates cannot be talked past.

**Which steps a run has is the profile's; what a step is made of is not.**
There are six kinds of step and the program owns all six:

- a **turn**, one message of the conversation in a reading or a writing mode,
  which always says whether it could not do the work and says the grade and
  any open questions where the step asks for them;
- an **agent**, a sub-agent that reads the work it did not do and answers
  clean or with findings, falling back to a turn of the session's own where
  there is no supervisor to spawn one from;
- a **fan-out**, writer children over paths they each declare, with the turn
  that makes their work fit together after;
- a **command**, which runs the item's own checks and the project's, or one
  the step names, with no model in it at all: the exit status is the verdict;
- a **gate**, which stops and asks you;
- a **finish**, which is where a run ends — in a commit, in an archive, in a
  note, in a command, or in the `stop` hook a project has armed.

A profile composes those kinds and cannot invent one. Each kind carries its
own answer shape — the marker lines the program reads back — and its own
reading of them, and both arrive after whatever the step's instruction said. A
step that could write its own answer shape could write one the program cannot
parse, and a step the program cannot read is a step it cannot gate; that is
the same line the [wordings](#the-stage-prompts-are-yours-to-edit) draw, one
level up. A profile is checked when it is read, too: a run with no finish
could never end, a gate after a step that writes would ask you about a tree
that has already moved, a division into lanes with no turn to integrate them
would hand on work nobody read as one piece, and a commit in a run whose steps
never write would carry somebody else's work or refuse every time.

The run a checkout of code gets is the one shhh ships, and its steps are named
`research`, `pause`, `split`, `fan-out`, `implement`, `verify`, `review`,
`remediate` and `commit`: research the item and plan the change, pause on what
research found, build it — in lanes where the work is graded largest — verify,
review, remediate what either turned up, and commit. The rest of this section
is that run, which is the worked example the kinds were drawn from.

Research happens in the read-only mode, and its answer is the plan in the
same shape a plan is always asked for, plus the grade and any open question.
The grade is re-graded from what research found, and the number of fix
rounds the run gets is set by that grade, not by the item. A backlog graded
some other way is spent against its own scale rather than translated into this
one: the run reads where a grade sits on the profile's scale and never the
word, so a two-grade backlog gets the smallest grade's ceremony and the
largest's without the runner knowing either word.

Whether the run then pauses is the gate's rule at that grade, from a closed
set: never, on questions, on questions or an upgrade, or always from a named
grade up. In the code run the smallest grade never pauses — and an open
question on one ends the run rather than being guessed at, because a runner
that answers a product question for you is not running your backlog, it is
writing it. The middle grade pauses when research left a question or graded
it up. The largest always pauses before anything is built, because that is the
moment spend and blast radius are decided. The pause shows the plan, the
questions and the grade, and takes one of three answers: go ahead, with a note
if there is one to give; re-plan with a note that answers or steers, so
research runs again with it in front; or stop. A note goes onto the item's
record either way and in front of the model for the step it was written for. A
run never silently gets bigger.

Verification is a command step: the item's own tests and the project's checks,
run by shhh rather than described by the model, with nothing that loads a
model anywhere in it. The tests are the ones the item listed when the run
started: the run tells the model to tick the item's boxes as it works, and a
command the model wrote into the file during the run is not one shhh will run
unasked. A failure spends a fix round; when the rounds are spent the run stops
with the failure as evidence. The review reads the change as a critic and
answers clean or with findings, and findings spend a round the same way. The
smallest grade reviews itself in the session's own turn; anything larger is
read by a reviewer child that did not write it — a second opinion is only one
if it comes from somewhere else — and where no child can be had the session
reviews and the record says so.

The run works in the mode that asks only when the classifier cannot
decide, whatever mode the session was in, and puts the session's mode
back afterwards. The review and the commit message are written in the
read-only mode, so nothing can change between the verification that
passed and the commit. While a run is going the input takes commands
only: a sentence typed mid-step would steer the model out of its step,
and one typed between steps would start work the run would then commit
as its own. Stopping the run is a command, and cancelling a step's turn
is the same as stopping it. A backlog runner that asked at every edit would be a
session with extra steps; one that never asked would be one you could not
steer. The classifier failing closed is the steering.

The commit is shhh's to make, not the model's. Only paths the run itself
changed are staged, by name, and never a backlog file; a tree that already
holds staged changes the run did not make stops the run instead of
committing a stranger. The message is written by the model in the
repository's own style, read from its history, and the report the model
writes goes onto the item as it is archived.

**What a run needs, it asks for before it starts — step by step.** A step
that writes needs a session that tracks what it changed; a division into lanes
needs a supervisor as well; a command step needs somewhere to run a command;
and a commit finish needs a repository. Each is checked against the steps this
run actually has, before the first turn, and the refusal names what the step
wanted and the way through it. It is per step and not per session because "this
session does not track changes" is the right sentence for a step that writes
and the wrong one for a run that never writes: a read-only session can work a
backlog of readings, and nothing about the session it could not use is asked.
A step that only sometimes runs asks for nothing up front — the division into
lanes happens at one grade and falls back to the session's own turn where it
cannot happen at all.

That is what makes the backlog workable from a conversation as well as from
the coding agent ([`chat.md`](chat.md#the-backlog-is-here-too)). A
conversation can take a step that reads, a step that hands the work to one of
its colleagues, a gate, and a finish that archives or writes the work up. It
cannot take a step that changes the tree, a step whose verdict is a command's
exit status, or a finish that commits, and it says which of them it was
turned away by rather than saying it has no backlog.

**A run may end in the write-up.** Where the session keeps a shared notebook,
a `note` finish spends a turn on the report and puts it there — signed by the
run, titled with the item's name — and the archived item says where it went.
Where there is no notebook the run archives instead: a turn spent asking for
a write-up nobody in the session can read produces what the code already
knows.

The reason the question is asked at the start rather than at the step is that
every step before it spends a turn: a run that did all of them and only then
found it had nowhere to put the result has spent them for an item it leaves
half-finished — and what it said at that point was about an index the
directory does not have.

**A run without a commit is the other definition of done.** Asked for with
a flag, or set as the project's answer, the run's finish becomes the archive:
the item is archived with a report that says the work was not committed and
names the paths it is in, and the row that closes the run says the same. It is
one step changed and not a step removed, which is also what a profile whose
work does not land in a repository states for itself. That is what a directory
with no repository takes, and what a project whose commits are made elsewhere
takes everywhere. It is never the fallback for a run that asked for a commit
and could not have one: becoming a no-commit run on its own would change what
done means without being asked, and leave an item marked done that did not
land.

A run that stops — a question, spent rounds, a commit that cannot be made
— leaves the item blocked with the evidence written on it and the work so
far in the tree, uncommitted and named. Nothing is stashed or reset: the
work is yours to keep or drop, and the item says exactly where it stopped.
What is left is offered as a follow-up item, after the blocked one, on
the same card the session's own proposals use; accepting it is what lets
the blocked item be archived once the rest lands.

A run survives its session. Every transition is written to a checkpoint
beside the backlog, and an item found in progress with one is continued
from the step it was at — the step starts over, because a step is the
smallest thing that can be judged and the conversation that was mid-way
through it is gone, but the plan, the answers and the rounds spent are
kept, and so is the work of the steps before it. A turn that gets in
ahead of a step — a compaction, a skill being loaded — pauses the run
the same way rather than failing the item: nothing about the item is
wrong, only the conversation moved. Starting a new conversation does not
end the run either: the checkpoint was written to survive exactly that, so
the item stays in progress and the new conversation's first row names the
command that picks it up. Stopping the run is the one explicit end, and
that is the one that puts the item back to open with the tree as the run
left it.

**A run whose steps have changed under it is refused rather than resumed.**
The checkpoint records the shape the run began under, and a project that has
since named a different profile is asking for a different run: the plan was
made for steps that no longer exist, and half a run of one shape joined to
half of another is not one run's worth of work. The item goes back to open and
a run can start over under the new steps. The wordings are the softer case of
the same fact — they are files a continuing session re-reads, so a run picked
up after one moved carries on and the row says the words changed.

**A step's answer has to be a whole one.** Every gate in the run reads what
a turn said, and a reply the model did not finish reads exactly like one it
did: the sentence simply stops. So the run asks how the turn ended before it
reads what the turn produced. A reply cut off at the model's output ceiling
is finished first — the run sends the same instruction the session offers
behind a key, once per step, and grades the two halves as the one answer
they are. A second ceiling in the same step stops the item with that as its
evidence, because a step free to ask for one more paragraph every time it
filled a budget would be under no ceiling at all, and because half a review
graded as a whole one is exactly the mistake the gates exist to prevent. A
reply the wire dropped is not finished by the run at all: what was kept is
half a sentence, whether it is worth keeping is a judgement, and the run has
no standing to make one — so it pauses at its checkpoint the way a displaced
turn does, and the row says which of the two happened.

An unattended run is held to the same reading. Each step that spends a turn
there is a process of its own that finishes its own cut-off reply once and
says in its transcript when it could not, and a step whose answer came back
cut anyway stops the item rather than being graded — the sprint is where this matters
most, because a half answer taken for a whole one is the kind of mistake
nobody is there to catch.

## The stage prompts are yours to edit

Each step of a run tells the model what that step is for, and the words are
shhh's. They are the general answer, which is the wrong answer often enough
to matter: a monorepo has conventions a sentence about AGENTS.md does not
reach, a project in a language the standards sentence was not written for
reads it as noise, and a team with its own review checklist has to fork the
program to use it.

So the words come out of the program and into files. There is one wording per
step of the run, named `todo_` and the step's own name, plus `todo_standards`
for the one sentence the steps that change the tree all carry and
`todo_<step>_task` for the task a step hands to a sub-agent. The run a
checkout of code gets has seven of them — `todo_research`, `todo_implement`,
`todo_review`, `todo_review_task`, `todo_remediate`, `todo_commit` and
`todo_standards` — and a project whose run has other steps has a key per step
of its own, because the keys are read off the run rather than out of a table.
Each lives in a file named for it, and that file's contents replace that
step's instruction. A file whose
presence is the override is the whole convention: the checkout's own
`.shhh/prompts/` first, then a `[prompts]` key where one names a path, then a
`prompts/` directory beside your settings
([where](configuration.md#the-mechanism-is-code-its-wording-is-configuration)).
`shhh config init` writes that last directory with the built-in words already
in it, which is where editing one starts.

A wording nothing replaced keeps the built-in words, and a file that cannot
be read stops the session with the path and the reason, exactly as the
steer's does: a run on the built-in words while the person believes it is
running theirs is the failure the whole arrangement exists to prevent.

**A checkout says it in files rather than keys.** A project's wordings live
at `.shhh/prompts/todo_<step>.md`, and inside that checkout they beat the
person's own. They are files by convention because `[prompts]` is one of
the tables a checkout's own settings may not set — a path written in a
checkout is a path in every clone of it, anywhere on the machine — and
because a wording written for this repository should travel with it. Like every other file a
checkout asks a session to load, they are behind its trust answer: a wording
is not prose the model chooses to read, it is what shhh itself says at a
step that changes the tree without asking, and a clone that could rewrite it
could take the standards sentence out of every run.

**The file is the instruction and nothing else.** The item, the plan, the
answers from a pause, the findings from a review and the change are blocks
the run hands the model. A file may name `{{item}}`, `{{plan}}`,
`{{answers}}`, `{{findings}}` or `{{diff}}` and put one exactly where it
wants it, mid-sentence included; a block it does not name is taken after the
instruction, in the order the built-in has them. `{{diff}}` is what changed:
the diff itself for the reviewer sub-agent, which has no commands to go and
look, and for the review step the instruction that finds it. A substitution
a step cannot fill — the findings in a research prompt, the change in a
commit prompt — is refused when the file is read, because a mistyped one
reaches the model as literal punctuation and the wording still looks like a
wording.

A few sentences are never the file's: whether there is a repository to read a
diff or a house commit style out of is a fact about the machine, so those
follow the instruction whatever it said. The built-in wordings place their own
blocks, which is why a step nothing replaced reads as it always has.

**The answer shape is not yours to edit.** The grade line, `questions:`,
`blocked:`, `verdict:` and `COMMIT:`/`REPORT:` lines are how the run reads a
step's answer back, and they are appended after whatever the file said. The
shape is built from the step's kind and from what the step says it reads, so
the grade line asks for the profile's own field and its own words rather than
anybody's constants. A wording that stopped asking for the grade would make
every research turn look like a block; the gate would not fail, it would
quietly stop being a gate. The mechanism stays in the program and the wording
comes out of it, which is the same line drawn for the steer and the check-in
([`configuration.md`](configuration.md#the-mechanism-is-code-its-wording-is-configuration)).

The wordings are part of what a session sent, so they fold into the session's
`prompt_hash` and divide the record the way an edited steer does. A run picked
up from a checkpoint reads them as they now stand — they are files, and the
session that continues the run has read the files — and where they moved
since the run started, the run's row says so, because a run whose steps were
asked different things is not one run's worth of work.

The grooming pass is not a step of a run and takes none of these: it states
the built-in standards sentence whatever a run was configured with.

## A large item is built in lanes

A large item is the one size the session does not build itself. After the
pause, one more read-only turn divides the approved plan into lanes: a
short name, the paths the lane may touch, and what it builds. Code checks
the division before anything is spawned — between two and four lanes, each
with a task and at least one path, no path shared between two lanes,
nothing under the backlog — and a division that fails the check is not a
blocked item, it is a plan the session builds whole, with the record saying
why. The orchestrator can also answer that the plan does not divide; a
plan whose steps all rest on one new foundation is that kind, and a wrong
split costs more than no split.

Each lane goes to a writer child, the same kind a session spawns by hand:
its own copy of the tree, the item and its lane in the task by content
rather than by path, because a copy of an uncommitted backlog holds no
item files. The lanes are written blind to each other, which is why each
must build against the tree as it stands. A lane's patch is the run's to
take — the lanes were checked disjoint, and the tree is verified and
reviewed after — so it lands without a card; a patch the supervisor flags
as overwriting another's is refused, and the run blocks on it, with the
other lanes' work in the tree and the missing one named. A command a
writer's classifier cannot decide goes to the person the way every child's
does; that is the steering a fan-out keeps.

A lane is not finished when its patch lands. The patch reaches the session
while the writer that wrote it is still ending its turn, and the account
of what it built — the thing the integration turn wires from — arrives
after; on a loaded machine the last lane to land is routinely not the last
lane to report. So the run waits for every lane's account of itself rather
than for the last patch. When it has them all, the session takes one turn
of its own in the working mode to make the lanes fit — wire what the
reports say needs wiring, tick the item's boxes, which no lane could — and
hands the tree to verification. From there a large item is a medium one:
the same checks, the same reviewer child, the same rounds. A run continued
in a new session at its fan-out spawns only the lanes that had not landed,
under new names, and one that landed without its report being kept is read
from the tree rather than waited on.

## A sprint is runs with a session between them

One item is a run. The whole ready list is a sprint: `/todo run --all` in a
session, `shhh todo run --all` from a script, working the items one at a
time — the sprint file's set in its order where the backlog holds one, the
whole ready list where it does not — until nothing is ready, until the cap
is reached, or until an item blocks.

**Each item gets a session of its own.** When one item is archived the
session ends and another begins, exactly as it would if you had quit and
launched again: the record closes and a new one opens, the prompt is built
again from the checkout as it now stands, and the next item starts from
nothing. The previous item's conversation is cost and noise to the next
one, and the checkpoint already carries everything a step needs. It also
means the record has one session per item rather than one for the night,
which is what makes anything computed over it — spend, rounds, how often a
run finishes — a figure about an item.

**Done is the runner's own ending and never something the model printed.**
An item counts as finished when the machine reached done, which is after
the verification passed, the review came back clean, a real commit landed
and the item was archived with its report. Where the project names checks
for a turn's close, those run too: a sprint is unattended by definition, so
it honours them without being asked, where a session you are watching
leaves that switch off. An item counts as blocked when the machine reached
blocked. There is no sentence the model can write that ends an item either
way.

**It stops on the first block.** A blocked item has a follow-up written for
it, and what comes next in the list may be resting on the work that did not
land, so nothing further is attempted. The four endings are named rather
than implied — nothing ready, the cap reached, an item blocked, or you
stopped it — because a sprint that ran out of work and a sprint that hit a
wall leave the same quiet screen.

**One attempt per item.** The remediation rounds inside a run are the
retry; a second run over an item the first could not finish is a second
chance at the same failure. An item whose implement step left the tree
exactly as it found it is a block for the same reason: there is nothing to
review and nothing to commit, and another round over the same plan would
produce the same nothing. A cap on how long one item may take is available
and off by default, read at the boundary between two steps so that it ends
an item rather than cutting a turn in half and leaving a tree nothing has
read.

The sprint keeps a checkpoint of its own beside the items', so a sprint
that dies with its process is picked up by the same command in a fresh one:
it names the item it was on, and that item's checkpoint names the step.
Stopping it keeps that item's checkpoint too — the steps already done are
in the tree, and the stop was aimed at the loop.

From a script it is the same machine with the screen taken away. Each step
that spends a turn is one `shhh code --print` in the checkout, and what the
step produced is read out of that transcript whatever status the process
left. The gates that would ask a person cannot: a run that reaches the pause
stops with the questions written on the item rather than guessing an answer,
and a review that would have gone to a second agent is taken in the session,
which is what a session with no supervisor already does. The exit status is
the run's own ending ([`headless.md`](headless.md#the-exit-code-is-the-contract)).

## Done is archived, not deleted

A finished item moves into an archive beside the active ones, with the
record of what was done appended to it. It stops appearing in any list and
stops counting as outstanding, but the file — the criteria it was held to,
the decisions made while doing it — is still there to read, and still there
for the items that depended on it to find.

Deleting would be simpler and worse. A dependency on a deleted item is
indistinguishable from a dependency on a typo, and the reasoning behind a
change is worth more a month later than it was the day it was written.

## Related

- [`sessions-and-memory.md`](sessions-and-memory.md) — what a session
  remembers, as distinct from what the project owes
- [`skills.md`](skills.md) — the other thing a checkout carries for shhh
- [`configuration.md`](configuration.md) — where settings live, and how a
  layout change is handled
