package shell

// Which shell a command actually goes through.
//
// Six places used to answer this themselves, each with the same three lines:
// read $SHELL, fall back to /bin/sh, run it with -c. That is correct on Unix
// and is not a shell resolution at all on Windows, where there is no $SHELL,
// no /bin/sh and no -c — so every one of those six was a command that could
// not run.
//
// It is one place now, and it answers with the flags as well as the path,
// because the two cannot be decided apart: cmd takes /C and PowerShell takes
// -Command, and a caller that resolved the path here and hardcoded -c would
// be back where it started.
//
// There are two questions, not one, and conflating them was a bug.
//
//   - Current is "which shell is the user in". It belongs to `shhh cmd`,
//     whose whole output is a command the user runs and keeps in their own
//     history, so it has to be their shell's syntax or it is worthless.
//   - Execution is "which shell does shhh run a command through". It belongs
//     to everything else — the agent's execute_command, a background
//     process, the body of a sandbox wrapper — and there the answer is bash,
//     because the command was composed by a model rather than by the user.
//
// Within each question the prompt and the runner read the same answer. That
// is the part that must not drift: a prompt describing one shell while the
// runner uses another is worse than either, because the model writes correct
// PowerShell for cmd to choke on.
// See docs/capabilities/generation.md#the-prompt-is-told-which-shell-it-is-writing-for.

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Shell is the shell a command line is handed to.
type Shell struct {
	// Path is the executable to spawn, and Name its base name without any
	// extension — `bash`, `fish`, `pwsh`, `cmd`. Name is what the prompt's
	// syntax rules key on, so it must never carry the `.exe`.
	Path string
	Name string
	// Flag is the argument, or arguments, that introduce a command line. It
	// is part of the shell rather than the caller's because it varies with
	// the shell and getting it wrong is silent: cmd treats an unknown leading
	// flag as a filename.
	Flag []string
}

// Argv is the whole invocation for a command line.
func (s Shell) Argv(command string) []string {
	argv := make([]string, 0, len(s.Flag)+2)
	argv = append(argv, s.Path)
	argv = append(argv, s.Flag...)
	return append(argv, command)
}

// PowerShell reports whether this is one of the two PowerShells. They take
// the same syntax and differ only in name, so everything that cares about the
// language asks this rather than comparing to two strings.
func (s Shell) PowerShell() bool { return s.Name == "pwsh" || s.Name == "powershell" }

// Current is the shell the user is in: $SHELL, or the POSIX floor beneath it.
//
// Only the command generator wants this. Everything that runs a command on
// the user's behalf wants Execution instead.
func Current() Shell { return resolve(runtime.GOOS, lookupEnv, exec.LookPath) }

// Execution is the shell shhh runs a composed command through.
//
// It is deliberately not Current, for three reasons that all point the same
// way.
//
// The command is written by a model, and every model writes bash by default.
// The syntax rules a prompt can carry (internal/prompt) move the odds; they
// do not move them far enough, and they cannot help at all where the user's
// shell simply lacks the construct — fish has no heredoc, and a heredoc is
// how a model writes a file into a command.
//
// The user's shell reads the user's startup files. This is the assumption
// that was wrong before: `$SHELL -c` is not quiet. fish sources config.fish
// on every invocation including a non-interactive one, and zsh sources
// .zshenv, so a prompt banner, a version-manager hook or a slow tool init is
// paid once per command and lands in the captured output the model reads
// back as the command's own. `sh -c` and `bash -c` are the two that really
// are quiet, which is the same reason Windows asks for -NoProfile below.
//
// And every other coding agent runs bash. A command that works in one of
// them and fails here reads as a bug in shhh, and is one.
//
// bash is preferred over sh because the POSIX a model writes is really
// bash's — [[ ]], <<<, arrays, pipefail — and it is looked up on PATH rather
// than hardcoded so that a machine with a modern bash ahead of an ancient
// /bin/bash gets the modern one. /bin/sh is the floor, and it is the same
// floor Current falls back to.
func Execution() Shell { return resolveExec(runtime.GOOS, lookupEnv, exec.LookPath) }

// resolve is Current with its two sources of truth passed in, so the Windows
// answer can be asserted from any machine. Testing a platform branch only on
// that platform means testing it nowhere.
func resolve(goos string, env func(string) string, look func(string) (string, error)) Shell {
	if goos == "windows" {
		return resolveWindows(env, look)
	}
	sh := env("SHELL")
	if sh == "" {
		// The POSIX floor. Every system that is not Windows has it, which is
		// what makes it a fallback rather than a guess.
		sh = "/bin/sh"
	}
	return Shell{Path: filepath.Clean(sh), Name: base(sh), Flag: []string{"-c"}}
}

// resolveExec is Execution with its two sources of truth passed in, for the
// same reason resolve takes them.
func resolveExec(goos string, env func(string) string, look func(string) (string, error)) Shell {
	if goos == "windows" {
		// No POSIX floor to prefer and no bash to find, so the execution
		// shell is the platform's own — the same resolution Current makes,
		// and the reason the syntax rules still have a cmd and a PowerShell
		// branch to pick from.
		return resolveWindows(env, look)
	}
	if path, err := look("bash"); err == nil {
		return Shell{Path: path, Name: "bash", Flag: []string{"-c"}}
	}
	return Shell{Path: "/bin/sh", Name: "sh", Flag: []string{"-c"}}
}

// resolveWindows picks the best shell present, newest first.
//
// PowerShell is preferred over cmd because it is what Windows development
// actually happens in — a generated command using a pipeline, an environment
// variable or a path with a space is ordinary PowerShell and is a fight in
// cmd. cmd is the floor rather than the choice: it is the one shell that is
// certainly there.
//
// -NoProfile is not a preference either. A profile prints banners and sets
// aliases, and both end up in captured output that the model reads as the
// command's own. It is the same property Execution picks bash for on Unix —
// stated as a flag here because PowerShell has no quiet mode without it.
func resolveWindows(env func(string) string, look func(string) (string, error)) Shell {
	for _, name := range []string{"pwsh", "powershell"} {
		if path, err := look(name); err == nil {
			return Shell{Path: path, Name: name, Flag: []string{"-NoProfile", "-Command"}}
		}
	}
	comspec := env("ComSpec")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	return Shell{Path: comspec, Name: base(comspec), Flag: []string{"/C"}}
}

// base is a shell's name as the syntax rules spell it: no directory, no
// extension, folded to lower case — `powershell.exe`, `PowerShell.EXE` and
// `powershell` are one shell, and nothing downstream should have to know
// three spellings of it.
//
// Both separators are cut, rather than filepath.Base, because path/filepath
// is the *host's* rules: a backslash is an ordinary character in a name on
// Unix, so filepath.Base leaves a Windows path whole there. That is harmless
// on Windows and makes the Windows branch impossible to test anywhere else,
// which is the same as not testing it.
func base(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		path = path[i+1:]
	}
	if i := strings.LastIndexByte(path, '.'); i > 0 {
		path = path[:i]
	}
	return strings.ToLower(path)
}
