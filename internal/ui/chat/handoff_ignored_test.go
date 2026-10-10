package chat

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// ignoredRepo is a checkout whose .gitignore covers *.log, with kept.log
// tracked anyway, and a changes store holding one record per name given.
func ignoredRepo(t *testing.T, checkout bool, tracks map[string]changeset.Tracking) Model {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if checkout {
		run("init")
	}
	for _, n := range []string{".gitignore", "kept.log", "scratch.log", "notes.md"} {
		body := "x\n"
		if n == ".gitignore" {
			body = "*.log\n"
		}
		if err := os.WriteFile(filepath.Join(dir, n), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if checkout {
		run("add", "-f", "kept.log")
	}
	m, _ := handoffModel(t, &handoffProvider{})
	m.wiring.Tracker = changeset.NewTracker(dir)
	m.turnCount = 1
	m.runFrom = 1
	for name, tr := range tracks {
		m.changes.Add(1, changeset.Record{Path: name, After: "x\n", AfterExists: true, Track: tr})
	}
	return m
}

func closeFor(m Model) Model {
	turn, _ := m.changes.Turn(1)
	m.appendEntry(entry{kind: entryTurnClose, turn: 1, close: &components.TurnClose{Changes: m.turnChangesFor(turn, false)}})
	return m
}

func TestHandoff_IgnoredChangesOweNoOffer(t *testing.T) {
	m := closeFor(ignoredRepo(t, true, map[string]changeset.Tracking{"scratch.log": changeset.TrackUntracked}))
	if m.handoffOwed() {
		t.Fatal("a close whose every path git ignores owes no offer")
	}
	m = closeFor(ignoredRepo(t, true, map[string]changeset.Tracking{
		"scratch.log": changeset.TrackUntracked, "notes.md": changeset.TrackUntracked}))
	if !m.handoffOwed() {
		t.Fatal("one path git would commit among ignored ones still owes the offer")
	}
}

func TestHandoff_ATrackedFileThatMatchesAPatternStillOwes(t *testing.T) {
	m := closeFor(ignoredRepo(t, true, map[string]changeset.Tracking{"kept.log": changeset.TrackTracked}))
	if !m.handoffOwed() {
		t.Fatal("a tracked file is committed work even when a pattern matches its name")
	}
}

func TestHandoff_NoCheckoutStillOffers(t *testing.T) {
	m := closeFor(ignoredRepo(t, false, map[string]changeset.Tracking{"scratch.log": changeset.TrackUnknown}))
	if !m.handoffOwed() {
		t.Fatal("outside a checkout nothing is ignored, so the offer stays")
	}
}

func TestClose_AnIgnoredPathSaysSo(t *testing.T) {
	m := ignoredRepo(t, true, map[string]changeset.Tracking{"scratch.log": changeset.TrackUntracked})
	turn, _ := m.changes.Turn(1)
	row := m.turnChangesFor(turn, false)
	if row.Files != 1 || row.Ignored != 1 || !strings.Contains(row.Note, "ignored") {
		t.Fatalf("the row should count the file and say ignored: %+v", row)
	}
}
