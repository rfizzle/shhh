package agent

// The grant ladder's two primitives (S-054): what [a] records for a command,
// and what it records for an edit.

import "testing"

func TestGrantPrefix(t *testing.T) {
	cases := []struct{ command, want string }{
		// The grant stops in front of the first argument, so a path, a flag
		// and a glob all end it.
		{"go test ./internal/ui/...", "go test"},
		{"go build -o bin/shhh ./cmd/shhh", "go build"},
		{"pytest tests/unit", "pytest"},
		// Bare words all the way down are all part of the name: `npm run
		// lint` is not a licence to run every npm script.
		{"npm run lint", "npm run lint"},
		{"docker compose up -d", "docker compose up"},
		{"make", "make"},
		// The first word is kept whatever it looks like: it is the whole name
		// of what runs.
		{"./scripts/release.sh --dry-run", "./scripts/release.sh"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := GrantPrefix(c.command); got != c.want {
			t.Errorf("GrantPrefix(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}

func TestGrantPrefixIsMatchedByTheAllowlistItJoins(t *testing.T) {
	// The prefix is only useful if the allowlist it is recorded in accepts
	// the commands it was derived from, and refuses the ones beside them.
	list := []string{GrantPrefix("go test ./internal/ui/...")}
	for _, ok := range []string{"go test ./...", "go test -run TestX ./internal"} {
		if !AllowlistMatches(list, ok) {
			t.Errorf("%q should match the grant it came from", ok)
		}
	}
	for _, no := range []string{"go build ./...", "go", "gotest", "go test ./... ; rm -rf ~"} {
		if AllowlistMatches(list, no) {
			t.Errorf("%q should not match the grant", no)
		}
	}
}

func TestPathUnder(t *testing.T) {
	dirs := []string{"internal/ui"}
	for _, in := range []string{"internal/ui/chat/model.go", "internal/ui/card.go"} {
		if !PathUnder(dirs, in) {
			t.Errorf("%q should be under the grant", in)
		}
	}
	for _, out := range []string{"internal/agent/mode.go", "internal/ui", "README.md", "internal/uix/a.go"} {
		if PathUnder(dirs, out) {
			t.Errorf("%q should not be under the grant", out)
		}
	}
	// A path that climbs back out is not inside, however it is written.
	if PathUnder(dirs, "internal/ui/../agent/mode.go") {
		t.Error("a path that leaves the granted directory is not under it")
	}
	if PathUnder(nil, "internal/ui/chat/model.go") {
		t.Error("no grants means nothing is granted")
	}
}
