package runner

// What a command needed from the machine and did not get.
//
// A command that never became a process fails for a reason that has nothing
// to do with what it was going to do: the directory it was to run in was
// removed under the session, the shell every command here goes through is not
// installed, the containment mechanism is gone, the operating system refused.
// Those four and a spawn that failed for none of them are the whole
// vocabulary, and this is where the operating system's answer is read into
// it.
//
// It is done here, at the spawn, rather than by whatever formats the result:
// this is the only place that still knows what was being attempted — which
// directory, and whether argv[0] was the execution shell or a mechanism
// wrapped around it — and an error text read back later would be a guess
// about both. What every surface downstream gets is the category and the
// operating system's own words, which is why none of them has to recognise a
// platform's phrasing.
//
// Only a spawn is classified. A command that started and failed is the
// command's own business, `command not found` from a shell included: that is
// a program that is not installed, printed by a shell that ran perfectly, and
// filing it under a broken machine would send a model to look at the host
// when what is wrong is the line it wrote.

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strings"

	"github.com/rfizzle/shhh/internal/tools"
)

// spawnKind is what argv[0] was: the execution shell, or a mechanism some
// caller built the argv around. It is passed down rather than recognised,
// because the caller is the only one that knows — a path compared against
// today's shell would call yesterday's contained command a missing shell, and
// the answer decides which of two prerequisites a reader is told to look at.
type spawnKind int

const (
	// spawnShell is a command line the execution shell was asked to run.
	spawnShell spawnKind = iota
	// spawnWrapped is a pre-built argv, which in this codebase is a command
	// with a containment mechanism in front of it.
	spawnWrapped
)

// startFailure is the result of a command that never became a process: the
// prerequisite that was missing, and the harness's own account of it as the
// output, so the older output/status callers carry the same words the typed
// result does.
func startFailure(dir string, kind spawnKind, err error) tools.ExecResult {
	prereq := classifyStart(dir, kind, err)
	detail := errorText(err)
	if prereq == tools.PrereqWorkingDir && dir != "" && !strings.Contains(detail, dir) {
		// The chdir fails in the forked child, and what the parent reports
		// the errno against is argv[0] — so the operating system's own
		// sentence about a missing working directory usually names the
		// shell. The directory is the fact the reader is missing, and it is
		// added rather than substituted: the errno is still the evidence.
		detail = dir + " — " + detail
	}
	return tools.ExecResult{
		Output:   tools.ExecPrereqReport(prereq, detail),
		ExitCode: -1,
		Outcome:  tools.ExecDidNotStart,
		Prereq:   prereq,
	}
}

// WrapFailure is the result of a contained command whose wrap could not be
// built, so nothing was spawned at all. It is a spawn failure one step
// earlier — the mechanism in front of the command was never reached — and it
// is classified here beside the spawn's own for the reason that one is: the
// category is decided once, where the failure is still an error and not yet
// text.
//
// The working directory is asked about first, as classifyStart asks about
// it: the policy a wrap is built from starts at the process's working
// directory, so a checkout removed under the session fails the wrap rather
// than the spawn, and a reader told the containment was missing would go
// looking for a mechanism that is perfectly fine. It is asked whether the
// directory is still there and not only whether it can be named, because a
// platform can go on naming one that has been removed. Everything else a wrap can
// refuse is the containment's own — a mechanism gone, or a policy it cannot
// express — and a contained command is never run bare instead.
// See docs/capabilities/containment.md#a-command-that-never-started-names-what-it-needed.
func WrapFailure(err error) tools.ExecResult {
	prereq := tools.PrereqContainment
	if inheritedDirGone() != nil {
		prereq = tools.PrereqWorkingDir
	}
	return tools.ExecResult{
		Output:   tools.ExecPrereqReport(prereq, errorText(err)),
		ExitCode: -1,
		Outcome:  tools.ExecDidNotStart,
		Prereq:   prereq,
	}
}

// inheritedDirFailure is the result of a command that was to run in this
// process's own working directory when that directory has been removed. It is
// answered before the spawn rather than read from it, because the spawn does
// not fail: a child inherits a removed directory as happily as a present one,
// `ls` in it prints nothing and exits 0, and the reader is told a command ran
// against a checkout that is not there. A contained command reaches the same
// category through WrapFailure, since a policy cannot be built over a
// directory that is gone; asking here is what makes the bare spawn — every
// command on a host with no containment — agree with it.
// See docs/capabilities/containment.md#a-command-that-never-started-names-what-it-needed.
func inheritedDirFailure() (tools.ExecResult, bool) {
	err := inheritedDirGone()
	if err == nil {
		return tools.ExecResult{}, false
	}
	return tools.ExecResult{
		Output:   tools.ExecPrereqReport(tools.PrereqWorkingDir, errorText(err)),
		ExitCode: -1,
		Outcome:  tools.ExecDidNotStart,
		Prereq:   tools.PrereqWorkingDir,
	}, true
}

// inheritedDirGone is the error saying this process's working directory is
// gone, or nil while it is there. It is asked whether the directory is still
// there and not only whether it can be named, because a platform can go on
// naming one that has been removed.
func inheritedDirGone() error {
	wd, err := getwd()
	if err != nil {
		return err
	}
	if !isDir(wd) {
		return &fs.PathError{Op: "stat", Path: wd, Err: fs.ErrNotExist}
	}
	return nil
}

// getwd is how WrapFailure asks after the working directory. It is a
// variable because a test that removed its own working directory to find out
// would have to change into one first, and a test here never changes
// directory.
var getwd = os.Getwd

// classifyStart names the prerequisite behind a spawn error.
//
// The working directory is asked about rather than inferred, and it is asked
// about first, because it is the one prerequisite the error text cannot be
// trusted on: a directory that is gone reports the same ENOENT as a shell
// that is gone, and a directory that turned out to be a file is reported
// against the shell's path rather than its own. A stat settles both, and it
// is only ever paid for by a command that has already failed.
func classifyStart(dir string, kind spawnKind, err error) tools.ExecPrereq {
	if dir != "" && !isDir(dir) {
		return tools.PrereqWorkingDir
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && pathErr.Op == "chdir" {
		return tools.PrereqWorkingDir
	}
	switch {
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		// The program itself is not there. Which program that is, is the
		// caller's answer and not this function's.
		if kind == spawnWrapped {
			return tools.PrereqContainment
		}
		return tools.PrereqShell
	case errors.Is(err, fs.ErrPermission):
		return tools.PrereqPermission
	}
	return tools.PrereqSpawn
}

// isDir reports whether dir is there and is a directory. A path that cannot
// be stat'd at all is not one either, which is the answer that matters: the
// command was about to be run in it.
func isDir(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// errorText is the operating system's words, kept exactly as it said them. A
// nil error has none, which is a spawn that failed without saying why.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
