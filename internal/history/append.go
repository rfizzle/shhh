package history

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Append(shell, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}

	path, err := historyPath(shell)
	if err != nil {
		return err
	}

	if isDuplicate(path, command, shell) {
		return nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(formatEntry(shell, command))
	return err
}

func historyPath(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	switch shell {
	case "zsh":
		return filepath.Join(home, ".zsh_history"), nil
	case "bash":
		return filepath.Join(home, ".bash_history"), nil
	case "fish":
		return filepath.Join(home, ".local", "share", "fish", "fish_history"), nil
	default:
		return filepath.Join(home, ".bash_history"), nil
	}
}

func formatEntry(shell, command string) string {
	switch shell {
	case "zsh":
		return fmt.Sprintf(": %d:0;%s\n", time.Now().Unix(), command)
	case "fish":
		return fmt.Sprintf("- cmd: %s\n  when: %d\n", command, time.Now().Unix())
	default:
		return command + "\n"
	}
}

func isDuplicate(path, command, shell string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	var last string
	var lastCmd string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		switch shell {
		case "fish":
			if strings.HasPrefix(line, "- cmd: ") {
				lastCmd = strings.TrimPrefix(line, "- cmd: ")
			}
		case "zsh":
			last = line
		default:
			last = line
		}
	}

	switch shell {
	case "zsh":
		if idx := strings.Index(last, ";"); idx >= 0 {
			return last[idx+1:] == command
		}
		return false
	case "fish":
		return lastCmd == command
	default:
		return last == command
	}
}
