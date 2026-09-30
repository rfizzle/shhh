package safety_test

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/radius"
)

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
