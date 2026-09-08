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

It speaks the openai-compatible SSE dialect only, because that is the one
dialect a base_url on its own redirects; the same choice the CLI's
print-mode tests make.

    fakeprovider.py <port> <replies-file>
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1])
REPLIES = []
with open(sys.argv[2], encoding="utf-8") as fh:
    for line in fh:
        line = line.rstrip("\n")
        if not line.strip() or line.startswith("#"):
            continue
        if line.startswith("+") and REPLIES:
            REPLIES[-1].append(line[1:])
        else:
            REPLIES.append([line])
if not REPLIES:
    sys.exit("fakeprovider: the replies file is empty")
turn = {"i": 0}


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
        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        self.rfile.read(n)
        parts = REPLIES[min(turn["i"], len(REPLIES) - 1)]
        turn["i"] += 1
        self.log_message("reply %d: %s", turn["i"], " + ".join(parts)[:60])
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        calls = 0
        for part in parts:
            if part.startswith("tool:"):
                _, name, args = part.split(":", 2)
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


ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
