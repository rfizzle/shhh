---
name: documentation
description: How shhh writes documentation and code comments — where a fact belongs, how code cites docs/, and the test a comment has to pass to earn its place. Use when writing or editing anything in docs/, when writing a comment that explains a decision, when reviewing whether a comment earns its length, or when tempted to reference a story, a spec section, or when something was built.
---

# Documentation and comments

Two rules carry most of this. **Each document answers exactly one question.**
**Code cites documents; documents never cite code.**

## Where a fact belongs

| Where | Question it answers |
|---|---|
| `README.md` | How do I use shhh? |
| `docs/product.md` | What is shhh, and who is it for? |
| `docs/architecture.md` | What are the big shapes, and why those? |
| `docs/capabilities/` | What can it do, and why does that exist? |
| `docs/interface/` | What must every surface obey? |
| the `shhh Design System` project | What does it look like, exactly? |
| `AGENTS.md` | Where is the code, what are the traps? |
| a code comment | Why is *this* line like this? |

A fact filed under the wrong question rots there. A rendering rule in a
delivery log is invisible; a roadmap in a design document is stale in a sprint.

Full architecture: `docs/README.md`. Repo-specific rules: `AGENTS.md`.

## The citation convention

A document names no Go symbol — it would drift silently the moment the symbol
is renamed. The map from intent to code is `AGENTS.md`; the map from code to
intent is the citation in the comment:

```go
// Commands always carry the mutation rail: shhh cannot know whether a command
// wrote something, so it assumes it did.
// See docs/interface/principles.md#weight-tracks-risk.
```

- **The reason goes in the comment as prose.** The citation is a pointer to the
  long form, never a substitute — a reader who does not open the document must
  still understand why the line is the way it is.
- **Cite only for product and design decisions.** Local mechanics do not get
  one: `SetMaxOpenConns(1)` is explained by the sentence beside it.
- **Headings are anchors — treat one like an exported symbol.** Renaming one
  breaks every citation to it.
- **A section nothing cites is either wrong or unnecessary.**

`make docs-check` verifies every citation resolves and lists uncited documents.

## What a comment has to earn

A comment earns its place by saying something the code cannot. Before writing
one, decide which of these it is — if it is none, delete it.

- **Why this and not the obvious alternative.** The reader is about to
  "simplify" it back.
- **What breaks if you change it, and how that failure looks.** Lead with the
  symptom, because the symptom is what the next person actually sees. "Don't
  put the call id in that field; it addresses every result to a function the
  model never called, and the model just calls again" beats any restatement of
  the rule.
- **A constraint from outside this file.** A protocol quirk, an OS limit, a
  vendor's dialect.
- **A non-obvious invariant** — especially one the type system does not hold.

Length is not the test; density is. A long comment that names a real failure
is worth more than three lines restating the signature — the most valuable
comments in this tree are among its longest, and the least valuable were
one-line cross-references.

**Measure before assuming a codebase is over-commented.** This one reads as
verbose and is not: its comment-to-code ratio and its block lengths both sit
just inside the Go standard library's. Volume is not the problem; a comment
that says nothing is, at any length. How to run that comparison — and when the
stdlib is the wrong yardstick — is the `sound-patterns` skill.

## When a comment is missing

The opposite failure, and the easier one to miss because nothing on screen
looks wrong. Write one when:

- **An exported identifier's name does not say what it returns, costs or
  mutates.** `Resolve()` and `Append()` need a line; `DeleteSnippet()` does not.
- **A branch is deliberately empty or a value deliberately ignored.** Without a
  line saying so it reads as an unfinished edit.
- **A number was chosen rather than derived.** A threshold, a cap, a timeout:
  say what it was measured against.
- **A caller has to maintain an invariant** the signature cannot express.
- **The code works around something outside it** — a terminal, a vendor's
  dialect, an OS limit.

## What never earns its place

These four were removed wholesale from this codebase — about 2,600 references.
Worked before/after examples: `references/examples.md`.

- **Story, sprint or backlog identifiers** (`S-060`, `E-018`), or anything
  under `.plan/`. Planning is not in this repository, so the reference points
  at something the reader cannot open — and it answers "when was this built",
  which is not a question the code should ask. Planning cites the capabilities
  in `docs/`; `docs/` never cites planning. `make docs-check` fails on one.
- **Cross-references the sentence already contains.**
  `Detail bodies indent, they do not re-grid (§6a)` says it twice — once
  usably, once as a lookup. Delete the pointer; keep the words.
- **History as explanation.** "Story X added the fifth option" describes a
  calendar, not the software. Say what it does. History earns its place only
  when it stops a regression, and then it names *what broke*, not when:
  "this was a TrimSpace once, and it worked by accident."
- **A restatement of the code.** If the comment and the signature say the same
  thing, the comment is a second place to update and a second place to be
  wrong.

## Never put a reference in data

A doc or spec reference belongs in a comment. In a string literal it becomes
test output or an error message; in a golden fixture's label it becomes
committed expected output, which couples editing a heading to regenerating
goldens. This is a real bug this repo had. `make docs-check` fails on it.

## Before you finish

- `make docs-check` — citations resolve, nothing uncited, no story or spec
  references in code, strings or goldens.
- Comments wrap at ~78 columns, like the code around them.
