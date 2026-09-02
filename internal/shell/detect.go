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

// DetectExec is Detect for a session that runs commands itself: the same
// machine and the same directory, but the shell it names is the one those
// commands will actually go through (Execution).
//
// It exists so the pairing stays impossible to get wrong. A prompt built from
// Detect while execute_command runs Execution is exactly the drift the
// package comment says must not happen — the model would write fish for bash
// to read — so the two prompts that matter each take the Info that matches
// their runner: the generator takes Detect, an agent takes this.
func DetectExec() Info {
	info := Detect()
	info.Shell = Execution().Name
	return info
}
