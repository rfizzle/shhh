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

A grant reaches the model when it is made. The system prompt names the scope
once, at the start of a session, and it is not rewritten mid-session, because
that would pay for the cached prompt again; so a directory added or dropped
with `/add-dir` is said to the model as a message of the session's own, at the
next round boundary if it is working. Otherwise the model goes on asking for a
directory it already has, or steering around one it may now use. A grant made
on the card is not announced: it answered the model's own call, and that call
running is the answer. The next session is told the scope as it stands in its
prompt, so a grant is said once.

### Asking for a directory is the way through

A model that meets the boundary without knowing it can be moved treats it as
an obstacle to engineer around: it copies the files it needed into the
workspace, points `HOME` or a tool's cache at a directory it can write, or
reaches the path through some other tool that was not refused. Each of those
is work the person did not ask for, and each is worse than the one sentence
that would have done it — "this needs `../shared`, may I have it?" — because
the grant is cheap, reversible with `/add-dir drop`, and exactly what the
scope is for.

So the prompt says that asking is the expected move and not a failure: when
an edit is refused, or a contained command fails on a path outside the
scope, the model names the directory and why the task needs it and asks for
`/add-dir`. Where the path was only somewhere to put scratch output, the
answer is the workspace and nobody is asked anything.

A `-p` run and a served session have no way to widen the scope once they are
running, so they are not told to ask for a command they cannot receive: they
finish what the scope allows and say which directory the next run needs with
`--add-dir`.

### Two classes of directory never come along

- **Refused.** A path behind the deny mask cannot be granted at all, by any
  key. The mask cannot be disabled, so neither can this. The model is told so
  in those words and is not pointed at `/add-dir`: a refusal that names a way
  to widen the scope, for a path no widening reaches, reads as a door that
  is only hard to open, and invites the search for another way in.
- **Sensitive.** A home directory, a system root, another tool's credential
  store, shhh's own configuration and state, or a repository's store and
  hooks ([below](#the-repositorys-own-programs-are-read-only)). It can be
  granted, but only by a person answering for it — never by a permissive mode
  and never by the classifier. For a credential store the grant is also what
  makes it readable at all.

The second class is the interesting one. It exists because "can be granted"
and "can be granted without a human" are different questions, and a mode that
was turned on for convenience must not be able to answer the second one.

## The deny mask is not configurable

Credential stores that no session should ever read or write are unreachable,
always. There is a setting to add to the mask and none to subtract from it.

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

Shhh's own configuration and state begin masked too, but they are a different
case: developing shhh is a legitimate reason to change them. Grant either with
`/add-dir` for this session or `--add-dir` for one launch. A trusted checkout
can keep the same deliberate exception in `.shhh/config.toml` under
`behavior.scope_dirs`; use the absolute directory, for example
`/Users/me/.config/shhh` or `/Users/me/.local/share/shhh`. The grant is marked
sensitive because it lets a contained command change the settings and session
records it is otherwise protected from.

## A denial arrives as the command's own error

A refusal is not labelled. Seatbelt errors a masked read rather than emptying
it, so a contained command meets the boundary as an errno and describes it in
its own vocabulary — a certificate store that would not open, a cache that
could not be created, a database file that is unavailable. None of those
sentences contains the word sandbox, and both a person and a model read them
as a broken tool and debug them as one.

So the containment says it first. A session is told what contains it and what
that allows before it runs anything, and it is told that a command failing on
a path outside the working scope is the boundary speaking rather than the tool
misbehaving. That is one sentence against the rounds it costs to bisect a
program that is working correctly.

**A mask still has to be walkable.** A path is not one question to the kernel.
A program that resolves a path before opening it asks about every directory on
the way down, and a deny over a directory refuses those questions too. SQLite
is the one that matters here: its Unix backend walks a database's path a
component at a time, asking of each whether it is a symbolic link, and treats
any refusal as fatal. A scratch directory allowed *inside* a masked one was
therefore reachable in principle and unusable in practice — a plain file
written successfully beside a database that could not be opened at all, and an
error saying only that it could not be opened. Every store a contained command
touches sat behind that, which on macOS is every test run over a package that
keeps one.

What the mask gives back is the walk and nothing else. The directories between
a mask and an allowance inside it answer the one question the walk asks —
that they are there — and answer nothing else: what is *in* them is a
different operation and stays denied. Widening the mask would have worked too,
and would have traded the protection for the bug.

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

**On macOS the literal `/tmp` is not the scratch.** Seatbelt says what a
process may reach and cannot put a different directory at a path, so the only
way to make the literal `/tmp` writable there is to open the host's shared
one — the channel this section exists to close, and a door into every other
session's leftovers. It stays hidden: a tool that asks `TMPDIR` gets the
session's own directory, and a tool that writes to `/tmp` by name is refused
and needs the path granted or its own temporary-directory setting pointed at
`TMPDIR`. What the hidden directory still answers is that it exists. `/tmp` is
a link to `/private/tmp`, and a program that resolves a path before using it
asks about the target; refusing that question would be an error about a
directory that plainly is there, raised in programs that never meant to write
to it. Its contents stay unreadable and unwritable. Where the mechanism gives
the command a filesystem of its own, `/tmp` itself is the private one and a
write there simply works.

## Apple toolchain shims stay compatible

On macOS, `/usr/bin/git` is an Xcode command-line-tool shim. It resolves the
selected developer tool through `xcrun`, whose lookup cache is not controlled
by `TMPDIR` and lives in the host's per-user temporary directory. Letting that
directory back into a contained process would reopen the shared scratch
channel.

So containment resolves Apple Git before it starts, then runs the resolved
developer-toolchain binary and puts its directory first on `PATH`. A quality
gate and its children therefore use the selected Git without needing access to
the host temporary directory. Other Xcode tools remain subject to their own
filesystem needs; an exception must be a scoped grant, never a broad allowance
for the host temporary root.

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

## A contained command's network can be a list of hosts

The two profiles answer the network with a switch: every host, or none. Most
work that needs the network needs very little of it — the package registry,
the module proxy, the one API under test — and the switch makes the person
choose between a command that can reach anything and one that cannot install
a dependency.

`sandbox.allow_hosts` is the third answer. Under the `workspace` profile a
list names the only hosts a contained command reaches; an empty list is the
profile's own answer, and the netless profile does not read it at all, since
closing the network is the stronger statement and a list cannot weaken it. A
host is matched exactly, the way the fetcher's own host list is:
`registry.npmjs.org` does not cover `npmjs.org`, and a wildcard is refused
rather than read as something near it.

**Neither mechanism can name a host to the kernel, so the list is read by a
proxy.** The command gets no network of its own — an empty namespace under
bubblewrap, every socket denied under Seatbelt, whose network rules name
`localhost` or any host and nothing between — and one way out: a proxy shhh
runs outside containment, which the proxy variables in the command's
environment point at. Under Seatbelt that is one loopback port the profile
allows; under bubblewrap the namespace has no route to the host's loopback, so
the proxy listens on a socket file bound into the namespace and a small bridge
inside it carries a port there to the socket.

The proxy checks the host a request names before it resolves anything, so a
host that is not listed is never looked up; the command has no resolver of its
own to look one up with. It never follows a redirect — a tunnel is opaque to
it and a plain request is answered once and the connection closed — so a
listed host that points elsewhere hands the command an address it has to ask
for again, and is refused there. What is refused is refused in HTTP, with the
list in the answer, so the tool that asked reports the proxy's reason rather
than a dropped connection. A tool that ignores the proxy variables cannot
connect at all, which is the list holding rather than failing.

**A listed name that resolves to this machine is refused.** The proxy runs
outside containment, so whatever it dials is reached from the host's side of
the wall. It resolves a listed name before it dials anything and checks every
address the name answers with: loopback, the unspecified address, link-local —
where a cloud host's metadata service answers — or private space (RFC 1918 and
IPv6 unique-local) refuses the request with a 403 naming the address and why,
and so does a name that answers with one public address and one private one,
since either could be the one dialled. The connection then goes to an address
that was checked rather than to the name again, so an answer that changes
between the check and the dial is not the one used. Otherwise the list would
be a way from a contained command onto the host's own services by way of a
name somebody else's DNS answers for. An entry written as the address itself —
`127.0.0.1`, `10.0.0.5` — is dialled as written, because somebody typing that
address meant it.

The loopback differs between the two, because the namespace is the command's
own under bubblewrap and the host's under Seatbelt: a test server the command
starts is reachable to it on Linux, and on macOS the command reaches the proxy
and nothing else on the machine, the way the netless profile reaches nothing. On Linux the bridge holds port 3128 on that loopback, where the proxy
variables point, so it is the one port a contained command under a host list
cannot bind for a server of its own.

**A mechanism that cannot hold the list runs the switch, and says so.** A host
with no mechanism has no wall for a list to be a door in, and a disposable
container's network is a switch; there the profile's answer is what runs,
`shhh doctor` says the list is not held, and the approval card and the prompt
describe the network that is actually open. Every surface that reports the
network — the doctor's row, `/sandbox doctor`, the card's `network: 2 hosts`
and the sentence the model is told — reads the same answer.

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
work is running bare again. A child's command that would otherwise be routed to
you is refused before its card, for the reason above: an approval spent on a
command that will be refused whatever you answer is not a decision.

**A writer's commands are required to be contained, whatever the session
requires of its own.** A session's own commands are each put to you on a card,
or run under a mode you chose while watching; a writer's run in a copy of the
tree while you read something else, and nothing is in front of any one of
them. Where the host has a mechanism a writer's commands run contained anyway;
where it has none, they are refused with the doctor's fix, and the spawn card
says so before you approve the writer, so nobody learns it from a report of
refused builds. The writer is told in its own prompt that its commands will be
refused, and asked to name the ones that would verify its change instead of
spending its rounds finding out. Researchers and reviewers run no command, so
nothing changes for them. `agents.require_sandbox = false` hands a writer back
to the session's rule; `sandbox.require` on the session still wins where it is
set. Neither can be set by a checkout, because either decides the containment
itself.

**A command you typed is never refused by it.** `/run` and `!` are yours, they
are never contained, and a requirement about the assistant's commands has
nothing to say about them. The requirement is off by default: a machine with
no bubblewrap is still a machine somebody has to work on.

## A git write is not a command

A session that requires containment on a host with none still puts the
assistant's git writes — staging, a commit, a new branch, a switch — to you on
their card. They are not refused with the sentence a command gets.

The requirement is about command lines the assistant wrote: text handed to a
shell, which can do anything a shell can do, and which containment exists to
bound. A git write is not one. It is one of four closed verbs, shhh builds the
arguments, and nothing the model says reaches a shell. It is asked about at
the write tier, beside an edit, for the same reason an edit is: it changes
your checkout, and the card says how, including the way back.

What a git write runs besides git is the checkout's own programs, and a
commit's hooks above all. Whether those run is already decided, and by
something other than containment: **the checkout's trust.** A trusted checkout
runs its commit hooks, as it runs its quality suites, and neither of those is
contained on a host with no mechanism either. An untrusted one commits with
`--no-verify`, which skips its pre-commit and commit-msg hooks and no others,
and the receipt names those two as skipped. The commit card's `hooks` row
names the same two before you answer. Refusing git writes here would have the requirement decide
a question trust already answers, and the answer you would get is a commit you
could not make, with nothing in the refusal that could fix it.

What this leaves open: in a trusted checkout, a commit you approve runs its
hooks as you, uncontained, even in a session that requires containment. If
that is not acceptable for a checkout, withdraw its trust rather than rely on
the requirement.

An unattended run answers the same way: a git write goes to `--yes` or the
classifier like any other write, and the containment refusal is not consulted
for it. The model reads what the card or the run answered, as it would for an
edit.

## The repository's own programs are read-only

The other half of that: with a mechanism in force, a contained command cannot
write the places git reads a program from. The repository's `config` (where
`core.hooksPath`, `core.fsmonitor`, `gpg.program` and every filter and diff
driver are named), its hooks directory wherever `core.hooksPath` puts it, and
`info/` (which holds `info/attributes`) are read-only to it. So are the paths
that say where the store is: a `commondir` file in the store, the `commondir`
and `config.worktree` of each linked worktree, the `.git` file of a checkout
whose store lives elsewhere, and a `.git` in the workspace when the workspace
is below the checkout's top — git finds that one first.

Git runs these on the host. A commit you approve runs the hooks; the reading
of the tree that happens between rounds, and the read-only `git` tool, run
`git status`, which runs an fsmonitor and a clean filter from the config. None
of those is contained, and the tree reading is asked of nobody. A contained
command that could write one of these paths would be a bounded command
leaving an unbounded one behind for the next host-side git to start, as you.
Trust decides whether a checkout's own programs run; this is what stops a
command the session ran from becoming one of them.

Reads stay open, and so does the rest of the store: `git status`, `log` and
`diff` need the config and the objects, and staging, a branch or a fetch are
ordinary work. What a contained command meets is a refusal to write, in its
own words — `could not lock config file`, a read-only file system, an
operation not permitted — and the model reads that as the command's result,
the way it reads any denial. That costs the commands that write the config
themselves: `git remote add`, `git config`, `git push -u`, a branch created
to track a remote, `git sparse-checkout`. Run those yourself, or grant the
store.

The grant is the store, as it is for a credential store: `/add-dir` on the
repository's `.git`, or on anything inside it, or on the hooks directory,
makes all of it writable to contained commands for the session. The working
scope classifies each of those sensitive, so no mode and no classifier makes
that grant; a person does, knowing that the next commit runs what is there.
Masking was chosen over checking at commit time — naming the hooks that
changed on the card and refusing a commit whose hooks a command wrote —
because the commit is not the only thing that runs them: the tree reading
runs the config's programs with no card at all.

On both mechanisms the directories between the workspace and those paths —
`.git` itself, in an ordinary checkout — cannot be renamed, because moving
`.git` aside, writing a hook into it under a name no rule covers and moving it
back would walk around every other rule here. Files are still created inside
them; every lock git takes is one.

The two mechanisms do not hold the rest equally. Seatbelt's rules are about
names, so a path that does not exist yet is as read-only as one that does.
Bubblewrap can only mount over a path that exists, so what it holds is the
entries that are there; an entry that is absent, or that is a symbolic link,
it cannot hold, and `/sandbox doctor` names each of those as not held rather
than claiming it. Two of those matter: `commondir`, which
is normally absent in an ordinary checkout and redirects the whole store when
it is written, and a `.git` in a workspace below the checkout's top, which a
contained `git init` makes and git then finds first. Creating either to mount
over would change the repository — an empty `commondir` breaks it.

Where the paths are is read before every command, from git and from the
`.git` found by walking up, and a store git cannot read still gets its
ordinary layout masked: a command that broke the store for a moment, so that
git answered nothing, does not buy the next command an unmasked config.

What this leaves open, in a trusted checkout. A hook the checkout already has
may run what the working tree says — husky's scripts, the pre-commit
framework's configuration, a `package.json` — and the working tree is the one
thing a contained command is there to write; that is the checkout's trust
answering, and withdrawing trust is the way to refuse it. Under bubblewrap, a
`commondir` a contained command writes into a store that had none, and a
`.git` it makes in a workspace below the checkout's top, are what the host's
git will read next — [the next section](#the-hosts-own-git-runs-nothing-a-command-wrote)
says why shhh does not pin the store to close them. A repository a contained
command creates in a workspace that had none is masked from the next command
on, but not in the command that made it. And configuration outside the store
— the global file, a file an `include.path` names in the working tree — is
only as protected as the grants around it.

## The host's own git runs nothing a command wrote

The mask decides what a contained command may write; this is the other half,
what shhh's own git does with what it finds. Shhh runs git on the host, as
you, in more places than the `git` tool: the reading of the tree between
rounds, the survey of the workspace at the start, the fingerprint a gate
result is pinned to, the check of whether a file was tracked before an edit,
the seeding and the patch of a writer's copy, and the backlog runner's
commit. None of those is carded, and the tree reading is asked of nobody.
Every one of them runs git the same way.

- **No program the configuration names that a reading can do without.** An
  fsmonitor is blanked, and the pager, an external diff, a textconv driver
  and a signature verifier are turned off wherever the verb would start one.
  A clean filter is not: a status needs it to know whether a file changed,
  which is why the store that names one is masked.
- **No submodule is asked anything.** Git's own answer to "is this submodule
  dirty" is to run git inside it, under the submodule's store, and read its
  files through that store's clean filters. A submodule's store is not the
  superproject's, so the mask does not name it, and a contained command can
  make a repository, stage it as a submodule and write its store with nothing
  but the working tree. Every reading that compares the working tree
  therefore leaves submodules out, whatever `.gitmodules` asks for; staging a
  writer's copy leaves out every submodule path; and a commit is told the
  same by configuration, because it takes no flag for it. What that costs: a
  submodule's own changes and its moved pointer are not in the tree notice,
  the survey's count, a writer's patch or the lines a history reading shows.

**The store is not pinned.** Git could be told, for every call, which store
to use — the one found when the session started — and a `commondir` or a
subdirectory `.git` written afterwards would then be ignored rather than
followed. Shhh does not do this. Git takes a pinned store and a pinned
working tree as a pair, so the pin would be one answer per checkout, and the
host's git runs in several: your checkout, each writer's copy, each copy the
backlog runner makes, a scratch index. And the pin is inherited by everything
git starts, so a trusted checkout's commit hook that runs git in another
repository — a hook manager keeping its own clone — would be pointed at this
one. The two cases it would close are open only under bubblewrap, which
cannot mount over a path that is absent, and `/sandbox doctor` names each
there as not held; Seatbelt holds both by name. A workspace at the
checkout's top has no subdirectory `.git` to find.

## The sandbox image ships with the binary

A container sandbox needs an image, and an image nobody chose is one nobody
built the tools into: with the network off there is no installing anything
once the container is running. So each release builds one, pushes it, and
links its digest into the binary it ships. `sandbox.container_image` unset
means the image built beside this shhh, and updating shhh updates the image
with it, so the tools a session finds in the container are the ones that
release was made with.

**A configured image replaces it and is never second-guessed.** The released
image is the answer only where the setting is empty, and the image policy
applies to it exactly as it applies to a named one: it is digest-pinned, and
`sandbox.image_allowlist`, when set, must list it. An allowlist is a
restriction somebody wrote on purpose, so it refuses the default like any other
image rather than making an exception for shhh's own.

**A build that did not come from a release has no image.** The digest exists
only once the release has pushed the image, so a `make build` carries none and
container sandboxes stay unavailable until the setting names one, which
`shhh doctor` says.

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

**Quitting has to finish the stop, not start it.** The kill behind the grace
is a timer inside shhh's own process, and quitting takes that process with it,
so a session that merely cancelled on its way out left exactly the orphan all
of this exists to stop — at the one moment there is nobody left to notice. A
session that is leaving therefore drains what it started before it goes:
interrupt, wait, kill whatever ignored the interrupt, wait again, the whole of
it inside the same short grace, so a quit with something to stop is still a
quit. On Linux the kernel is told separately to kill a captured command if
shhh disappears without running that path at all — a crash, or a kill from
outside. A command that reached its ceiling and was handed to the session's
processes is not drained: it changed owner, and its owner decides when it
stops.

**Nothing is signalled once a command's wait has returned.** A process group
is named by a number, and the moment the command has been reaped that number
belongs to the machine again. On a fork-heavy build it can be handed out well
inside the grace, so a kill that arrived late would not be a late kill for the
command that was cancelled; it would be a prompt one for a stranger.

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

## The model is told what its commands run under

The mechanism, the profile it applies, whether the network is open and how
long a command may run are resolved once, when the session opens, and stated
in the prompt beside the working scope. They are one answer about this machine
and this session rather than a range of possibilities, so they are said
plainly: a hedge costs the contained sessions the most, and the model cannot
find any of it out except by spending a round on a command that fails.

The netless profile pays for the block on its own. A contained session's
package install fails on the name lookup, which reads exactly like a broken
resolver — and a model that was not told there is no network debugs DNS,
retries against a second registry and asks for a proxy setting, three rounds
spent on a wall that was never going to move. Told plainly, it says what it
needed and works with what is already in the checkout.

A host list is the same wall with a door in it, and it is stated the same way:
the hosts are named, with the proxy that carries them, so a `curl` to any
other host is a refusal the model has already read about rather than a
surprise it goes on to debug.

The ceiling is stated in the same place and for the same reason. What happens
at it is not the same on every surface — a run whose commands are inside a
disposable container has nowhere to move one to — so what is stated is what
this session will actually do, not the rule in general.

## What is reported is what is in force

Every surface that mentions containment reports the mechanism actually
containing the process, not the one that was requested, and asks the path
that will run it rather than the one beside it. Where nothing is containing
it, the surface says so in those words and does not soften it.

A tool that reports its intended security posture rather than its actual one
is worse than a tool with none, because it is believed. Where containment is
unavailable, the honest answer changes the user's behaviour; a reassuring one
does not.

## A command that never started names what it needed

A command can fail before it is a process at all, for a reason that has
nothing to do with the line the model wrote. There are five such reasons,
and they are a closed set:

| Category | What was missing | Recorded as |
|---|---|---|
| working dir | the directory the command was to run in — usually a worktree or checkout removed under a running session | `harness-working-directory` |
| execution shell | the shell every command is run through | `harness-execution-shell` |
| containment | the mechanism that was to contain it, or a wrap it could not build; the command is never run bare instead | `harness-containment` |
| permission | the operating system's permission to run it | `harness-permission` |
| spawn | anything else the spawn refused, so that the four above never have to guess | `harness-spawn` |

The category is decided once, where the failure is still an error rather than
text, and it travels with the result from there. The command's row names it
in the outcome field — `did not start · working dir` — in words rather
than in colour alone, and in the table's own words, which are short enough
that a 60-column row carries the category whole rather than clipping it, with a dash where the duration would be because nothing
ran. The operating system's own words and the one thing still possible are
folded beneath the row. The model reads the same category on its result's
first line, and the session record files the call under the class in the
right-hand column, so a run of them in the record says which part of the
machine was wrong rather than only that something was.

The working directory is asked about before anything else, including before
blaming containment: the policy a contained command runs under starts from
the session's own directory, so a checkout removed under a contained session
fails there, and a reader told the mechanism was missing would go looking for
a fault in something that is fine.

A missing shell is the one reason that can hide behind a mechanism. What is
spawned for a contained command is the mechanism, and it starts; only inside
the sandbox does its exec of the shell fail, so no spawn error carries the
category, and the ending is an exit status like any command's. Neither
mechanism sets a status aside for it — bubblewrap exits 1 and Seatbelt's
`env` exits 127, which is also what a shell answers for a program that is not
installed — so the status alone would file ordinary failed commands under a
broken machine. What is read instead is the mechanism's own line, in one
place beside the mechanisms: the whole of the output is a single line naming
the very shell the wrap put there, in the words of the program that tried to
exec it — `bwrap: execvp <shell>: …` with status 1, `env: <shell>: …` with
status 127, or the host-list bridge's `shhh: network bridge: fork/exec
<shell>: …` with 127 — and that is `did not start · execution shell`, the row
the bare spawn draws. The two mechanisms do not produce it the same way, so
each is matched in its own words and its own status and never in the
other's. A command that ran cannot print that line alone: its shell would
have had to start to print anything.

A command that ran is never one of these. A shell answering `command not
found` for a program that is not installed ran perfectly and printed a fact
about the line, and a command whose ending nobody could read — it started,
printed, and the wait for it failed — says `did not complete` on its row
rather than `stopped`, which is the reader's own cancel, or `did not start`,
which its output would contradict.

## Related

- [`approvals-and-safety.md`](approvals-and-safety.md) — deciding whether it runs
- [`../interface/surfaces.md`](../interface/surfaces.md) — how it is reported
