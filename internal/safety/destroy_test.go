package safety_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/scope"
)

// destroyFixture is a scratch machine: a home directory, a repository to
// work in and a sibling checkout beside it, all under the test's own
// directory. Nothing here names the real filesystem: `/` is answered from the
// text, and `~` and `$HOME` expand to the scratch home, which the process's
// own HOME is pointed at so the deny mask is read there too.
func destroyFixture(t *testing.T) radius.Where {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	ws := filepath.Join(base, "ws")
	for _, d := range []string{home, ws, filepath.Join(base, "other-repo"), filepath.Join(ws, "node_modules"), filepath.Join(ws, "bin")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	if out, err := exec.Command("git", "init", "-q", ws).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.Symlink(home, filepath.Join(ws, "home-link")); err != nil {
		t.Fatal(err)
	}
	sc, errs := scope.New(ws)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return radius.Where{Dir: sc.Root(), Root: sc.Root(), Home: home, Scope: sc}
}

// TestDestroys_IrreplaceableSpellings is the corpus for the rule that refuses
// a destroying command outright: every line names a target this session may
// not destroy, written the ways a person or a model writes it, and each has
// to be refused naming what it is. A spelling missing here is a spelling that
// walks past the rule to the card, which is a keystroke from running.
func TestDestroys_IrreplaceableSpellings(t *testing.T) {
	w := destroyFixture(t)
	cases := []struct {
		command string
		want    string
	}{
		{"rm -rf /", "the filesystem root"},
		{"rm -rf //", "the filesystem root"},
		{"/bin/rm -fr /", "the filesystem root"},
		{"sudo rm -r --no-preserve-root /", "the filesystem root"},
		{"sudo -u root rm -rf /", "the filesystem root"},
		{"rm -rf -- /", "the filesystem root"},
		{"find / -delete", "the filesystem root"},
		{"find / -exec rm -rf {} +", "the filesystem root"},
		{"chown -R nobody /", "the filesystem root"},
		{"bash -c 'rm -rf /'", "the filesystem root"},
		{"sh -lc \"cd /tmp && rm -rf /\"", "the filesystem root"},
		{"eval rm -rf /", "the filesystem root"},
		{"cd sub && rm -rf /", "the filesystem root"},
		{"rm -rf ~", "your home directory"},
		{"rm -rf ~/", "your home directory"},
		{`rm -rf "$HOME"`, "your home directory"},
		{"rm -rf $HOME", "your home directory"},
		{`rm -rf "${HOME}"`, "your home directory"},
		{"rm -R --force ~", "your home directory"},
		{"env FOO=1 rm -rf ~", "your home directory"},
		{"nice -n 10 rm -rf ~", "your home directory"},
		{"chmod -R 777 ~", "your home directory"},
		{"rm -rf home-link/", "your home directory"},
		{"find ~ -delete", "your home directory"},
		{"rm -rf .", "the workspace root"},
		{"rm -rf ./", "the workspace root"},
		{"chmod -R 755 .", "the workspace root"},
		{"git clean -fdx", "the workspace root"},
		{"find . -delete", "the workspace root"},
		{"rm -rf .git", "a repository's git store"},
		{"rm -rf .git/objects", "a repository's git store"},
		{"rm -rf ../other-repo", "outside the working scope"},
		{"git -C ../other-repo clean -fdx", "outside the working scope"},
		{"rm -rf ~/Documents", "outside the working scope"},
		{"find ~ -name '*.log' -delete", "outside the working scope"},
		{"echo x > /dev/sda", "a device"},
		{"cat img > /dev/disk/by-id/usb-stick", "a device"},
		{"rm -rf /*", "the filesystem root"},
		{"sudo rm -rf ~/*", "your home directory"},
		{`rm -rf "$HOME"/*`, "your home directory"},
		{"dd if=/dev/zero of=/dev/sda bs=1M", "a device"},
		{"make clean && rm -rf ~", "your home directory"},
	}
	for _, c := range cases {
		d := radius.Destroys(c.command, w)
		got := d.Refusal()
		if got == "" {
			t.Errorf("%q was not refused (targets %+v, unresolved %v)", c.command, d.Targets, d.Unresolved)
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%q refused as %q, want it named %q", c.command, got, c.want)
		}
	}
}

// TestDestroys_WhatTheRuleLeavesToTheCard holds the other side of the line:
// a delete of something the session may destroy, and a target the reading
// cannot prove, are refused by nothing here and go to the card they always
// had. The rule refuses only what it can prove.
func TestDestroys_WhatTheRuleLeavesToTheCard(t *testing.T) {
	w := destroyFixture(t)
	cases := []struct {
		command    string
		unresolved bool
	}{
		{"rm -rf .tmp/test-build", false},
		{"rm -rf node_modules", false},
		{"rm -rf ./bin", false},
		{"rm -rf bin/ node_modules/", false},
		{"find . -name '*.pyc' -delete", false},
		{"git clean -fdx build", false},
		{"git clean -n -fdx", false},
		{"chmod -R 755 ./bin", false},
		{"echo done > /dev/null", false},
		{"echo hi > /dev/tty1", false},
		{"echo hi > /dev/./null", false},
		{"rm -rf /tmp/*", true},
		{"dd if=disk.img of=/dev/null", false},
		// Not a recursive delete, and not a destroying row.
		{"rm ~/.bashrc", false},
		{"rm -rf '$HOME'", false},
		// A link removed without its slash is the link, not what it
		// points at.
		{"rm -rf home-link", false},
		// What the reading cannot prove is left to the card.
		{"rm -rf $DIR", true},
		{"rm -rf *", true},
		{"rm -rf ~other", true},
		{"rm -rf $(pwd)", true},
		{"rm -rf `pwd`", true},
		{"cd .. && rm -rf ws", true},
		{"cd /tmp; rm -rf .", true},
		{"rm -rf {a,b}", true},
	}
	for _, c := range cases {
		d := radius.Destroys(c.command, w)
		if got := d.Refusal(); got != "" {
			t.Errorf("%q was refused as %q; the rule refuses only what it can prove", c.command, got)
		}
		if c.unresolved && len(d.Unresolved) == 0 {
			t.Errorf("%q proved nothing and said nothing about it (targets %+v)", c.command, d.Targets)
		}
	}
}

// A surface with no working scope — the one-shot — has nothing to be outside
// of, and still refuses every target that is irreplaceable without one.
func TestDestroys_WithNoScope(t *testing.T) {
	w := destroyFixture(t)
	w.Scope = nil
	for command, want := range map[string]string{
		"rm -rf /":                    "the filesystem root",
		"rm -rf ~":                    "your home directory",
		"rm -rf .":                    "the workspace root",
		"rm -rf .git":                 "a repository's git store",
		"dd if=img of=/dev/sda bs=1M": "a device",
	} {
		if got := radius.Destroys(command, w).Refusal(); !strings.Contains(got, want) {
			t.Errorf("%q with no scope refused as %q, want %q", command, got, want)
		}
	}
}
