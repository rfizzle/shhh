package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	codeBlockStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("236"))
	codeBlockLangStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("241")).
				Italic(true)
	inlineCodeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("236"))
)

func highlightCode(text string) string {
	lines := strings.Split(text, "\n")
	var result strings.Builder
	inBlock := false
	var blockLines []string
	var lang string

	for _, line := range lines {
		if !inBlock && strings.HasPrefix(line, "```") {
			inBlock = true
			lang = strings.TrimPrefix(line, "```")
			lang = strings.TrimSpace(lang)
			blockLines = nil
			continue
		}
		if inBlock && strings.HasPrefix(line, "```") {
			if lang != "" {
				result.WriteString(codeBlockLangStyle.Render(lang) + "\n")
			}
			for _, bl := range blockLines {
				result.WriteString(codeBlockStyle.Render(bl) + "\n")
			}
			inBlock = false
			lang = ""
			continue
		}
		if inBlock {
			blockLines = append(blockLines, line)
			continue
		}
		result.WriteString(highlightInlineCode(line) + "\n")
	}

	// Unclosed fence — render what we have as a code block (streaming mid-block)
	if inBlock {
		if lang != "" {
			result.WriteString(codeBlockLangStyle.Render(lang) + "\n")
		}
		for _, bl := range blockLines {
			result.WriteString(codeBlockStyle.Render(bl) + "\n")
		}
	}

	return strings.TrimRight(result.String(), "\n")
}

func highlightInlineCode(line string) string {
	var result strings.Builder
	for {
		start := strings.Index(line, "`")
		if start == -1 {
			result.WriteString(line)
			break
		}
		end := strings.Index(line[start+1:], "`")
		if end == -1 {
			result.WriteString(line)
			break
		}
		end += start + 1
		result.WriteString(line[:start])
		code := line[start+1 : end]
		result.WriteString(inlineCodeStyle.Render(code))
		line = line[end+1:]
	}
	return result.String()
}
