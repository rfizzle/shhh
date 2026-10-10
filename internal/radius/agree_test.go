package radius

import (
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
)

// radius reads the inspection allowlist as its one table of what changes
// nothing: every built-in entry is touches "nothing", and an invocation the
// allowlist refuses because it writes or runs a program is never reported as
// nothing, so the card for it cannot say what the allowlist would not.
func TestRadius_AgreesWithTheAllowlist(t *testing.T) {
	for _, entry := range agent.ReadOnlyCommands() {
		c := Outline(entry)
		if v, _ := c.Touches(); v != "nothing" || c.Level != Low {
			t.Errorf("Outline(%q): touches %q at %v; the allowlist runs it as a read", entry, v, c.Level)
		}
	}
	for _, command := range []string{
		"git branch topic", "git branch -D topic", "git diff --output=out", "git remote -v add x y",
		"git tag -l -d v1", "sort -o out go.mod", "sort --compress-program=sh go.mod",
		"rg --pre ./x foo", "rg -z foo", "uniq in out", "date -s 2020-01-01", "hostname newname",
		"file -C", "go list -toolexec=/tmp/x ./...", "go doc -http fmt", "node --version -e x",
		"fd -x rm", "env sh -c id", "find . -delete",
	} {
		if agent.ReadOnlyAllowed(command, nil) {
			t.Fatalf("%q is allowlisted; the case belongs to the allowlist's own test", command)
		}
		c := Outline(command)
		if v, _ := c.Touches(); v == "nothing" || c.Level == Low {
			t.Errorf("Outline(%q) says it touches %q at %v, and the allowlist refuses it", command, v, c.Level)
		}
	}
}
