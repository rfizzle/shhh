# MCP servers

The Model Context Protocol is how a tool that lives outside shhh — an
issue tracker, a documentation index, a database, a vendor's API — is
offered to a model as a set of callable tools with schemas. A server speaks
the protocol; a client connects, asks what the server has, and calls it.
shhh is a client. A session connects the servers the person defined, and
their tools join the toolset beside the file tools and the web, named for
where they came from.

Servers exist here because the useful tool for a job is often one nobody
would build into a shell assistant: the model that can read the ticket it is
fixing, or search the documentation of the framework it is using, wastes
fewer rounds guessing. The protocol is the way to give it that without
writing a tool per service.

## shhh speaks the protocol and nothing else

A server is a command to spawn or a URL to reach. shhh starts the command
and talks over its pipes, or sends requests to the URL with the headers it
was told to send. That is the whole of what it does.

What it deliberately does not do is authorise. A remote server that wants a
login — a browser redirect, a token exchange, a refresh — gets it from the
forwarder the person put in front of it: a local process that speaks stdio
to shhh and carries the authorisation on the other side. The forwarders
exist, they are what every other client uses, and a login flow inside shhh
would be a second implementation of one that already works, with its own
bugs, its own token cache and its own way of going stale. A server that
answers a request with "log in first" is reported as a server that did not
connect, with the forwarder named as the fix.

The same rule gives the session's secrets one shape. A token goes in a
header or an environment variable as a reference to the environment, and
shhh sends what it resolved. It never learns what a token is for.

## Where a server is defined

A person's servers are their own: a section per server in the config file,
or a JSON catalog beside it in the shape every MCP client documents, so a
definition pasted from a vendor's README works as it is. A project's servers
travel with the checkout, in the same JSON shape, at the repository root or
under the project's own directory. A project definition shadows the
person's of the same name, the precedence every other harness applies and
what a person means when they copy a shared definition into a checkout to
change it.

A definition that cannot be read is a line in the listing, never a reason
the session does not start. The rule is the skills catalog's, for the same
reason: a project file arrived with a clone, and the person opening the
session may not have written it.

## A value in the file is a value in a backup

A definition names a token by reference — `${NAME}` — and the value comes
from the environment when the session starts. The config file is read by
more things than shhh, is synced and backed up, and is pasted into issues;
a token in it is a token in all of those places. The command that writes a
definition refuses a value that looks like a credential and says how to
write it instead.

A reference to a variable that is not set keeps the server from starting,
and the listing says which variable. The alternative — sending the header
with nothing after the word — fails at the far end, a round later, in the
server's words rather than shhh's.

## A checkout cannot start a process

A server definition in a project file is an instruction to run a command on
the person's machine, and it arrived with a clone. So a project server is not
started until the person has trusted the checkout it came with. The listing
shows it as waiting, says why, and offers the answer on the row; an edit to
any file the checkout declares — a different command here, a new skill next
door — asks again, because what was trusted was the checkout as it stood.

One answer rather than one per server, because a definition file is not the
only thing in a repository that runs as the person who cloned it: the skills,
the agent profiles and the quality suites do too, and being asked five times
about one clone teaches nobody anything the first question did not. What
that answer covers, where it is kept, and why the instruction files are
outside it are in
[`approvals-and-safety.md`](approvals-and-safety.md#a-checkout-declares-what-it-runs).

Trust granted in a session takes effect in the next one: the prompt that
names the servers was built when the session started, and a server that
joined without being named would be one the model does not know how to use.

## A server cannot vouch for itself

The protocol lets a server annotate a tool as read-only, or as
non-destructive. shhh reads the annotations, shows them, and grants nothing
from them. A server is a program somebody else wrote, often reached over a
network, and the only thing an annotation proves is that the server said
so. The specification itself says a client must not make decisions on
them from an untrusted server, and no server is trusted in that sense.

What decides is the person's word. A server marked read-only in their own
configuration runs the way a file read does, without asking. A project
file cannot mark its own server read-only; the word is ignored there and the
listing says so. This is the skills rule again — nothing in a repository can
pre-approve anything — applied to the one place a repository could try.

## A call is a command unless you said otherwise

A tool on a server the person did not mark read-only needs an answer before
it runs, in every mode that asks for one: it is a request to something shhh
cannot see the far side of, and a call that creates an issue or sends a
message is an act like a command, not a read like a file. The card says
where the call goes — a process on this machine, or a host the request
leaves for — what leaves with it, and what comes back. Auto mode asks its
classifier; plan mode refuses. The transcript draws the call with its own
glyph and the mutation rail, because shhh assumes the far end wrote
something, the way it assumes a command did.

A call on a read-only server is a read, and draws as one.

## What a conversation may reach

A conversation changes nothing, so it connects only the servers the person
marked read-only; the rest are listed as left out and the listing says why.
A child agent, which has no card to ask on, is handed the same read-only
servers and nothing else — the rule that already gives children the skills
catalog and the notebook, because a read is a read whatever the child's
tier.

## A server that did not answer is a row

Every server is connected when the session starts, all of them at once,
each under a timeout. One that did not answer — did not start, listed no
tools, asked for a login, timed out — is reported before the session opens
and again in the session's listing, with what it costs and what would fix
it, and the session starts without it. A failed server that stopped the
session would make the model's tools hostage to somebody else's uptime; a
failed server that was silently left out would be a tool the model was
told about and cannot reach.

The same listing is a command of its own, re-cut from the doctor screen:
one row per server, the transport as the verb, what it reaches, how many
tools it offers, and, for one that did not connect, the reason and the fix
on the row — including the offer to trust a project server that is waiting
on the person.

## Related

- [`skills.md`](skills.md) — the same leniency about files, the same rule
  that a checkout grants nothing
- [`approvals-and-safety.md`](approvals-and-safety.md) — the tiers a server
  call is placed in
- [`chat.md`](chat.md) — why a conversation takes only read-only servers
- [`secrets.md`](secrets.md) — how a token stays out of the file and the
  transcript
