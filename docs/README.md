# shhh documentation

Each document here answers exactly one question. That is the whole
organising rule, and the rest of this page is what follows from it — where a
given fact belongs, and how code refers back to it.

## What answers what

| Where | Question it answers | Changes when |
|---|---|---|
| [`README.md`](../README.md) | How do I use shhh? | a release changes user-facing behaviour |
| [`product.md`](product.md) | What is shhh, and who is it for? | the product's purpose changes |
| [`architecture.md`](architecture.md) | What are the big shapes, and why those? | a structural decision is taken or reversed |
| [`capabilities/`](capabilities/) | What can it do, and why does that exist? | a capability is added, or its rationale shifts |
| [`interface/`](interface/) | What must every surface obey? | an invariant changes |
| the `shhh Design System` project | What does it look like, exactly? | the visual spec changes |
| [`AGENTS.md`](../AGENTS.md) | Where is the code, what are the traps? | the code moves |
| code comments | Why is *this* line like this? | the line changes |

The lifetimes are the point. A fact filed under the wrong question outlives
its usefulness there: a roadmap in a design document is stale within a sprint,
and a rendering rule in a delivery log is invisible to the next person who
needs it.

Planning lives outside the repository and stays there. Nothing here — no
document, no comment — refers to a story, a sprint or a backlog item. Work is
described by what it does, and a story that needs grounding cites a capability
in this tree rather than the other way round.

## The four rules

**1. A document names no Go symbol.** Not a function, not a file, not a
package. A document that can name a function drifts the moment that function is
renamed, and the drift is silent. Descriptions here are of behaviour and
intent; the map from intent to code is [`AGENTS.md`](../AGENTS.md), and the
map from code to intent is the citation in the comment.

**2. Code cites documents; documents never cite code.** The dependency points
one way on purpose. A comment that needs to explain a product decision states
the reason in prose and cites the section that holds the long form:

```go
// Commands always carry the mutation rail: shhh cannot know whether a command
// wrote something, so it assumes it did.
// See docs/interface/principles.md#weight-tracks-risk.
```

The path is the identifier. It is greppable, it opens in an editor, and it
explains itself without a lookup table — which is exactly what a bare story
number could not do.

**3. Headings are anchors, so treat one like an exported symbol.** Code cites
`file.md#heading`. Renaming a heading breaks every citation to it, silently.
Rename one only deliberately, and fix the citations in the same commit.

**4. A section nothing cites is either wrong or unnecessary.** The citations
are the test that the documentation still describes the software. A capability
section with no comment pointing at it has either been removed from the
product or was never a real decision.

The `documentation` skill in
[`.agents/skills/documentation/`](../.agents/skills/documentation/SKILL.md)
turns these rules into working guidance, with worked examples.

`make docs-check` enforces rules 3 and 4: it verifies every citation in the
tree resolves to a real file and heading, and lists documents nothing cites.
It runs as part of `make ci`.

## What does not belong here

- **Story and epic numbers** (`S-142`, `E-018`), and anything else that points
  at a planning tool. They record when work happened, not why the software is
  this way, and the planning that holds them is not part of this repository —
  so a reference to one is a reference a reader cannot follow.
- **Roadmaps, phases and open questions.** Planning artefacts age faster than
  anything around them, and none of them is a description of the software.
- **Exact visual specification.** Column widths, colour rungs, glyph
  assignments and the artboards themselves are normative in the
  `shhh Design System` project in Claude Design (projectId
  `8bd9b60d-8d86-403e-a591-c15a9ebccfd9`, read with the DesignSync tool).
  Re-drawing an artboard in Markdown produces a second copy that disagrees
  with the first. [`interface/`](interface/) says what the rules *are* and why
  they hold; the design system says what they *measure*.
- **Changelogs.** A document describes the software as it is now. How it got
  that way is what git is for.
