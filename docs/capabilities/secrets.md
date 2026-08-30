# Secrets

A secret is a value the session may use and the model may never see: an API
key for the service being debugged, a database password, a token a script
needs. The model is told the secret's name and how to use it; the value goes
into every command the model runs, and comes out of everything the model
reads.

Secrets exist because the useful debugging session and the leak are the same
session. "Call the API with my key and tell me why it 400s" is exactly the
kind of thing the agent is for, and the obvious way to do it — paste the key
into the chat — puts it in the conversation, the provider's logs, the saved
session and the scrollback. Pretending the model does not need the key
means the user runs the commands by hand and pastes the output back, which
is the work the agent was supposed to do. The secret is how the key is
present without being visible.

## A secret is an environment variable

The model reaches a secret one way: as an environment variable of the
secret's name, in every command it runs. `curl -H "Authorization: Bearer
$API_KEY"`, `os.environ["API_KEY"]` in a script a command runs, `.env`-style
tooling that reads the process environment — all of it works, because that
is where the value is.

The variable is the deterministic form. The model is not offered a
substitution syntax to write into tool arguments, because a value
substituted into a `write_file` call is a value on disk in plain text, and
the file it landed in is one `read_file` away. An environment variable is
present only while the command runs and leaves nothing behind unless the
command itself writes it somewhere — and if it does, the scrub still applies
to whatever reads it back.

Every path a command takes carries the variable: plain, with live output,
contained under the OS mechanism, inside a disposable container (where the
name is passed explicitly, because a sandbox container starts with no host
environment at all), in a sub-agent's worktree, and in a long-running
process the model starts. A model-supplied variable of the same name cannot
shadow a secret in any of them. A path that missed the variable would fail
as "unset", the model would conclude the name was wrong, and the diagnosis
would go somewhere unhelpful.

## The value is scrubbed at every door

Nothing the model reads may contain a value. The scrub runs on every tool
result, every command's output — including the live tail on screen, because
the screen is the scrollback and the copy buffer — every message on its way
into the conversation, and every request on its way to a provider. The
conversation is the thing that gets saved, resumed, compacted and shown, so
it is scrubbed as it is written; the request is scrubbed again as it leaves,
because a resumed session or a front-end's own stream is a path around the
first door. Sub-agents have the same scrub on their own conversations.

Scrubbing at the executor rather than at one tool is deliberate. The command
that prints the key is the obvious case; `read_file` on the `.env` it lives
in, a web fetch that returns the page it was posted to, and a process
tool's log are the same leak through a different tool.

What is scrubbed is more than the value. The base64, hex and URL-escaped
forms of it are replaced too, because `echo $KEY | base64` is the first
thing a model tries when told it cannot see something, and a fragment of the
value eight bytes or longer is replaced on its own, because `cut -c1-20`
prints a prefix, a wrapped terminal splits a token over two lines, and half
a key is still a key. What replaces it names the secret — `[secret:API_KEY]`
— so the model can tell that the key was there and was used, rather than
reading a blank as a failure and debugging the wrong thing.

The scrub is a pattern over text, and that is its limit. A value transformed
some other way — reversed, run through a cipher, printed one character per
line — is not recognised, and a value shorter than the fragment length is
only ever matched whole. The scrub keeps a model that is doing its job from
seeing the key by accident; it is not a proof against a model that has
decided to exfiltrate one. That is what approval is for: the command that
sends the key somewhere new is a command, and it is shown before it runs.

## Where a value comes from

Values come from the environment, or from the user's hands, and never from
a file shhh owns. Config names secrets — `secrets.env` lists variables to
declare in every session — and reads their values from the environment at
start, because a config file is read by more things than shhh and a token in
it is a token in a backup. A variable the config names and the environment
lacks is a warning and not a secret: config is standing policy, and a
laptop without the variable set should still open a session.

`--secret NAME` on `shhh chat` and `shhh code` declares one for this run,
reading it from the environment; unlike config, a name the environment
lacks is an error, because the user asked for it here and now. `--secret
NAME=value` gives the value on the command line, which the shell's history
and every process listing will remember — the form to prefer is the bare
name.

`/secret set NAME` in a session does the same mid-conversation, and it is
the form that matters most: the model has just asked for a key it does not
have, and the answer is one command away rather than a restart. A secret
added this way is announced to the model as a user message, because the
system prompt that named the others was written before this one existed
and the model has no other way to learn the name. `/secret set NAME=value`
is accepted and kept out of input recall, so an up-arrow never puts the
value back on screen. `/secret forget NAME` removes one; what was already
scrubbed stays scrubbed, since the placeholders in the conversation are
only text.

The model is told what is there. The system prompt carries the names, the
one way to use them, and what the placeholder means — never a value, never a
length, never a prefix that would narrow a guess.

## Related

- [`approvals-and-safety.md`](approvals-and-safety.md) — the command that
  would send a secret somewhere is still a command, and is still shown
- [`containment.md`](containment.md) — what a command holding the value can
  reach
- [`configuration.md`](configuration.md) — where `secrets.env` lives
