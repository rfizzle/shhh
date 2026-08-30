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
every dependency that used it.

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

## Where the backlog lives

The backlog is a directory inside the checkout's shhh directory at the
repository root. Every session opened anywhere under that root sees the
same list.

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

## The backlog is in view, and the file is still the item

A session shows the backlog two ways. In the inspector rail, a block lists
the first few active items in working order with what each waits on, so
"what is next" is on screen beside "where are we". From the input, one
command lists everything, picks an item to read, starts a new item from a
sentence, or changes one item's state — blocked, reopened, archived — and
says what it did. It can also drop an item outright; that is the one verb
here that loses information, and it says so when it has.

None of that replaces the file. Editing an item means opening the file,
and the session hands it to your own editor rather than offering a form:
the sections are prose, and the place to write prose is an editor. When
the editor comes back the session re-reads the backlog and says what the
file now reads as — including why it no longer loads, if that is what the
edit did — so a broken header is a sentence on screen, not an item that
quietly disappeared.

## A run is turns with gates between them

An item can be worked by the session itself: research, implement, verify,
review, commit, archive. Each stage is one turn of the conversation with a
prompt that says what the stage is for and the exact shape of the answer
it wants, and between the turns the decision of what happens next is made
by code reading that answer — never by the model deciding which stage it
is in. That is what makes the run deterministic: the same answers produce
the same path every time, and the gates cannot be talked past.

Research happens in the read-only mode, and its answer is the plan in the
same shape a plan is always asked for, plus a size and any open question.
The size is re-graded from what the research found, and the number of fix
rounds the run gets is set by that size, not by the item.

Whether the run then pauses is decided by the size. A small item never
pauses — and an open question on one ends the run rather than being
guessed at, because a runner that answers a product question for you is
not running your backlog, it is writing it. A medium item pauses when
research left a question or graded it up. A large item always pauses
before anything is built, because that is the moment spend and blast
radius are decided. The pause shows the plan, the questions and the size,
and takes one of three answers: go ahead, with a note if there is one to
give; re-plan with a note that answers or steers, so research runs again
with it in front; or stop. A note goes onto the item's record either way
and in front of the model for the stage it was written for. A run never
silently gets bigger.

Verification is the item's own tests and the project's checks, run by
shhh rather than described by the model. The tests are the ones the item
listed when the run started: the run tells the model to tick the item's
boxes as it works, and a command the model wrote into the file during the
run is not one shhh will run unasked. A failure spends a fix round;
when the rounds are spent the run stops with the failure as evidence. A
review reads the change as a critic and answers clean or with findings,
and findings spend a round the same way. A small item reviews itself in
the session's own turn; anything larger is read by a reviewer child that
did not write it — a second opinion is only one if it comes from
somewhere else — and where no child can be had the session reviews and
the record says so.

The run works in the mode that asks only when the classifier cannot
decide, whatever mode the session was in, and puts the session's mode
back afterwards. The review and the commit message are written in the
read-only mode, so nothing can change between the verification that
passed and the commit. While a run is going the input takes commands
only: a sentence typed mid-stage would steer the model out of its stage,
and one typed between stages would start work the run would then commit
as its own. Stopping the run is a command, and cancelling a stage's turn
is the same as stopping it. A backlog runner that asked at every edit would be a
session with extra steps; one that never asked would be one you could not
steer. The classifier failing closed is the steering.

The commit is shhh's to make, not the model's. Only paths the run itself
changed are staged, by name, and never a backlog file; a tree that already
holds staged changes the run did not make stops the run instead of
committing a stranger. The message is written by the model in the
repository's own style, read from its history, and the report the model
writes goes onto the item as it is archived.

A run that stops — a question, spent rounds, a commit that cannot be made
— leaves the item blocked with the evidence written on it and the work so
far in the tree, uncommitted and named. Nothing is stashed or reset: the
work is yours to keep or drop, and the item says exactly where it stopped.
What is left is offered as a follow-up item, after the blocked one, on
the same card the session's own proposals use; accepting it is what lets
the blocked item be archived once the rest lands.

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
