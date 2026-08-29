package chat

// The streaming render (S-149,
// docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it).
// The contract the cache lives or dies by is the first test: every prefix of
// every document must render byte for byte the way renderMarkdown renders it
// whole. The rest is the boundary vocabulary and the win itself.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// streamCorpus is the shapes an assistant answer arrives in, one document per
// construct the boundary rules have an opinion about. A construct that is not
// here is a construct nothing is stopping from drifting.
var streamCorpus = map[string]string{
	"empty":            "",
	"bare prose":       "plain sentence with no structure at all\n",
	"headings":         "# Title\n\nBody paragraph.\n\n## Sub\n\nMore body.\n\n### Deep\n\nEnd.\n",
	"setext":           "Setext\n======\n\nafter\n\nAnother\n-------\n\ntail\n",
	"lists":            "- one\n- two\n  - nested a\n  - nested b\n- three\n\nafter the list\n\n1. first\n2. second\n\n   loose continuation\n\n3. third\n\nend\n",
	"fences":           "```\nno lang\n```\n\nmiddle\n\n~~~python\ndef f():\n    return 1\n~~~\n\nafter tildes\n",
	"inline":           "Text with `inline code` and **bold** and _em_ and a [link](https://x).\n\nSecond para.\n",
	"tables":           "| a | b |\n|---|---|\n| 1 | 2 |\n\nafter table\n\n| c | d |\n|---|---|\n| 3 | 4 |\n",
	"quotes":           "> quoted\n> more quote\n>\n> - list in quote\n\nafter quote\n\n> second quote\n\nend\n",
	"thematic breaks":  "---\n\nafter a thematic break\n\n***\n\nend\n\n___\n\nlast\n",
	"raw html":         "<div>\n  raw html\n</div>\n\nafter html\n\n<!-- comment -->\n\nend\n",
	"link refs":        "See [a][b] below.\n\n[b]: https://example.com\n\nAfter the definition.\n",
	"indented code":    "    indented code block\n    second line\n\nafter indented\n",
	"unicode":          "Unicode ✓ ⚙ ▎ and a 日本語 line that should wrap somewhere around here for sure.\n\nnext\n",
	"opens in a fence": "```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```\n",
	"blank runs":       "Para.\n\n\n\nThree blank lines above.\n\n \n\nblank-with-space above.\n",
	"paren ordered":    "1) paren ordered\n2) second\n\nafter\n",
	"long prose": strings.Repeat("A fairly long paragraph of prose that wraps at every width we test. ", 6) +
		"\n\n" + strings.Repeat("And a second one, just as long, so the document is big enough to matter. ", 6) + "\n",
}

// TestStreamingMarkdown_MatchesTheWholeRender is the contract. A message
// arrives a byte at a time, and at every one of those moments the cached glue
// must be indistinguishable from re-rendering the whole thing — not close, not
// visually the same, the same bytes. The selection is a pair of coordinates
// into this string (S-145) and the message is re-rendered whole the instant it
// freezes into an entry, so a byte of drift is a jump on the last token.
func TestStreamingMarkdown_MatchesTheWholeRender(t *testing.T) {
	monoRestore(t)
	for _, mono := range []bool{false, true} {
		components.SetMono(mono)
		for _, w := range goldenWidths {
			for name, doc := range streamCorpus {
				var s streamingMarkdown
				for i := 1; i <= len(doc); i++ {
					want := renderMarkdown(doc[:i], w)
					if got := s.Render(doc[:i], w); got != want {
						t.Fatalf("mono=%v w=%d %s: after %q\n got %q\nwant %q",
							mono, w, name, doc[:i], got, want)
					}
				}
			}
		}
	}
}

// TestStreamingMarkdown_AdvancesTheBoundary is the other half: correctness is
// free if the cache never fires. A document of finished blocks must leave all
// but its last block behind.
func TestStreamingMarkdown_AdvancesTheBoundary(t *testing.T) {
	doc := streamCorpus["long prose"]
	var s streamingMarkdown
	for i := 1; i <= len(doc); i++ {
		s.Render(doc[:i], 80)
	}
	if s.stablePrefix == "" {
		t.Fatal("the boundary never moved; every chunk paid for a whole render")
	}
	if !strings.HasPrefix(doc, s.stablePrefix) {
		t.Fatal("the stable prefix is not a prefix of the message")
	}
	// The first paragraph is stable long before the second one ends.
	if first := strings.Index(doc, "\n\n") + 2; len(s.stablePrefix) < first {
		t.Fatalf("the boundary stopped at %d, short of the first blank line at %d", len(s.stablePrefix), first)
	}
}

// TestStreamingMarkdown_DropsTheCache: the render is keyed on the two things
// renderMarkdown's own renderer is keyed on (highlight.go), plus the message
// itself. A resize, a palette swap (S-095) or a different message must not be
// answered out of the old cache.
func TestStreamingMarkdown_DropsTheCache(t *testing.T) {
	monoRestore(t)
	doc := streamCorpus["headings"]

	var s streamingMarkdown
	for i := 1; i <= len(doc); i++ {
		s.Render(doc[:i], 80)
	}

	if got, want := s.Render(doc, 60), renderMarkdown(doc, 60); got != want {
		t.Error("a resize did not drop the cache")
	}
	components.SetMono(true)
	if got, want := s.Render(doc, 60), renderMarkdown(doc, 60); got != want {
		t.Error("the mono swap did not drop the cache")
	}
	components.SetMono(false)

	other := "A different answer entirely.\n\nWith two paragraphs.\n"
	if got, want := s.Render(other, 60), renderMarkdown(other, 60); got != want {
		t.Error("a new message did not drop the cache")
	}
}

func TestBoundaryVocabulary(t *testing.T) {
	tests := []struct {
		fn   func(string) bool
		name string
		yes  []string
		no   []string
	}{
		{isFenceLine, "isFenceLine",
			[]string{"```", "```go", "~~~", "   ```", "````"},
			[]string{"``", "  x```", "    ```", "text"}},
		{isListMarker, "isListMarker",
			[]string{"- a", "* a", "+ a", "1. a", "12) a", "-\tx"},
			[]string{"-a", "-", "1.a", "1234567890. a", "text"}},
		{isATXHeading, "isATXHeading",
			[]string{"# a", "###### a", "#", "   ## b"},
			[]string{"####### a", "#a", "    # a", "text"}},
		{isThematicBreak, "isThematicBreak",
			[]string{"---", "***", "___", "- - -", "-- -", "  *****"},
			[]string{"--", "- -", "-*-", "- a", "text"}},
		{isSetextUnderline, "isSetextUnderline",
			[]string{"=", "===", "---", "  === "},
			[]string{"=a", "=-=", "", "text"}},
		{isHTMLBlockOpener, "isHTMLBlockOpener",
			[]string{"<div>", "</div>", "<!-- c -->", "<?php", "<![CDATA[x", "<!DOCTYPE", "<Script>"},
			[]string{"<3", "<-", "<<", "a <div>", "text"}},
		{isLinkRefDefinition, "isLinkRefDefinition",
			[]string{"[a]: http://x", "  [a b]: /y", "[a]:x"},
			[]string{"[a]:", "[]: x", "[a] x", "a [b]: c", "text"}},
	}
	for _, tc := range tests {
		for _, in := range tc.yes {
			if !tc.fn(in) {
				t.Errorf("%s(%q) = false, want true", tc.name, in)
			}
		}
		for _, in := range tc.no {
			if tc.fn(in) {
				t.Errorf("%s(%q) = true, want false", tc.name, in)
			}
		}
	}
}

func TestBlankLineBefore(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", -1},
		{"one line\n", -1},
		{"a\nb\n", -1},
		{"a\n\nb\n", 3},
		{"a\n\n\nb\n", 4},
		{"a\n  \nb\n", 5},
		{"a\n\nb\n\nc\n", 6},
	}
	for _, tc := range tests {
		if got := blankLineBefore(tc.in, len(tc.in)); got != tc.want {
			t.Errorf("blankLineBefore(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestStreamingRepaint_RidesTheTick is the throttle half of §10h. A chunk that
// lands while the tick chain is running records that a repaint is owed and
// leaves it to the tick; a chunk that lands with nothing ticking repaints
// itself, because nothing else is going to (§10c: one clock, and this is it).
func TestStreamingRepaint_RidesTheTick(t *testing.T) {
	m := readyModel(t)
	m.setTurnState(stateStreaming)

	// Nothing ticking: the chunk paints itself.
	updated, _ := m.Update(tokenMsg{text: "first chunk\n"})
	m = updated.(Model)
	if m.streamDirty {
		t.Fatal("with no tick chain running the repaint cannot be deferred")
	}
	if !strings.Contains(stripANSI(m.viewport.View()), "first chunk") {
		t.Fatal("the chunk should be on screen already")
	}

	// Ticking: the chunk waits for the tick.
	m.spinning = true
	updated, _ = m.Update(tokenMsg{text: "\n\nsecond chunk\n"})
	m = updated.(Model)
	if !m.streamDirty {
		t.Fatal("a chunk that lands while the chain runs should owe a repaint, not take one")
	}
	if strings.Contains(stripANSI(m.viewport.View()), "second chunk") {
		t.Fatal("the transcript should still be one tick behind")
	}

	updated, _ = m.Update(m.spinner.Tick())
	m = updated.(Model)
	if m.streamDirty {
		t.Fatal("the tick should have paid the repaint")
	}
	if !strings.Contains(stripANSI(m.viewport.View()), "second chunk") {
		t.Fatal("the tick should have put the chunk on screen")
	}
}

// TestStreamingRepaint_FreezesWithoutAJump is the property the byte contract
// exists for, asserted through the model rather than the cache: the transcript
// the last chunk leaves on screen is the transcript the frozen entry draws.
// A cache that drifted would show it here as a jump on the final token.
func TestStreamingRepaint_FreezesWithoutAJump(t *testing.T) {
	answer := streamCorpus["headings"]
	m := readyModel(t)
	m.setTurnState(stateStreaming)
	for i := 0; i < len(answer); i += 7 {
		updated, _ := m.Update(tokenMsg{text: answer[i:min(i+7, len(answer))]})
		m = updated.(Model)
	}
	streaming := m.renderHistoryRaw()

	m.finishStreaming()
	frozen := m.renderHistoryRaw()

	if !strings.Contains(frozen, strings.TrimSpace(renderMarkdown(answer, m.transcriptWidth()))) {
		t.Fatal("the frozen entry should hold the whole answer")
	}
	// The frozen entry ends in the newline every entry ends in (renderEntry);
	// everything before it has to be the same bytes.
	if strings.TrimRight(streaming, "\n") != strings.TrimRight(frozen, "\n") {
		t.Errorf("the transcript moved when the message froze\n streaming %q\n frozen    %q",
			stripANSI(streaming), stripANSI(frozen))
	}
}

// BenchmarkStreamingMarkdown measures the thing S-149 exists for: one answer
// arriving in chunks, rendered the old way and the new way.
func BenchmarkStreamingMarkdown(b *testing.B) {
	var doc strings.Builder
	for i := range 24 {
		fmt.Fprintf(&doc, "## Section %d\n\n", i)
		doc.WriteString("A paragraph of prose long enough to wrap at eighty columns, saying what the change does.\n\n")
		doc.WriteString("- a bullet\n- another\n- a third\n\n")
		fmt.Fprintf(&doc, "```go\nfunc f%d() error {\n\treturn nil\n}\n```\n\n", i)
	}
	src := doc.String()
	var cuts []int
	for i := 12; i < len(src); i += 12 {
		cuts = append(cuts, i)
	}
	cuts = append(cuts, len(src))

	b.Run("whole", func(b *testing.B) {
		for b.Loop() {
			for _, c := range cuts {
				renderMarkdown(src[:c], 80)
			}
		}
	})
	b.Run("stable prefix", func(b *testing.B) {
		for b.Loop() {
			var s streamingMarkdown
			for _, c := range cuts {
				s.Render(src[:c], 80)
			}
		}
	})
}
