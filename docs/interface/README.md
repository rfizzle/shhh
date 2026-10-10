# Interface

The rules every surface obeys, and what each surface is for.

| Document | What it covers |
|---|---|
| [`principles.md`](principles.md) | The five invariants and the grammar that follows from them |
| [`surfaces.md`](surfaces.md) | What each surface is for and when the session shows it |
| [`departures.md`](departures.md) | Where the implementation deliberately differs from the design system, and why |
| [`reserved-keys.md`](reserved-keys.md) | The chords the desktops, terminals and multiplexers take, which the register may not spend |

**The goldens are the design record.** Every surface is captured by its golden
test at four widths in two palettes, and every driven scene leaves a text and
a colour capture for each step it names; both are the binary's own output, so
neither can drift from it. `make design` lays them out as a static book under
`docs/design/` (not committed, built on demand): one page per golden with the
110-column colour render first, the other widths below it, the mono render on a
toggle, and the section of [`surfaces.md`](surfaces.md) that speaks for it,
then a page per scene. Open `docs/design/index.html`. A surface that is not
built yet is drafted as a text panel in its story, in the golden's shape, and
approved there before the code.

**Exact visual specification is not here.** Column widths, colour rungs and
glyph assignments are what the goldens capture, and `make design` builds them
into the book. The Claude Design project was retired
on 2026-10-10; a drawing of it that a reader still holds is old
([departures](departures.md#the-design-project-was-retired-and-its-last-drawings-are-old)).

Re-drawing a golden in Markdown produces a second source of truth that
disagrees with the first, and the disagreement is found by a reader who cannot
tell which one is stale. These documents say what the rules *are* and why they
hold; the goldens say what they measure.
