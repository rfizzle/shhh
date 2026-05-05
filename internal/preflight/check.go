package preflight

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Result struct {
	OK      bool
	Command string
	Errors  []string
}

func Check(command, shell string) Result {
	cmds := splitCommands(command)
	var errors []string

	for _, c := range cmds {
		if err := checkBinary(c); err != "" {
			errors = append(errors, err)
		}
	}

	if err := checkSyntax(command, shell); err != "" {
		errors = append(errors, err)
	}

	return Result{
		OK:      len(errors) == 0,
		Command: command,
		Errors:  errors,
	}
}

func checkBinary(command string) string {
	bin := extractBinary(command)
	if bin == "" {
		return ""
	}

	if isShellBuiltin(bin) {
		return ""
	}

	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Sprintf("command not found: %s", bin)
	}
	return ""
}

func checkSyntax(command, shell string) string {
	sh := resolveShell(shell)
	var flag string
	switch filepath.Base(sh) {
	case "bash", "sh", "zsh":
		flag = "-n"
	case "fish":
		flag = "--no-execute"
	default:
		return ""
	}

	cmd := exec.Command(sh, flag, "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Sprintf("syntax error: %s", msg)
	}
	return ""
}

func extractBinary(command string) string {
	command = strings.TrimSpace(command)

	// Skip env var assignments at the start
	for strings.Contains(command, "=") {
		parts := strings.SplitN(command, " ", 2)
		if len(parts) < 2 || !strings.Contains(parts[0], "=") {
			break
		}
		command = strings.TrimSpace(parts[1])
	}

	// Skip sudo
	if strings.HasPrefix(command, "sudo ") {
		command = strings.TrimSpace(strings.TrimPrefix(command, "sudo"))
	}

	// Get first word
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func resolveShell(shell string) string {
	if filepath.IsAbs(shell) {
		return shell
	}
	sh := os.Getenv("SHELL")
	if sh != "" {
		return sh
	}
	return "/bin/sh"
}

func isShellBuiltin(name string) bool {
	builtins := map[string]bool{
		"cd": true, "echo": true, "exit": true, "export": true,
		"set": true, "unset": true, "source": true, "alias": true,
		"type": true, "read": true, "printf": true, "test": true,
		"[": true, "[[": true, "true": true, "false": true,
		"return": true, "shift": true, "eval": true, "exec": true,
		"trap": true, "wait": true, "jobs": true, "fg": true,
		"bg": true, "pushd": true, "popd": true, "dirs": true,
		"builtin": true, "command": true, "declare": true,
		"local": true, "typeset": true, "ulimit": true,
		"umask": true, "hash": true, "enable": true,
		"complete": true, "compgen": true, "compopt": true,
	}
	return builtins[name]
}

func splitCommands(output string) []string {
	raw := strings.TrimSpace(output)
	if raw == "" {
		return nil
	}
	var cmds []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			cmds = append(cmds, line)
		}
	}
	return cmds
}
