package cli

// `shhh --version` reports like everything else: the build as the title, and
// the release check as a row with a glyph.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A dev build says why there is nothing to compare against, in the doctor's
// own words for the same finding.
func TestVersionTemplate_DevBuildHasNothingToCompare(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	defer restoreVersion(t, version)
	version = "dev"
	got := versionTemplate()
	for _, want := range []string{"shhh {{.Version}} · ", "⊘ update check", "a dev build has no released version"} {
		if !strings.Contains(got, want) {
			t.Fatalf("a dev build's version output lacks %q:\n%s", want, got)
		}
	}
}

// A build the cache says is current reports the build and nothing else: a
// row saying "you are up to date" is a row nobody needs on every run.
func TestVersionTemplate_CurrentBuildReportsOnlyItself(t *testing.T) {
	writeUpdateCache(t, "0.9.4")
	defer restoreVersion(t, version)
	version = "0.9.4"
	got := versionTemplate()
	if strings.Contains(got, "available") || strings.Contains(got, "⊘") {
		t.Fatalf("a current build should say only what it is:\n%s", got)
	}
	if !strings.Contains(got, "shhh {{.Version}} · ") {
		t.Fatalf("the build line is missing:\n%s", got)
	}
}

// A stale build names the release that is out and where to read about it.
func TestVersionTemplate_StaleBuildNamesTheRelease(t *testing.T) {
	writeUpdateCache(t, "0.9.9")
	defer restoreVersion(t, version)
	version = "0.9.4"
	got := versionTemplate()
	for _, want := range []string{"▸ 0.9.9 available", "github.com/rfizzle/shhh/releases"} {
		if !strings.Contains(got, want) {
			t.Fatalf("a stale build's version output lacks %q:\n%s", want, got)
		}
	}
}

// writeUpdateCache seeds the release-check cache the version line reads. It
// never reaches the network: the cache is the only thing the line looks at.
func writeUpdateCache(t *testing.T, latest string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	path := filepath.Join(dir, "shhh", "update_check.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"latest":"` + latest + `","checked_at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// restoreVersion puts the package's build string back, since it is a var the
// linker sets and these tests move it.
func restoreVersion(t *testing.T, was string) {
	t.Helper()
	version = was
}
