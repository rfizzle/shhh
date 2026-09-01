package parse

import "strings"

// Segments splits a URL path into its parts, discarding the empties that a
// leading, repeated or trailing slash produces, so that "/a//b/" and "/a/b"
// are the same two-part path.
func Segments(path string) []string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	var out []string
	for i, s := range parts {
		if s == "" && i < len(parts)-1 {
			continue
		}
		out = append(out, s)
	}
	return out
}
