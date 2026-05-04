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

%s

Shell: %s
OS: %s
Cwd: %s`, shellSyntaxRules(info.Shell), info.Shell, os, info.Cwd)
}

func BuildChat(info shell.Info) string {
	os := friendlyOS(info.OS)
	return fmt.Sprintf(`You are a helpful shell assistant. Help the user accomplish tasks via shell commands.
When suggesting commands, format them in markdown code blocks. Explain briefly what each command does.
You can suggest multi-step solutions and answer follow-up questions.

%s

Shell: %s
OS: %s
Cwd: %s`, shellSyntaxRules(info.Shell), info.Shell, os, info.Cwd)
}

func BuildExplain() string {
	return "Explain this shell command concisely. Break down each part (flags, pipes, redirections). Be brief — a few lines, not paragraphs."
}

func shellSyntaxRules(sh string) string {
	switch sh {
	case "fish":
		return `IMPORTANT: Generate fish shell syntax only.
- Variables: $VAR (never ${VAR} or ${{VAR}})
- Set variables: set VAR value (not VAR=value or export VAR=value)
- Conditionals: if/else/end (not if/then/fi)
- Loops: for x in items; ...; end (not do/done)
- Command substitution: (command) (not $(command) or backticks)
- Logical operators: ; and / ; or (not && or ||)
- No function keyword needed: function name; ...; end
- String escaping: single quotes or backslash (no $'...' ANSI-C quoting)
- Test: test EXPR or [ EXPR ] (not [[ ]])
- Stderr redirect: 2>/dev/null (same as POSIX)
- Process substitution: use psub, e.g. diff (cmd1 | psub) (cmd2 | psub)`
	case "bash":
		return `Generate bash syntax. Use ${VAR} for variable expansion, $() for command substitution, [[ ]] for conditionals.`
	case "zsh":
		return `Generate zsh syntax. Use ${VAR} for variable expansion, $() for command substitution, [[ ]] for conditionals.`
	default:
		return `Generate POSIX-compatible shell syntax.`
	}
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
