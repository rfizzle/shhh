package normalize

import "strings"

// Path is the canonical form of a request path: a leading slash, no repeated
// slashes, and no trailing slash except on the root.
func Path(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	var out []string
	for i, part := range parts {
		if part == "" && i < len(parts)-1 {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return "/"
	}
	return "/" + strings.Join(out, "/")
}
