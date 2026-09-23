#!/usr/bin/env python3
"""Regenerate the host-list snapshots shhh ships inside the binary.

Two lists ship: the Tranco ranking's top rows (what counts as a known host)
and the disposable-domains list. Both change slowly, so a copy built into the
binary is a fair floor under the download. The lists whose truth decays in
days — newly registered domains, malware hosts — do not ship: a snapshot of
them is wrong by the time the binary is installed.

The output is the on-disk form internal/web/reputation.go searches: a header
line, then one host per line, lower-cased, sorted by byte and unique.

Usage: scripts/host-lists.py [tranco.csv] [disposable.conf]
With no arguments both lists are fetched.
"""

import datetime
import json
import os
import re
import sys
import urllib.request

TRANCO_TOP = 10000
TRANCO_LATEST = "https://tranco-list.eu/api/lists/date/latest"
DISPOSABLE = ("https://raw.githubusercontent.com/disposable-email-domains/"
              "disposable-email-domains/main/disposable_email_blocklist.conf")
HOST = re.compile(r"^[a-z0-9._:-]+$")
OUT = os.path.join(os.path.dirname(__file__), "..", "internal", "web", "hosts")


def fetch(url):
    with urllib.request.urlopen(url, timeout=120) as r:
        return r.read().decode("utf-8", "replace")


def normalize(hosts):
    out = set()
    for h in hosts:
        h = h.strip().lower().rstrip(".")
        if h.startswith("[") and h.endswith("]"):
            h = h[1:-1]
        if h and len(h) <= 253 and HOST.match(h):
            out.add(h)
    return sorted(out, key=lambda s: s.encode())


def write(name, hosts):
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    with open(os.path.join(OUT, name + ".hosts"), "w", newline="\n") as f:
        f.write("#shhh-hosts 1 %s %s\n" % (name, stamp))
        for h in normalize(hosts):
            f.write(h + "\n")


def main(argv):
    if len(argv) > 1:
        tranco = open(argv[1]).read()
    else:
        latest = json.loads(fetch(TRANCO_LATEST))["list_id"]
        tranco = fetch("https://tranco-list.eu/download/%s/%d" % (latest, TRANCO_TOP))
    rows = [line.split(",", 1)[1] for line in tranco.splitlines() if "," in line]
    write("tranco", rows[:TRANCO_TOP])

    disposable = open(argv[2]).read() if len(argv) > 2 else fetch(DISPOSABLE)
    lines = [l.split()[0] for l in disposable.splitlines()
             if l.strip() and not l.strip().startswith("#")]
    write("disposable", lines)


if __name__ == "__main__":
    main(sys.argv)
