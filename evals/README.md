# Evals

Tasks for measuring what a session actually does with them. `shhh eval` runs
them; [`docs/capabilities/evals.md`](../docs/capabilities/evals.md) is why they
are shaped this way.

```
shhh eval                          # the whole suite, once each
shhh eval --repeat 3               # enough attempts to tell flaky from failing
shhh eval --case trace-the-cause   # one of them
shhh eval --model claude-sonnet-5  # measure a different model
```

They cost real requests, which is why `make ci` does not run them.

## Writing one

A case is a directory:

```
my-case/
  case.toml
  workspace/        # copied for every attempt; never edited in place
```

```toml
name = "my-case"                      # optional; defaults to the directory
prompt = "what the session is asked"
requires = ["go", "git"]              # skipped, not failed, if these are absent
check = ["go", "test", "./..."]       # exit zero is the pass
```

Every key is top level. There are no tables on purpose — a TOML table swallows
every key written after it, so there is no order to get wrong.

**Write the check first and watch it fail.** A check the session can satisfy
without doing the work is a case that measures nothing, and the only way to
know is to see it red before anything has touched it.

**Put the bug where the prompt says it is.** A case that claims the cause is
two packages away has to actually have it two packages away, or it is
measuring something other than what it says.

**Do not ask the session to change the tests.** They are the verdict.

## The cases

| Case | What it is for |
|---|---|
| `fix-failing-test` | The ordinary loop: read the failure, find the one wrong line, fix it. |
| `implement-to-spec` | Build to a specification held in tests, across two packages, and wire it up. |
| `trace-the-cause` | The failing test is two packages away from the bug. Rewards search over reading. |
