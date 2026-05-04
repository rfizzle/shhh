package shell

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

type Info struct {
	Shell string
	OS    string
	Arch  string
	Cwd   string
	IsRoot bool
}

func Detect() Info {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}

	cwd, _ := os.Getwd()

	uid, _ := strconv.Atoi(os.Getenv("EUID"))
	if uid != 0 {
		uid = os.Getuid()
	}

	return Info{
		Shell:  filepath.Base(sh),
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		Cwd:    cwd,
		IsRoot: uid == 0,
	}
}
