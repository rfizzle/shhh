# DESIGN-TUI.md — retired

> **This document no longer exists.** Its durable content lives in
> [`docs/`](docs/README.md); its visual specification was always normative in
> the `shhh Design System` project in Claude Design, not here.
>
> This index is transitional. Code comments still carry `§` references to the
> section numbering this file used, and it exists so those resolve in one hop
> while they are migrated out. **Delete this file once nothing cites a `§`.**

## Why it went

It had grown to 5,371 lines doing four jobs at once: specification,
implementation blueprint, changelog (its preamble tracked which story added
which section), and a Markdown re-drawing of artboards that its own header
declared the design system was authoritative for. None of those is a document
that answers "what does shhh do, and why".

What replaced it, and the rules that keep it from happening again, are in
[`docs/README.md`](docs/README.md). How to cite a document from a code comment
is in [`AGENTS.md`](AGENTS.md#documentation).

## Where each section went

`cites` is how many references remain in code comments today — the number this
migration drives to zero.

| was  | what it was                    | now                                                                              | cites |
|------|--------------------------------|----------------------------------------------------------------------------------|------:|
| §1   | the interface principles       | docs/interface/principles.md#the-grammar                                         |     3 |
| §2   | the approval card              | docs/interface/surfaces.md#the-approval-card                                     |    12 |
| §2a  | the approval card              | docs/interface/surfaces.md#the-approval-card                                     |       |
| §2b  | the uncontained card           | docs/interface/surfaces.md#the-approval-card                                     |     2 |
| §2c  | the approval card              | docs/interface/surfaces.md#the-approval-card                                     |       |
| §2d  | the approval card              | docs/interface/surfaces.md#the-approval-card                                     |       |
| §2e  | the approval queue             | docs/interface/surfaces.md#the-approval-card                                     |     2 |
| §3   | the diff view                  | docs/interface/surfaces.md#the-diff-view                                         |     5 |
| §3a  | the collapsed diff row         | docs/interface/surfaces.md#the-diff-view                                         |     2 |
| §3b  | the expanded diff              | docs/interface/surfaces.md#the-diff-view                                         |     9 |
| §3c  | the full-screen diff           | docs/interface/surfaces.md#the-diff-view                                         |    13 |
| §4   | the selector                   | docs/interface/surfaces.md#selectors                                             |     5 |
| §4a  | the single-select card         | docs/interface/surfaces.md#selectors                                             |    59 |
| §4b  | the multi-select               | docs/interface/surfaces.md#selectors                                             |     2 |
| §4c  | the note select                | docs/interface/surfaces.md#selectors                                             |     5 |
| §4d  | the plan card                  | docs/interface/surfaces.md#selectors                                             |    12 |
| §5   | the inline confirm             | docs/interface/surfaces.md#the-inline-confirm                                    |    15 |
| §6   | the column grid                | docs/interface/principles.md#one-grid                                            |     3 |
| §6a  | the column grid                | docs/interface/principles.md#one-grid                                            |    48 |
| §6b  | the row kinds                  | docs/interface/principles.md#one-grid                                            |     5 |
| §6c  | the verb vocabulary            | docs/interface/principles.md#closed-vocabularies                                 |    12 |
| §6d  | the outcome vocabulary         | docs/interface/principles.md#closed-vocabularies                                 |    18 |
| §7   | reading mode                   | docs/interface/surfaces.md#reading-mode                                          |     9 |
| §7a  | reading mode                   | docs/interface/surfaces.md#reading-mode                                          |    74 |
| §7b  | the mid-sentence decision rule | docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard |    83 |
| §7c  | the register of keyed surfaces | docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard |    34 |
| §7d  | the key register               | docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard |    18 |
| §7e  | what the pointer can reach     | docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard |     5 |
| §8   | the vitals bar                 | docs/interface/surfaces.md#the-input-frame                                       |     6 |
| §8a  | the vitals segments            | docs/interface/surfaces.md#the-input-frame                                       |     6 |
| §8b  | the field-drop order           | docs/interface/surfaces.md#the-input-frame                                       |     5 |
| §8c  | the width ladder               | docs/interface/surfaces.md#the-input-frame                                       |     9 |
| §8d  | the turn status line           | docs/interface/surfaces.md#the-input-frame                                       |    24 |
| §9   | the agent manager              | docs/interface/surfaces.md#the-agent-manager                                     |       |
| §9a  | the agent list                 | docs/interface/surfaces.md#the-agent-manager                                     |    11 |
| §9b  | the attached view              | docs/interface/surfaces.md#the-agent-manager                                     |     1 |
| §9c  | detached approval routing      | docs/interface/surfaces.md#the-agent-manager                                     |     7 |
| §9d  | the agent manager              | docs/interface/surfaces.md#the-agent-manager                                     |       |
| §9e  | the agent manager              | docs/interface/surfaces.md#the-agent-manager                                     |       |
| §9f  | the agent manager              | docs/interface/surfaces.md#the-agent-manager                                     |       |
| §9g  | the fan-out lanes              | docs/interface/surfaces.md#the-agent-manager                                     |     5 |
| §9h  | the agent manager              | docs/interface/surfaces.md#the-agent-manager                                     |       |
| §10  | the palette                    | docs/interface/README.md                                                         |     1 |
| §10a | the palette                    | docs/architecture.md#colour-is-resolved-once-at-the-top                          |    26 |
| §10b | the backgrounds                | docs/interface/README.md                                                         |     3 |
| §10c | the meters                     | docs/interface/README.md                                                         |    63 |
| §10d | the glyph set                  | docs/interface/principles.md#colour-never-carries-meaning-alone                  |     4 |
| §10e | the drawing kit                | docs/interface/README.md                                                         |    11 |
| §10f | the mono palette               | docs/interface/principles.md#colour-never-carries-meaning-alone                  |    17 |
| §10g | the scroll gutter              | docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it        |    16 |
| §10h | the streaming render           | docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it        |     7 |
| §10i | foreign output                 | docs/interface/principles.md#one-grid                                            |     5 |
| §10j | the working label              | docs/interface/README.md                                                         |     1 |
| §10k | the terminal capability probe  | docs/architecture.md#only-one-place-speaks-to-the-terminal                       |    17 |
| §10l | the summons                    | docs/interface/surfaces.md#when-you-are-not-there                                |    11 |
| §10m | the transcript window          | docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it        |     7 |
| §10n | the layout model               | docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it        |     8 |
| §11  | the component contract         | AGENTS.md                                                                        |     2 |
| §12  | the input frame                | docs/interface/surfaces.md#the-input-frame                                       |    11 |
| §12a | the frame anatomy              | docs/interface/surfaces.md#the-input-frame                                       |    12 |
| §12b | the layout modes               | docs/interface/surfaces.md#the-input-frame                                       |     4 |
| §12c | the mode-aware accent          | docs/interface/surfaces.md#the-input-frame                                       |     1 |
| §12d | the attached frame             | docs/interface/surfaces.md#the-input-frame                                       |     4 |
| §12e | the layout accounting          | docs/interface/surfaces.md#the-input-frame                                       |     2 |
| §12f | the input frame                | docs/interface/surfaces.md#the-input-frame                                       |       |
| §12g | the staged rail                | docs/interface/surfaces.md#the-input-frame                                       |    15 |
| §12h | the staged-picture surface     | docs/interface/surfaces.md#a-staged-picture                                      |    14 |
| §12i | the input frame                | docs/interface/surfaces.md#the-input-frame                                       |       |
| §13  | the step outline               | docs/interface/surfaces.md#the-step                                              |    11 |
| §13a | where steps come from          | docs/interface/surfaces.md#the-step                                              |     2 |
| §13b | step folding                   | docs/interface/surfaces.md#the-step                                              |     9 |
| §13c | the verbosity setting          | docs/interface/surfaces.md#the-step                                              |     9 |
| §13d | the step detail view           | docs/interface/surfaces.md#the-step                                              |    11 |
| §14  | the mutation rail              | docs/interface/principles.md#weight-tracks-risk                                  |    16 |
| §15  | the inspector rail             | docs/interface/surfaces.md#the-inspector-rail                                    |    14 |
| §15a | the rail's scope rule          | docs/interface/surfaces.md#the-inspector-rail                                    |    23 |
| §15b | the rail blocks                | docs/interface/surfaces.md#the-inspector-rail                                    |     7 |
| §15c | the rail rules                 | docs/interface/surfaces.md#the-inspector-rail                                    |     9 |
| §15d | the session summary            | docs/interface/surfaces.md#the-session-summary                                   |     6 |
| §16  | the turn close                 | docs/interface/surfaces.md#the-turns-close                                       |    27 |
| §16a | review mode                    | docs/interface/surfaces.md#the-turns-close                                       |    20 |
| §17  | the recovery states            | docs/interface/surfaces.md#the-recovery-row                                      |     2 |
| §17a | the failure row                | docs/interface/surfaces.md#the-recovery-row                                      |    57 |
| §17b | the two recovery cards         | docs/interface/surfaces.md#the-recovery-row                                      |    14 |
| §17c | the start screen               | docs/interface/surfaces.md#the-start-screen                                      |    20 |
| §18  | —                              | docs/interface/surfaces.md#the-palette                                           |       |
| §18a | the palette surface            | docs/interface/surfaces.md#the-palette                                           |    10 |
| §18b | the one-shot result            | docs/interface/surfaces.md#the-one-shot-result                                   |     4 |
| §18c | the alternatives picker        | docs/interface/surfaces.md#the-one-shot-result                                   |       |
| §19  | the supporting screens         | docs/interface/surfaces.md#the-supporting-screens                                |    20 |
| §19a | the config screen              | docs/interface/surfaces.md#the-supporting-screens                                |    30 |
| §19b | the history browser            | docs/interface/surfaces.md#the-supporting-screens                                |    17 |
| §19c | the metrics screen             | docs/interface/surfaces.md#the-supporting-screens                                |    20 |
| §19d | the doctor screen              | docs/interface/surfaces.md#the-supporting-screens                                |    15 |
| §20  | the primitives audit           | docs/interface/departures.md                                                     |     2 |
| §21  | —                              | docs/capabilities/providers.md#a-gateway-is-a-provider-with-addresses-inside-it  |       |
| §21a | —                              | docs/capabilities/providers.md#a-gateway-is-a-provider-with-addresses-inside-it  |       |
| §21b | —                              | docs/capabilities/providers.md#a-gateway-is-a-provider-with-addresses-inside-it  |       |
| §21c | —                              | docs/capabilities/providers.md#a-gateway-is-a-provider-with-addresses-inside-it  |       |
| §22  | —                              | docs/interface/surfaces.md#outside-the-tui                                       |       |
| §22a | —                              | docs/interface/surfaces.md#outside-the-tui                                       |       |
| §22b | the exit banner                | docs/interface/surfaces.md#outside-the-tui                                       |     2 |

## Sections with no destination

Two parts of the old document were not carried into `docs/`:

- **§11 implementation notes** folded into [`AGENTS.md`](AGENTS.md), which is
  where the map from intent to code belongs.
- **§20's primitives audit** became
  [`docs/interface/departures.md`](docs/interface/departures.md). The audit
  framing was history, but the departures it recorded are live decisions.

The artboards themselves were not carried anywhere, deliberately. They are in
the design system, which is where they were normative all along.

