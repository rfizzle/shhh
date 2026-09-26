package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// releasedMigrations is a digest of every migration a build has shipped, by
// position. A step inserted anywhere but the end moves every later one and
// fails here; so does editing one that has already run somewhere.
var releasedMigrations = []string{
	"56dfcbc7f1ba67b8", "f573e5fc7e3b8c19", "f19178ca28fc154c", "dbe1170e25ffde15", "08da1ac8e5e78d03", // 1–5
	"68c7caf883e311b2", "da892da2f4372b73", "a459de0eb4d929e1", "d08b9711e8c7ba74", "a44c6e5c70bb5566", // 6–10
	"9f82d5e49e8ece57", "223651076464e1a8", "7172863de0809d35", "701359cd7727d1df", "7f98b0af92233d88", // 11–15
	"3ee636c18b2ed1fd", "d56300df60cc31f7", "5591a31b4469cece", "aabdd76b25390279", "21016e64cc6b71cb", // 16–20
	"8e9a5311d2847d18", "d6533ec00bec68ef", "dccfbc30c3b6ff96", "a18271435197185b", "77d7ed1a6d99fc54", // 21–25
	"b00d2a8a53c5a516", "c3472114a3a2a217", "0557076b95fa8040", "0b578545ddafff17", "f7677565891a2bd3", // 26–30
	"3f92de38008719b0", "460f5f7aff01708c", "4263bb5d44fa1db5", "6a110a7e2db15cf4", "b51d7450e2fd2f1b", // 31–35
	"c910ea96aa088b4f", "a28fe39f63ad2151", "f8eb46457110934a", "8e7a066f412525ac", "340dfeffcaf2f4a0", // 36–40
	"f7677565891a2bd3", // 41: migration 30 again, for the stores that recorded it unrun
}

func migrationDigest(m string) string {
	sum := sha256.Sum256([]byte(m))
	return hex.EncodeToString(sum[:8])
}

// The list is held append-only. The recorded version is a count, so a step
// inserted below it is recorded as done on every store already past that
// position and never runs there — which is how stores came to record the
// child-budget columns without having them.
func TestMigrations_AreAppendOnly(t *testing.T) {
	const rule = "migrations are appended and never inserted, removed or edited: " +
		"schema_version is a count, so a step placed below it is recorded as done on every store already past it without running. " +
		"Put the change at the end of the list as a new step"
	if len(migrations) < len(releasedMigrations) {
		t.Fatalf("%d migrations, but %d were released — %s", len(migrations), len(releasedMigrations), rule)
	}
	for i, want := range releasedMigrations {
		if got := migrationDigest(migrations[i]); got != want {
			t.Fatalf("migration %d is not the one released (digest %s, want %s) — %s", i+1, got, want, rule)
		}
	}
	if len(migrations) > len(releasedMigrations) {
		var add []string
		for _, m := range migrations[len(releasedMigrations):] {
			add = append(add, fmt.Sprintf("%q,", migrationDigest(m)))
		}
		t.Fatalf("%d new migration(s) appended; add their digests to releasedMigrations: %s",
			len(migrations)-len(releasedMigrations), strings.Join(add, " "))
	}
}

// openWith opens path with the migration list replaced by list, the way a
// build that shipped that list would have opened it.
func openWith(t *testing.T, path string, list []string) {
	t.Helper()
	back := migrations
	migrations = list
	defer func() { migrations = back }()
	db, err := OpenPath(path)
	if err != nil {
		t.Fatalf("open with %d migrations: %v", len(list), err)
	}
	must(t, db.Close())
}

// childBudgetStep is the position the child-budget columns were inserted at.
const childBudgetStep = 30

// skippedChildBudget is the list a store was upgraded through when the
// child-budget step was inserted at position 30 rather than appended: the
// version was recorded and the columns were never added, while every step
// after it ran.
func skippedChildBudget() []string {
	list := append([]string(nil), migrations[:childBudgetRepairMigration-1]...)
	list[childBudgetStep-1] = `SELECT 1`
	return list
}

// schemaOf is every table's columns, sorted, so two stores whose columns were
// added in a different order still compare equal.
func schemaOf(t *testing.T, db *DB) []string {
	t.Helper()
	rows, err := db.sql.Query(`SELECT m.name, p.name, p.type, p."notnull", COALESCE(p.dflt_value, ''), p.pk
		FROM sqlite_master m, pragma_table_info(m.name) p
		WHERE m.type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var table, column, typ, dflt string
		var notNull, pk int
		if err := rows.Scan(&table, &column, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s.%s %s notnull=%d default=%s pk=%d", table, column, typ, notNull, dflt, pk))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func recordedVersion(t *testing.T, db *DB) int {
	t.Helper()
	var v int
	if err := db.sql.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// A store that recorded the child-budget step without running it opens,
// reads as current, and cannot answer a single query over its sessions —
// which is the dashboard. The appended step puts the columns back, and the
// rows written before it read the columns as NULL.
func TestMigrate_RepairsAStoreThatRecordedTheChildBudgetStepUnrun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.db")
	broken := skippedChildBudget()
	openWith(t, path, broken)

	back := migrations
	migrations = broken
	db, err := OpenPath(path)
	if err != nil {
		migrations = back
		t.Fatalf("the broken store should open: %v", err)
	}
	if v := recordedVersion(t, db); v != len(broken) {
		t.Errorf("broken store records version %d, want %d", v, len(broken))
	}
	parent, err := db.StartAgentSession("code", "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	child, err := db.StartChildAgentSession(parent, "subagent", "test", "test", "writer-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`UPDATE agent_sessions SET end_reason = 'done', ended_at = started_at WHERE id = ?`, child); err != nil {
		t.Fatal(err)
	}
	_, err = db.AgentSessions(time.Time{}, 20)
	must(t, db.Close())
	migrations = back
	if err == nil || !strings.Contains(err.Error(), "no such column: child_budget") {
		t.Fatalf("the reproduction should fail on child_budget, got %v", err)
	}

	db, err = OpenPath(path)
	if err != nil {
		t.Fatalf("reopen with the repair: %v", err)
	}
	defer func() { _ = db.Close() }()
	if v := recordedVersion(t, db); v != len(migrations) {
		t.Errorf("repaired store records version %d, want %d", v, len(migrations))
	}
	sessions, err := db.AgentSessions(time.Time{}, 20)
	if err != nil {
		t.Fatalf("the dashboard's session read still fails after the repair: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("read %d sessions, want 2", len(sessions))
	}
	if _, ok, err := db.AgentSession(child); err != nil || !ok {
		t.Fatalf("read the child back: ok=%v err=%v", ok, err)
	}
	var budget, fresh *int64
	if err := db.sql.QueryRow(`SELECT child_budget, child_tokens_fresh FROM agent_sessions WHERE id = ?`, child).Scan(&budget, &fresh); err != nil {
		t.Fatal(err)
	}
	if budget != nil || fresh != nil {
		t.Errorf("a row written before the repair should read NULL, got budget=%v fresh=%v", budget, fresh)
	}
}

// Every way a store can reach the current list ends at one schema: brand
// new, upgraded from before the child-budget step, upgraded from a store
// that ran it, and upgraded from one that recorded it without running it.
func TestMigrate_EveryPathToTheRepairEndsAtOneSchema(t *testing.T) {
	dir := t.TempDir()
	before := migrations[:childBudgetStep-1]
	paths := map[string][]string{
		"fresh":          nil,
		"never had it":   before,
		"ran it":         migrations[:childBudgetRepairMigration-1],
		"recorded unrun": skippedChildBudget(),
	}
	var want []string
	var names []string
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".db")
		if list := paths[name]; list != nil {
			openWith(t, path, list)
		}
		db, err := OpenPath(path)
		if err != nil {
			t.Fatalf("%s: open: %v", name, err)
		}
		got := schemaOf(t, db)
		v := recordedVersion(t, db)
		must(t, db.Close())
		if v != len(migrations) {
			t.Errorf("%s: version %d, want %d", name, v, len(migrations))
		}
		if want == nil {
			want = got
			continue
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: schema differs from %s:\ngot  %v\nwant %v", name, names[0], got, want)
		}
	}
}
