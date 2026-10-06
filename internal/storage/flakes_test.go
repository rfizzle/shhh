package storage

import (
	"testing"
	"time"
)

// Each flake is counted against its checkout, suite and check, the count
// before it is what RecordFlake answers, and the row keeps the latest
// flake's command, first-run exit code and session.
func TestFlakes_ACheckIsCountedPerCheckout(t *testing.T) {
	db := openTestDB(t)
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	steps := []struct {
		name       string
		flake      Flake
		at         time.Time
		wantBefore int
	}{
		{"the first flake has none before it",
			Flake{Root: "/a", Suite: "default", Check: "test", Command: "make test", FirstExit: 2, LastSession: "s1"}, t0, 0},
		{"the second counts the first",
			Flake{Root: "/a", Suite: "default", Check: "test", Command: "make test -p 2", FirstExit: 1, LastSession: "s2"}, t0.Add(time.Hour), 1},
		{"another check is its own count",
			Flake{Root: "/a", Suite: "default", Check: "lint", Command: "make lint", FirstExit: 3, LastSession: "s2"}, t0.Add(2 * time.Hour), 0},
		{"another suite is its own count",
			Flake{Root: "/a", Suite: "fast", Check: "test", Command: "make test", FirstExit: 1, LastSession: "s2"}, t0.Add(30 * time.Minute), 0},
		{"another checkout is its own count",
			Flake{Root: "/b", Suite: "default", Check: "test", Command: "make test", FirstExit: 1, LastSession: "s3"}, t0, 0},
		{"the third counts both before it",
			Flake{Root: "/a", Suite: "default", Check: "test", Command: "make test -p 2", FirstExit: 7, LastSession: "s4"}, t0.Add(3 * time.Hour), 2},
	}
	for _, st := range steps {
		before, err := db.RecordFlake(st.flake, st.at)
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if before != st.wantBefore {
			t.Errorf("%s: before = %d, want %d", st.name, before, st.wantBefore)
		}
	}

	got, err := db.FlakesFor("/a")
	if err != nil {
		t.Fatal(err)
	}
	want := []Flake{
		{Root: "/a", Suite: "default", Check: "test", Command: "make test -p 2", Seen: 3, FirstExit: 7,
			FirstAt: t0, LastAt: t0.Add(3 * time.Hour), LastSession: "s4"},
		{Root: "/a", Suite: "default", Check: "lint", Command: "make lint", Seen: 1, FirstExit: 3,
			FirstAt: t0.Add(2 * time.Hour), LastAt: t0.Add(2 * time.Hour), LastSession: "s2"},
		{Root: "/a", Suite: "fast", Check: "test", Command: "make test", Seen: 1, FirstExit: 1,
			FirstAt: t0.Add(30 * time.Minute), LastAt: t0.Add(30 * time.Minute), LastSession: "s2"},
	}
	if len(got) != len(want) {
		t.Fatalf("FlakesFor(/a) = %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v\nwant %+v", i, got[i], want[i])
		}
	}
	if none, err := db.FlakesFor("/c"); err != nil || len(none) != 0 {
		t.Errorf("a checkout with no flakes read back %v, %v", none, err)
	}
}

// The ledger survives the store being opened again: it is a count across
// sessions, and a session that started over at zero would say every flake
// was the first.
func TestFlakes_TheCountOutlivesTheSession(t *testing.T) {
	path := t.TempDir() + "/flakes.db"
	for i, want := range []int{0, 1} {
		db, err := OpenPath(path)
		if err != nil {
			t.Fatal(err)
		}
		before, err := db.RecordFlake(Flake{Root: "/a", Suite: "default", Check: "test", Command: "make test", FirstExit: 1}, time.Now())
		_ = db.Close()
		if err != nil || before != want {
			t.Fatalf("open %d: before = %d, %v; want %d", i+1, before, err, want)
		}
	}
}
