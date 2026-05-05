package stdin

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

func Read(r io.Reader, maxChars int) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lines []string
	total := 0
	truncated := false

	for scanner.Scan() {
		line := scanner.Text()
		needed := len(line)
		if len(lines) > 0 {
			needed++
		}
		if total+needed > maxChars {
			remaining := maxChars - total
			if len(lines) > 0 {
				remaining--
			}
			if remaining > 0 {
				lines = append(lines, line[:remaining])
			}
			truncated = true
			break
		}
		lines = append(lines, line)
		total += needed
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}

	content := strings.Join(lines, "\n")
	if truncated {
		content += "\n[...truncated]"
	}
	return content, nil
}

func FormatPromptWithContext(prompt, context string) string {
	return fmt.Sprintf("<context>\n%s\n</context>\n\n%s", context, prompt)
}
