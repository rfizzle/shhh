package chat

import "strings"

// extractCodeBlocks returns the contents of fenced code blocks (``` or ~~~)
// in markdown text, in order of appearance. The info string (language tag)
// is dropped.
func extractCodeBlocks(text string) []string {
	var blocks []string
	var current []string
	var fence string
	inBlock := false

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inBlock {
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				fence = trimmed[:3]
				inBlock = true
				current = nil
			}
			continue
		}
		if strings.HasPrefix(trimmed, fence) {
			inBlock = false
			blocks = append(blocks, strings.Join(current, "\n"))
			continue
		}
		current = append(current, line)
	}
	return blocks
}
