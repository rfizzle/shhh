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
