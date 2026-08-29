package project

import (
	"os"
	"path/filepath"
)

// contextFilenames are the recognized project-context files, in precedence
// order within a directory: a shhh-specific .shhh file wins over the generic
// AGENTS.md convention.
var contextFilenames = []string{".shhh", "AGENTS.md"}

// Find returns the path and contents of the nearest project-context file,
// walking up from the working directory. The path is what the start screen
// names: a session that says what it read is a session whose system
// prompt is not a secret.
func Find() (path, content string) {
	dir, err := os.Getwd()
	if err != nil {
		return "", ""
	}

	for {
		for _, name := range contextFilenames {
			p := filepath.Join(dir, name)
			data, err := os.ReadFile(p)
			if err == nil {
				return p, string(data)
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", ""
}

func FindContext() string {
	_, content := Find()
	return content
}
