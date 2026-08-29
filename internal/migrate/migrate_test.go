package migrate

// The migration machinery, checked against a home directory built in a temp
// dir. Every path here comes from $HOME, so a test can stand up a machine
// shaped the old way and watch a migration reshape it — the whole point of
// keeping the detection and the moving separate from the surface that offers
// them.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/storage"
)

// fakeHome builds a home directory with the given files in it, relative to
// the home root, and points $HOME at it. Every helper in this package reads
// $HOME through os.UserHomeDir, and so do config.Paths, storage.Dir and
// pricing.CacheDir — so one Setenv moves the whole layout.
func fakeHome(t *testing.T, files ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	for _, rel := range files {
		path := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// exists is the question every assertion below asks.
func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return err == nil
}

// A machine on the current layout has nothing to migrate, and the check that
// says so must not cost anything or invent work.
func TestPlan_CurrentLayoutHasNothingToDo(t *testing.T) {
	fakeHome(t, ".config/shhh/config.toml", ".local/share/shhh/shhh.db")
	if pending := Plan(); len(pending) != 0 {
		t.Fatalf("a machine already on the current layout has %d migrations pending: %+v", len(pending), pending)
	}
}

// The old directory held both kinds of state together, so the migration has
// to split it: what a person edits goes to the config directory, everything
// else to the data directory.
func TestLegacyAppleDirs_SplitsSettingsFromState(t *testing.T) {
	home := fakeHome(t,
		"Library/Application Support/shhh/config.toml",
		"Library/Application Support/shhh/providers.toml",
		"Library/Application Support/shhh/providers/gateway.toml",
		"Library/Application Support/shhh/shhh.db",
		"Library/Application Support/shhh/evidence/run.json",
		"Library/Caches/shhh/model_prices.json",
	)

	pending := Plan()
	if len(pending) != 1 {
		t.Fatalf("the old layout was not detected: %+v", pending)
	}
	p := pending[0]
	if !p.Auto() {
		t.Fatal("a migration with nothing but moves in it should be one shhh can make itself")
	}
	if p.Consequence == "" || p.Summary == "" {
		t.Fatalf("the migration does not say what it is or what it costs: %+v", p)
	}

	if _, err := p.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}

	for _, want := range []string{
		".config/shhh/config.toml",
		".config/shhh/providers.toml",
		".config/shhh/providers/gateway.toml",
		".local/share/shhh/shhh.db",
		".local/share/shhh/evidence/run.json",
		".cache/shhh/model_prices.json",
	} {
		if !exists(t, filepath.Join(home, filepath.FromSlash(want))) {
			t.Errorf("%s did not arrive", want)
		}
	}
	if exists(t, filepath.Join(home, "Library", "Application Support", "shhh")) {
		t.Error("the emptied old directory was left behind")
	}
	if exists(t, filepath.Join(home, "Library", "Caches", "shhh")) {
		t.Error("the emptied old cache directory was left behind")
	}
	if pending := Plan(); len(pending) != 0 {
		t.Fatalf("the migration reports itself as still pending after it ran: %+v", pending)
	}
}

// A file already at the destination is never replaced. This is the case the
// first command after an upgrade creates on its own — opening the store
// writes an empty one at the new path — so it has to be the safe one.
func TestLegacyAppleDirs_NeverOverwritesTheNewLayout(t *testing.T) {
	home := fakeHome(t,
		"Library/Application Support/shhh/shhh.db",
		".local/share/shhh/shhh.db",
	)
	if err := os.WriteFile(filepath.Join(home, ".local", "share", "shhh", "shhh.db"),
		[]byte("the new one"), 0o600); err != nil {
		t.Fatal(err)
	}

	pending := Plan()
	if len(pending) != 1 {
		t.Fatalf("a machine with a conflict reports nothing: %+v", pending)
	}
	if pending[0].Auto() {
		t.Fatal("a migration that is nothing but a conflict offered to make itself")
	}
	steps := strings.Join(pending[0].Steps, "\n")
	if !strings.Contains(steps, "already there") || !strings.Contains(steps, "remove or rename") {
		t.Fatalf("the conflict does not say what it is or what to do about it:\n%s", steps)
	}

	got, err := os.ReadFile(filepath.Join(home, ".local", "share", "shhh", "shhh.db"))
	if err != nil || string(got) != "the new one" {
		t.Fatalf("the file at the destination was touched: %q %v", got, err)
	}
	if !exists(t, filepath.Join(home, "Library", "Application Support", "shhh", "shhh.db")) {
		t.Fatal("the conflicting file was removed from the old directory")
	}
}

// The old directory is only removed once it is empty. A conflict left in it
// is a file the reader still has to look at, and taking the directory would
// take that with it.
func TestLegacyAppleDirs_KeepsARootThatStillHoldsAConflict(t *testing.T) {
	home := fakeHome(t,
		"Library/Application Support/shhh/config.toml",
		"Library/Application Support/shhh/shhh.db",
		".config/shhh/config.toml",
	)

	pending := Plan()
	if len(pending) != 1 || !pending[0].Auto() {
		t.Fatalf("a migration with one move and one conflict should still be applicable: %+v", pending)
	}
	if _, err := pending[0].Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if !exists(t, filepath.Join(home, ".local", "share", "shhh", "shhh.db")) {
		t.Error("the entry that had no conflict did not move")
	}
	if !exists(t, filepath.Join(home, "Library", "Application Support", "shhh", "config.toml")) {
		t.Error("the conflicting file was removed with the directory")
	}
}

// XDG variables win over the default directories, on every platform now.
// This is the half of "one layout" that is easy to lose: dropping the macOS
// branch is only an improvement if the variables still decide.
func TestLegacyAppleDirs_HonoursXDG(t *testing.T) {
	home := fakeHome(t, "Library/Application Support/shhh/config.toml")
	xdg := filepath.Join(home, "elsewhere", "config")
	t.Setenv("XDG_CONFIG_HOME", xdg)

	pending := Plan()
	if len(pending) != 1 {
		t.Fatalf("nothing detected: %+v", pending)
	}
	if _, err := pending[0].Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !exists(t, filepath.Join(xdg, "shhh", "config.toml")) {
		t.Fatalf("the settings did not go to XDG_CONFIG_HOME")
	}
}

// An apply that fails partway reports what it managed. A caller that only
// learned about the error could not tell a run that did nothing from one that
// did most of it.
func TestApplyMoves_ReportsWhatItManagedBeforeFailing(t *testing.T) {
	home := t.TempDir()
	from := filepath.Join(home, "old")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(from, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The second destination is a path that cannot be created, because a file
	// stands where its parent directory would have to go.
	blocked := filepath.Join(home, "new")
	if err := os.WriteFile(blocked, []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}

	moves := []move{
		{from: filepath.Join(from, "a"), to: filepath.Join(home, "fine", "a")},
		{from: filepath.Join(from, "b"), to: filepath.Join(blocked, "b")},
	}
	done, err := applyMoves(moves, nil)
	if err == nil {
		t.Fatal("a move into a path that cannot exist reported success")
	}
	if len(done) != 1 || !strings.Contains(done[0], "moved") {
		t.Fatalf("the moves that did happen were not reported: %v", done)
	}
}

// The case every macOS user will actually hit: the first command run after
// the upgrade opened the store, which created an empty one at the new path.
// That is not a conflict — nothing has ever been written to it — and treating
// it as one would mean asking everybody to delete a file to get their own
// history back.
func TestLegacyAppleDirs_ReplacesAStoreNothingHasBeenRecordedIn(t *testing.T) {
	home := fakeHome(t)
	old := filepath.Join(home, "Library", "Application Support", "shhh", "shhh.db")
	if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
		t.Fatal(err)
	}
	// A real store with something in it, and a fresh empty one where the new
	// layout would have put it.
	mine, err := storage.OpenPath(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mine.SQL().Exec(
		`INSERT INTO snippets (name, command) VALUES ('deploy', 'make deploy')`); err != nil {
		t.Fatal(err)
	}
	mine.Close()

	fresh, err := storage.Open()
	if err != nil {
		t.Fatal(err)
	}
	fresh.Close()

	pending := Plan()
	if len(pending) != 1 || !pending[0].Auto() {
		t.Fatalf("an empty store at the destination was treated as a conflict: %+v", pending)
	}
	if _, err := pending[0].Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}

	moved, err := storage.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer moved.Close()
	var name string
	if err := moved.SQL().QueryRow(`SELECT name FROM snippets`).Scan(&name); err != nil {
		t.Fatalf("the store that had something in it did not survive: %v", err)
	}
	if name != "deploy" {
		t.Fatalf("the wrong store won: %q", name)
	}
}

// A destination shhh cannot read is one it must not delete. Anything that
// does not answer counts as occupied.
func TestVacant(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	zero := filepath.Join(dir, "zero")
	if err := os.WriteFile(zero, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	notADatabase := filepath.Join(dir, "junk.db")
	if err := os.WriteFile(notADatabase, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]bool{
		empty:                      true,
		zero:                       true,
		notADatabase:               false,
		filepath.Join(dir, "gone"): false,
	} {
		if got := vacant(path); got != want {
			t.Errorf("vacant(%s) = %v, want %v", filepath.Base(path), got, want)
		}
	}
}
