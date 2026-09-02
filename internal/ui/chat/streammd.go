package chat

// The streaming render (
// docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it).
//
// The transcript freezes every step block but the last (model.go), so
// the only thing it re-renders each frame is the message still arriving. That
// one message was re-parsed whole on every chunk: goldmark plus the ANSI
// renderer over the entire accumulated answer, once per token. The cost is
// quadratic in the length of the answer, and it is paid at exactly the moment
// the user is watching the screen — a long answer slows down as it gets
// longer, which reads as the model slowing down.
//
// The fix is the one Crush wrote down: a *stable prefix*. Find a position in
// the message after which no markdown construct can still be open, render
// everything before it once, keep that render, and thereafter render only the
// tail that follows it. The boundary search is incremental — it scans the
// delta, not the document — so a chunk costs O(chunk) instead of O(message).
//
// What shhh asks of it that Crush does not: **the glued render must be the
// byte-for-byte render of the whole message.** Two things depend on that. The
// selection (select.go) is a pair of coordinates into this string, and
// the message freezes into an `entryAssistant` the moment the stream ends —
// rendered whole, by renderMarkdown, from the top. A prefix cache that
// drifted a byte would move the selection under the cursor and jump the
// transcript on the last token. So every rule below is written to preserve
// the bytes, the contract is asserted over a corpus in streammd_test.go, and
// any position the rules cannot vouch for falls back to the full render
// rather than guessing.

import (
	"strings"

	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/markdown"
)

// streamingMarkdown caches the render of a stable prefix of one message.
//
// Invariants:
//
//   - stablePrefix is a literal byte prefix of the content last rendered. A
//     content that does not extend it drops the cache — the message was
//     replaced, not appended to.
//   - stablePrefixRender is the render of stablePrefix as an unfinished
//     document — renderMarkdown's output with the last line's padding still on
//     it, because a block follows (renderUnfinished). Trimming it the way a
//     finished message is trimmed is the last thing done, on the way out.
//   - width and mono are the two things renderMarkdown's own renderer is keyed
//     on (highlight.go). Either changing drops the cache.
type streamingMarkdown struct {
	width              int
	mono               bool
	stablePrefix       string
	stablePrefixRender string
	// Cumulative state measured at the boundary, so a new candidate is
	// validated against the delta rather than by re-scanning the prefix.
	// baseFences is always even — an odd count is never a safe boundary — so
	// the delta scan always starts outside a fence.
	baseFences     int
	baseListMarker bool
}

// Reset drops every cached field, so the next Render is a full render.
func (s *streamingMarkdown) Reset() {
	*s = streamingMarkdown{}
}

// Render returns renderMarkdown(content, width), reusing the cached prefix
// render where the boundary rules can vouch that gluing reproduces it
// exactly. Every uncertain case falls through to renderMarkdown itself.
func (s *streamingMarkdown) Render(content string, width int) string {
	if width <= 0 {
		width = 80
	}
	mono := components.Mono()

	// A new message, a resize, or a palette swap: nothing cached applies. Pay
	// for the full render, then — since the boundary search is cheap next to
	// the render we just did — seed a prefix for the chunk after this one.
	if width != s.width || mono != s.mono || !strings.HasPrefix(content, s.stablePrefix) {
		s.Reset()
		s.width, s.mono = width, mono
		out := renderMarkdown(content, width)
		if p := findSafeBoundary(content); p > 0 && !hasHTMLOrLinkRef(content[p:]) {
			s.adopt(content, p, width)
		}
		return out
	}

	boundary := s.boundaryAfter(content)
	if boundary < 0 {
		// Nothing in the message can be cut yet. Full render, cache untouched:
		// a later chunk may close whatever is open.
		return renderMarkdown(content, width)
	}
	// A link reference definition that has not arrived yet is the one hazard
	// the prefix cannot be checked for: `[ref][x]` renders as literal text
	// until `[x]: …` lands somewhere below it, and that landing rewrites a
	// prefix already frozen. So the tail is checked too, and a message that
	// grows one falls back to full renders from there on — the same answer
	// the prefix-side check gives once the definition is behind the boundary.
	if hasHTMLOrLinkRef(content[boundary:]) {
		return renderMarkdown(content, width)
	}
	if boundary > len(s.stablePrefix) {
		s.adopt(content, boundary, width)
	}
	tail := content[len(s.stablePrefix):]
	if strings.TrimSpace(tail) == "" {
		// The message ends at the boundary; the cached render is the answer,
		// finished the way renderMarkdown finishes a whole document.
		return trimBlankLines(s.stablePrefixRender)
	}
	return trimBlankLines(s.stablePrefixRender + "\n" + renderContinuation(tail, width))
}

// adopt moves the boundary out to p, folding the chunk that has just become
// stable into the cached render.
//
// The chunk is what gets rendered, never the whole prefix. Re-rendering the
// prefix each time the boundary moved would put the quadratic straight back —
// once per block instead of once per token, which is better but still wrong.
func (s *streamingMarkdown) adopt(content string, p, width int) {
	chunk := content[len(s.stablePrefix):p]
	if strings.TrimSpace(chunk) == "" {
		// A run of blank lines longer than one: the boundary walks through it
		// a line at a time, and the renderer draws no more for three blank
		// lines than for one. The cached render already says everything this
		// chunk has to say.
		s.stablePrefix = content[:p]
		return
	}
	if s.stablePrefix == "" {
		// The chunk opens the document, so it is rendered as one — there is no
		// block before it for the seam to separate it from.
		s.stablePrefixRender = renderUnfinished(chunk, width)
	} else {
		s.stablePrefixRender += "\n" + renderContinuation(chunk, width)
	}
	s.baseFences += countFenceLines(chunk)
	s.baseListMarker = s.baseListMarker || hasListMarker(chunk)
	s.stablePrefix = content[:p]
}

// boundaryAfter returns the latest safe boundary at or after the cached one,
// scanning only the delta. It returns len(stablePrefix) when nothing new
// qualifies — the caller then re-renders the tail against the cache it has —
// and -1 only when there is no cache and no boundary anywhere.
func (s *streamingMarkdown) boundaryAfter(content string) int {
	if s.stablePrefix == "" {
		return findSafeBoundary(content)
	}
	for p := blankLineBefore(content, len(content)); p > len(s.stablePrefix); p = blankLineBefore(content, p-1) {
		if s.safeIncremental(content, p) {
			return p
		}
	}
	return len(s.stablePrefix)
}

// safeIncremental is safeBoundaryAt with the whole-prefix scans replaced by
// the cumulative state plus a scan of the delta.
func (s *streamingMarkdown) safeIncremental(content string, p int) bool {
	delta := content[len(s.stablePrefix):p]
	if (s.baseFences+countFenceLines(delta))%2 != 0 {
		return false
	}
	if hasHTMLOrLinkRef(delta) {
		return false
	}
	return closesCleanly(content, p, s.baseListMarker || hasListMarker(delta))
}

// findSafeBoundary returns the offset of the first byte after the latest
// blank line at which content can be cut, or -1 when there is none. Latest
// wins: the more of the message that is stable, the less is re-rendered.
func findSafeBoundary(content string) int {
	for p := blankLineBefore(content, len(content)); p > 0; p = blankLineBefore(content, p-1) {
		if safeBoundaryAt(content, p) {
			return p
		}
	}
	return -1
}

// safeBoundaryAt reports whether content[:p] can be rendered on its own and
// glued back to a render of content[p:] without changing a byte. p is the
// start of a line with a blank line before it.
func safeBoundaryAt(content string, p int) bool {
	prefix := content[:p]
	// An unclosed fence would syntax-highlight the tail as prose.
	if countFenceLines(prefix)%2 != 0 {
		return false
	}
	// An HTML block or a link reference definition reaches across the cut: the
	// tail is rendered as an independent document and cannot see either.
	if hasHTMLOrLinkRef(prefix) {
		return false
	}
	return closesCleanly(content, p, hasListMarker(prefix))
}

// closesCleanly holds the checks that read the two lines either side of the
// cut, shared by the full and incremental paths. listSeen says whether a list
// marker has appeared anywhere before the cut.
func closesCleanly(content string, p int, listSeen bool) bool {
	last := lastNonBlankLine(content[:p])

	// A loose list keeps its item open across a blank line when what follows
	// is an indented continuation paragraph. That paragraph is the last line
	// before the cut and carries no marker, so opensConstruct would wave it
	// through; a marker earlier in the prefix is what gives it away.
	if listSeen && last != "" && !isListMarker(strings.TrimLeft(last, " \t")) {
		if last[0] == ' ' || last[0] == '\t' {
			return false
		}
	}
	if last != "" && opensConstruct(last) {
		return false
	}
	// Glamour closes a heading and a thematic break with a bare reset on a
	// line of its own — a line with no cells in it at all, which is not
	// something the next block's leading margin can follow. A prefix that ends
	// in one cannot be glued back to its tail byte for byte, and bytes are the
	// whole contract.
	if isATXHeading(last) || isThematicBreak(last) {
		return false
	}
	// A setext underline arriving after the cut would retroactively turn the
	// prefix's last paragraph into a heading — the prefix render would change
	// after it had been frozen.
	if rest := content[p:]; rest != "" && isSetextUnderline(firstNonBlankLine(rest)) {
		return false
	}
	return true
}

// blankLineBefore returns the offset of the first byte after the latest blank
// line ending strictly before until, or -1 when there is none. A blank line
// is "\n" followed by a line of nothing but spaces and tabs and another "\n";
// the offset returned is the start of the first line after it.
func blankLineBefore(content string, until int) int {
	end := until
	for end > 0 {
		nl := strings.LastIndexByte(content[:end], '\n')
		if nl < 0 {
			return -1
		}
		if prev := strings.LastIndexByte(content[:nl], '\n'); prev >= 0 && isSpacesOnly(content[prev+1:nl]) {
			return nl + 1
		}
		end = nl
	}
	return -1
}

func isSpacesOnly(s string) bool {
	return strings.Trim(s, " \t") == ""
}

// mdLines yields the lines of s without their terminators.
func mdLines(s string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for line := range strings.Lines(s) {
			if !yield(strings.TrimRight(line, "\n")) {
				return
			}
		}
	}
}

// countFenceLines counts the lines that toggle a fenced code block: three or
// more backticks or tildes as the first non-space content of a line, after at
// most three spaces of indent (CommonMark). An even count means every fence
// that opened has closed. Openers and closers are not told apart — toggling
// is all the boundary check needs.
func countFenceLines(s string) int {
	n := 0
	for line := range mdLines(s) {
		if isFenceLine(line) {
			n++
		}
	}
	return n
}

func isFenceLine(line string) bool {
	i := indentEnd(line)
	if i >= len(line) {
		return false
	}
	c := line[i]
	if c != '`' && c != '~' {
		return false
	}
	run := 0
	for ; i < len(line) && line[i] == c; i++ {
		run++
	}
	return run >= 3
}

// indentEnd returns the offset past at most three leading spaces, the most
// CommonMark allows a block construct to be indented by.
func indentEnd(line string) int {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	return i
}

// hasListMarker reports whether any line outside a fence is a list item.
func hasListMarker(s string) bool {
	inFence := false
	for line := range mdLines(s) {
		if isFenceLine(line) {
			inFence = !inFence
			continue
		}
		if !inFence && isListMarker(strings.TrimLeft(line, " \t")) {
			return true
		}
	}
	return false
}

// hasHTMLOrLinkRef reports whether any line outside a fence opens an HTML
// block or defines a link reference — the two constructs whose meaning is
// carried from one part of a document to another, and so cannot survive being
// rendered as two documents.
func hasHTMLOrLinkRef(s string) bool {
	inFence := false
	for line := range mdLines(s) {
		if isFenceLine(line) {
			inFence = !inFence
			continue
		}
		if !inFence && (isHTMLBlockOpener(line) || isLinkRefDefinition(line)) {
			return true
		}
	}
	return false
}

func lastNonBlankLine(s string) string {
	last := ""
	for line := range mdLines(s) {
		if strings.TrimSpace(line) != "" {
			last = line
		}
	}
	return last
}

func firstNonBlankLine(s string) string {
	for line := range mdLines(s) {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// opensConstruct reports whether line leaves a markdown construct open across
// the blank line that follows it. Every doubtful shape answers yes: the cost
// of a false yes is one slow frame, the cost of a false no is a transcript
// that changes under a selection.
func opensConstruct(line string) bool {
	// Indented code: a tab, or four spaces.
	if line[0] == '\t' || strings.HasPrefix(line, "    ") {
		return true
	}
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return false
	}
	switch {
	case trimmed[0] == '>': // block quote
		return true
	case isListMarker(trimmed):
		return true
	case strings.ContainsRune(line, '|'): // table, or prose with a pipe in it
		return true
	case isSetextUnderline(trimmed):
		return true
	}
	return false
}

// isListMarker reports whether an already left-trimmed line starts with a
// CommonMark list marker followed by a space or tab.
func isListMarker(line string) bool {
	if line == "" {
		return false
	}
	i := 0
	if c := line[0]; c == '-' || c == '*' || c == '+' {
		i = 1
	} else {
		for i < len(line) && line[i] >= '0' && line[i] <= '9' {
			i++
		}
		// CommonMark caps an ordered marker at nine digits.
		if i == 0 || i > 9 || i >= len(line) || (line[i] != '.' && line[i] != ')') {
			return false
		}
		i++
	}
	return i < len(line) && (line[i] == ' ' || line[i] == '\t')
}

// isATXHeading reports whether line is a `#`-prefixed heading.
func isATXHeading(line string) bool {
	i := indentEnd(line)
	hashes := 0
	for ; i < len(line) && line[i] == '#'; i++ {
		hashes++
	}
	if hashes == 0 || hashes > 6 {
		return false
	}
	return i >= len(line) || line[i] == ' ' || line[i] == '\t'
}

// isThematicBreak reports whether line is a rule: three or more of `-`, `_` or
// `*`, spaces allowed between them and nothing else on the line. `---` is a
// setext underline as well, and is caught either way.
func isThematicBreak(line string) bool {
	i := indentEnd(line)
	if i >= len(line) {
		return false
	}
	c := line[i]
	if c != '-' && c != '_' && c != '*' {
		return false
	}
	n := 0
	for ; i < len(line); i++ {
		switch line[i] {
		case c:
			n++
		case ' ', '\t':
		default:
			return false
		}
	}
	return n >= 3
}

// isSetextUnderline reports whether line is nothing but '=' or nothing but
// '-'. A bare "-" is also a list marker, but isListMarker requires the trailing
// space and is asked first everywhere it matters.
func isSetextUnderline(line string) bool {
	i := indentEnd(line)
	if i >= len(line) {
		return false
	}
	c := line[i]
	if c != '=' && c != '-' {
		return false
	}
	j := i
	for ; j < len(line) && line[j] == c; j++ {
	}
	return isSpacesOnly(line[j:])
}

// isHTMLBlockOpener reports whether line looks like the start of one of
// CommonMark's seven HTML blocks. The match is deliberately loose: the
// question is only whether the line smells like markup, not what markup it is.
func isHTMLBlockOpener(line string) bool {
	rest := line[indentEnd(line):]
	if len(rest) < 2 || rest[0] != '<' {
		return false
	}
	switch {
	case strings.HasPrefix(rest, "<!--"), // comment
		strings.HasPrefix(rest, "<?"),        // processing instruction
		strings.HasPrefix(rest, "<![CDATA["): // CDATA
		return true
	case rest[1] == '!' && len(rest) >= 3 && isASCIILetter(rest[2]): // declaration
		return true
	}
	// The raw-text tags, then any open or close tag at all: a line that starts
	// "<name" or "</name" is markup as far as this check is concerned. "<3",
	// "<-" and a mid-line "<foo>" are not, because the tag must open the line.
	i := 1
	if rest[i] == '/' {
		i++
	}
	return i < len(rest) && isASCIILetter(rest[i])
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isLinkRefDefinition reports whether line is `[label]: destination` — a
// definition the tail may refer to and, rendered apart from it, would lose.
func isLinkRefDefinition(line string) bool {
	i := indentEnd(line)
	if i >= len(line) || line[i] != '[' {
		return false
	}
	i++
	label := i
	for i < len(line) && line[i] != ']' {
		i++
	}
	if i >= len(line) || i == label {
		return false
	}
	i++ // past ']'
	if i >= len(line) || line[i] != ':' {
		return false
	}
	i++
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return i < len(line)
}

// renderContinuation renders a run of markdown as the continuation of a
// document rather than as a document of its own, and returns it with the seam
// that separates it from what came before already on the front.
//
// The seam used to be the crux of this file. glamour separated two top-level
// blocks with a *padded* blank line whose padding depended on both blocks, so
// the only way to reproduce it was to render a sentinel paragraph in front of
// the text and throw its own lines away — a whole apparatus (sentinelBlock,
// sentinelHeight, trimUnfinished) that existed to reverse-engineer a byte
// this package could not otherwise predict.
//
// The renderer is shhh's now (internal/ui/markdown), and its seam is one
// padded blank row between any two top-level blocks, always. So the seam is
// simply written.
func renderContinuation(text string, width int) string {
	rows := markdown.Blocks(text, mdOptions(width))
	if len(rows) == 0 {
		return ""
	}
	return seamRow(width) + "\n" + strings.Join(rows, "\n")
}

// renderUnfinished renders a document that another block will follow. It is
// the plain render: the block below it brings its own seam.
func renderUnfinished(text string, width int) string {
	return renderMarkdownRaw(text, width)
}

// seamRow is the blank row between two top-level blocks, padded exactly as
// the renderer pads one, because the glued render has to be the byte-for-byte
// render of the whole message.
func seamRow(width int) string {
	if width <= 0 {
		width = 80
	}
	return strings.Repeat(" ", markdown.Options{Width: width}.FillWidth())
}
