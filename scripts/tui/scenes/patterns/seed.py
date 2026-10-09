"""Seed a scene's store with three sessions of one checkout that repeat.

Usage: seed.py <store.db> <checkout root>

The store must already exist with its tables (the scene's setup runs the
binary once first, which migrates it). Each session is written to both sides
of the join the patterns readings make, the way a real session writes them:
the record's events and a saved conversation linked to the session, with the
calls at the turn and round the events name.

Every session reads main.go, is asked about `go test ./...` and allows it,
runs `go vet`, `go build` and `go test` in that order, and has its default
gate suite fail before it passes - one pattern of each kind /patterns
proposes from.
"""

import hashlib
import json
import sqlite3
import sys


def fingerprint(root):
    # The checkout fingerprint sessions are stamped with: the first six
    # bytes of the root's SHA-256, in hex.
    return hashlib.sha256(root.encode()).hexdigest()[:12]


def call(cid, name, args):
    return {"ID": cid, "Name": name, "Arguments": json.dumps(args)}


def main():
    db_path, root = sys.argv[1], sys.argv[2]
    project = fingerprint(root)
    db = sqlite3.connect(db_path)
    for n in range(3):
        slot = "seeded-%d" % n
        cur = db.execute("INSERT INTO chat_sessions (name) VALUES (?)", (slot,))
        chat_id = cur.lastrowid
        cur = db.execute(
            "INSERT INTO agent_sessions (kind, provider, model, project, chat_session, chat_session_id)"
            " VALUES ('code', 'openai-compatible', 'scripted-model', ?, ?, ?)",
            (project, slot, chat_id))
        sid = cur.lastrowid
        rounds = [
            (1, [call("r", "read_file", {"path": "main.go"})],
             [("tool", "read_file", "ok", "")]),
            (2, [call("v", "execute_command", {"command": "go vet ./..."})],
             [("tool", "execute_command", "ok", "")]),
            (3, [call("b", "execute_command", {"command": "go build ./..."})],
             [("tool", "execute_command", "ok", "")]),
            (4, [call("t", "execute_command", {"command": "go test ./..."})],
             [("decision", "", "ask", "safety"), ("decision", "", "allow", "user"),
              ("tool", "execute_command", "ok", "")]),
        ]
        seq = 0
        db.execute(
            "INSERT INTO chat_messages (session_id, seq, role, content, turn, round) VALUES (?, ?, 'user', 'go', 1, 0)",
            (chat_id, seq))
        for rnd, calls, events in rounds:
            seq += 1
            db.execute(
                "INSERT INTO chat_messages (session_id, seq, role, content, tool_calls, turn, round)"
                " VALUES (?, ?, 'assistant', '', ?, 1, ?)",
                (chat_id, seq, json.dumps(calls), rnd))
            for kind, tool, outcome, reason in events:
                db.execute(
                    "INSERT INTO agent_events (session_id, kind, tool, outcome, reason, turn, round)"
                    " VALUES (?, ?, ?, ?, ?, 1, ?)",
                    (sid, kind, tool, outcome, reason, rnd))
        for verdict in ("fail", "pass"):
            db.execute(
                "INSERT INTO agent_events (session_id, kind, tool, outcome, reason) VALUES (?, 'signal', 'default', 'gate', ?)",
                (sid, verdict))
    db.commit()


if __name__ == "__main__":
    main()
