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
    [reading]     the session's own readings of its run — the summariser, the
                  classifier, the title — which otherwise take the scene's
                  next reply and leave the turn one short

A file with no header is one queue for everybody, which is what a scene
written before this is. An agent no queue was written for is answered with a
line that ends its turn, and the log says which.

A page is served for a scene that fetches one. Anything under /site/ is
answered with a small page whose text is its own path, so a fetch reaches
something on this machine and nothing past it; `{port}` in a tool call's
arguments is the port this endpoint took, which is how a reply names a URL
on a port nobody knew when the scene was written. The session only reaches
it with web.allow_private set, which a scene's launch line writes.

It speaks the openai-compatible SSE dialect only, because that is the one
dialect a base_url on its own redirects; the same choice the CLI's
print-mode tests make.

    fakeprovider.py <port> <replies-file>

Port 0 asks the kernel for a free one. The port taken is printed as `port
<n>` on stdout once it is bound and listening, which is how drive.sh learns
it: a port chosen in one process and bound in another leaves a window for
anything else on the host to take it.
"""
import json
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


PORT = int(sys.argv[1])
QUEUES = load(sys.argv[2])
# One queue for everybody is the shape of every scene written before queues,
# and is kept exactly: no routing, no reading held back, the next reply to
# whoever asks.
QUEUED = "" not in QUEUES
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
    with lock:
        known = list(children)
    for task, name in known:
        if task in first:
            return name
    return SESSION


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
        queue = route(body)
        count, parts, scripted = take(queue)
        self.log_message("reply %d%s%s: %s", count, " [%s]" % queue if queue else "",
                         "" if scripted else " (no queue written for it)",
                         " + ".join(parts)[:60])
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        calls = 0
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
                self.wfile.write(chunk({"tool_calls": [{"index": calls, "id": "call-%d" % (calls + 1),
                                        "type": "function",
                                        "function": {"name": name, "arguments": args}}]}))
                calls += 1
                continue
            # Splitting on spaces and rejoining with one is lossless, so a
            # line break written as \n survives inside whatever word it landed
            # in and reaches the client where the scene put it.
            for word in part.replace("\\n", "\n").split(" "):
                self.wfile.write(chunk({"content": word + " "}))
                self.wfile.flush()
        # A reply that asked for anything ends on tool_calls whatever else it
        # said; one that only spoke is the end of the turn.
        if calls:
            self.wfile.write(chunk({}, "tool_calls"))
        else:
            self.wfile.write(chunk({}, "stop", {"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13}))
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


server = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
# Bound and listening by the line above, so the port is said only once it is
# ours: the run that reads this line has nothing left to race with.
print("port %d" % server.server_address[1], flush=True)
server.serve_forever()
