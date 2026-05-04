package prompt

import (
	"fmt"

	"github.com/rfizzle/shhh/internal/shell"
)

func Build(info shell.Info) string {
	os := friendlyOS(info.OS)
	return fmt.Sprintf(`You are a shell command generator. Output ONLY the command. No explanation, no markdown, no code fences.

Shell: %s
OS: %s
Cwd: %s`, info.Shell, os, info.Cwd)
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
