# Worked examples

Every pair below is a real change made in this repository. The four
anti-patterns account for roughly 2,600 removed references.

## The pointer the sentence already contains

```go
- // Detail bodies indent, they do not re-grid (§6a).
+ // Detail bodies indent, they do not re-grid.
```

The prose names the concept. The pointer added a lookup, not a fact. Worse
when both halves repeat:

```go
- // scroll gutter's (S-147, §10g).
+ // scroll gutter's.
```

**Test:** cover the parenthetical. If the sentence still says the same thing,
it was noise.

## History standing in for explanation

```go
- // S-114 added the fifth: the commands the generator did not pick.
+ // The fifth is the commands the generator did not pick.

- // glamour filled rather than against the pane. Before S-147 it measured
+ // glamour filled rather than against the pane. It used to measure
```

The *when* was never the useful part. Where the history does prevent a
regression, keep it and drop the identifier — it names what broke:

```go
// The leading half was a strings.TrimSpace once, and it worked by accident.
```

## A reference that resolves to nothing

`.plan/` is gitignored; `DESIGN-TUI.md` was deleted. Both were cited from
hundreds of comments, so both sent readers to something they could not open.

```go
- // Package scope holds a session's working scope: the directories the work is
- // allowed to reach (S-141).
+ // Package scope holds a session's working scope: the directories the work is
+ // allowed to reach
+ // (docs/capabilities/containment.md#scope-is-the-set-of-directories-the-work-may-reach).
```

The prose was already good. Only the anchor was wrong — that is the usual
case, so rewrite the anchor and leave the explanation alone.

## A reference living in data

```go
- {Label: "output · a program's own colours, re-painted (§10i)", ...}
+ {Label: "output · a program's own colours, re-painted", ...}

- palette = "mono (two greys, S-095)"
+ palette = "mono (two greys)"
```

Both render into golden files, so a spec reference was sitting in committed
expected output across 150 fixtures. Changing a heading would have meant
regenerating goldens. Test names had the same problem:
`name: "approval card (§2)"` — the name already said which surface it covered.

## A comment that earns every line

```go
// Gemini pairs tool results by function name, not by id. FunctionResponse.Name
// must be the name of the function called, and the API sends no functionCall.id
// at all — the ids in provider.ToolCall are ours. Don't "simplify" this back to
// putting ToolCallID in that field; it addresses every result to a function the
// model never called, and the model just calls again.
```

Long, and worth it. It names the alternative the reader is about to reach for,
and it leads with the symptom — *the model just calls again* — which is what
they will actually observe. The cause is invisible from the symptom, which is
exactly when a comment is worth writing.

## A comment that earns its place by being short

```go
conn.SetMaxOpenConns(1)
```

Explained by one sentence beside it: the workload is one interactive process,
WAL and a busy timeout handle concurrency, and a second connection buys nothing
while introducing lock contention that is miserable to reproduce. No citation —
this is a local mechanic, not a product decision.

## Rewriting at altitude

When a reference is load-bearing in a sentence, substitution fails and you have
to read it. An automated pass produced all of these:

```
the failure row row        ← "the §17a row" + phrase substitution
the meters's               ← "§10c's"
the config screen's screen ← "§19a's screen"
```

If a mechanical rewrite cannot produce prose you would have written, the site
needs a person. That is the whole reason the last few hundred were done by hand.
