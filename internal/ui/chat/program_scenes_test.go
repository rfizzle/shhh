package chat

// Every driven scene answers for its route in the gate. A scene is proof a
// terminal can see, and it needs tmux and a built binary, so it runs in CI
// and never inside a contained session; the program tests are the same route
// as a go test verdict. What holds the two together is the scene's own head:
// its first line names the program tests that walk its route, or says why no
// program test can. This test reads every head and fails on one that does
// neither — including a name no test answers to, and a "none" with no reason
// behind it — so a scene cannot join the tree without its route being
// covered or the gap being argued.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sceneHead is the prefix of a scene's first line.
const sceneHead = "# program test: "

// scenesDir is where the driven scenes live, from this package's directory.
var scenesDir = filepath.Join("..", "..", "..", "scripts", "tui", "scenes")

// programTestDirs are the packages a scene's head may name a test from: the
// session's own, and the one-shot's.
var programTestDirs = []string{".", ".."}

var programTestFunc = regexp.MustCompile(`(?m)^func (TestProgram_\w+)\(t \*testing\.T\)`)

// programTests is every program test the named packages declare.
func programTests(t *testing.T) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	for _, dir := range programTestDirs {
		files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range programTestFunc.FindAllStringSubmatch(string(src), -1) {
				found[m[1]] = true
			}
		}
	}
	return found
}

// readSceneHead is what a scene's head says: the first line after the prefix
// and any continuation lines under it, up to the first line that is a bare
// "#" or not a comment at all.
func readSceneHead(steps string) (string, bool) {
	lines := strings.Split(steps, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], sceneHead) {
		return "", false
	}
	head := []string{strings.TrimPrefix(lines[0], sceneHead)}
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "# ") {
			break
		}
		head = append(head, strings.TrimPrefix(l, "# "))
	}
	return strings.Join(strings.Fields(strings.Join(head, " ")), " "), true
}

// checkSceneHead is what is wrong with one head, or "" when nothing is.
func checkSceneHead(head string, tests map[string]bool) string {
	if reason, ok := strings.CutPrefix(head, "none"); ok {
		reason = strings.TrimSpace(strings.TrimLeft(reason, " —-:"))
		// A reason is a sentence about the route, not a word to get past
		// this check with.
		if len(strings.Fields(reason)) < 4 {
			return "says no program test can cover it without saying why"
		}
		if strings.Contains(reason, "TestProgram_") {
			return "says no program test can cover it and names one"
		}
		return ""
	}
	var missing []string
	for _, name := range strings.Split(head, ",") {
		name = strings.TrimSpace(name)
		if !tests[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return "names " + strings.Join(missing, ", ") + ", which no program test in internal/ui/chat or internal/ui declares"
	}
	return ""
}

func TestScenes_EachNamesItsRoute(t *testing.T) {
	scenes, err := os.ReadDir(scenesDir)
	if err != nil {
		t.Fatal(err)
	}
	tests := programTests(t)
	if len(scenes) == 0 || len(tests) == 0 {
		t.Fatalf("found %d scenes and %d program tests; the paths this test reads have moved", len(scenes), len(tests))
	}
	for _, scene := range scenes {
		if !scene.IsDir() {
			continue
		}
		steps, err := os.ReadFile(filepath.Join(scenesDir, scene.Name(), "steps.txt"))
		if err != nil {
			t.Errorf("%s: %v", scene.Name(), err)
			continue
		}
		head, ok := readSceneHead(string(steps))
		if !ok {
			t.Errorf("%s: steps.txt does not open with %q naming the program tests that walk its route, or %q and why",
				scene.Name(), strings.TrimSpace(sceneHead), sceneHead+"none — ")
			continue
		}
		if problem := checkSceneHead(head, tests); problem != "" {
			t.Errorf("%s: its head %s", scene.Name(), problem)
		}
	}
}

// The check itself: a head that names nothing, a name nobody declares, and a
// none with no reason are all refused, and the two honest shapes pass.
func TestScenes_AHeadIsReadStrictly(t *testing.T) {
	tests := map[string]bool{"TestProgram_A": true, "TestProgram_B": true}
	for _, tc := range []struct {
		steps string
		ok    bool
	}{
		{"# program test: TestProgram_A\n#\n# prose\n", true},
		{"# program test: TestProgram_A,\n#   TestProgram_B\n#\n", true},
		{"# program test: none — a second process opens the conversation again\n", true},
		{"# program test: none\n", false},
		{"# program test: none — because of TestProgram_A and TestProgram_B\n", false},
		{"# program test: none — later\n", false},
		{"# program test: TestProgram_C\n", false},
		{"# program test: TestProgram_A, TestSomethingElse\n", false},
		{"# The path a first session takes\nsnap 01 \"x\"\n", false},
	} {
		head, found := readSceneHead(tc.steps)
		ok := found && checkSceneHead(head, tests) == ""
		if ok != tc.ok {
			t.Errorf("%q: read as %v, want %v", tc.steps, ok, tc.ok)
		}
	}
}
