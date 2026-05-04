package ui

import "strings"

func StripFences(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		} else {
			s = strings.TrimPrefix(s, "```")
		}
		s = strings.TrimRight(s, "\n")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
		return s
	}

	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' {
		return s[1 : len(s)-1]
	}

	return s
}
