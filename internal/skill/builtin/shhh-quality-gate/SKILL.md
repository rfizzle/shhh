---
name: shhh-quality-gate
description: Use when adding, editing or fixing this checkout's shhh quality gate (.shhh/quality.json), or when quality_gate answers that there is no quality config.
---

# The shhh quality gate

The gate is `.shhh/quality.json` at the checkout root: named suites of checks,
each an executable and its arguments. A run names a suite; the file is the only
place command text comes from. There is no shell: `exe` runs with `args` as
written, so pipes, `&&` and globs do nothing; put those in a script or make
target. Unknown keys are an error.

## The file

Top level:
- `"suites"`: object, required, at least one: suite name to suite. A run
  that names no suite takes `"default"`.
- `"max_parallel"`: checks run at once, 1 to 4 (default 1).
- `"on_close"`: a defined suite's name, run when a turn that changed files
  closes with nobody watching (an unattended run, a backlog run's stages).
- `"on_close_retries"`: how often a failing on-close verdict is handed back
  to be fixed (default 1; 0 reports it and ends the turn).
- `"generated"`: the paths the project generates rather than writes; work
  that lands regenerates them instead of merging them.

A suite:
- `"checks"`: list, at least one.
- `"timeout_seconds"`: bound on each check (default 600).
- `"require_containment"`: true makes a host with no sandbox a blocked
  verdict instead of a run on the host.
- `"allow_write"`: true lets checks write the workspace inside containment;
  the default is read-only.
- `"rerun_failed"`: 0 or 1 (default 1). With 1, a failed check runs once
  more, alone, over the same tree.

A check:
- `"name"` and `"exe"`: required.
- `"args"`: list of strings.
- `"scope"`: only `"packages"`. A scoped check needs an argument holding
  `{packages}`: an argument that is exactly it becomes one argument per Go
  package the changed files belong to, and one holding it among other text
  takes them joined by spaces (the form a make variable reads). It is the
  whole module when that cannot be said. Scoped checks run first and a
  failure ends the run. `{packages}` without the scope is refused.

A generated entry:
- `"name"`, `"exe"` and `"paths"` (repository-relative globs; `**` spans
  directories): required.
- `"args"`: may say `{pkg}`, the Go package of the matched path.
- `"timeout_seconds"`: default 600.

## Starter for a Go module

```json
{
  "max_parallel": 2,
  "suites": {
    "default": {
      "timeout_seconds": 600,
      "checks": [
        {"name": "changed tests", "exe": "go", "args": ["test", "{packages}"], "scope": "packages"},
        {"name": "test", "exe": "go", "args": ["test", "./..."]},
        {"name": "vet", "exe": "go", "args": ["vet", "./..."]}
      ]
    },
    "build": {
      "checks": [{"name": "build", "exe": "go", "args": ["build", "./..."]}]
    }
  }
}
```

## The same shape for a Node project

```json
{
  "suites": {
    "default": {
      "rerun_failed": 0,
      "checks": [
        {"name": "lint", "exe": "npm", "args": ["run", "lint"]},
        {"name": "test", "exe": "npm", "args": ["test"]}
      ]
    }
  }
}
```

Take the checks from the project's Makefile, package scripts or CI rather
than inventing them, and only ones that exit non-zero on a problem.

## Verdicts

- pass: every check passed over a tree that has not changed since. A check
  that failed and then passed on its rerun is reported as flaked and counts
  as passed.
- fail: a check failed. Fix the code, not the gate.
- blocked: nothing was judged. No config, an invalid one (the reason names
  the key), an unknown suite, or containment required and missing.
- cancelled: the run was stopped.

A result whose tree has changed since is stale, and a stale pass is not a
pass: run it again.

## Running it

- The `quality_gate` tool: action `run` with a suite name (default
  `default`), or action `result` for the last run and whether it is stale.
- In a session the person types `/gate run [suite]`, `/gate result`, or
  `/gate on` to run it as each turn that changed files closes.
- The file is read on every run, so an edit counts at the next one. Suites
  only run in a checkout the person has trusted (`shhh trust`, or `/trust`
  in a session).
