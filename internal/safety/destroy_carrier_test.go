package safety_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/safety"
)

// Both readers read the same command out of each of these lines: the safety
// table flags it and the irreplaceable-target rule refuses it. Each is a
// spelling one of them once read and the other walked past — a `case`
// pattern's or a flow word's parenthesis, and a carrier only one of them
// knew.
func TestBothReaders_ReadTheSameCommand(t *testing.T) {
	w := destroyFixture(t)
	other := filepath.Join(filepath.Dir(w.Root), "other-repo")
	cases := []struct {
		command string
		pattern string
		refusal string
	}{
		{"case x in x) rm -rf /;; esac", "rm -rf /", "the filesystem root"},
		{"case $1 in go) rm -rf ~;; esac", "rm -rf ~", "your home directory"},
		{"if(rm -rf /)", "rm -rf /", "the filesystem root"},
		{"(rm -rf ~)", "rm -rf ~", "your home directory"},
		{"exec rm -rf /", "rm -rf /", "the filesystem root"},
		{"exec rm -rf " + other, "rm -r -f", "outside the working scope"},
		{"stdbuf -oL rm -rf ~", "rm -rf ~", "your home directory"},
		{"setsid rm -rf /", "rm -rf /", "the filesystem root"},
	}
	for _, c := range cases {
		if ws := safety.Check(c.command); len(ws) == 0 || ws[0].Pattern != c.pattern {
			t.Errorf("Check(%q) = %v, want %q", c.command, ws, c.pattern)
		}
		if got := radius.Destroys(c.command, w).Refusal(); !strings.Contains(got, c.refusal) {
			t.Errorf("Destroys(%q).Refusal() = %q, want %q", c.command, got, c.refusal)
		}
	}
	// A substitution's parenthesis is not where a command starts for the
	// rule: what it expands to is not proved, so it is refused by nothing.
	for _, command := range []string{"rm -rf $(pwd)", "rm -rf ./bin $(echo x)"} {
		if got := radius.Destroys(command, w).Refusal(); got != "" {
			t.Errorf("Destroys(%q) was refused as %q; a substitution proves nothing", command, got)
		}
	}
}

// TestDestroys_BehindAFlowWordOrAWrapper holds the irreplaceable-target rule
// to the same reading the safety table takes: a destroying command behind a
// shell flow word, a wrapper or a group is refused as it is refused bare, and
// mkfs is refused against a device.
func TestDestroys_BehindAFlowWordOrAWrapper(t *testing.T) {
	w := destroyFixture(t)
	cases := []struct {
		command string
		want    string
	}{
		{"if x; then rm -rf /; fi", "the filesystem root"},
		{"if true; then :; else rm -rf ~; fi", "your home directory"},
		{"if a; then :; elif b; then rm -rf ~; fi", "your home directory"},
		{"while :; do rm -rf ~; done", "your home directory"},
		{"until false; do rm -rf /; done", "the filesystem root"},
		{"! rm -rf /", "the filesystem root"},
		{"{ rm -rf /; }", "the filesystem root"},
		{"! { rm -rf ~; }", "your home directory"},
		{"if x; then sudo -u root rm -rf /; fi", "the filesystem root"},
		{"nice -n 5 rm -rf /", "the filesystem root"},
		{"timeout 5 rm -rf /", "the filesystem root"},
		{"ionice -c3 rm -rf /", "the filesystem root"},
		{"if x; then find ~ -delete; fi", "your home directory"},
		{"if x; then git clean -fdx; fi", "the workspace root"},
	}
	for _, c := range cases {
		if got := radius.Destroys(c.command, w).Refusal(); !strings.Contains(got, c.want) {
			t.Errorf("Destroys(%q).Refusal() = %q, want %q", c.command, got, c.want)
		}
	}
}

// The rule still refuses only what it can prove: a flow word in front of a
// read, or of a delete the session may make, is refused by nothing here and
// goes to the card it always had.
func TestDestroys_AFlowWordInFrontOfAPlainCommand(t *testing.T) {
	w := destroyFixture(t)
	for _, command := range []string{
		"if grep -q x f; then echo y; fi",
		"! grep -q x f",
		"while read -r l; do echo $l; done < f",
		"if x; then rm -rf ./bin; fi",
		"echo then rm -rf /",
	} {
		if got := radius.Destroys(command, w).Refusal(); got != "" {
			t.Errorf("%q was refused as %q; the rule refuses only what it can prove", command, got)
		}
	}
}
