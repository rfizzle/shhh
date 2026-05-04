package shell

import (
	"os"
	"path/filepath"
	"runtime"
)

type Info struct {
	Shell string
	OS    string
	Arch  string
	Cwd   string
}

func Detect() Info {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}

	cwd, _ := os.Getwd()

	return Info{
		Shell: filepath.Base(sh),
		OS:    runtime.GOOS,
		Arch:  runtime.GOARCH,
		Cwd:   cwd,
	}
}
