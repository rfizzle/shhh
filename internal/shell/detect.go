package shell

import (
	"os"
	"runtime"
	"strconv"
)

// lookupEnv is the environment, a variable so a test can hand resolve a
// machine that is not this one.
var lookupEnv = os.Getenv

type Info struct {
	Shell  string
	OS     string
	Arch   string
	Cwd    string
	IsRoot bool
}

func Detect() Info {
	cwd, _ := os.Getwd()

	// Root is a Unix idea. os.Getuid answers -1 on Windows, which is already
	// not zero, but the question is asked explicitly here because what hangs
	// off the answer — whether to tell the model about sudo — has no meaning
	// there at all.
	root := false
	if runtime.GOOS != "windows" {
		uid, _ := strconv.Atoi(os.Getenv("EUID"))
		if uid != 0 {
			uid = os.Getuid()
		}
		root = uid == 0
	}

	return Info{
		Shell:  Current().Name,
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		Cwd:    cwd,
		IsRoot: root,
	}
}
