# Signing in with ChatGPT

A ChatGPT Plus or Pro subscriber can run shhh on their plan instead of on API
credit. The plan is its own provider, `chatgpt`, over the same wire as the
OpenAI Responses provider; what differs is who pays and what the route will
accept.

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
