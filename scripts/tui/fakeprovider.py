#!/usr/bin/env python3
"""A scripted openai-compatible endpoint for driving the shhh TUI.

The golden tests render a surface in-process; this is the other half, the
endpoint a real `shhh code` is pointed at so the whole program can be driven
from a terminal with a model that says exactly what the scene needs it to.

Every request gets the next line of the replies file, and the last line
repeats once the file is used up. A line is one of:

    Plain text, streamed a word at a time as the assistant's answer. A
    literal \n in it is a line break, because the one-shot's answer is
    line-oriented — the command, then the sentence saying what it does, then
    the alternatives — and a reply is one line of this file.
    tool:<name>:<json args>   one tool call, e.g.
    tool:execute_command:{"command":"echo hi"}

It speaks the openai-compatible SSE dialect only, because that is the one
dialect a base_url on its own redirects; the same choice the CLI's
print-mode tests make.

    fakeprovider.py <port> <replies-file>
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1])
with open(sys.argv[2], encoding="utf-8") as fh:
    REPLIES = [l.rstrip("\n") for l in fh if l.strip() and not l.startswith("#")]
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
        line = REPLIES[min(turn["i"], len(REPLIES) - 1)]
        turn["i"] += 1
        self.log_message("reply %d: %s", turn["i"], line[:60])
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        if line.startswith("tool:"):
            _, name, args = line.split(":", 2)
            self.wfile.write(chunk({"tool_calls": [{"index": 0, "id": "call-1", "type": "function",
                                    "function": {"name": name, "arguments": args}}]}))
            self.wfile.write(chunk({}, "tool_calls"))
        else:
            # Splitting on spaces and rejoining with one is lossless, so a
            # line break written as \n survives inside whatever word it landed
            # in and reaches the client where the scene put it.
            for word in line.replace("\\n", "\n").split(" "):
                self.wfile.write(chunk({"content": word + " "}))
                self.wfile.flush()
            self.wfile.write(chunk({}, "stop", {"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13}))
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
