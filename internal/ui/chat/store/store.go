package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

type Session struct {
	Name      string             `json:"name"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	Messages  []provider.Message `json:"messages"`
}

func Save(name string, messages []provider.Message) error {
	dir, err := chatDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	filename := sanitizeName(name) + ".json"
	path := filepath.Join(dir, filename)

	session := Session{
		Name:      name,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Messages:  messages,
	}

	if existing, loadErr := loadFromPath(path); loadErr == nil {
		session.CreatedAt = existing.CreatedAt
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func Load(name string) ([]provider.Message, error) {
	dir, err := chatDir()
	if err != nil {
		return nil, err
	}

	filename := sanitizeName(name) + ".json"
	path := filepath.Join(dir, filename)

	session, err := loadFromPath(path)
	if err != nil {
		return nil, fmt.Errorf("chat %q not found", name)
	}
	return session.Messages, nil
}

type ListEntry struct {
	Name      string
	UpdatedAt time.Time
	Turns     int
}

func List() ([]ListEntry, error) {
	dir, err := chatDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []ListEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		session, loadErr := loadFromPath(path)
		if loadErr != nil {
			continue
		}
		turns := 0
		for _, m := range session.Messages {
			if m.Role == provider.RoleUser {
				turns++
			}
		}
		result = append(result, ListEntry{
			Name:      session.Name,
			UpdatedAt: session.UpdatedAt,
			Turns:     turns,
		})
	}
	return result, nil
}

func loadFromPath(path string) (Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Session{}, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

var overrideDir string

func chatDir() (string, error) {
	if overrideDir != "" {
		return overrideDir, nil
	}
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "shhh", "chats"), nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "shhh", "chats"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "shhh", "chats"), nil
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, name)
	if name == "" {
		name = "unnamed"
	}
	return name
}
