package changeset

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTracker_AnIgnoredPathIsMarked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	for name, body := range map[string]string{
		".gitignore":  "*.log\n",
		"kept.log":    "x\n", // matches *.log but the index holds it
		"scratch.log": "x\n",
		"plain.txt":   "x\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-f", "kept.log")

	tr := NewTracker(dir)
	turn := Turn{Records: []Record{
		{Path: "kept.log", Track: TrackTracked},
		{Path: "scratch.log", Track: TrackUntracked},
		{Path: "plain.txt", Track: TrackUntracked},
	}}
	got := tr.MarkIgnored(turn)
	for path, want := range map[string]Tracking{"kept.log": TrackTracked, "scratch.log": TrackIgnored, "plain.txt": TrackUntracked} {
		if r, _ := got.Record(path); r.Track != want {
			t.Errorf("%s: want %v, got %v", path, want, r.Track)
		}
	}
	if turn.Records[1].Track != TrackUntracked {
		t.Error("marking must not change the turn it was given")
	}
	// Outside a checkout nothing is ignored.
	if r, _ := NewTracker(t.TempDir()).MarkIgnored(turn).Record("scratch.log"); r.Track != TrackUntracked {
		t.Errorf("no checkout should leave the record alone, got %v", r.Track)
	}
}
