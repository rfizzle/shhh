package chat

import (
	"path/filepath"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/components"
)

var (
	rendererMu     sync.Mutex
	cachedWidth    int
	cachedMono     bool
	cachedRenderer *glamour.TermRenderer
)

// markdownStyle is the glamour style the transcript renders assistant prose
// with. Mono mode (S-095) swaps the coloured "dark" theme for "ascii", which
// marks emphasis, headings and code with characters instead of colour — the
// invariant applied to prose: the ** stays when the colour goes.
func markdownStyle() string {
	if components.Mono() {
		return "ascii"
	}
	return "dark"
}

func getRenderer(width int) *glamour.TermRenderer {
	rendererMu.Lock()
	defer rendererMu.Unlock()
	mono := components.Mono()
	if cachedRenderer != nil && cachedWidth == width && cachedMono == mono {
		return cachedRenderer
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath(markdownStyle()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	cachedRenderer = r
	cachedWidth = width
	cachedMono = mono
	return r
}

func renderMarkdown(text string, width int) string {
	return trimBlankLines(renderMarkdownRaw(text, width))
}

// trimBlankLines finishes a whole document the way renderMarkdown has always
// finished one: the blank lines glamour puts around it go, and nothing inside
// it moves.
//
// The leading half was a strings.TrimSpace until S-155, and it worked by
// accident: glamour v1 opened every line with its style escape and put the
// document's two-column margin *after* it, so a leading TrimSpace stopped at
// the escape and the margin survived. v2 writes the margin as plain text
// ahead of the escape, and the same call ate it — which set the opening line
// of every paragraph two columns left of the rest of it, and told the
// selection's soft-wrap rule that the first row belonged to a different
// block (§15, select.go).
//
// The trailing half is unchanged, and is still the whole trailing run: a
// finished document has nothing after it, so its last line's padding is not
// holding anything up. (renderUnfinished keeps that padding, because there
// the seam does — streammd.go.)
func trimBlankLines(s string) string {
	return dropLeadingBlankLines(strings.TrimRight(s, " \t\n"))
}

// dropLeadingBlankLines removes the empty lines a render opens with, and
// stops at the first line that has anything under its escapes — whose own
// indent is the document margin and is kept.
func dropLeadingBlankLines(s string) string {
	for {
		nl := strings.IndexByte(s, '\n')
		if nl < 0 || strings.TrimSpace(ansi.Strip(s[:nl])) != "" {
			return s
		}
		s = s[nl+1:]
	}
}

// renderMarkdownRaw is the same as renderMarkdown without the trim. The
// streaming cache glues two renders together and needs the padding the trim
// takes off the end: a coloured terminal holds it in place with an escape,
// mono has nothing holding it, and the seam between two blocks is exactly
// where the difference shows (streammd.go).
func renderMarkdownRaw(text string, width int) string {
	if width <= 0 {
		width = 80
	}
	r := getRenderer(width)
	if r == nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	return out
}

// The syntax register (DESIGN-TUI.md §3b, §10a). Diff bodies used to
// highlight with stock monokai — greens, pinks and oranges from outside the
// product, sitting next to an add/del gutter drawn from the palette, two
// unrelated colour systems in one card. The theme below is the palette
// instead, read as a register rather than as state: structure in info, values
// in accent, the names a reader scans for a step brighter, the glue between
// them dimmer, comments dim.
//
// Four tokens are deliberately absent. Add, del, hunk and spin say something
// about the row — this line was added, that one removed, this is where the
// hunk starts, this is moving — and a string literal inside a removed line
// saying "added" is the card contradicting itself. The rule is checked in
// highlight_test.go, not just written here.
//
// The map is keyed on the broadest chroma type that should carry the tone;
// syntaxTone walks a token's parents to it, which is the inheritance a
// chroma.Style would have done and the reason none is built.
var syntaxTones map[chroma.TokenType]components.Token

// applySyntaxTones rebuilds the register from a token set. It is called from
// applyPalette with the styles, so a palette swap reaches the diff body the
// same way it reaches everything else.
func applySyntaxTones(p components.ColorTokens) {
	syntaxTones = map[chroma.TokenType]components.Token{
		chroma.Text:    p.Body,
		chroma.Name:    p.Body,
		chroma.Comment: p.Dim,
		// Structure: what the language says about itself.
		chroma.Keyword:       p.Info,
		chroma.NameTag:       p.Info,
		chroma.NameAttribute: p.Info,
		chroma.NameNamespace: p.Info,
		// Values: what the code is carrying.
		chroma.Literal: p.Accent,
		// The names a reader scans a diff for. Bright is a step on the grey
		// ladder rather than a hue, which is what keeps the register at two
		// colours (§10f: the mono capture of a diff has no highlighting at
		// all, so the register never has to survive the swap — it has to
		// survive being read in colour beside the gutter).
		chroma.NameFunction: p.Bright,
		chroma.NameClass:    p.Bright,
		chroma.NameBuiltin:  p.Bright,
		chroma.NameConstant: p.Bright,
		// The glue: operators, punctuation, the characters between the words.
		chroma.Operator:    p.Dimmer,
		chroma.Punctuation: p.Dimmer,
	}
}

// syntaxTone resolves one chroma token type to a palette token, walking up
// the type's parents the way a chroma.Style resolves inheritance —
// LiteralStringDouble to LiteralString to Literal. It reports false for a
// type nothing above it claims, which renders in the diff kind's own colour.
func syntaxTone(t chroma.TokenType) (components.Token, bool) {
	for ; t != 0; t = t.Parent() {
		if tone, ok := syntaxTones[t]; ok {
			return tone, true
		}
	}
	return components.Token{}, false
}

// lexerCache memoizes matchLexer. lexers.Match glob-matches the basename
// against every registered lexer's patterns — hundreds of filepath.Match calls
// per lookup — and every diff card re-derives its highlighter on each render,
// so a resize in a code-heavy session pays for the whole registry once per
// file per frame. The answer for a basename never changes, so it is looked up
// once and kept, misses included.
var lexerCache sync.Map // basename -> chroma.Lexer, nil when nothing matches

// matchLexer returns the coalesced chroma lexer for base, or nil when no lexer
// claims it. The returned lexer is shared across callers; chroma guards its own
// lazy rule compilation, and tokenising holds no state of its own.
func matchLexer(base string) chroma.Lexer {
	if v, ok := lexerCache.Load(base); ok {
		lexer, _ := v.(chroma.Lexer)
		return lexer
	}
	lexer := lexers.Match(base)
	if lexer != nil {
		lexer = chroma.Coalesce(lexer)
	}
	lexerCache.Store(base, lexer)
	return lexer
}

// diffSyntax returns a per-line syntax highlighter for path, or nil when no
// lexer matches (the diff then renders with plain diff colors).
func diffSyntax(path string) components.Syntax {
	// Mono declines highlighting outright rather than collapsing the register
	// onto its two greys: a diff body is where the +/- styling is already
	// carrying the distinction that matters, and a second grey ladder over it
	// would be decoration the reader has to unpick (S-095, §10f).
	if components.Mono() {
		return nil
	}
	lexer := matchLexer(filepath.Base(path))
	if lexer == nil {
		return nil
	}
	return func(line string) []components.Segment {
		it, err := lexer.Tokenise(nil, line)
		if err != nil {
			return nil
		}
		var segs []components.Segment
		for _, tok := range it.Tokens() {
			tone, _ := syntaxTone(tok.Type)
			segs = append(segs, components.Segment{Text: tok.Value, Color: tone})
		}
		// Lexers append the trailing newline they require; the renderer works
		// on bare lines, so trim it to reconstruct the input exactly.
		if n := len(segs) - 1; n >= 0 {
			segs[n].Text = strings.TrimSuffix(segs[n].Text, "\n")
			if segs[n].Text == "" {
				segs = segs[:n]
			}
		}
		return segs
	}
}
