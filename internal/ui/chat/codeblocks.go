package chat

import (
	"strings"

	"github.com/rfizzle/shhh/internal/ui/markdown"
)

// codeBlock is one fenced block from a response: the fence's info string
// reduced to its language tag (empty when the fence carried none) and the
// block body.
type codeBlock struct {
	lang string
	body string
}

// extractCodeBlockInfo returns the fenced code blocks (``` or ~~~) in
// markdown text, in order of appearance, with each block's language tag.
//
// The blocks are the ones the transcript heads and counts, read by the same
// parser that draws them, so block n here is the block a click on the n-th
// heading names. A scan of its own disagreed with the drawing wherever a
// fence was not at the start of a line: a block inside a quote was drawn and
// skipped, and a longer fence holding a shorter one was cut at the inner
// fence, so every block after either was copied under the wrong number. A
// fence that never closed is not a block yet, here as on the screen.
func extractCodeBlockInfo(text string) []codeBlock {
	var blocks []codeBlock
	for _, f := range markdown.FenceTexts(text) {
		blocks = append(blocks, codeBlock{lang: f.Lang, body: f.Body})
	}
	return blocks
}

// extractCodeBlocks returns just the bodies of the fenced code blocks in
// markdown text, in order of appearance.
func extractCodeBlocks(text string) []string {
	infos := extractCodeBlockInfo(text)
	if len(infos) == 0 {
		return nil
	}
	blocks := make([]string, len(infos))
	for i, b := range infos {
		blocks[i] = b.body
	}
	return blocks
}

// blockHead is a code block's first non-blank line, used as its picker row
// label.
func blockHead(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// blockLines is how many lines a code block holds, ignoring a trailing blank.
func blockLines(body string) int {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return 0
	}
	return strings.Count(body, "\n") + 1
}

// blockWord is what a block is called by its language: the fence's tag, or
// `code` where the fence named none, so a bare fence still has a word in the
// card's row and in the copy's confirmation rather than a gap between two
// separators.
func blockWord(lang string) string {
	if lang == "" {
		return "code"
	}
	return lang
}
