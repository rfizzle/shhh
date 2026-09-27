package cli

// The README is the front door, and two things on it go stale without anyone
// noticing: a command line whose verb or flag was renamed, and a picture
// whose scene was renamed or lost the snap it was drawn from. Both are held
// here, so the rename fails the gate instead of the reader.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const readmePath = "../../README.md"

func readReadme(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("reading the README: %v", err)
	}
	return string(b)
}

// fencedLines is every line inside a fenced block.
func fencedLines(doc string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			in = !in
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	return out
}

// An invocation starts where a prompt, a command substitution or a pipe
// hands the line to shhh, and ends where the shell takes it back.
var (
	readmeInvocation = regexp.MustCompile(`(?:^\$ |\$\(|\|\s*|^)shhh\s`)
	readmeShellEnd   = regexp.MustCompile(`\s#|[|)&;]`)
)

// readmeInvocations is each shhh invocation on the line, as its words after
// the binary's name.
func readmeInvocations(line string) [][]string {
	var out [][]string
	for _, loc := range readmeInvocation.FindAllStringIndex(line, -1) {
		rest := line[loc[1]:]
		if end := readmeShellEnd.FindStringIndex(rest); end != nil {
			rest = rest[:end[0]]
		}
		out = append(out, shellWords(rest))
	}
	return out
}

// shellWords splits on spaces, keeping a quoted run as one word.
func shellWords(s string) []string {
	var words []string
	var cur strings.Builder
	quote, has := rune(0), false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote, has = r, true
		case r == ' ' || r == '\t':
			if has || cur.Len() > 0 {
				words = append(words, cur.String())
			}
			cur.Reset()
			has = false
		default:
			cur.WriteRune(r)
		}
	}
	if has || cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words
}

// checkInvocation walks the words down the command tree, and says what in
// them the tree does not have. It returns the path it reached.
func checkInvocation(root *cobra.Command, words []string) (string, []string) {
	cmd, positional := root, false
	var unknown []string
	for _, w := range words {
		switch {
		case strings.HasPrefix(w, "--"):
			name, _, _ := strings.Cut(strings.TrimPrefix(w, "--"), "=")
			if cmd.Flags().Lookup(name) == nil && cmd.InheritedFlags().Lookup(name) == nil {
				unknown = append(unknown, w)
			}
		case strings.HasPrefix(w, "-") && len(w) > 1:
			for _, r := range w[1:] {
				s := string(r)
				if cmd.Flags().ShorthandLookup(s) == nil && cmd.InheritedFlags().ShorthandLookup(s) == nil {
					unknown = append(unknown, "-"+s)
				}
			}
		case positional:
		default:
			if sub := subcommand(cmd, w); sub != nil {
				cmd = sub
			} else {
				positional = true
			}
		}
	}
	return cmd.CommandPath(), unknown
}

func subcommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}

// Every command line the README shows is one the binary takes: its verbs are
// commands in the tree and its flags are flags those commands declare.
func TestReadme_EveryCommandLineExists(t *testing.T) {
	root := NewRootCmd()
	reached := map[string]bool{}
	for _, line := range fencedLines(readReadme(t)) {
		for _, words := range readmeInvocations(line) {
			path, unknown := checkInvocation(root, words)
			reached[path] = true
			for _, u := range unknown {
				t.Errorf("the README's %q uses %s, which %q does not take", strings.TrimSpace(line), u, path)
			}
		}
	}
	// The setup a reader is walked through is shown, so a verb that dropped
	// off the page is noticed as surely as one that was renamed.
	for _, want := range []string{
		"shhh cmd", "shhh code", "shhh chat", "shhh init",
		"shhh config", "shhh config init", "shhh config set",
		"shhh trust", "shhh doctor", "shhh keys",
	} {
		if !reached[want] {
			t.Errorf("the README shows no %q command line", want)
		}
	}
}

func TestReadme_InvocationsAreRead(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{`$ shhh config init --global     # write yours`, []string{"config init --global"}},
		{`eval "$(shhh init zsh)"    # in ~/.zshrc`, []string{"init zsh"}},
		{`$ echo "x" | shhh cmd | sh`, []string{"cmd"}},
		{`$ shhh code -p "summarise what changed"`, []string{"code -p summarise what changed"}},
		{`$ make build`, nil},
	} {
		var got []string
		for _, w := range readmeInvocations(tc.line) {
			got = append(got, strings.Join(w, " "))
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%q read as %q, want %q", tc.line, got, tc.want)
		}
	}
	path, unknown := checkInvocation(NewRootCmd(), []string{"config", "init", "--no-such-flag"})
	if path != "shhh config init" || len(unknown) != 1 {
		t.Errorf("a made-up flag reached %q and was reported as %q", path, unknown)
	}
}

var readmeImage = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)

// Every picture is drawn from a scene the harness still has: a still is
// `<scene>-<snap>.gif` and names a snap that scene takes, and a recording is
// `<scene>.cast.gif`. Every picture under docs/readme is on the page, so none
// is left behind when one is replaced.
func TestReadme_EveryPictureIsASceneSnap(t *testing.T) {
	doc := readReadme(t)
	scenes := filepath.Join("..", "..", "scripts", "tui", "scenes")
	shown := map[string]bool{}
	for _, m := range readmeImage.FindAllStringSubmatch(doc, -1) {
		alt, src := strings.TrimSpace(m[1]), m[2]
		if !strings.HasPrefix(src, "docs/readme/") {
			continue
		}
		shown[filepath.Base(src)] = true
		if alt == "" {
			t.Errorf("%s has no alt text", src)
		}
		if _, err := os.Stat(filepath.Join("..", "..", src)); err != nil {
			t.Errorf("the README shows %s, which is not there", src)
		}
		name := strings.TrimSuffix(filepath.Base(src), ".gif")
		if scene, ok := strings.CutSuffix(name, ".cast"); ok {
			if _, err := os.Stat(filepath.Join(scenes, scene, "steps.txt")); err != nil {
				t.Errorf("%s is a recording of scene %q, which does not exist", src, scene)
			}
			continue
		}
		if !sceneHasSnap(scenes, name) {
			t.Errorf("%s names no scene and snap under scripts/tui/scenes", src)
		}
	}
	if len(shown) == 0 {
		t.Fatal("the README shows no picture from docs/readme")
	}
	files, _ := filepath.Glob(filepath.Join("..", "..", "docs", "readme", "*.gif"))
	for _, f := range files {
		if !shown[filepath.Base(f)] {
			t.Errorf("docs/readme/%s is on no line of the README", filepath.Base(f))
		}
	}
}

// sceneHasSnap reports whether name is <scene>-<snap> for a scene whose steps
// take that snap.
func sceneHasSnap(scenes, name string) bool {
	for i := range name {
		if name[i] != '-' {
			continue
		}
		steps, err := os.ReadFile(filepath.Join(scenes, name[:i], "steps.txt"))
		if err != nil {
			continue
		}
		snap := regexp.MustCompile(`(?m)(^|\s)snap ` + regexp.QuoteMeta(name[i+1:]) + `(\s|$)`)
		if snap.Match(steps) {
			return true
		}
	}
	return false
}
