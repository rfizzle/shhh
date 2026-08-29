---
name: sound-patterns
description: How to decide whether a pattern, convention or claim about this codebase is sound — read and measure the Go standard library on this machine instead of asserting from memory. Use when choosing between approaches with no local precedent, when about to claim code is over- or under-something (over-commented, too long, too clever), when a reviewer and an author disagree on style, or when writing guidance others will follow.
---

# Deciding whether a pattern is sound

Two defaults. **When there is no local precedent and no strong reason, do what
the standard library does.** And **when you are about to assert something about
this codebase, measure it first.**

The stdlib is on this machine. `go env GOROOT` gives the path; `$GOROOT/src`
is the whole corpus, and `go doc <pkg>.<Sym>` reads the API without leaving the
terminal. It is a large, adversarially reviewed body of Go maintained for
decades — the best available answer to "what does normal look like".

## Read it; do not remember it

Recall is where this goes wrong. Checking `log/slog` for how it shapes optional
configuration, the obvious grep for `type Options struct` returns nothing: the
type is `HandlerOptions`. A claim built on the remembered name would have been
confidently wrong and unfalsifiable in review.

```bash
GOROOT=$(go env GOROOT)
grep -rn 'Options struct' $GOROOT/src/log/slog/*.go   # find the real shape
go doc log/slog.HandlerOptions                        # read the contract
```

## Measure, and normalize

Raw counts lie. Comparing naked returns between this repo and four stdlib
packages: 617 in the stdlib against 184 here, which reads as "the stdlib uses
them three times more". Per thousand lines it is 6.47 against 1.51 — more than
four times — and the direction of the surprise is the opposite of what the raw
numbers suggested.

```bash
# always divide by size, and compare distributions rather than anecdotes
GOROOT=$(go env GOROOT)
find $GOROOT/src/strings $GOROOT/src/os -name '*.go' ! -name '*_test.go'
```

Compare medians and p90s, not maxima. One 256-line comment block in the stdlib
says nothing about its habits; the median and p90 do.

## Take the right part of the corpus

The stdlib spans nearly two decades and its old code carries scars. For current
idiom read what was added recently — `log/slog`, `slices`, `maps`, `cmp`,
`iter`, `unique` — and treat `net/http` or `os` as evidence about longevity and
compatibility, not about how to write a new package today.

## Where the stdlib is the wrong reference

Say so out loud rather than forcing the comparison:

- **It is a library under a permanent compatibility promise.** This module is
  internal and can rename anything. Patterns that exist to avoid ever breaking
  an API are not free advice here.
- **It is read by millions of strangers.** Its doc-comment density answers a
  question an internal package does not have.
- **Some shapes exist because a language feature did not.** Pre-generics
  workarounds are history, not guidance.
- **Interactive and TUI code has no stdlib analogue.** There is nothing there
  about surfaces, redraw budgets or keyboard ownership. Reach for the local
  documentation in `docs/` instead.

## Soundness tests that need no corpus

The stdlib says what is normal, not what is correct. Independently of it, ask:

- **Can this be wrong silently?** A boolean can be wrong; a function that does
  not contain the code cannot.
- **Does it fail closed?** When the check errors, does the unsafe path open?
- **Is the invariant held by the type system, or by everyone remembering?**
- **Does the failure symptom point at its cause?** If not, that gap is what the
  comment is for.
- **What does it cost to reverse?** Prefer the cheaper mistake.

## Two ways this goes wrong

Both were caught in this repository rather than imagined.

- **Guidance derived from what you just changed inherits its shape.** A comment
  standard written straight after a large deletion contained only rules about
  restraint, and said nothing about a comment that is *missing* — the failure
  that leaves nothing on screen to notice. Check new guidance against the code
  you did **not** touch.
- **Verify a number before you build on it.** A first pass reported 61% of
  exported functions undocumented; it had counted methods on unexported types
  and test helpers. The real figure was 20%, and the sample showed those were
  self-describing. A section fixing a non-existent problem was one commit away.

A measurement you have not sanity-checked against a sample is a guess with a
decimal point.
