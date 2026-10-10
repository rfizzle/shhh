package profile

import (
	"fmt"
	"strings"
	"testing"
)

func TestProfile_AnUnknownModelKeyIsRefused(t *testing.T) {
	cases := []struct {
		name, body, key string
		line            int
	}{
		{"single form", "base_url = \"http://x/v1\"\n\n[[models]]\nid = \"m\"\nstructured_output = false\n", "structured_output", 5},
		{"provider form", "[[provider]]\nname = \"gw\"\nbase_url = \"http://x/v1\"\n\n  [[provider.models]]\n  id = \"m\"\n  structured_output = false\n", "structured_output", 7},
		{"profile key", "base_url = \"http://x/v1\"\nbase_urll = \"http://y\"\n", "base_urll", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadFile(writeProfile(t, t.TempDir(), "gw.toml", c.body))
			if err == nil {
				t.Fatal("an unknown key should be refused")
			}
			msg := err.Error()
			if !strings.Contains(msg, "unknown key") || !strings.Contains(msg, c.key) ||
				!strings.Contains(msg, fmt.Sprintf("gw.toml:%d:", c.line)) {
				t.Fatalf("want the key %q and line %d named, got %q", c.key, c.line, msg)
			}
		})
	}
}

func TestProfile_KnownKeysStillLoad(t *testing.T) {
	body := "base_url = \"http://x/v1\"\n\n[[models]]\nid = \"m\"\ncontext_window = 10\nmax_tokens = 5\ncost = { input = 1, output = 2 }\nreasoning = { kind = \"effort\", levels = [\"xhigh\"], always_on = true }\ndecisions = true\nstructured_outputs = false\n"
	if _, err := LoadFile(writeProfile(t, t.TempDir(), "gw.toml", body)); err != nil {
		t.Fatal(err)
	}
}
