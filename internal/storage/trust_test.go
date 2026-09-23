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
	if err := db.TrustProject("/a", "fp1", map[string]string{"skills": "d1"}); err != nil {
		t.Fatal(err)
	}
	if kinds, ok := db.ProjectTrusted("/a"); !ok || kinds["skills"] != "d1" {
		t.Errorf("trusted = %v %v", kinds, ok)
	}
	if _, ok := db.ProjectTrusted("/b"); ok {
		t.Error("trust leaked across roots")
	}
	if err := db.TrustProject("/a", "fp2", map[string]string{"skills": "d2"}); err != nil {
		t.Fatal(err)
	}
	if kinds, _ := db.ProjectTrusted("/a"); kinds["skills"] != "d2" {
		t.Errorf("re-trust did not replace: %v", kinds)
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

// A re-stamp moves an answer to the digests the checkout stands at now, and
// never makes one: a checkout nobody trusted stays untrusted however many
// sessions start in it.
func TestRestampMovesAnAnswerAndNeverMakesOne(t *testing.T) {
	db, err := OpenPath(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if had, err := db.RestampProject("/a", "fp", map[string]string{"skills": "d"}); err != nil || had {
		t.Fatalf("re-stamp of nothing = %v %v", had, err)
	}
	if _, ok := db.ProjectTrusted("/a"); ok {
		t.Fatal("a re-stamp trusted a checkout nobody answered for")
	}
	must(t, db.TrustProject("/a", "fp1", map[string]string{"skills": "d1"}))
	if had, err := db.RestampProject("/a", "fp2", map[string]string{"skills": "d2"}); err != nil || !had {
		t.Fatalf("re-stamp = %v %v", had, err)
	}
	if kinds, ok := db.ProjectTrusted("/a"); !ok || kinds["skills"] != "d2" {
		t.Errorf("after the re-stamp = %v %v", kinds, ok)
	}
}

// An answer recorded as one digest over everything is carried as trusted,
// with no digests per kind: nothing can say what moved since, so the next
// session reads it as unchanged and stamps it.
func TestMigrationCarriesAOneDigestAnswerAsTrusted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.db")

	back := migrations
	defer func() { migrations = back }()
	for i, m := range migrations {
		if strings.Contains(m, "ALTER TABLE project_trust ADD COLUMN kinds") {
			migrations = back[:i]
			break
		}
	}
	if len(migrations) == len(back) {
		t.Fatal("no migration adds the per-kind digests")
	}
	old, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.sql.Exec(`INSERT INTO project_trust (root, fingerprint) VALUES ('/repo', 'fp-whole')`); err != nil {
		t.Fatal(err)
	}
	must(t, old.Close())

	migrations = back
	db, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	kinds, ok := db.ProjectTrusted("/repo")
	if !ok {
		t.Fatal("the answer was lost in the migration")
	}
	if len(kinds) != 0 {
		t.Errorf("a one-digest answer came back with digests per kind: %v", kinds)
	}
}

// The per-server answers become one per checkout. Nothing a person said is
// dropped on the way: the root is still on record.
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
		if _, ok := db.ProjectTrusted(root); !ok {
			t.Fatalf("%s lost its answer in the migration", root)
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
