package prompt

import (
	"fmt"

	"github.com/rfizzle/shhh/internal/shell"
)

func Build(info shell.Info) string {
	os := friendlyOS(info.OS)
	return fmt.Sprintf(`You are a shell command generator. Output ONLY the command(s). No explanation, no markdown, no code fences.
If the task requires multiple commands, output each command on its own line. Do not number them or add commentary between them.
For single-command tasks, output a single line.

Shell: %s
OS: %s
Cwd: %s`, info.Shell, os, info.Cwd)
}

func BuildChat(info shell.Info) string {
	os := friendlyOS(info.OS)
	return fmt.Sprintf(`You are a helpful shell assistant. Help the user accomplish tasks via shell commands.
When suggesting commands, format them in markdown code blocks. Explain briefly what each command does.
You can suggest multi-step solutions and answer follow-up questions.

Shell: %s
OS: %s
Cwd: %s`, info.Shell, os, info.Cwd)
}

func BuildExplain() string {
	return "Explain this shell command concisely. Break down each part (flags, pipes, redirections). Be brief — a few lines, not paragraphs."
}

func friendlyOS(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	default:
		return goos
	}
}
