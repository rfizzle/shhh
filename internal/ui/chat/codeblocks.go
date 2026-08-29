package chat

import "strings"

// codeBlock is one fenced block from a response: the fence's info string
// reduced to its language tag (empty when the fence carried none) and the
// block body.
type codeBlock struct {
	lang string
	body string
}

// extractCodeBlockInfo returns the fenced code blocks (``` or ~~~) in
// markdown text, in order of appearance, with each block's language tag.
func extractCodeBlockInfo(text string) []codeBlock {
	var blocks []codeBlock
	var current []string
	var fence, lang string
	inBlock := false

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inBlock {
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				fence = trimmed[:3]
				lang = ""
				if info := strings.Fields(trimmed[3:]); len(info) > 0 {
					lang = info[0]
				}
				inBlock = true
				current = nil
			}
			continue
		}
		if strings.HasPrefix(trimmed, fence) {
			inBlock = false
			blocks = append(blocks, codeBlock{lang: lang, body: strings.Join(current, "\n")})
			continue
		}
		current = append(current, line)
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
