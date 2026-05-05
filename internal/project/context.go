package project

import (
	"os"
	"path/filepath"
)

func FindContext() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		path := filepath.Join(dir, ".shhh")
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
