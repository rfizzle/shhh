#!/usr/bin/env python3
"""Regenerate the model-data snapshot shhh ships inside the binary.

The snapshot is the public LiteLLM table trimmed to the providers shhh speaks
and the fields it reads, so a fresh or offline install still knows what a
model costs and how it spells its thinking level. The downloaded table is
overlaid on it at runtime; this file is the floor, not the source of truth.

Usage: scripts/model-data.py [path-to-full-table.json] > internal/pricing/models.json
With no argument the table is fetched from GitHub.
"""

import json
import sys
import urllib.request

URL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

PROVIDERS = {"anthropic", "openai", "gemini", "vertex_ai-language-models", "openrouter"}

FIELDS = [
    "input_cost_per_token",
    "output_cost_per_token",
    "max_input_tokens",
    "max_output_tokens",
    "supports_reasoning",
    "supports_adaptive_thinking",
    "supports_legacy_thinking",
    "thinking_always_on",
    "supports_xhigh_reasoning_effort",
    "supports_max_reasoning_effort",
    "supports_minimal_reasoning_effort",
    "supports_none_reasoning_effort",
]


def main():
    if len(sys.argv) > 1:
        with open(sys.argv[1]) as f:
            table = json.load(f)
    else:
        with urllib.request.urlopen(URL, timeout=30) as r:
            table = json.load(r)

    out = {}
    for key, entry in sorted(table.items()):
        if not isinstance(entry, dict) or entry.get("mode") != "chat":
            continue
        if entry.get("litellm_provider") not in PROVIDERS:
            continue
        if entry.get("litellm_provider") == "vertex_ai-language-models" and not key.startswith("gemini"):
            continue
        kept = {f: entry[f] for f in FIELDS if f in entry}
        if not kept:
            continue
        out[key] = kept

    json.dump(out, sys.stdout, indent=1, sort_keys=True)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
