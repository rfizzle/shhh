# Containment

Approval decides whether something runs. Containment decides what it can reach
once it does. They are independent on purpose: a command you approved is still
a command that can be wrong.

## Scope is the set of directories the work may reach

A session starts scoped to the directory it was opened in. That is the right
default and the wrong one the moment the work spills over — a config directory
the project reads, a sibling checkout, a vendored dependency outside the tree.

Before this existed the only answers were to edit a config file and restart,
or to watch contained commands fail on paths that were plainly part of the
job. Neither is a decision; both are obstacles.

Adding a directory is a permission grant and goes through the same machinery
every other grant does: you ask for it, or you answer the card that appears
when an action reaches outside. What the scope holds is what OS-level
containment makes writable and what edits may touch without asking again — one
list, not several that agree by convention.

### Two classes of directory never come along

- **Refused.** A path behind the deny mask cannot be granted at all, by any
  key. The mask cannot be disabled, so neither can this.
- **Sensitive.** A home directory, a system root, another tool's credential
  store. It can be granted, but only by a person answering for it — never by a
  permissive mode and never by the classifier. For a credential store the
  grant is also what makes it readable at all.

The second class is the interesting one. It exists because "can be granted"
and "can be granted without a human" are different questions, and a mode that
was turned on for convenience must not be able to answer the second one.

## The deny mask is not configurable

Credential stores and shhh's own state are unreachable, always. There is a
setting to add to the mask and none to subtract from it.

A configurable mask is a mask that gets configured away — by a user
troubleshooting something unrelated, by a script, by a session that argued
persuasively. The protection is only worth having if it cannot be turned off,
so it cannot be.

What is behind it, always:

- `~/.ssh`, `~/.aws`, `~/.config/gh` — the signing keys, the cloud
  credentials, the forge token.
- `~/.netrc`, `~/.gnupg`, `~/.password-store`, `~/.secrets` — the plaintext
  passwords curl and git read without being asked, the GPG home, the password
  store. Nothing legitimate writes to any of these, so nothing needs a way
  back: the working scope refuses to hold them at all.
- shhh's own config and state directories, so a contained command cannot read
  the session's database or edit the settings it runs under.

Another tool's credential store is a different question, because a session
sometimes has honest business with one: `kubectl get pods` is a command a
person asks for. So `~/.kube`, `~/.docker`, `~/.azure`, `~/.config/gcloud` and
`~/.gem` are read the way they are written — masked while nothing has granted
them, readable and writable once the directory is in the working scope, which
only a person can put it in.

That is one grant, not a second mechanism, and it is still true that nothing
subtracts from the mask: the answer to "the build needs the registry login" is
`/add-dir ~/.docker`, said once and out loud, and it is the same sentence that
lets the build write there. `/sandbox doctor` lists the mask as it stands at
the moment it is asked, so a store that is hidden is named where the reader is
already looking for it.

The grant is the store and not the file in it. A mask cannot be given a hole —
a policy whose writable path sits inside a masked one is refused rather than
weakened — so granting a subdirectory of a store makes the whole store
readable, and the card says so before the grant rather than after it.

## The temporary directory is the session's own

Everything else about containment is a wall with the workspace on one side of
it. `/tmp` was a hole straight through: a writable bind of the host's shared
temporary directory, on both mechanisms, because builds need scratch space.

A shared scratch directory is a channel in both directions. A contained
command can read what an uncontained process left there — a token some other
tool cached, a socket it is listening on — and can leave something an
uncontained process will later read and act on. Nothing else about the
boundary is worth much while one directory is open in both directions, and the
approval card's answer to "what can it reach" did not mention it.

So the temporary directory is the session's own. Where the mechanism can give
a command a filesystem of its own it gets an empty one, and where it cannot it
gets a directory nothing outside this session may read. `TMPDIR` points at it
either way, so a build that asks the usual question gets the usual answer, and
the host's directory is not reachable at all. The toolchains this costs
nothing: Go and npm keep their caches under `HOME`, not in the temporary
directory.

**A tool that really does need the host's `/tmp` is a grant like any other.** A
language server's socket, a build cache somebody points at: `/add-dir` the path
and it comes back, and only that path comes back. That is the same sentence
that makes it writable, said once and out loud, and it is the only way the
directory returns.

The scratch does not survive the command that wrote it where the mechanism
hands out a filesystem of its own, which is the one behaviour a person can
notice: two commands that pass a file to each other through `/tmp` are two
commands that now have to pass it through the workspace. `/sandbox doctor`
names the directory in force, so the answer is where the question is asked.

## A contained command carries almost no environment

The mask decides what a command can read. It cannot decide what the command
was told, and what it was told used to be everything: the whole environment
the session was started from, crossing into containment untouched.

That is worse than it sounds. `SSH_AUTH_SOCK` is the address of an agent
holding keys the mask has just made unreadable — and an agent will sign for
anything that can reach its socket, so a private key hidden on the filesystem
is still a private key in use. The same is true of every token a shell profile
exports for convenience.

So the environment is rebuilt rather than filtered. A contained command gets
where to find programs, whose home this is, what language to speak, the
caches that are already writable, and the session's own secrets — the ones
somebody named, which is the whole of how a value gets on the list. Nothing
else travels, including variables shhh has never heard of, which is the class
the leak came from.

**A list of what may cross, never a list of what may not.** A mask has to
know the name of the thing it is stopping, and the next tool to invent a
credential variable will not be on it.

Naming one is how anything else gets on the list, and there are two ways to
do it: the person declares a session secret, or the assistant passes a
variable to the one process it is starting. Both widen the list by name and
for nothing else, and where the two collide the person's value is the one
that arrives.

Dropping the address is most of the answer and not all of it, because a path
is a convention as much as an address: the agent's socket is masked as well,
so a command that guessed where to look finds nothing there.

Where the mechanism has namespaces to give, they go with it, for the same
reason and at no cost: on Linux a contained command gets its own process, IPC
and hostname namespaces, so it cannot see, signal or talk to the rest of the
machine. None of this needs anything configured and none of it can be turned
off.

## Containment can be required

Where no mechanism is available, an approved command runs as you, and every
surface says so. That is honest, and honesty is not the same as a decision:
nobody chose it, it is just what the host turned out to be.

The requirement is how it becomes a choice. With it, a session on a host with
no mechanism refuses the assistant's commands outright rather than running
them bare, and the refusal carries what `shhh doctor` would have said about
installing one — the same wording, because being told twice in two spellings
is two things to keep true.

**The refusal is the model's to read, and no card is drawn for it.** A card
exists to put a decision to a person, and there is no decision left when the
answer is the same whichever key they press. What the model gets back is why
it could not run and what would fix it, which is the one thing it can act on.

**A sub-agent's commands are refused too.** A child has no card to draw and
nobody in front of it to draw one for, which makes it the path a requirement
that stopped at the session would be walked around by — one fan-out and the
work is running bare again.

**A command you typed is never refused by it.** `/run` and `!` are yours, they
are never contained, and a requirement about the assistant's commands has
nothing to say about them. The requirement is off by default: a machine with
no bubblewrap is still a machine somebody has to work on.

## A cancelled command takes its children with it

Every captured command is a shell, and the work is that shell's children.
Cancelling used to signal only the shell, so the build, the watcher or the
test runner underneath went on running with nothing left watching it. A
session interrupted a few times left a few of those behind, each still holding
the port or the lock the next attempt needed.

A command now runs in its own process group and cancelling signals the group.
Interrupt first — that is what the reader pressed, and it is the signal a
compiler or a test runner knows how to stop cleanly on — then kill, after a
short grace, for whatever ignored it.

Underneath both there is a bound on the wait itself. A surviving relative that
inherited the output pipe keeps it open after its parent is gone, and reading
that pipe is exactly what the runner is blocked on, so a command that is
already dead could still hold the turn open.

## A command that will not finish is not waited on forever

There is a ceiling on how long one command the assistant runs may take.
Reaching it is not the ordinary case and is not meant to be: the number is far
past a full test suite, a cold dependency install or a release build, so what
it actually catches is the command that was never going to finish — one
waiting on a prompt nobody will answer, a watcher started in the foreground, a
network read with no timeout of its own.

**A command the reader typed is never bounded by it.** They are in front of
the session and chose to run the thing; the key that cancels it is their
ceiling, and a limit that cut their build short would be the tool overruling
them about their own machine.

The ceiling matters most where there is nobody to do that. A headless run and
a sub-agent both have no reader, and a command that hangs there does not hang
one command — it holds the whole run until something outside kills it, and the
parent waits on a report that is never coming.

**Being stopped and having failed are different, and are said differently.**
What comes back from a killed command is whatever it printed and an exit code
that says only that it did not exit normally, which reads exactly like a
broken command — so the reason is appended in words: that it did not fail, it
did not finish, and what to do about it. Without that the model debugs a
command that was working.

**A command that is still printing is moved, not killed.** The ceiling catches
two different things. One is the command that was never going to print again;
killing that costs nothing. The other is a dev server, a watcher or a log
tail started in the foreground, which is working perfectly and merely never
going to return — and killing that throws away a running server for a mistake
that is cheap to undo the other way. So a command that has printed something
by the time its ceiling arrives is handed to the process supervisor as it is,
still running, under a name taken from the program it runs; a command that has
printed nothing is stopped as before. Nothing is respawned: a port already
bound or a build already half done makes a second start a different command.

What comes back names the process, because the model already has the verbs for
one — read its output, write to it, stop it — and the whole point of moving it
rather than killing it is that those verbs now apply. It is stopped when the
session ends, like anything else the session started, and it counts in what
the session reports as running.

**One command's output cannot take the session's memory with it.** Output is
held to a bound as it arrives, far above the few thousand bytes any reader or
model is shown, so a build with a verbose flag left on is capped while it runs
rather than after it finishes. What is kept is both ends — the bound's first
half from where the command started, its second half from where the command
stopped — because a command says how it went in its last lines, and output cut
to its opening would report a long build by its warmup and send the model to
run the whole thing again with a pipe into `tail`. What was dropped is the
middle, and it is counted there in the output, because a silent gap reads as
the command having gone quiet.

## A started process is contained too

There are two ways for the assistant to run something: a command that returns
when it is done, and a named process that keeps running while the work goes
on around it. They spawn the same shell and reach the same filesystem, so the
mechanism wraps both. A process is the one that outlives the call that
started it, which makes it the part of a session still running when you ask
what is contained — and the containment report counts it there.

Where the mechanism cannot wrap a start, the start is refused and the
refusal names the mechanism. This is the same rule the ordinary path has:
a command that was going to be contained never quietly runs bare instead.
Falling back would be worse here than anywhere else, because a process
lasts — every surface would go on saying the session is contained for as
long as the one thing outside it kept running.

A start can pass variables of its own — the port to listen on, the mode to
run in. Under containment those go into the policy rather than onto the
spawn, because the mechanism clears whatever the spawn was given and rebuilds
the environment from the policy alone. Left on the spawn they are dropped
without a word, and the failure that follows points nowhere near its cause: a
server told to listen on 3001 comes up on 3000, the probe finds nothing, and
what is being debugged is a process that is running fine.

A process can also be given a terminal instead of pipes, for the commands
that behave differently when nobody appears to be watching: a REPL that only
prompts on one, a tool that asks for a passphrase, a runner whose progress
output goes quiet down a pipe. It is asked for and never assumed, because a
terminal has a single stream and the split between a command's output and its
errors is gone the moment one is used. The mechanism wraps such a process
exactly as it wraps any other, and where the platform has no terminal to give,
the start says so in a sentence rather than pretending.

The exception is a run whose commands go inside a disposable container. A
process cannot follow them in: what would be left holding it is the client
that started the exec rather than the process itself, so stopping it would
leave something running in a container nobody is watching. Such a run
refuses a start rather than spawning it on the host, which is the same
answer for the same reason — outside the container is bare. The command
ceiling answers to the same fact: there is nowhere to move a command to, so
one that reaches it there is stopped whether or not it was still printing.

## What is reported is what is in force

Every surface that mentions containment reports the mechanism actually
containing the process, not the one that was requested, and asks the path
that will run it rather than the one beside it. Where nothing is containing
it, the surface says so in those words and does not soften it.

A tool that reports its intended security posture rather than its actual one
is worse than a tool with none, because it is believed. Where containment is
unavailable, the honest answer changes the user's behaviour; a reassuring one
does not.

## Related

- [`approvals-and-safety.md`](approvals-and-safety.md) — deciding whether it runs
- [`../interface/surfaces.md`](../interface/surfaces.md) — how it is reported
