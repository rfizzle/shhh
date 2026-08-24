package diff

import (
	"regexp"
	"strconv"
	"strings"
)

// File is one file's portion of a multi-file patch (e.g. `git diff` output),
// for the full-screen session diff view (S-074).
type File struct {
	Path string
	// Binary marks a file git reported as a binary change; Hunks is empty.
	Binary bool
	Hunks  []Hunk
}

var patchHunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParsePatch parses unified git-diff output into per-file hunks with line
// numbers taken from the @@ headers, marking intraline emphasis like Compute.
// Unrecognized header lines are skipped; an empty or unparseable patch
// returns nil.
func ParsePatch(patch string) []File {
	var files []File
	var hunk *Hunk
	oldNo, newNo := 0, 0

	flushHunk := func() {
		if hunk != nil && len(files) > 0 {
			f := &files[len(files)-1]
			f.Hunks = append(f.Hunks, *hunk)
		}
		hunk = nil
	}

	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushHunk()
			files = append(files, File{Path: parsePatchPath(line)})
		case patchHunkRe.MatchString(line):
			m := patchHunkRe.FindStringSubmatch(line)
			flushHunk()
			if len(files) == 0 {
				// A headerless patch (plain `diff -u` output) still parses.
				files = append(files, File{})
			}
			hunk = &Hunk{
				OldStart: atoiOr(m[1], 0),
				OldCount: atoiOr(m[2], 1),
				NewStart: atoiOr(m[3], 0),
				NewCount: atoiOr(m[4], 1),
			}
			oldNo, newNo = hunk.OldStart, hunk.NewStart
		// Inside a hunk, +++/---/etc cannot occur: only content markers do.
		case hunk != nil && strings.HasPrefix(line, "+"):
			hunk.Lines = append(hunk.Lines, Line{Kind: Add, Text: line[1:], NewNo: newNo})
			newNo++
		case hunk != nil && strings.HasPrefix(line, "-"):
			hunk.Lines = append(hunk.Lines, Line{Kind: Del, Text: line[1:], OldNo: oldNo})
			oldNo++
		case hunk != nil && strings.HasPrefix(line, " "):
			hunk.Lines = append(hunk.Lines, Line{Kind: Context, Text: line[1:], OldNo: oldNo, NewNo: newNo})
			oldNo++
			newNo++
		case hunk != nil:
			// "\ No newline at end of file" and blank separators.
		case strings.HasPrefix(line, "+++ "):
			// The b-side name wins when present (creations, renames).
			if p := parseMarkerPath(line); p != "" && len(files) > 0 {
				files[len(files)-1].Path = p
			}
		case strings.HasPrefix(line, "--- "):
			if p := parseMarkerPath(line); p != "" && len(files) > 0 && files[len(files)-1].Path == "" {
				files[len(files)-1].Path = p
			}
		case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
			if len(files) > 0 {
				files[len(files)-1].Binary = true
			}
		}
	}
	flushHunk()

	for fi := range files {
		for hi := range files[fi].Hunks {
			markIntraline(files[fi].Hunks[hi].Lines)
		}
	}
	return files
}

// parsePatchPath extracts the b-side path from a "diff --git a/x b/y" line.
func parsePatchPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if i := strings.Index(rest, " b/"); i >= 0 {
		return rest[i+3:]
	}
	return rest
}

// parseMarkerPath extracts the path from a "--- a/x" or "+++ b/x" file
// marker; /dev/null (creations/deletions) returns "".
func parseMarkerPath(line string) string {
	p := line[4:]
	// Some diffs append a tab plus a timestamp after the path.
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	if p == "/dev/null" {
		return ""
	}
	p = strings.TrimPrefix(p, "a/")
	p = strings.TrimPrefix(p, "b/")
	return p
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
