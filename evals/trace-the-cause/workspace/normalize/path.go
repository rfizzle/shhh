package normalize

import (
	"strings"

	"example.com/router/parse"
)

// Path is the canonical form of a request path: a leading slash, no repeated
// slashes, and no trailing slash except on the root.
//
// Everything that keys on a path — routing, the cache, the access log — goes
// through here first, so that two spellings of one path cannot become two
// different things.
func Path(p string) string {
	segs := parse.Segments(p)
	if len(segs) == 0 {
		return "/"
	}
	return "/" + strings.Join(segs, "/")
}
