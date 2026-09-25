---
name: observe-read
description: How to read shhh's own session record (the agent_sessions and agent_events tables behind `shhh observe`) to find where sessions spend rounds, tokens and failures, and how to turn a reading into a proposal that clears a high bar — a tool, an argument default, a result wording, a host install, or nothing. Use when asked whether shhh needs a new model tool, why sessions are slow or long, what the model reaches for and what it never uses, or whether a design decision the docs defer to a measurement (the repository map) should move. Do not use it for reviewing one session's transcript; that is `shhh observe session <id> --transcript`.
---

# Reading the record before proposing anything

Every session shhh runs writes a content-free record: one row per session,
one event per tool call, decision, turn and signal, placed at a turn and a
round. It is the only evidence about what the model actually does with the
surface it is given, and a proposal about the surface that is not read
against it is a preference. The bar this skill holds a proposal to is the one
in `AGENTS.md`'s scope section: a broadly applicable, low-risk change that
prevents serious correctness, security, data-loss, reliability or major
maintenance harm, or recovers a large share of what sessions spend. A
negligible gain is reported as "nothing clears the bar", which is a finding.

## Open the record

The store is `shhh.db` under the data directory — `storage.Dir`, which is
`$XDG_DATA_HOME/shhh` or `~/.local/share/shhh` on every platform. Read it
with `sqlite3` directly, read-only:

```bash
DB=${XDG_DATA_HOME:-$HOME/.local/share}/shhh/shhh.db
sqlite3 "file:$DB?mode=ro" 'select count(*) from agent_sessions; select min(started_at), max(started_at) from agent_sessions'
```

Two traps before the first query. **The installed `shhh` may be older than
the store**, and the dashboard it draws is only as good as the schema the
binary expects; when `shhh observe` errors, build the checkout's binary into
the scratch directory and try that before concluding anything. **A column the
query names may be missing from a store that upgraded across a migration
inserted mid-list** — check `select max(version) from schema_version` against
`len(migrations)` in `internal/storage/migrate.go` and `pragma
table_info('agent_sessions')` against the columns the failing query names.
Either way the direct queries below still work, and a broken dashboard is
itself a finding worth filing.

The vocabulary every code column draws from is `internal/observe`: the tool
outcomes and failure classes (`ClassFromResult`), the decision reasons
(`Reason*`), the signals (`Signal*`) and the turn outcomes (`Turn*`). Read
the constants' comments there for what a code means rather than guessing
from its spelling. Signals put the code in `reason` and, for a few, a
qualifier in `tool`; a tool event's `tool` is the tool name and its `reason`
is the failure class.

## The readings

Take all of these; each answers a different question and a proposal usually
rests on two or three together.

**What the model reaches for, and what fails.** The first table of any
reading. A tool with zero rows across many sessions either was never
registered on this machine or is not being reached for; those are different
findings and the next section tells them apart.

```sql
select tool, count(*) n, sum(outcome<>'ok') notok
from agent_events where kind='tool' group by tool order by n desc;

select tool, outcome, reason, count(*) from agent_events
where kind='tool' and outcome<>'ok' group by 1,2,3 order by 4 desc;
```

**Read-tier volume against writes.** The read tools are `read_file`,
`search`, `glob`, `list_directory`, `git`, `fd`, the six language-server
verbs and the structural readers; the writes are `edit_file` and
`write_file`. The ratio is the cost of finding things.

**Reads before the first write.** This is the number
`docs/capabilities/coding-agent.md#where-a-map-would-sit` defers the
repository-map decision to — the doc names a dozen as the trigger. Take it
over the sessions that wrote at all, and report the median and the range,
never the mean:

```sql
with fw as (select session_id, min(id) fid from agent_events
            where kind='tool' and tool in ('edit_file','write_file') group by 1)
select n, count(*) from (
  select e.session_id, count(*) n from agent_events e
  join fw on fw.session_id=e.session_id
  where e.kind='tool' and e.id<fw.fid group by 1
) group by 1 order by 1;
```

**What follows what.** Adjacency is how a round is shown to be spent on a
tool's result rather than on the task: an edit followed by a re-read of the
same file, a command followed by `evidence`, `evidence` followed by
`evidence` (paging), a search followed by the same search. The record holds
no arguments, so adjacency by tool name is the closest reading there is.

```sql
with t as (select session_id, tool, outcome,
                  lead(tool) over (partition by session_id order by id) nxt
           from agent_events where kind='tool')
select tool, outcome, nxt, count(*) from t
where tool in ('edit_file','execute_command','evidence') group by 1,2,3 order by 4 desc;
```

**Rounds per turn.** `max(round)` grouped by session and turn, as a
distribution. The tail is where the check-in machinery, the round cap and
the context recovery are exercised; a proposal about efficiency is judged
against the tail, not the median.

**Signals, decisions, turn outcomes.** Repeat notices per tool
(`SignalRepeat`, qualifier in `tool`), check-ins and their verdicts, trims
and compactions, and the decision reasons — how much of the gate is the
classifier, the mode, or a person. `cap-paused` and `cancelled` turns are
the ones to read closely.

**Cohorts.** Split by `kind` (a session, a headless run, each child role),
by `project` (a fingerprint of the checkout) and by `started_at`. A reading
dominated by one project on one machine says something about that project
and machine; say so.

## Read the surface beside the record

A number means nothing without what the model was given. Read, in this
order:

- `internal/cli/registrable.go` — every optional definition a surface can
  register, and the conditions each is registered under (a binary on PATH, a
  key, a store, a trusted checkout).
- `internal/prompt/toolbox.go` — the one sentence the model reads about when
  each tool is the right answer, and `internal/prompt/system.go`'s
  `BuildAgent` for what the base prompt asks of it (a "re-read after
  editing" instruction, for instance, is a round the record can count).
- The host: `which gopls rust-analyzer pyright-langserver ast-grep fd sd jaq yq tokei pdftotext bwrap`.
  A tool that needs a binary the machine lacks was never in any session here,
  and every reading that would have gone through it went through `search`
  and `read_file` instead. Name the confound before the number.
- `docs/capabilities/` — the sections that already decided against something
  (the map, an embedding index, a task-list protocol, a child that asks, a
  mailbox between children) and the reason. A proposal that re-opens one has
  to say what the record shows that the reason did not anticipate; a
  proposal that does not know the section is not ready.
- `shhh doctor` and the tail of `shhh.log` beside the store: refusals,
  retries, tree-check budget warnings — the mechanisms that fail without
  stopping a session leave their only trace here.

## Confounds to name before concluding

- **The record is content-free by design.** No path, no command, no query.
  An `execute_command` that ended non-zero may be a failing test the model
  then fixed, which is the loop working; do not read exit-status failures as
  tool defects without adjacency showing the model was stuck.
- **Counts are not tokens.** A read is one round whatever the file's size,
  and `read_file` returns whole files by default; the lever on read cost is
  usually the size of what comes back, not the number of calls.
- **Small n.** Four calls at a hundred per cent failure is a thing to look
  at, not a rate. Say the count.
- **A per-session tool count folds turns together.** Reads before the first
  write can span a research turn and an implementing turn; that is what the
  doc's measurement is, but say it.
- **One machine, one project.** The author's own store is mostly shhh
  worked on by shhh. What is true there may not be true of a Python
  monorepo with pyright on PATH.

## Judging a proposal

Prefer the answer that is not a new tool. In order of how cheap and how
sure they are: a host install that turns on tools already built; a default
on an argument the model rarely sets (a page size, a context width); a result
wording that gives the model in one round what it spends a second round
asking for; a toolbox line or a definition's description; and only then a
new definition. A new tool is tokens in every request's prefix, one more
thing the tiers have to place, and one more surface the safety table has to
know; it clears the bar only where the record shows a repeated cost no
cheaper change reaches.

A tool that changes what the model can *do* rather than how cheaply it does
it is judged by the tiers first: a read is a closed verb set that cannot
act, a write goes through the mutating path and its card, a command through
the execute tier. If the candidate does not fit one tier cleanly, it is not
a tool here — see `AGENTS.md`'s tool security tiers and
`docs/architecture.md#tiers-not-permissions`.

Report separately, as a windfall, anything the reading turned up that is
not a proposal about the surface: a dashboard that will not open, a
migration that skipped a store, a mechanism whose warnings fill the log.
The reader decides whether to take it on.

## Writing it up

A proposal states the reading (the query and the numbers, the cohort, the
date range), the confound it survives, the change in one sentence, and the
channel it moves — the definition's schema or description, the toolbox line,
a prompt paragraph, or none — with the `docs/capabilities/` section the
reason will live in. That is the same "The model is told:" criterion every
backlog item that touches the model's reading carries, so a proposal written
this way is an item when the reader wants one; file it where this checkout
keeps its backlog, in the shape the items there already have, and never
cite the item from code or docs.

Where the answer is that nothing clears the bar, write that down with the
numbers that say so and the date, so the next reading starts from a
baseline rather than from the same argument.
