package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectTrustIsPerRootAndReplaceable(t *testing.T) {
	db, err := OpenPath(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, ok := db.ProjectTrusted("/a"); ok {
		t.Fatal("trusted before anything was recorded")
	}
	if err := db.TrustProject("/a", "fp1"); err != nil {
		t.Fatal(err)
	}
	if fp, ok := db.ProjectTrusted("/a"); !ok || fp != "fp1" {
		t.Errorf("trusted = %q %v", fp, ok)
	}
	if _, ok := db.ProjectTrusted("/b"); ok {
		t.Error("trust leaked across roots")
	}
	if err := db.TrustProject("/a", "fp2"); err != nil {
		t.Fatal(err)
	}
	if fp, _ := db.ProjectTrusted("/a"); fp != "fp2" {
		t.Errorf("re-trust did not replace: %q", fp)
	}
	had, err := db.DistrustProject("/a")
	if err != nil || !had {
		t.Errorf("distrust = %v %v", had, err)
	}
	if had, _ := db.DistrustProject("/a"); had {
		t.Error("distrust of nothing reported a row")
	}
	if _, ok := db.ProjectTrusted("/a"); ok {
		t.Error("still trusted after distrust")
	}
}

// The per-server answers become one per checkout. Nothing a person said is
// dropped on the way: the root is still on record, and it re-asks because
// the answer now covers more than the server it was given about.
func TestMigrationCarriesServerTrustToTheCheckout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.db")

	// The store at the step that still had the per-server table, written
	// the way the build of the day would have written it.
	back := migrations
	defer func() { migrations = back }()
	for i, m := range migrations {
		if !strings.Contains(m, "CREATE TABLE IF NOT EXISTS mcp_trust") {
			continue
		}
		migrations = back[:i+1]
		break
	}
	if len(migrations) == len(back) {
		t.Fatal("no migration creates the per-server table")
	}
	old, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][3]string{{"/repo", "gh", "fp-gh"}, {"/repo", "docs", "fp-docs"}, {"/other", "gh", "fp-gh"}} {
		if _, err := old.sql.Exec(
			`INSERT INTO mcp_trust (root, name, fingerprint, trusted_at) VALUES (?, ?, ?, '2026-01-01T00:00:00.000Z')`,
			row[0], row[1], row[2]); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	migrations = back
	db, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, root := range []string{"/repo", "/other"} {
		fp, ok := db.ProjectTrusted(root)
		if !ok {
			t.Fatalf("%s lost its answer in the migration", root)
		}
		if fp == "" {
			t.Errorf("%s carried no fingerprint, so nothing can read as edited", root)
		}
	}
	if _, err := db.sql.Exec(`SELECT 1 FROM mcp_trust`); err == nil {
		t.Error("the per-server table survived the migration")
	}
}

// must fails the test on an error from setting it up.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
