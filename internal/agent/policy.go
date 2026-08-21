package agent

// Shared approval-policy helpers used by both front-ends: the chat TUI's
// session policy (S-054) and headless print mode's --allow flags (S-057).

import "strings"

// allowlistUnsafe are shell metacharacters that could chain a second command
// onto an allowlisted prefix (e.g. "git status; rm -rf ~" prefix-matching the
// entry "git status"), so commands containing any of them never match.
const allowlistUnsafe = ";&|`$()<>\n"

// AllowlistMatches reports whether command's leading words exactly match all
// words of some allowlist entry ("go test" matches "go test ./...").
func AllowlistMatches(allowlist []string, command string) bool {
	if strings.ContainsAny(command, allowlistUnsafe) {
		return false
	}
	words := strings.Fields(command)
	for _, entry := range allowlist {
		pattern := strings.Fields(entry)
		if len(pattern) == 0 || len(pattern) > len(words) {
			continue
		}
		match := true
		for i, w := range pattern {
			if words[i] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
