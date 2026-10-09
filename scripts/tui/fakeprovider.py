#!/usr/bin/env python3
"""A scripted openai-compatible endpoint for driving the shhh TUI.

The golden tests render a surface in-process; this is the other half, the
endpoint a real `shhh code` is pointed at so the whole program can be driven
from a terminal with a model that says exactly what the scene needs it to.

Every request gets the next reply of the replies file, and the last one
repeats once the file is used up. A line is one of:

    Plain text, streamed a word at a time as the assistant's answer. A
    literal \n in it is a line break, because the one-shot's answer is
    line-oriented — the command, then the sentence saying what it does, then
    the alternatives — and a reply is one line of this file.
    tool:<name>:<json args>   one tool call, e.g.
    tool:execute_command:{"command":"echo hi"}
    wait:<seconds>            hold the request open before answering it
    status:<code>:<message>   refuse the request with that HTTP status and
                              error message, as a provider that will not
                              answer does — the way a scene reaches a
                              failure row

`wait` is how a scene reaches a phase that is otherwise a moment wide. A
turn is thinking from the request leaving to the first token arriving, and
against an endpoint that answers instantly that is a frame nobody can capture
— so the scene about the thinking phase says how long the model takes to
begin. It holds the request rather than pausing mid-answer, which is what the
phase is: the client is streaming and has nothing yet.

A line beginning with + continues the reply above it rather than being one of
its own, so a single reply can carry several parts:

    Locate the round accounting
    +tool:read_file:{"path":"loop.go"}
    +tool:read_file:{"path":"round.go"}

That is what a step is made of. The transcript titles a step with the
assistant prose immediately preceding a batch of calls, so a scene that wants
a step — and the folded run of read-only rows inside one — needs the sentence
and the calls in one reply; sent as replies of their own, the sentence would
be a turn that ended before the calls were asked for.

A `+` with no reply above it is an error naming the file and the line, not a
reply of its own: it says the scene meant to continue something, and streamed
as prose it becomes a step title nobody wrote over one call fewer than the
scene says it drives.

A line `[name]` on its own opens a queue, and from there each agent is
answered from the queue of its own name rather than all of them from one:

    [session]     what the reader's own session is answered with
    [writer-1]    what the child that spawn_agent named writer-1 is answered
                  with — one queue per child, so a fan-out scene scripts each
                  of them instead of writing every racer the same uniform
                  round to survive the race between them
    [reading]     the session's own readings of its run — the summariser and
                  the classifier — which otherwise take the scene's next
                  reply and leave the turn one short
    [title]       the session's title, and [account] its standing account,
                  where a scene wants words of its own there; without the
                  queue the endpoint answers each with a line of its own
    [handoff]     the handoff /handoff asks for, answered the same way
    [suggestion]  the next step offered in the empty draft after a turn;
                  without the queue the endpoint answers with nothing, which
                  offers none
    [start_offers] the start screen's reading at session open, whose answer
                  is the offers' JSON on one line; without the queue the
                  endpoint answers with nothing, which keeps the fixed rows
    [integrator]  the integration writer the supervisor starts itself when
                  two writers' changes conflict: no spawn_agent call names
                  it, so it is known by the conflict its first turn opens on

A file with no header is one queue for everybody, which is what a scene
written before this is. An agent no queue was written for is answered with a
line that ends its turn, and the log says which.

The title and the standing account are asked by default beside every
reading, and no scene about some other surface should have to script them.
So a request offering the one tool either is asked to call —
session_title, session_account — is answered here with a fixed call of that
tool, unless the scene wrote a [title] or [account] queue, which wins. It is
known by that tool and never by where it falls in the run: a request that
stopped carrying its tool is answered from the queues as any other, and takes
a reply the scene wrote for something else, which fails the scene rather than
being absorbed here.

The next step offered in an empty draft is asked at every turn's close too,
and it asks for prose rather than a call, so it offers no tool to be known
by: it is known by its evidence, whose first field no other request carries.
Unscripted it is answered with nothing, which the session reads as no offer —
so a scene about another surface draws the empty draft it always drew. The
start screen's reading at session open is the same kind of request, known by
the field its own evidence carries, and is answered with nothing the same way
unless the scene wrote a [start_offers] queue: every session opens with one,
and a scene about another surface keeps the rows it always drew.

A page is served for a scene that fetches one. Anything under /site/ is
answered with a small page whose text is its own path, so a fetch reaches
something on this machine and nothing past it; `{port}` in a tool call's
arguments is the port this endpoint took, which is how a reply names a URL
on a port nobody knew when the scene was written. The session only reaches
it with web.allow_private set, which a scene's launch line writes.

Every round closes with usage, a tool round as well as the closing reply:
the prompt is the request received, bytes over four, and the completion what
was written, so the session's report lands where its own estimate of the same
request does rather than under the band it calibrates within.

It speaks the openai-compatible SSE dialect only, because that is the one
dialect a base_url on its own redirects; the same choice the CLI's
print-mode tests make.

    fakeprovider.py <port> <replies-file>

FAKE_PACE_MS in the environment (`25-60`, or one number) paces a plain
reply's words that many milliseconds apart; unset, a reply arrives at once.

Port 0 asks the kernel for a free one. The port taken is printed as `port
<n>` on stdout once it is bound and listening, which is how drive.sh learns
it: a port chosen in one process and bound in another leaves a window for
anything else on the host to take it.
"""
import json
import os
import random
import re
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# A queue header is a name in brackets alone on its line. Nothing else in a
# replies file looks like one, and a file with no header has no queues at all,
# so a reply that begins with a bracket is still a reply.
HEADER = re.compile(r"^\[([a-z0-9][a-z0-9 _-]*)\]$")
SESSION = "session"
READING = "reading"
INTEGRATOR = "integrator"
# What an integration writer's first turn opens on. Its task quotes the
# conflicting writer's, so without this it would be taken for that writer.
CONFLICT = "# The conflict"
# What an agent no queue was written for is answered with. It has to end a
# turn rather than ask for anything, and to read correctly to a child, a
# summariser and a classifier alike.
UNSCRIPTED = "Nothing to report."


def load(path):
    """Read a replies file into one queue of replies per agent."""
    queues = {}
    name = ""
    unheaded = 0
    with open(path, encoding="utf-8") as fh:
        for n, line in enumerate(fh, 1):
            line = line.rstrip("\n")
            if not line.strip() or line.startswith("#"):
                continue
            head = HEADER.match(line)
            if head:
                name = head.group(1)
                queues.setdefault(name, [])
                continue
            if line.startswith("+"):
                if not queues.get(name):
                    sys.exit("fakeprovider: %s:%d: a reply cannot begin with +, which "
                             "continues the reply above it and there is none: %s" % (path, n, line))
                queues[name][-1].append(line[1:])
                continue
            if not name and not queues.get(""):
                unheaded = n
            queues.setdefault(name, []).append([line])
    if queues.get("") and len(queues) > 1:
        sys.exit("fakeprovider: %s:%d: a reply above the first [queue] header, in a file "
                 "that has one — every reply belongs to an agent" % (path, unheaded))
    if not any(queues.values()):
        sys.exit("fakeprovider: the replies file is empty")
    return queues


def pace(value):
    """FAKE_PACE_MS as the bounds of the gap between words, in milliseconds.

    Unset, a reply streams as fast as the socket takes it, which is what a
    scene that is only a test wants. Set — `25-60`, or one number for a fixed
    gap — each word after the first waits a gap drawn between the two, so a
    recording shows a reply arriving the way a model's does rather than in one
    frame. It is an environment variable and not a reply directive because it
    is a fact about the run rather than the scene: the same scene is a fast
    test under `make tui-check` and a paced recording for the README.
    """
    if not value:
        return None
    lo, _, hi = value.partition("-")
    try:
        bounds = (float(lo), float(hi or lo))
    except ValueError:
        sys.exit("fakeprovider: FAKE_PACE_MS is milliseconds, `25-60` or `40`: %r" % value)
    return (min(bounds), max(bounds))


PACE = pace(os.environ.get("FAKE_PACE_MS", ""))
# Loaded from the replies file by main, and set by a test directly.
QUEUES = {}
# One queue for everybody is the shape of every scene written before queues,
# and is kept exactly: no routing, no reading held back, the next reply to
# whoever asks.
QUEUED = False
# How many of each queue have been handed out, and what the endpoint knows
# about the children it has been asked to spawn. Both are read and written
# from the request threads, which a fan-out runs several of at once.
turn = {}
children = []
lock = threading.Lock()


def route(body):
    """Which agent's queue a request belongs to.

    The session and its children are asked with the whole toolset; the
    session's own readings of its run are asked with one tool or none, which
    is what tells them apart without reading a word of anybody's prompt.

    A child is known by its task. The system prompt cannot say which child is
    asking — two writers of one session are handed the same one, down to the
    sentence — but the task is the scene's own words, and this endpoint handed
    out the spawn_agent call that paired it with a name.
    """
    if not QUEUED:
        return ""
    if len(body.get("tools") or []) < 2:
        return READING
    first = ""
    for message in body.get("messages") or []:
        if message.get("role") == "user":
            first = str(message.get("content") or "")
            break
    if first.startswith(CONFLICT) and INTEGRATOR in QUEUES:
        return INTEGRATOR
    with lock:
        known = list(children)
    for task, name in known:
        if task in first:
            return name
    return SESSION


# The session's readings of itself the endpoint answers on its own: the tool
# each is asked to call, the queue a scene overrides it with, and the answer.
FLOWS = {
    "session_title": ("title", ['tool:session_title:{"title":"A scripted session"}']),
    "session_account": ("account", ['tool:session_account:{"account":"Working through a scripted session."}']),
    # The handoff /handoff asks for, which a scene about it words in a
    # [handoff] queue of its own.
    "session_handoff": ("handoff", ['tool:session_handoff:{"summary":"A scripted handoff","done":[],"open":[],"decisions":[]}']),
    # An empty reply is no offer: the session trims it to nothing.
    "suggestion": ("suggestion", [""]),
    # Nothing is no offers: the screen keeps its fixed rows.
    "start_offers": ("start_offers", [""]),
}

# The field the next-step request's evidence opens with, quoted as the JSON
# carries it.
SUGGESTION_EVIDENCE = '"last_instruction"'
# And the field the start screen's reading opens with.
START_OFFERS_EVIDENCE = '"checkout_facts"'


def evidenced(body, field):
    """Whether a request is a toolless reading whose evidence names the field
    only that reading's evidence carries."""
    if body.get("tools"):
        return False
    msgs = body.get("messages") or []
    last = msgs[-1] if msgs and isinstance(msgs[-1], dict) else {}
    content = last.get("content")
    return isinstance(content, str) and content.startswith("UNTRUSTED EVIDENCE:") and field in content


def suggesting(body):
    """Whether a request is the next-step offer."""
    return evidenced(body, SUGGESTION_EVIDENCE)


def flow(body):
    """The tool of a flow the endpoint answers itself, or "".

    Known by the request's own marker — it offers exactly one tool, and that
    tool is one of FLOWS — so a request that lost its tool is not one. The
    next-step offer is the one flow with no tool, and is known by its
    evidence instead.
    """
    if suggesting(body):
        return "suggestion"
    if evidenced(body, START_OFFERS_EVIDENCE):
        return "start_offers"
    tools = body.get("tools") or []
    if len(tools) != 1 or not isinstance(tools[0], dict):
        return ""
    name = str((tools[0].get("function") or {}).get("name") or "")
    return name if name in FLOWS else ""


def answer(body):
    """Who a request is answered for and with what: the queue, the count
    in it, the reply's parts, and what the log adds about where they came
    from."""
    name = flow(body)
    if name:
        queue, line = FLOWS[name]
        if QUEUES.get(queue):
            count, parts, _ = take(queue)
            return queue, count, parts, ""
        with lock:
            count = turn.get(queue, 0) + 1
            turn[queue] = count
        return queue, count, line, " (answered by the harness)"
    queue = route(body)
    count, parts, scripted = take(queue)
    return queue, count, parts, "" if scripted else " (no queue written for it)"


def take(queue):
    """The next reply of a queue. The last one repeats once it is used up."""
    with lock:
        i = turn.get(queue, 0)
        turn[queue] = i + 1
    replies = QUEUES.get(queue) or []
    if not replies:
        return i + 1, [UNSCRIPTED], False
    return i + 1, replies[min(i, len(replies) - 1)], True


def remember_child(args):
    """Pair a child's name with its task, as the spawn call goes out.

    Here because this is the one place both are in the same hand: the call
    carries the name, and every request the child makes afterwards carries
    only the task it was given.
    """
    try:
        spec = json.loads(args)
    except ValueError:
        return
    task = str(spec.get("task") or "").strip()
    name = str(spec.get("name") or spec.get("role") or "").strip()
    if task and name:
        with lock:
            children.append((task, name))


# What the endpoint says it can run, for the scenes that open the model
# picker. The first is the one every scene is started on; the rest are there
# so the list is a list — a picker over one row is not the surface.
MODELS = ["scripted-model", "scripted-mini", "scripted-fast", "scripted-long"]


def text_of(content):
    """A message's content as the text it carries: a string, or the text
    parts of a list of parts (an attachment's image part carries none)."""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "".join(str(p.get("text") or "") for p in content if isinstance(p, dict))
    return ""


def prompt_tokens(body):
    """What the request would cost a real provider, near enough to be read as
    one: its bytes over four — the messages' text, the arguments of the calls
    they carry, and the tool definitions sent in front of them — which is the
    session's own estimate of the same request, so the report lands inside
    the band the session calibrates against rather than under its floor."""
    n = len(json.dumps(body.get("tools") or [])) if body.get("tools") else 0
    for message in body.get("messages") or []:
        n += len(text_of(message.get("content")).encode())
        for call in message.get("tool_calls") or []:
            n += len(str((call.get("function") or {}).get("arguments") or "").encode())
    return max(n // 4, 1)


def usage(prompt, wrote):
    """The usage a round closes on: the prompt as received, the completion as
    written, bytes over four each."""
    completion = max(wrote // 4, 1)
    return {"prompt_tokens": prompt, "completion_tokens": completion,
            "total_tokens": prompt + completion}


def chunk(delta, finish=None, usage=None):
    body = {"id": "scripted", "object": "chat.completion.chunk", "choices": [
        {"index": 0, "delta": delta, "finish_reason": finish}]}
    if usage:
        body["usage"] = usage
    return ("data: " + json.dumps(body) + "\n\n").encode()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        # One line per request on stderr, which drive.sh keeps beside the
        # captures: a step that never drew is read against what was asked.
        sys.stderr.write("%s %s\n" % (self.command, fmt % args))
        sys.stderr.flush()

    def do_GET(self):
        # The model list, so a scene can open the picker rather than the usage
        # text. It is the one GET the CLI makes, and a 404 with no body
        # reaches the reader as "malformed response" — an error about the
        # endpoint, on a screen the scene meant to be about the list.
        if self.path.rstrip("/").endswith("/models"):
            body = json.dumps({"data": [{"id": m} for m in MODELS]}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path.startswith("/site/"):
            page = self.path[len("/site/"):].replace("-", " ")
            body = ("<html><head><title>%s</title></head><body><p>%s</p></body></html>"
                    % (page, page)).encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(n)
        try:
            body = json.loads(raw or b"{}")
        except ValueError:
            body = {}
        queue, count, parts, note = answer(body)
        self.log_message("reply %d%s%s: %s", count, " [%s]" % queue if queue else "",
                         note, " + ".join(parts)[:60])
        if parts and parts[0].startswith("status:"):
            # The request refused outright, in the shape the dialect's own
            # errors take, so the session classifies it as it would the real
            # provider's refusal.
            _, code, message = parts[0].split(":", 2)
            body = json.dumps({"error": {"message": message, "type": "scripted", "code": None}}).encode()
            self.send_response(int(code))
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        calls = 0
        wrote = 0
        for part in parts:
            if part.startswith("wait:"):
                # Before the headers would be a request that has not been
                # answered; after them the client is already streaming and
                # waiting on a first token, which is the phase being drawn.
                time.sleep(float(part.split(":", 1)[1]))
                continue
            if part.startswith("tool:"):
                _, name, args = part.split(":", 2)
                args = args.replace("{port}", str(self.server.server_address[1]))
                if QUEUED and name == "spawn_agent":
                    remember_child(args)
                wrote += len(name) + len(args.encode())
                self.wfile.write(chunk({"tool_calls": [{"index": calls, "id": "call-%d" % (calls + 1),
                                        "type": "function",
                                        "function": {"name": name, "arguments": args}}]}))
                calls += 1
                continue
            # Splitting on spaces and rejoining with one is lossless, so a
            # line break written as \n survives inside whatever word it landed
            # in and reaches the client where the scene put it.
            for i, word in enumerate(part.replace("\\n", "\n").split(" ")):
                if PACE and i:
                    time.sleep(random.uniform(*PACE) / 1000)
                self.wfile.write(chunk({"content": word + " "}))
                wrote += len((word + " ").encode())
                self.wfile.flush()
        # A reply that asked for anything ends on tool_calls whatever else it
        # said; one that only spoke is the end of the turn. Both carry usage,
        # as a real provider's rounds do: a tool round that reported nothing
        # would leave the rail's count where the round before it put it.
        spent = usage(prompt_tokens(body), wrote)
        self.wfile.write(chunk({}, "tool_calls" if calls else "stop", spent))
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


def main():
    global QUEUES, QUEUED
    port = int(sys.argv[1])
    QUEUES = load(sys.argv[2])
    QUEUED = "" not in QUEUES
    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    # Bound and listening by the line above, so the port is said only once it
    # is ours: the run that reads this line has nothing left to race with.
    print("port %d" % server.server_address[1], flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
