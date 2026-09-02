package shell

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// env and look stand in for a machine that is not this one, which is the only
// way a Windows branch gets tested at all.
func env(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func has(names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(names, name) {
			return `C:\Program Files\` + name + `.exe`, nil
		}
		return "", exec.ErrNotFound
	}
}

func hasNothing(string) (string, error) { return "", exec.ErrNotFound }

func TestResolveUsesTheUsersShellOnUnix(t *testing.T) {
	sh := resolve("linux", env(map[string]string{"SHELL": "/usr/bin/fish"}), hasNothing)
	if sh.Name != "fish" || sh.Path != "/usr/bin/fish" {
		t.Fatalf("got %+v", sh)
	}
	if got := sh.Argv("ls"); !slices.Equal(got, []string{"/usr/bin/fish", "-c", "ls"}) {
		t.Errorf("argv = %v", got)
	}
}

func TestResolveFallsBackToPosixSh(t *testing.T) {
	sh := resolve("darwin", env(nil), hasNothing)
	if sh.Name != "sh" || sh.Path != "/bin/sh" {
		t.Fatalf("got %+v", sh)
	}
}

// PowerShell is preferred over cmd, and the newer one over the older: a
// generated command that pipes or quotes is ordinary there and a fight in cmd.
func TestResolvePrefersPwshOnWindows(t *testing.T) {
	sh := resolve("windows", env(nil), has("pwsh", "powershell"))
	if sh.Name != "pwsh" {
		t.Fatalf("name = %q, want pwsh", sh.Name)
	}
	if !sh.PowerShell() {
		t.Error("pwsh is a PowerShell")
	}
}

func TestResolveFallsBackToWindowsPowerShell(t *testing.T) {
	sh := resolve("windows", env(nil), has("powershell"))
	if sh.Name != "powershell" || !sh.PowerShell() {
		t.Fatalf("got %+v", sh)
	}
}

// The flags are part of the shell because getting them wrong is silent: cmd
// reads an unknown leading flag as a filename.
func TestResolveGivesPowerShellItsOwnFlags(t *testing.T) {
	sh := resolve("windows", env(nil), has("pwsh"))
	argv := sh.Argv("Get-ChildItem")
	if !slices.Contains(argv, "-Command") {
		t.Errorf("PowerShell takes -Command: %v", argv)
	}
	if !slices.Contains(argv, "-NoProfile") {
		t.Errorf("a profile's banner would be read as the command's output: %v", argv)
	}
	if argv[len(argv)-1] != "Get-ChildItem" {
		t.Errorf("the command line goes last: %v", argv)
	}
}

// cmd is the floor: the one shell that is certainly there.
func TestResolveFallsBackToComSpec(t *testing.T) {
	sh := resolve("windows", env(map[string]string{"ComSpec": `C:\Windows\System32\cmd.exe`}), hasNothing)
	if sh.Name != "cmd" {
		t.Fatalf("name = %q, want cmd", sh.Name)
	}
	if sh.PowerShell() {
		t.Error("cmd is not a PowerShell")
	}
	if got := sh.Argv("dir"); !slices.Equal(got, []string{`C:\Windows\System32\cmd.exe`, "/C", "dir"}) {
		t.Errorf("argv = %v", got)
	}
}

func TestResolveKnowsWhereCmdIsWithoutComSpec(t *testing.T) {
	sh := resolve("windows", env(nil), hasNothing)
	if sh.Name != "cmd" || !strings.HasSuffix(sh.Path, "cmd.exe") {
		t.Fatalf("got %+v", sh)
	}
}

// The name is what the prompt's syntax rules key on, so it must never carry
// the extension: powershell.exe and powershell are one shell.
func TestTheNameCarriesNoExtension(t *testing.T) {
	for path, want := range map[string]string{
		`C:\Program Files\PowerShell\7\pwsh.exe`: "pwsh",
		`C:\Windows\System32\cmd.exe`:            "cmd",
		"/bin/bash":                              "bash",
		"/usr/local/bin/fish":                    "fish",
	} {
		if got := base(path); got != want {
			t.Errorf("base(%q) = %q, want %q", path, got, want)
		}
	}
}

// A shell whose name arrives in the wrong case is the same shell.
func TestTheNameIsCaseFolded(t *testing.T) {
	if got := base(`C:\Windows\System32\CMD.EXE`); got != "cmd" {
		t.Errorf("got %q", got)
	}
}

func TestArgvDoesNotAliasTheShellsFlags(t *testing.T) {
	sh := resolve("windows", env(nil), has("pwsh"))
	first := sh.Argv("one")
	second := sh.Argv("two")
	if first[len(first)-1] != "one" || second[len(second)-1] != "two" {
		t.Fatalf("one invocation overwrote the other: %v %v", first, second)
	}
}

func TestLookPathErrorsAreNotSwallowed(t *testing.T) {
	// A lookup that fails for a reason other than absence still means the
	// shell is not usable, and the next candidate is what to try.
	sh := resolve("windows", env(nil), func(name string) (string, error) {
		if name == "pwsh" {
			return "", errors.New("permission denied")
		}
		return `C:\powershell.exe`, nil
	})
	if sh.Name != "powershell" {
		t.Fatalf("name = %q", sh.Name)
	}
}

// The execution shell ignores $SHELL entirely. This is the whole point of it
// being a second resolution: the user is in fish, and the command a model
// wrote still goes to bash.
func TestResolveExecIgnoresTheUsersShell(t *testing.T) {
	for _, user := range []string{"/usr/bin/fish", "/bin/zsh", "/usr/bin/nu"} {
		sh := resolveExec("darwin", env(map[string]string{"SHELL": user}), has("bash"))
		if sh.Name != "bash" {
			t.Errorf("SHELL=%s: name = %q, want bash", user, sh.Name)
		}
		if got := sh.Argv("ls"); !slices.Equal(got, []string{`C:\Program Files\bash.exe`, "-c", "ls"}) {
			t.Errorf("SHELL=%s: argv = %v", user, got)
		}
	}
}

// bash comes off PATH rather than /bin/bash, so a machine carrying a modern
// bash ahead of an ancient one gets the modern one.
func TestResolveExecTakesBashFromPath(t *testing.T) {
	sh := resolveExec("linux", env(nil), func(name string) (string, error) {
		if name == "bash" {
			return "/opt/homebrew/bin/bash", nil
		}
		return "", exec.ErrNotFound
	})
	if sh.Path != "/opt/homebrew/bin/bash" {
		t.Fatalf("path = %q", sh.Path)
	}
}

// The floor is the POSIX floor, not the user's shell: a machine with no bash
// runs /bin/sh, which is the one thing every non-Windows system has.
func TestResolveExecFallsBackToPosixSh(t *testing.T) {
	sh := resolveExec("linux", env(map[string]string{"SHELL": "/usr/bin/fish"}), hasNothing)
	if sh.Name != "sh" || sh.Path != "/bin/sh" {
		t.Fatalf("got %+v", sh)
	}
}

// Windows has no bash to prefer and no POSIX floor to fall back to, so the
// execution shell is the platform's own — the same answer Current gives.
func TestResolveExecOnWindowsIsTheWindowsResolution(t *testing.T) {
	look := has("pwsh", "powershell")
	if got, want := resolveExec("windows", env(nil), look), resolve("windows", env(nil), look); !slices.Equal(got.Argv("ls"), want.Argv("ls")) {
		t.Fatalf("exec = %v, current = %v", got.Argv("ls"), want.Argv("ls"))
	}
}
