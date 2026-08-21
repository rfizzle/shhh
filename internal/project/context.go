package project

import (
	"os"
	"path/filepath"
)

// contextFilenames are the recognized project-context files, in precedence
// order within a directory: a shhh-specific .shhh file wins over the generic
// AGENTS.md convention.
var contextFilenames = []string{".shhh", "AGENTS.md"}

func FindContext() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		for _, name := range contextFilenames {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil {
				return string(data)
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
