# Signing in with ChatGPT

A ChatGPT Plus or Pro subscriber can sign in and have shhh spend their plan
instead of API credit. There is no key to create, paste or keep: the person
approves shhh once in the browser, and shhh keeps the sign-in fresh from
then on.

It exists because a key is the wrong ask for somebody who already pays for
ChatGPT. Creating one means an API account, a payment method and a second
bill for work the plan already covers. OpenAI offers plan use to open-source
apps, and shhh is one, so the sign-in is the shorter way in for the people
who have a plan.

## What it costs

A session on the plan is paid for by the plan's allowance, not by API
credit. The allowance is the plan's: Plus users share one five-hour limit
across every app that uses their plan, ChatGPT itself included, and Pro users
do not. Usage is shown and managed at <https://chatgpt.com/settings/usage>.

shhh never falls back to API credit on its own. A session that reaches the
plan's limit, or a sign-in the plan does not cover, stops and says so; moving
to a key is a provider switch, and only the person makes it. Paying for the
same work twice without being asked is the one outcome this whole route is
built to avoid.

## Signing in

`shhh providers login chatgpt` opens the browser on OpenAI's sign-in page
and waits for it to come back. The browser returns to a listener on
`127.0.0.1` on a port the system picks — never `localhost`, which OpenAI
refuses, and never an address another machine can reach. The listener is
open before the browser is, and closes when the sign-in is done, refused,
cancelled, or not finished within five minutes (`--wait` changes that).

`--no-browser`, or a machine where no browser opens, prints the address
instead, to be opened on a machine whose browser can reach this host's
`127.0.0.1` and the printed port. The address is printed on the terminal and
nowhere else: a returning sign-in's address carries the previous sign-in as a
hint. On a host other people also log in to, `--no-browser` is the way to
sign in: the opener is handed the address as an argument, and a program's
arguments are in the process list every local user can read.

The first sign-in on a host registers shhh there, and OpenAI hands back an
identifier for that registration which every later sign-in reuses. Each host
also has an ID of its own, made on the first sign-in and kept from then on,
sign-out included, so plan use is attributed to the machine it came from.

ChatGPT can grant the sign-in without plan use — the person can untick it on
the permission page. That sign-in is kept, and the command says plan use is
off; `shhh providers login chatgpt --consent` signs in again and asks for the
permission page to be shown again.

On a machine with no provider set up at all, the sign-in makes ChatGPT the
default. Anywhere else it changes nothing and names the one setting that
would: replacing a working setup is the person's call, not a side effect of
signing in.

### What the sign-in checks

Everything that can be forged or crossed in the exchange is checked before
anything is written, so a sign-in that fails part-way leaves the previous one
as it was:

- the browser's return carries the single-use value this attempt sent, and
  is refused before anything else in it is read if it does not;
- a first sign-in comes back registered, and a returning one comes back for
  the same registration;
- the code is redeemed with a proof that whoever redeems it is whoever asked
  for it (PKCE), so a code read off the screen is no use on its own;
- the identity token is signed by one of OpenAI's published keys, was issued
  by OpenAI to this registration, has not expired, and answers this attempt
  and no other;
- a returning sign-in is the same ChatGPT account as the one saved. A
  different account is refused rather than swapped in; changing accounts is
  deleting the record and signing in again.

## Where the sign-in lives

The record is `credentials.json` in the `chatgpt` directory of shhh's data
directory (`$XDG_DATA_HOME/shhh`, or `~/.local/share/shhh`), owner-only, beside
the host ID and the lock file. It is in the data directory and not the config
directory because the config directory is the one people keep in a dotfiles
repository, and this file holds a credential that can spend the plan. **Never
commit it, and never copy it anywhere it would be shared.** The data
directory is also kept out of the commands an assistant runs.

### What keeps the tokens from the model

The sign-in holds three tokens: one sent with each request, one that proves
who signed in, and one that fetches a new pair when the first runs out. shhh
never names them to the model and never hands them to a command it runs.
Every one a session has learned — read from the record when the session
opened, from a sign-in, or from a refresh — is scrubbed from everything the
model reads, whole, encoded, or in pieces, and stands as a placeholder that
names the kind and no variable (see
[Secrets](secrets.md#the-value-is-scrubbed-at-every-door)). The last of the
three has no shape anything could recognise it by, which is why it is
scrubbed by value; the other two are scrubbed by everything after their
common header, so another service's token printed in the same session is
still recognised by its shape. A rotated-out token stays scrubbed for the
rest of the session.

The scrub covers what a session has learned, and no more. A refresh token
another shhh process writes while this session runs is scrubbed here only
once this session reads it. The record itself is kept from an assistant's
commands by containment; with containment off, a command can read the file,
and a token this session has not learned would reach the model.

The tokens are not in the config file, the provider listing, the diagnostic
log, a receipt or a session's record. The log keeps what happened — a
sign-in, a refresh, a refusal and the server's code for it — and never an
address or a token.

### One refresh at a time

The token sent with requests lasts an hour, and the one that renews it is
replaced every time it is used: presenting a spent one is treated as a
replay, and signs the person out. So two shhh processes on one host — two
terminals, a server and a scheduled run — must never renew at once. A
process renews holding a lock beside the record, reads the record again once
it has the lock, and finds the token another process just fetched rather
than spending the old one a second time.

Renewal happens a few minutes before expiry, never before the earliest time
OpenAI names, and at once when a request was refused. A refusal that means
the sign-in is finished — spent, expired, replayed, revoked — clears the
tokens and asks for a sign-in; a network that is down, a timeout or a server
error keeps the record, because a minute's outage should not cost a sign-in.

There is one window no lock closes: a renewal whose answer arrives and whose
record is not yet written when the process dies. The new token was issued
and never kept, the old one is spent, and the next run asks for a sign-in.
Every client of a token that rotates has the same window.

## Signing in for a remote host

The loopback sign-in needs a browser that can reach the host's
`127.0.0.1`. A server, a VM or a container usually has none, and OpenAI's
answer for that case is to sign in on a machine that has one and copy the
sign-in across: sign in locally, then copy `credentials.json` into the
remote host's `chatgpt` directory over SSH. Copy that file only — not
`host-id`, which identifies the machine it is on. The remote host keeps its
own ID and renews the sign-in on its own from then on.

OpenAI says two things are not yet available for a copied sign-in:
attributing plan use to the host it was copied to, and revoking plan access
for that host alone.

## A run nobody is watching never signs in

A run with nobody at the terminal — a one-shot `shhh -p`, a served session,
an unattended coding run, the backlog runner — never opens a browser or a
listener. With ChatGPT chosen and no usable sign-in, it reports
"not signed in to ChatGPT — run `shhh providers login chatgpt`" and exits 1,
exactly as a missing key does. A sign-in that expires mid-run ends that turn
the way a dead key would. Nothing fails open into a prompt nobody can
answer.

## Signing out

`shhh providers logout chatgpt` tells OpenAI the sign-in is finished, then
clears the tokens whether OpenAI heard or not. A network or server failure
is tried again a few times first. When the revocation was not confirmed the
command says so: the app may still be connected to the account until it is
disconnected in ChatGPT Settings. The host's registration and ID are kept, so
signing in again is one approval in the browser.

## What a plan session cannot do

OpenAI serves the plan route under preview limits, and a request that breaks
one is refused whole rather than served in part. So the plan session holds
every request to those limits before it is sent, in one place, and each limit
is a single line there: when OpenAI lifts one, shhh can follow in one change.

**Where it sends and with what.** Every request goes to OpenAI's own endpoint,
whatever base URL or gateway is configured, and goes under the signed-in
account's access token, never an API key from the flag, the environment or the
configuration file. The token is good for that endpoint and nothing else, so
it is not handed anywhere a key would be. A token the endpoint refuses is
refreshed once and the request sent once more; a second refusal ends the
turn and asks for a sign-in.

**What the request leaves out.** The conversation is sent whole every turn,
nothing is stored on OpenAI's side, and the answer always streams. The fields
the route rejects are never sent: the temperature and the output ceiling among
them, which means a bounded call — a title, a summary, the permission
classifier — is bounded by what it asks for and not by a token limit. Some
fields the route's documentation neither accepts nor rejects, and until a call
against the route has shown each is accepted they are left out too: the
reasoning level (the model thinks at its own default, whatever level the
session shows), the structured-answer format (a call that wanted an object
reads one out of the text instead), the prompt-cache hints, the request for
the model's sealed reasoning and its replay, and the tool choice. A turn on
which no tool may be called carries no tools at all, which is the same promise
in a form the route is known to take.

**How the tools travel.** The route takes function tools only in a group, so
shhh's tools travel as one namespace. Each tool inside it keeps its own name
and description; the model is told about the tools exactly what it is told on
any other provider, and a call it makes comes back under the tool's own name.

**What cannot ride along.** Pictures and documents go inline as they do
anywhere else. A recording cannot: the route takes no audio, so a message that
carries one is refused before anything is sent, with the reason. A recording
further back in a conversation that came from another provider goes as a line
saying it was left out, so the session is not refused on every turn for
something it cannot remove. Nothing shhh sends needs the Files API, which the
route does not serve.

**What is off.** The Decisions API is a different route, and the plan's
documentation says other routes are not permitted by analogy, so the
permission classifier on a plan session asks through an ordinary completion.
The cache keep-alive is off too: it is a one-token request, the route takes
no output ceiling, and each refresh would be a whole turn on the allowance.
Hosted tools — OpenAI's own web search, file search, code interpreter and the
like — are not sent; shhh's tools run on this machine.

**What a refusal means.** The plan's refusals are their own family, and each
is named for what can be done about it. A spent allowance is a usage limit,
not a rate limit: it is never waited out, because the plan names no reset and
the limit may be the plan's own or the app's share of it. An account,
workspace or operation the plan will not serve is not eligible, and neither a
retry nor another sign-in changes that. A request the route does not serve is
unsupported, and the failure names the part it refused. A check on OpenAI's
side that could not be made is a temporary outage and is retried on the usual
bounded schedule. The endpoint's code and request id are kept with every
refusal.

**What never happens.** A plan session that fails stays a plan session. OpenAI
never moves a refused request to another billing path, and shhh does not
either: switching to a provider that bills a key is the person's own choice,
made from the failure's offer to switch, never a fallback taken on their
behalf.

## Related

- [Providers](providers.md) — the backends, and how a failure is classified.
- [Secrets](secrets.md) — the scrub every value the model must not see goes
  through.
- [Containment](containment.md) — what keeps an assistant's command away from
  shhh's own directories.
