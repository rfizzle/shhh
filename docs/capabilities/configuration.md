# Configuration

## Two files, one resolution order

Settings live in a TOML file in the platform's conventional configuration
directory, and a checkout may keep a second one of its own at
`.shhh/config.toml`. Every value resolves most-specific-first: an explicit
flag, then the environment, then the checkout's file, then yours, then a
default.

Four keys have the top two ranks, and they are the ones a single run is most
often started with a different answer to: which provider, which model, its
key and its reasoning level, all under `[provider]`. The provider's base URL
has the environment rank and no flag. Every other key is a file or the
default — there is no flag and no environment variable for it.

No setting reverses this order. That uniformity is worth more than the
flexibility of special-casing, because it is what makes a wrong value
*findable*: a user who can predict where a value came from can fix it. Every
surface that shows a value says which rank it came from, the checkout
included, for the same reason.

The two files merge key by key rather than one replacing the other. What is
true of a repository — which commands run without asking, which mode a
session starts in, which model reviews here — travels with the repository;
what is true of the person stays in their own file, and a key the checkout
does not name is left exactly as they wrote it. A whole-file project config
would make everyone who clones the repository restate their provider and
their key in it, which is why nobody in the field does that either.

Four keys are the exception, and they extend rather than replace:
`behavior.command_allowlist`, `behavior.command_denylist`,
`behavior.read_only_commands` and `behavior.scope_dirs`. A checkout adding a
command to the allowlist cannot know what is already on the person's list, so
replacing would quietly take away commands that have nothing to do with this
repository — and the symptom is a session asking about `ls` again with
nothing on screen to say why. The deny list unions for the same reason read
in the other direction: a checkout may add a command it does not want run
here, and may never drop one the person refuses everywhere. The checkout's
scope directories are resolved against the checkout, so a relative path means
the same directory in every clone. Every other list is a complete answer and
overrides like a scalar.

A short set of keys is refused in the checkout's file, whatever the answer
to trust was. Each is a key whose value in a checkout is a value in every
clone of it, or one that reaches past the tree onto the machine:

| Key | Why not |
|---|---|
| `provider.api_key`, `web.search_api_key` | a credential in a checkout is a credential in every clone of it |
| `provider.api_key_env`, `web.search_api_key_env` | it would let the checkout choose which of your variables is sent as a key |
| `secrets.env` | it declares which of your environment variables a session may spend, which is about the machine rather than the tree |
| `[sandbox]` | it decides what a contained command may reach, which is the containment itself |
| `[mcp.servers]` | a server is a program to start, and a checkout names its servers in `.shhh/mcp.json` instead |
| `[prompts]` | it points at a file anywhere on the machine and replaces what a session is told |

Refusing them rather than leaning on trust alone is the second gate: the
field's other harnesses let a project file set nearly anything, and trust is
one answer given once, months before the commit that adds a key to that
file. A refused key stops the load with the reason and the file the key
belongs in, the way an unknown key does.

The checkout's file is read only where the checkout is trusted. Until then
it is withheld with the rest of what the checkout declares and named on the
start screen, in `/status` and in the doctor's trust row, and the session
runs on the user's file alone.

Writes go to the user's file — `config set`, the config screen's `[w]`, the
slash commands that save their answer — because that is the file that is
the person's. `config set --project` writes the checkout's, refusing a key
from the set above before it creates anything. A write to a key the checkout
overrides is made and says so: the user's file is read in every other
checkout, so refusing it would punish them for where they were standing,
and a confirmation that let them believe the value took effect here is the
one thing it must not do.

Two keys name a value rather than holding one. `provider.api_key_env` and
`web.search_api_key_env` are the name of an environment variable, read at
start and read ahead of the `api_key` and `search_api_key` beside them —
the same shape MCP headers, `secrets.env` and gateway profiles already take,
and for their reason: a file that holds a credential is a credential in
every backup, sync and pasted issue of that file. The two that hold one stay
for a release and the doctor warns on them. This is not another rank; the
variable is the answer that file gave, and everything above it still
outranks it.

The same reason decides what happens to a key the file names that no setting
reads. It is refused, with the file, the key and the nearest key it might have
been, and nothing starts until it is fixed. A file that loaded past it would
leave the default in force while the person reads their own file and cannot
see why — the one failure this arrangement exists to prevent — and a warning
is not enough, because a warning on the alternate screen is painted over and
one on stderr before a headless run lands in a log nobody tails. The doctor's
config row carries the same refusal, so that is where to read it.

## One layout everywhere

Three directories, and the same three on every platform: settings where a
person edits them, state where the program keeps what it recorded, cache for
whatever can be fetched again. The XDG environment variables decide where each
one is when they are set, and there is a conventional default under the home
directory when they are not.

There used to be a second layout — macOS read a single `Library` directory for
all three — and it cost more than it bought. "Where are my settings" became a
question with two answers and no way to tell from outside which one a given
machine would give; a Mac with an XDG directory *and* the platform one had two
config files, only one of which was ever read; and it was the one place where
the settings a person edits and the database they never open sat in the same
directory, which is exactly the distinction the other platforms were keeping.

Conventional per-platform placement is a real principle and this is a real
departure from it. It is made once, in favour of the property that a user can
predict where their own settings are — the same property the resolution order
exists for.

Windows is included in "everywhere", and is the same departure rather than a
second one: settings sit under the home directory there too, not in AppData.
It is what git, ssh and half the tools on the machine already do, the XDG
variables still decide it when they are set, and one answer to "where are my
settings" is worth more than each platform's own.

## A migration is a doctor check

Old layouts are not read. A version that changes where something lives stops
looking in the old place entirely, and what would have been a permanent
fallback becomes a check in `shhh doctor` instead.

The check states that this machine is still shaped the old way, what that
costs in the words of what the reader will find missing, and exactly what
would move. Where the change is one the program can make correctly on its own
it also offers to make it, after asking. Where it is not — two files both
claiming to be the config, and only the person who wrote them knowing which —
it says so and leaves the decision alone.

The two alternatives are worse in opposite directions. Migrating silently at
startup makes a change the reader did not ask for and cannot watch, on the one
run where they are least able to reason about it. Reading both layouts forever
means a decision that was supposed to be over is paid for on every startup,
indefinitely. Detecting costs a stat in a command nobody runs in a loop, and
the reader finds out at the moment they went looking for why something was not
where they left it.

## Editing it asks first

The interactive editor is a surface that changes your machine, so it is one
that confirms before it writes.

Diagnostics do not repair. When a check fails it *names* the fix and shows it
to you; applying it is a separate act on a screen built to ask. A diagnostic
tool that silently repairs things is one you cannot use to find out what is
wrong.

A pending migration is the single exception, and it is one because it is not a
repair: nothing is broken, the machine is merely shaped an older way, and the
change is mechanical rather than a judgement about what you meant. It is still
offered rather than made — the same confirm the editor uses, on the row that
found it — so the rule that holds is the one about asking, not the one about
diagnostics being read-only.

## A write changes one line

A write from any surface — `config set`, the editor's `[w]`, a slash command
that saves its answer, `shhh mcp add` — changes the key it was asked to
change and leaves every other byte of the file as the person wrote it: the
order of the sections, the blank lines, and above all the comments. The file
is theirs. A three-line file with a comment saying why is a record of a
decision, and a write that hands back eighty lines with the comment gone has
destroyed the record to save one value.

A key set to its zero value is taken out of the file rather than written as
zero. Unset means the default, and for a few keys zero and unset are not the
same fact — a negative round limit is a limit removed, and a written zero
beside a comment saying "unset means the default" is a value nobody chose.
The file never holds a zero the person did not type.

A file that does not exist is created holding only what was written, under
its section header. A file that does not parse is refused untouched: the
person is told, and the write is theirs to make once the file reads.

## A value is refused before it is written

A setting takes a shape — a number, a boolean, one of a few words — and a
value that is not that shape is refused, naming the key and what it wanted.
Nothing is written and the file is left exactly as it was.

The alternative is worse than it sounds, because the natural failure of a
loose parser is not an error but a *plausible* value. A retention measured in
days, given a word, becomes zero; zero is not "no bound" here but "keep
nothing", and the next startup prunes the history the setting was meant to
protect. A flag given `yes` becomes false, which is the opposite of what was
asked for and looks in the file like a decision. Both report success. Neither
is visible until the thing they turned off is missed.

The words a value may be are judged by the same code the running session
judges them with — one list of permission modes, one of reasoning levels, one
of cache lifetimes, one of containment profiles — so `config set`, the editor
and the slash commands
that save their answer all refuse the same value for the same reason. A
second list, written beside the writer for convenience, is a list that
eventually disagrees with the first, and the disagreement shows up as a value
that saved cleanly and does nothing.

A negative is a value where a key says what a negative means — no round
limit, no command timeout, an interval that never widens — and a refusal
everywhere else. Elsewhere a minus sign is a slipped finger, and what it
writes is a ceiling nothing can satisfy: a setting that reads as present and
turns its feature off.

A range is not a shape, and a number outside one is held to it rather than
refused. The inspector rail's width is the case: it takes `auto` or a column
count, and a count outside what the surface has room for is a request the
layout narrows, not a mistake — the answer to "72 columns of rail" on a
terminal that cannot seat one is the widest that fits, and the surface says
so where it reports the layout. Refusing it would leave the reader with the
width they already had and no way to tell which limit they hit. What is
refused is a value that is neither the word nor a count, because that is a
setting with no meaning at any width.

## A failed check says what it will cost you

Diagnostics report in the words of the surface where the consequence will
appear — what an approval will look like, what will run as you — rather than
naming a missing component.

"Bubblewrap not found" tells a user who already knows what bubblewrap is
something they could have guessed. What they need is what changes about their
session, and that is stated instead.

## Project context is opt-in and lives with the project

A project can carry its own context file, created deliberately — by the
command that scaffolds one, or by accepting the offer a session makes on
first contact in a checkout that has none. Neither writes it on the way past.

Most checkouts already carry one, written for something else. A directory's
instruction file is the first of `.shhh/project.md`, `AGENTS.md` and
`CLAUDE.md` that is there and says something — one file per directory, so a
project that keeps the same instructions under two names is not told them
twice, and shhh's own file wins where a project wants to say something only
to shhh. Reading only the first of those was the single thing that made a
repository the rest of the field had already instructed open as if nobody
had ever written anything down for it.

A session reads them all, from the project root down to the working
directory, and the user's own `instructions.md` beside the config file ahead
of all of them. The root is the checkout's, or with no checkout the nearest
directory above that already keeps shhh's own state; where nothing marks
where the project begins the nearest single file above is read instead, and
a boundary something did mark is never climbed past — a sibling checkout's
instructions are not this one's. Order is the whole of it: outermost first, so the file
nearest the working directory comes last and has the last word wherever two
of them disagree. That is the order a model reading top to bottom takes them
in, and it is what lets a monorepo say how the build works at the root and
how one service differs inside it, instead of forcing a choice between the
two. Each file sits under a heading naming its path, so the model can tell
which instruction came from where, and every surface that names them — the
start screen's context line, `shhh doctor`'s project row — lists them in the
same order.

They come with a bound. The set is capped in bytes, and over the cap the
files farthest from the working directory are cut first, because the nearest
one describes the directory the session was actually opened in. A cut is
stated in the heading above the file it happened to: a model following half
an instruction should be able to see that the other half existed. Nothing
here follows an `@path` import — a line like that is read as the text it is,
not as a pointer to another file.

A checkout's instruction files carry exactly the trust its context file
already carried, and nothing more: they are prose that can only instruct,
read from the checkout the same way and behind the same gate. The user's
`instructions.md` is the user's own writing rather than a checkout's, so it
asks nothing of the project and is read wherever shhh runs.

What a checkout carries layers over what the user has: its agent profiles, its
MCP servers and its skills, each shadowing the user's by name, those
instruction files, and its settings, which merge over the user's key by key
(*Two files, one resolution order*). A value the checkout overrides is said to
be the checkout's by the surface that shows it rather than shown as the winner
alone — otherwise a user reads their own configuration and cannot see why it
is not what they set.

## The mechanism is code, its wording is configuration

A session interrupts its own turns. It asks a long one what it has got, and
it tells one that has wandered which instruction it was judged against. What
those interruptions are for, when each fires, and that a steer asks rather
than accuses are decisions about the product, and they stay in the program.
How many rounds pass, how far the interval widens, how much of the
instruction is quoted back, and the sentences themselves are none of those
things, and they are exactly what someone tuning a session has to be able to
change. So they come out: four numbers as keys, and the wordings as files.
The same line divides a backlog run: what each step is
for is a wording and how its answer is read back is not, so the step
instructions are files too and the marker lines the run parses are appended
after whatever the file said
([`todo.md`](todo.md#the-stage-prompts-are-yours-to-edit)). The keys below are
the steps of the run a checkout of code gets; a project whose run has steps of
its own has a wording per step of its own, found by convention under a prompts
directory rather than by a key here.

The line is drawn there because of what it costs to be wrong on either side.
A wording in the program can only be changed by a build, and a change nobody
can afford to make is a change nobody makes. A mechanism in the file can be
half-configured into a shape the program was never written for, and the
failure arrives in a session, in front of a user, rather than in a review.

**A wording that cannot be read stops the session.** Not a warning and not a
fallback: the path, the reason, and no session. The failure this guards
against is a session running the built-in steer while the person who wrote
the path believes it is running theirs — and then a fortnight of comparison
across two groups of sessions that are in fact one group. Nothing read from
the record afterwards recovers from that, so it fails at the start, where the
person who wrote the path is still watching.

An empty file is that failure wearing a disguise, and is refused the same
way. Nothing has an empty wording to send, so a file a truncated write left
with nothing in it would silently mean "not configured" — the built-in words
back in place, and a record that says the session overrode nothing.

**A wording is a file whose presence is the override.** A file named for the
wording, in a prompts directory, is that wording — nothing has to point at
it. Three directories can hold one and the most specific wins: the trusted
checkout's own `.shhh/prompts/`, then a path a `[prompts]` key names, then a
`prompts/` directory beside your settings file. A key that names a file which
is not there is still an answer and a wrong one, so it stops the session with
the path rather than quietly falling through to the directory below.

The convention came after the keys and mostly replaced them. With four
wordings and no directory for them, a key naming a path was the right shape;
with eleven and a checkout scope, a file that is the override is what agent
profiles and skills already do, and it is what lets one command write a
directory somebody edits without also editing the settings. The keys stay for
the case they were built for: one wording, kept somewhere else.

**A checkout gets the convention and no key at all.** `[prompts]` is one of
the few tables a repository's own settings may not set — it points at a file
anywhere on the machine, and a path in a checkout is a path in every clone of
it. What a repository does instead is put the wording where the wording
belongs, in its own `.shhh/prompts/`, and inside that checkout those files
beat the person's own, per key, so a project that replaces one leaves the
rest of the person's answers standing. They are behind the checkout's trust
answer, like everything else a clone asks a session to load, and the start
screen names the ones it handed over.

**One command writes the file you would have written.** `shhh config init`
creates a settings file holding every key, commented out at its default with
the sentence that says what it decides above it, and a `prompts/` directory
holding every wording as a file already carrying the built-in text. Editing a
prompt is then opening a file, rather than finding the built-in prose in the
program and a key to point at it. `--project` writes the checkout's pair
instead, with the keys a checkout may not decide left out.

It is the one deliberate write-everything act, and it is not the flattening
the targeted rewrite exists to prevent: a commented default is not a value in
the file. It also never writes over anything. A file already there — the
settings, or one wording — stops the whole command, which names what is in
the way and offers `--stdout`: the same scaffold printed with your own values
filled in uncommented, so expanding a three-line file is a print and a paste
and never a rewrite behind you.

The placeholders a wording takes are named in the comment above its key in
the settings file and never in the wording's own file, because that file is
sent to the model as written.

**A wording is part of what a session was sent, so it is part of the
fingerprint.** The record already fingerprints the system prompt as it went
out, which is what puts sessions on either side of an edit to it. A steer file
is sent to the same model in the same session by the same mechanism, and an
override that did not divide the record the same way would be a dial with no
instrument on it. A session that overrode nothing fingerprints exactly as it
did before, so turning the ability on divides nothing by itself.

A file holding the built-in text divides nothing either. It asks the model
for exactly what the built-in asks, and a scaffold that put every session
after it in a different group from every session before it would be dividing
the record on a change nobody made. Whitespace around the edges of the file
does not count, because that is what an editor adds when it saves one.

**The substitutions are checked before anything runs on them.** A wording is
prose with a few values dropped into it — the rounds that have gone, the
instruction to quote back — and a misspelled one is invisible: it reaches the
model as literal punctuation, the value it stood for never arrives, and the
wording still reads like a wording. So a file naming a substitution that does
not exist is refused the way an unreadable one is, and so is one naming a
substitution that belongs to a different wording. Some are sent exactly as
written and take none at all, which makes any of them a mistake there.

## A failure is written down

A refused request is a row on the screen for as long as the screen lasts.
The log is where it is still legible in the morning, and in another pane
while the request is failing — which is the case it exists for: you start a
tail, you run the thing that breaks, and you watch it break.

It holds what has no other surface. Every request a provider refused, in the
words the failure taxonomy already reads it in, and whatever a library writes
to the standard logger while the alternate screen is up — which would
otherwise be painted over a running session. It does not hold the
conversation: that is in the state directory too, and a log that repeated it
would be a second transcript with none of a transcript's structure.

The mechanisms that fail quietly write here too, and they are the reason to
open the file at all. A permission classifier that used up its attempts and
fell back to asking; a session reading that timed out, leaving the block on
the rail standing and stale; a language server that never completed its
handshake; an MCP server that would not connect; commands running with
nothing containing them because this host offers no mechanism. Each of those
changes how a session behaves and none of them stops it, so the symptom is
always something else — more approval cards than usual, navigation answering
out of its fallbacks, tools that are simply absent. Each line names the
mechanism and what went wrong with it, and stops there. It does not repeat
the failing component's own words: where those are a provider's, the refused
request has already written a line of its own here, and where they are a
server's or this host's they are on the screen and in `shhh doctor`, in front
of the person who can act on them. They are also where the paths and the
command lines are, and this file is shared between sessions and outlives all
of them — a diagnostic is not the place to accumulate what a machine was
pointed at.

Nothing is written until something goes wrong. A machine where nothing has
gone wrong therefore has no log file at all, and the reader is told that
rather than shown an empty file shhh made for itself on the way past. Once
there is one it is bounded, and the older generation is dropped when it fills:
a diagnostic that fills a disk is a fault of its own.

Two sessions write to one file, which is what settles how the writing works.
The bound is checked for each line rather than once when a session starts, or
an afternoon of being refused by one provider would have no bound at all; and
a session does not hold the file between lines, or it would go on writing
into a generation another session had already set aside — the failure you
opened the tail for being exactly the one that went missing. A line that
cannot be written is dropped rather than announced, because the only place to
announce it is the screen the session is drawing on, and the next line tries
again: a directory that could not be reached a minute ago can be reached
now.

It is state, so it lives where state lives, and it inherits what that costs
and what it buys — one layout on every platform, and the containment deny
mask, which is why an approved command cannot read the log any more than it
can read the database beside it.

## The keymap file

Keys move in a file of their own, `keybindings.toml`, beside `config.toml` and
the agents and skills directories. It is not a section of the config: a
setting is a value the program reads while it is running, and a keybinding is
read once before anything draws, because the hint that offers a key and the
handler that answers it are one declaration and they have to move together.

A file names a key by where it lives in the keyboard shhh ships — the surface
group, then the act — and gives the keystrokes it should answer to, one as
text or several as a list. Nothing else is in it. The words beside a key stay
the program's, because a key that moved is still the same act, and which
surface a key belongs to is not a thing a file gets to decide.

It is the user's file and a checkout does not layer one, unlike the settings
above. A repository that could move a key would be a repository deciding what
the keys under someone's hands do, which is a worse trade than project
settings already make.

**A file is applied whole or refused whole.** Half a file is a keyboard nobody
has ever seen, and the person holding it would be debugging against a document
describing neither their file nor the default. A refusal says what it refused
and the keyboard shhh ships runs instead — out loud, rather than swallowed: a
session quietly running neither keyboard is the failure this guards against.

Two things a file cannot do.

**It cannot leave one surface answering a keystroke with two acts.** That is
the rule the register of keyed surfaces exists to make checkable
([why](../interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard)),
and a file that broke it would raise nothing at the keyboard — it would
produce whichever act the code happened to check first, on one screen and not
the one beside it. The refusal names the surface, the keystroke and both acts.

**It cannot put a destructive act on a movement key.** Ending a process,
deleting a saved chat, forcing an undo past its confirm: these are the keys a
reader must not reach by reflex, which is why the agent manager kills on a
capital and gave up the lower-case letter to do it
([the rule](../interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard)).
A file that took the letter back would undo that decision from outside the
program, where no review sees it.

**It cannot move a key onto a chord the desktop or the terminal takes.**
Mission Control has `ctrl+↑`, Windows Terminal has `alt+enter`, tmux has
`ctrl+b`, and a hint offering any of them is a false offer on the machine the
reader is holding. The refusal names the chord, who takes it and where
([the inventory](../interface/reserved-keys.md)); a modifier on the arrow,
function and navigation rows is reported everywhere and taken by nobody,
which is where a file that needs a third key should look.

Modal editing is not this. A file moves keys inside the keyboard that already
exists; it does not add a mode with a keyboard of its own.

## Every setting

Every key is declared once, and this table is that declaration printed. The
config screen's rows, what `shhh config list` prints and the parser that
judges a written value all come from the same place, so a key exists nowhere
it is not also visible and adding one costs one entry rather than three
edits.

The default column is what stands when nothing sets the key — not a literal
in the file, which is why some of them are sentences. A key whose name is in
brackets in that column has no value of its own until something else decides
it. Unless a row says otherwise, a key is a file or the default: there is
no flag and no environment variable for it.

Two tables are not here because a key is the wrong shape for them.
`[mcp.servers]` is a definition per server and `shhh mcp` is the surface that
knows it; the profiles under `[agents]` are one key per role, which the
`[agents]` table below states as the shape it is.

The same declaration is what `shhh config init` writes out as a file: every
key here, commented out at its default, with its sentence above it. It is the
answer to reading a table of a hundred keys and wondering which of them your
own file could hold.

<!-- BEGIN generated settings reference — written by `make docs` from the settings table; edit the table, not this. -->

**`[provider]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `default` | text | `openai` | Which provider a request goes to: a built-in one, or a gateway profile from `shhh providers`. `--provider` and `SHHH_PROVIDER` are read ahead of the file. |
| `model` | text | (the provider's own default) | The model a session runs on. `--model` and `SHHH_MODEL` are read ahead of the file. |
| `api_key` | text | (from the environment) | The provider key itself, which puts a copy of it in every copy of this file; `api_key_env` is the form to prefer. `--api-key` and `SHHH_API_KEY` are read ahead of the file. It is a credential: the listing says whether it is set, never what it is. |
| `api_key_env` | variable | (the provider's own variable) | The environment variable the provider key is read from at start, so the file names the key instead of holding it. It is read ahead of `api_key`. |
| `base_url` | text | (the provider's own) | Where the provider's API is, for a gateway or a self-hosted endpoint. `SHHH_BASE_URL` is read ahead of the file. |
| `name` | text | (the provider's own) | What the provider is called on screen, for a gateway that fronts several. |
| `reasoning` | word: `off`, `low`, `medium`, `high`, `xhigh`, `max` | `medium` | How hard the model thinks before it answers; the level is fitted to each model, so a rung it lacks lowers to the one it has. `--reasoning` and `SHHH_REASONING` are read ahead of the file. |
| `cache_ttl` | word: `5m`, `1h` | `1h` | How long the opening a session repeats every round stays cached between rounds. |

**`[behavior]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `silent_mode` | true/false | `off` | Print the generated command and nothing else, for a shell that pipes it somewhere. |
| `shell` | text | (your login shell) | The shell commands are run through. |
| `context_max_tokens` | number | 8000 tokens | The token budget for the shell context a generated command is written against. |
| `max_tool_rounds` | number | `150` | How many consecutive tool rounds one turn may take; a negative removes the cap for every run in scope. |
| `tree_check` | true/false | `on` | Tell a turn when the working tree moved in a way its own edits do not explain. |
| `command_timeout_seconds` | number | `600` | How long one command the assistant runs may take before it is cancelled; a negative removes the ceiling. |
| `safety_warnings` | true/false | `on` | Say what a destructive command will do before it is approved. |
| `system_prompt_extra` | text | (nothing) | Text appended to every system prompt. |
| `command_allowlist` | list | (empty — every command asks) | Command prefixes that auto-approve in a session; a safety-flagged command always asks anyway. |
| `command_denylist` | list | (empty — nothing is refused in advance) | Command prefixes refused in every mode; read before the allowlist, and no approval can allow one. |
| `read_only_commands` | list | (the built-in inspection list alone) | Commands added to the built-in inspection list that runs without asking; entries skip the built-in flag guards. |
| `read_only_auto` | true/false | `on` | Run the built-in inspection list without asking; off makes a read prompt like anything else. |
| `scope_dirs` | list | (the directory the session opened in) | Directories added to a session's working scope at start, beside the one it was opened in. |
| `default_mode` | word: `manual`, `accept-edits`, `auto`, `plan` | `manual` | The permission mode a session starts in. |
| `mode_cycle` | list: `manual`, `accept-edits`, `auto`, `plan` | manual, accept-edits, auto, plan | The order the mode key walks the permission modes in. |
| `classifier_model` | text | (the provider's small model, or the session's own) | The model auto mode's permission classifier runs on. |
| `classifier_timeout_seconds` | number | `30` | How long one classifier request may take. |
| `classifier_max_tokens` | number | `8192` | The ceiling on a classifier response, the reasoning it does before answering included. |
| `classifier_retries` | number | `1` | How many extra attempts an invalid or failed classifier response gets before it fails closed. |
| `memory_disabled` | true/false | `off` | Turn durable memory off: nothing is injected and the remember tool is not registered. |
| `memory_max_entries` | number | `20` | How many memories are injected into one session's system prompt. |
| `memory_max_tokens` | number | `1200` | The token budget for the injected memory block. |
| `check_in_interval_rounds` | number | 40 rounds | How many tool rounds pass before a turn is asked to take stock. |
| `check_in_max_doublings` | number | 2 doublings | How far that interval widens over one turn; a negative fixes it, so a long turn is asked at the same rate throughout. |
| `provider_retries` | number | 3 attempts | How many times one stall — a rate limit, an overloaded provider, a connection that died before a token — is asked again before the failure stands; zero is a machine that would rather see the failure than sit out a wait. |

**`[sandbox]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `require` | true/false | `off` | Refuse an assistant command where no containment mechanism is in force, rather than running it unconfined. `--require-sandbox` is read ahead of the file. |
| `profile` | word: `workspace`, `workspace-netless` | `workspace` | What a contained command may reach: the workspace with the network untouched, or the same with the network closed. |
| `deny_extra` | list | (the built-in deny mask alone) | Paths added to the built-in deny mask; a contained command sees them as empty. |
| `write_extra` | list | (the workspace, scratch and the toolchain caches) | Paths writable inside containment, beside the workspace. |
| `container_engine` | word: `podman`, `docker` | (auto-detected, a rootless engine first) | Which engine runs a container sandbox. |
| `container_image` | text | (unset — container sandboxes are unavailable) | The digest-pinned image (name@sha256:…) a sandbox container runs. |
| `image_allowlist` | list | (any digest-pinned image) | The only sandbox images that may run, as digest-pinned references. |
| `container_memory` | text | `2g` | The memory ceiling on a sandbox container. |
| `container_cpus` | text | `2` | The CPU ceiling on a sandbox container. |
| `container_pids` | number | `256` | The process ceiling inside a sandbox container. |
| `container_ttl_hours` | number | `24` | How long a sandbox container may live before startup reconciliation reaps it. |
| `require_isolation` | word: `process`, `container`, `vm` | (none required) | Refuse to create a sandbox below this verified level; a requirement that cannot be verified fails rather than downgrading. |

**`[web]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `allow_private` | true/false | `off` | Let a fetch reach private, loopback, link-local and CGNAT addresses, and lift the 80/443 port list; cloud metadata stays blocked either way. |
| `fetch_max_bytes` | number | 2 MiB | The download ceiling on one fetch. |
| `fetch_timeout_seconds` | number | `30` | How long one fetch may take, redirects and the body read included. |
| `cache_ttl_minutes` | number | `60` | How long a cached response stays fresh. |
| `search_provider` | word: `brave` | `brave` | Which backend the web_search tool asks. |
| `search_api_key` | text | (unset — web_search is not registered) | The search backend's key itself, which puts a copy of it in every copy of this file; `search_api_key_env` is the form to prefer. It is a credential: the listing says whether it is set, never what it is. |
| `search_api_key_env` | variable | (unset — web_search is not registered) | The environment variable the search backend's key is read from at start, so the file names the key instead of holding it. It is read ahead of `search_api_key`. |

**`[lsp]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `disabled` | true/false | `off` | Turn the language-server integration off: no servers, no navigation tools, no diagnostics. |
| `request_timeout_seconds` | number | `15` | How long one language-server request may take, the initialize handshake included. |
| `diagnostics_timeout_seconds` | number | `3` | How long an applied edit waits for the server to re-check the file; a check that lands later rides with the next tool result rather than being dropped. |

**`[appearance]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `accent_color` | text | (the palette's own) | The accent the surfaces are painted with. |
| `theme` | word: `auto`, `dark`, `light`, `charm` | `auto` | Which colour table every surface draws with: `auto` asks the terminal what its own background is and takes the table chosen for that ground, or name one. |
| `mouse` | true/false | `on` | Terminal mouse reporting: the wheel scrolls the transcript and shhh selects text itself. Off leaves the terminal its native click-drag selection. |
| `notify` | true/false | `on` | Raise a desktop notification when a turn stops while the window is not the one in front. |
| `window_title` | true/false | `on` | Name the terminal's own tab after the session. |
| `paste_lines` | number | 10 lines | The height past which a paste is staged as an attachment instead of typed into the draft; a negative turns that half of the test off. |
| `paste_columns` | number | 1000 columns | The width past which a paste is staged as an attachment; a negative turns that half of the test off. |
| `rail_width` | text | `auto` | How many columns the inspector rail takes: `auto`, which widens the rail with the terminal, or a column count for a pane you chose the size of. |

**`[history]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `retention_days` | number | 90 days | How long a recorded session is kept before startup prunes it. |

**`[chats]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `retention_days` | number | (off — a saved chat is kept until you delete it) | How long a saved conversation nobody has written to is kept before startup prunes it, with a chat's branches going when it does; unset keeps every conversation. |

**`[reports]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `retention_days` | number | 90 days | How long a generated report page is kept. |

**`[observe]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `retention_days` | number | 180 days | How long a session's record and its events are kept before startup prunes them; longer than history's window because a comparison reads back across a change made a quarter ago. |

**`[otel]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `endpoint` | text | (off — the record stays on this machine) | Where an OTLP collector listens, as a URL with its scheme; each session is sent to it as one span when the session ends. |

**`[agents]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `model` | text | `inherit` | The model every sub-agent runs, unless its role says otherwise; `inherit` is the session's own. |
| `profiles.<role>.model` | text | (the sub-agent model) | The model one role runs — the role is the key's own segment, so any role a spawn names can have one. |
| `max_concurrent` | number | `3` | How many children may run at once; further spawns queue. |

**`[summary]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `model` | text | (the provider's small model, or the session's own) | The model that takes the periodic reading of the session. |
| `interval_rounds` | number | `10` | How many tool rounds pass between two readings; higher is cheaper and staler. |
| `min_gap_seconds` | number | `20` | The floor on wall-clock time between two readings, so a burst of fast rounds cannot rewrite the block repeatedly. |
| `timeout_seconds` | number | `20` | How long one reading may take. |
| `max_tokens` | number | `8192` | The ceiling on a reading's response, the reasoning it does before answering included. |
| `disabled` | true/false | `off` | Turn the reading off entirely: no requests are made and the rail draws no summary block. |
| `headless` | true/false | `on` | Take readings in a non-interactive run, which is the surface with nobody in front of it. |
| `subagents` | true/false | `off` | Take readings in each spawned child; a fan-out of six is six more readings per interval. |
| `intervene_cooldown_intervals` | number | 2 readings | How many reading intervals pass between two verdict-driven interventions. |
| `steer_target_chars` | number | 400 characters | How much of the instruction a steer quotes back to a drifting turn; a negative quotes it whole. |
| `title` | true/false | on when a summary model is set, off otherwise | Ask the summary model to name an unnamed session after its first turn, for the saved-chat listings. |

**`[secrets]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `env` | list | (nothing declared) | Environment variables to declare as secrets in every session: the model may use the value and never sees it. |
| `env_mask` | true/false | `on` | Keep variables whose names end in _KEY, _SECRET or _TOKEN out of the environment of the commands the assistant runs, unless secrets.env declares one. |

**`[mcp]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `disabled` | true/false | `off` | Start no MCP server and register no MCP tool, whatever the file defines. |
| `startup_timeout_seconds` | number | `20` | How long each MCP server has to connect and list its tools; one that has not answered is reported and left out. |

**`[prompts]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `steer` | path | (the built-in wording) | A file whose contents replace the message a drifting turn is given; it may place `{{target}}` and `{{reason}}`. |
| `check_in` | path | (the built-in wording) | A file whose contents replace the message a turn that has reached its interval is given; it may place `{{rounds}}` and `{{finished}}`. |
| `summary` | path | (the built-in wording) | A file whose contents replace the reading instruction the summarizing model is sent. |
| `classifier` | path | (the built-in wording) | A file whose contents replace the instruction auto mode's permission classifier is sent. |
| `todo_standards` | path | (the built-in wording) | A file whose contents replace the sentence every step of a backlog run that changes the tree carries. |
| `todo_research` | path | (the built-in wording) | A file whose contents replace what a backlog run's research step is told; it may place `{{item}}` and `{{answers}}`. |
| `todo_implement` | path | (the built-in wording) | A file whose contents replace what a backlog run's implement step is told; it may place `{{item}}`, `{{plan}}` and `{{answers}}`. |
| `todo_review` | path | (the built-in wording) | A file whose contents replace what a backlog run's review step is told; it may place `{{item}}`, `{{plan}}` and `{{diff}}`. |
| `todo_review_task` | path | (the built-in wording) | A file whose contents replace what the reviewer sub-agent is asked; it may place `{{item}}`, `{{plan}}` and `{{diff}}`. |
| `todo_remediate` | path | (the built-in wording) | A file whose contents replace what a backlog run's remediate step is told; it may place `{{item}}` and `{{findings}}`. |
| `todo_commit` | path | (the built-in wording) | A file whose contents replace what a backlog run's commit step is told; it may place `{{item}}`. |

**`[hooks]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `disabled` | true/false | `off` | Fire no hook at any seam, whatever the files define. |
| `timeout_seconds` | number | `30` | The longest any hook may take, and the cap on a hook's own timeout; it can be raised no higher than the command timeout, and there is no way to turn it off. |

**`[todo]`**

| Key | Takes | Default | What it decides |
|---|---|---|---|
| `root` | path | (the project you are in, else the global backlog) | Where the backlog lives when the working directory is part of no project; a session inside a project always reads that project's backlog. |
| `profile` | text | `code` | The profile this project's backlog is written in and worked under: what an item is called, which fields it carries, and which steps a run takes; it is looked for in this checkout, then beside your settings, then among the ones built in. |
| `commit` | true/false | `on` | End a backlog run in a commit; off leaves the change in the working tree, which is the answer for a directory that is not a repository. |
| `item_timeout_minutes` | number | 0 (no cap) | How long one item of a sprint may take before it is blocked and the sprint stops; zero leaves it uncapped. |
| `groom_stale_commits` | number | the profile's own | How far an item's last reading may fall behind — in whatever the profile measures staleness by — before the backlog says so; unset keeps the profile's own threshold, and a negative number turns the warning off. |

<!-- END generated settings reference -->

## Related

- [`providers.md`](providers.md) — provider and gateway settings
- [`../architecture.md`](../architecture.md) — why resolution is uniform
