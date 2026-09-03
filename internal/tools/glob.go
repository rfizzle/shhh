package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

var globFiles = Definition{
	Tool: provider.Tool{
		Name:        GlobName,
		Description: "Find files by glob pattern, e.g. **/*.go or cmd/*/main.go. Use ** to match any number of directories. Returns matching file paths relative to the search root, skipping .git, node_modules, vendor and anything .gitignore names.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "Glob pattern with / separators; * matches within a path segment, ** matches across segments"},
				"path": {"type": "string", "description": "Optional directory to search in (defaults to current directory)"}
			},
			"required": ["pattern"]
		}`),
	},
	Execute: executeGlob,
}

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func executeGlob(raw json.RawMessage) (string, error) {
	var args globArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if args.Path == "" {
		args.Path = "."
	}

	patSegs := strings.Split(strings.Trim(filepath.ToSlash(args.Pattern), "/"), "/")
	for _, seg := range patSegs {
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, "x"); err != nil {
			return "", fmt.Errorf("invalid pattern %q: %w", args.Pattern, err)
		}
	}

	info, err := os.Stat(args.Path)
	if err != nil {
		return "", fmt.Errorf("cannot access path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", args.Path)
	}

	var results []string
	truncated := false
	ignore := newWalkIgnore(args.Path)
	err = filepath.WalkDir(args.Path, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != args.Path && skipWalk(name) {
				return filepath.SkipDir
			}
			if ignore.dir(p) {
				return filepath.SkipDir
			}
			return nil
		}
		if ignore.file(p) {
			return nil
		}
		rel, err := filepath.Rel(args.Path, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		ok, err := matchGlob(patSegs, strings.Split(rel, "/"))
		if err != nil {
			return fmt.Errorf("invalid pattern %q: %w", args.Pattern, err)
		}
		if !ok {
			return nil
		}
		if len(results) >= MaxGlobResults {
			truncated = true
			return filepath.SkipAll
		}
		results = append(results, rel)
		return nil
	})
	if err != nil {
		return "", err
	}

	if len(results) == 0 {
		return "No files matched.", nil
	}
	out := strings.Join(results, "\n")
	if truncated {
		out += fmt.Sprintf("\n… (truncated at %d files; narrow the pattern or path to see more)", MaxGlobResults)
	}
	return out, nil
}

// matchGlob matches slash-split pattern segments against path segments.
// A "**" segment matches zero or more path segments; every other segment is a
// single-segment path.Match.
func matchGlob(patSegs, nameSegs []string) (bool, error) {
	if len(patSegs) == 0 {
		return len(nameSegs) == 0, nil
	}
	if patSegs[0] == "**" {
		if ok, err := matchGlob(patSegs[1:], nameSegs); err != nil || ok {
			return ok, err
		}
		if len(nameSegs) == 0 {
			return false, nil
		}
		return matchGlob(patSegs, nameSegs[1:])
	}
	if len(nameSegs) == 0 {
		return false, nil
	}
	ok, err := path.Match(patSegs[0], nameSegs[0])
	if err != nil || !ok {
		return false, err
	}
	return matchGlob(patSegs[1:], nameSegs[1:])
}
