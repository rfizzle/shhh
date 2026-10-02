package chat

import (
	"path/filepath"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/markdown"
)

// mdOptions is how the transcript asks for a render: the pane, the palette
// state, and the fence highlighter that puts a code block in the same syntax
// register the diff view uses (fenceSyntax).
func mdOptions(width int) markdown.Options {
	return markdown.Options{Width: width, Mono: components.Mono(), Syntax: fenceSyntax}
}

// replyOptions is the same render set at the body column, where the
// catalogue puts every block of prose: the reply and a paragraph the model
// wrote between its calls start where a card's sentence and a thought do,
// four columns in, rather than at the markdown margin a sent message keeps
// for its prompt mark (docs/interface/surfaces.md#the-leading-columns). Every
// render of the model's own prose takes it, the answer still arriving and
// the layout its headings are found in included, so the glued stream and
// the finished reply are the same rows.
func replyOptions(width int) markdown.Options {
	o := mdOptions(width)
	o.Indent = components.GridDetailIndent - markdown.Margin
	return o
}

func renderMarkdown(text string, width int) string {
	return trimBlankLines(renderMarkdownRaw(text, width))
}

// renderReaderMarkdown is the same render in the reader's own register: a
// sent message is drawn brighter than the reply under it, because the
// transcript labels neither half and the weight is what tells them apart
// (render.go, markdown.ProseBright).
func renderReaderMarkdown(text string, width int) string {
	o := mdOptions(width)
	o.Prose = markdown.ProseBright
	return trimBlankLines(strings.Join(markdown.Blocks(text, o), "\n"))
}

// trimBlankLines finishes a whole document: the last row's padding comes off,
// because a finished document has nothing after it and that padding is not
// holding anything up.
//
// The leading half no longer has anything to do — internal/ui/markdown emits
// no blank row before the first block or after the last — and it is kept
// because the streaming cache glues renders and this is the one place that
// says what a finished one looks like. It is a strings.TrimRight rather than
// a TrimSpace: TrimSpace would eat the document margin off the opening row,
// which is what set the first line of every paragraph two columns left of the
// rest of it and told the selection that the first row was a different block
// (select.go).
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
	return strings.Join(markdown.Blocks(text, replyOptions(width)), "\n")
}

// fenceLexerCache memoizes fenceLexer, for the reason lexerCache exists: the
// lookup is a scan of the registry, and the transcript re-derives every
// fence's highlighter on each frame.
var fenceLexerCache sync.Map // language -> chroma.Lexer, nil when nothing claims it

// fenceLexer is the highlighter for a fence's info string.
//
// It is lexers.Get rather than the lexerCache's Match, because the two are
// asked different questions: a diff hunk has a filename to glob, and a fence
// has the word the model wrote after the backticks.
func fenceLexer(lang string) chroma.Lexer {
	if v, ok := fenceLexerCache.Load(lang); ok {
		lexer, _ := v.(chroma.Lexer)
		return lexer
	}
	lexer := lexers.Get(lang)
	if lexer == lexers.Fallback {
		// The fallback lexer emits one token for the whole line, which is
		// the same answer as no highlighting and costs a tokenise to say.
		lexer = nil
	}
	if lexer != nil {
		lexer = chroma.Coalesce(lexer)
	}
	fenceLexerCache.Store(lang, lexer)
	return lexer
}

// fenceSyntax highlights a fenced block through the same palette register
// the diff bodies use (syntaxTones), so a Go function reads the same colour
// whether the model quoted it or changed it.
//
// The block is tokenised once, whole, and the tokens are split back into its
// lines. Lexing it a line at a time read every construct that spans lines as
// code: the digits inside a three-line block comment came out as numbers, a
// Python docstring's words as names, and a heredoc's body as shell. A token
// that crosses a line break is cut at the break and each piece keeps the
// token's tone.
//
// The style it builds per segment is the one style in this package that is
// not a token on Styles, and it cannot be: which tone a segment takes is the
// lexer's answer about that segment, not a slot a surface draws from. The
// palette half is still built once — syntaxTones is the register, rebuilt by
// applyPalette — and this only dresses a run of text in the tone that
// register already chose.
func fenceSyntax(lang string, lines []string) [][]markdown.Segment {
	lexer := fenceLexer(lang)
	if lexer == nil {
		return nil
	}
	it, err := lexer.Tokenise(nil, strings.Join(lines, "\n")+"\n")
	if err != nil {
		return nil
	}
	out := make([][]markdown.Segment, 1, len(lines))
	for _, tok := range it.Tokens() {
		var style lipgloss.Style
		tone, styled := syntaxTone(tok.Type)
		if styled {
			style = lipgloss.NewStyle().Foreground(tone.Color())
		}
		for i, piece := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				out = append(out, nil)
			}
			if piece != "" {
				n := len(out) - 1
				out[n] = append(out[n], markdown.Segment{Text: piece, Style: style, Styled: styled})
			}
		}
	}
	// The newline the lexer was handed after the last line opens one more
	// line than the block has, and it holds nothing.
	if len(out) == len(lines)+1 && len(out[len(lines)]) == 0 {
		out = out[:len(lines)]
	}
	if len(out) != len(lines) {
		return nil
	}
	return out
}

// The syntax register (docs/interface/surfaces.md#the-diff-view). Diff
// bodies used to highlight with stock monokai — greens, pinks and oranges
// from outside the product, sitting next to an add/del gutter drawn from the
// palette, two unrelated colour systems in one card. The theme below is the
// palette instead, read as a register rather than as state: structure in
// info, values in accent, the names a reader scans for a step brighter, the
// glue between them dimmer, comments dim.
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
		// colours (the mono capture of a diff has no highlighting at
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
// against every registered lexer's patterns — hundreds of filepath.Match
// calls per lookup — and every diff card re-derives its highlighter on each
// render, so a resize in a code-heavy session pays for the whole registry
// once per file per frame. The answer for a basename never changes, so it is
// looked up once and kept, misses included.
var lexerCache sync.Map // basename -> chroma.Lexer, nil when nothing matches

// matchLexer returns the coalesced chroma lexer for base, or nil when no
// lexer claims it. The returned lexer is shared across callers; chroma guards
// its own lazy rule compilation, and tokenising holds no state of its own.
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
	// would be decoration the reader has to unpick.
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
