---
name: testing
description: Use this skill when adding, changing, debugging, or running shhh tests and quality gates, especially when a test needs HTTP, a subprocess, a clipboard, a listener, an operating-system sandbox, or a constrained agent session. It chooses the hermetic, contract, or integration tier and the fixture pattern that keeps results repeatable. Do not use it for product documentation or visual TUI inspection alone; use the documentation or tui-drive skill for those.
---

# Testing shhh

Start by classifying the evidence the change needs. A test belongs to exactly
one execution tier; do not make a session gate discover a host prerequisite by
failing after it starts.

| Tier | Purpose | Where it runs |
|---|---|---|
| Hermetic | Ordinary behaviour and failure handling | Default package tests and contained quality suites |
| Contract | A real loopback service or host executable boundary | `make test-contract` on a listener-capable host |
| Integration | The operating-system containment mechanism itself | `make test-integration` on its supported runner |

## Hermetic tests

Keep the default suite inside the session boundary. Use `t.TempDir` for files,
`t.Setenv` for process configuration, and a controllable command or transport
at the seam instead of a host service.

- For an HTTP peer that is only a test fixture, use `internal/testhttp`; do not
  start an `httptest` server or bind a TCP port.
- Pass its client through the existing client seam. A test must not replace the
  production dialer merely to reach a fake endpoint.
- For a host command such as the clipboard helper, substitute the command at
  the package's test seam and restore it with `t.Cleanup`.
- A contained quality suite declares `require_containment`. It must not depend
  on a listener, clipboard, container engine, host daemon, outside network, or
  a writable shared build cache.

The quality runner gives Go checks a private build cache. When driving Go
directly in a restricted shell, point `GOCACHE` at a fresh writable directory;
do not weaken containment just to reuse a host cache.

## Contract and integration tests

Use a contract test only when the fact being proved is the real boundary:
binary-to-provider traffic, telemetry export, fixture-site fetching, report
serving, loopback behaviour, or an OS service. Gate it on
`SHHH_TEST_CONTRACT=1`, skip when it is not selected, and fail clearly when the
selected host cannot provide the prerequisite. Run it through `make
test-contract`; it is intentionally not evidence from a contained session.

Put containment-specific checks behind the `integration` build tag and run
them with `make test-integration`. A missing supported mechanism fails that
selected runner. A platform where the mechanism cannot exist may skip, but the
skip is never a green substitute for the selected runner.

## Before finishing

1. Run the narrow package test with a private `GOCACHE` when the shell is
   constrained.
2. Run `make docs-check` after changing testing guidance.
3. Run the relevant contract or integration target only when the host can
   satisfy its declared prerequisite.
4. Keep a fixture migration and its production seam in the same reviewable
   change; do not silently hide a real-boundary test in the default suite.
