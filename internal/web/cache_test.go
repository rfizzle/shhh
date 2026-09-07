package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCache_PutGetRoundtrip(t *testing.T) {
	cache, err := OpenCache(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	res := Result{
		FinalURL:    "https://example.com/final",
		Status:      200,
		ContentType: "text/html",
		Body:        []byte("<p>hi</p>"),
		Truncated:   true,
	}
	cache.Put("https://example.com/", "https://example.com/final", res)

	got, ok := cache.Get("https://example.com/")
	if !ok {
		t.Fatal("expected cache hit for requested URL")
	}
	if got.FinalURL != res.FinalURL || got.Status != 200 || got.ContentType != "text/html" || !got.Truncated {
		t.Errorf("meta mismatch: %+v", got)
	}
	if string(got.Body) != "<p>hi</p>" {
		t.Errorf("body = %q", got.Body)
	}

	// The final URL is content-addressed too, so a later direct fetch hits.
	if _, ok := cache.Get("https://example.com/final"); !ok {
		t.Error("expected cache hit for final URL")
	}
	if _, ok := cache.Get("https://example.com/other"); ok {
		t.Error("unexpected hit for a different URL")
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	cache, err := OpenCache(t.TempDir(), 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	cache.Put("https://example.com/", "", Result{Status: 200, Body: []byte("x")})
	if _, ok := cache.Get("https://example.com/"); !ok {
		t.Fatal("expected fresh hit")
	}
	time.Sleep(80 * time.Millisecond)
	if _, ok := cache.Get("https://example.com/"); ok {
		t.Error("expected expired entry to miss")
	}
}

func TestCache_PruneRemovesExpiredFiles(t *testing.T) {
	dir := t.TempDir()
	cache, err := OpenCache(dir, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	cache.Put("https://example.com/", "", Result{Status: 200, Body: []byte("x")})
	time.Sleep(80 * time.Millisecond)
	cache.Prune()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected pruned dir, found %d entries", len(entries))
	}
}

func TestCache_UserOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := filepath.Join(t.TempDir(), "webcache")
	cache, err := OpenCache(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cache.Put("https://example.com/", "", Result{Status: 200, Body: []byte("x")})

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir perm = %o, want 700", perm)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s perm = %o, want 600", e.Name(), perm)
		}
	}
}

// vaultScrub stands in for the session's rewrite: the same function the
// evidence store is given.
func vaultScrub(s string) string { return strings.ReplaceAll(s, "sk-secret", "[secret:KEY]") }

func TestCache_PutScrubsTheURLAndTheBody(t *testing.T) {
	dir := t.TempDir()
	cache, err := OpenCache(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cache.SetScrub(vaultScrub)

	requested := "https://example.com/api?token=sk-secret"
	cache.Put(requested, "https://example.com/final?token=sk-secret", Result{
		FinalURL:    "https://example.com/final?token=sk-secret",
		Status:      200,
		ContentType: "text/html; charset=utf-8",
		Body:        []byte("<p>the key is sk-secret</p>"),
	})

	// Nothing under the directory holds the value — not the body, not the
	// URL the fetch asked for, not the one that answered.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected the entry to be written")
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "sk-secret") {
			t.Errorf("%s holds the value: %s", e.Name(), data)
		}
	}

	// The key is the URL as asked for, so the entry is still found.
	got, ok := cache.Get(requested)
	if !ok {
		t.Fatal("expected a hit on the requested URL")
	}
	if string(got.Body) != "<p>the key is [secret:KEY]</p>" {
		t.Errorf("body = %q", got.Body)
	}
	if got.FinalURL != "https://example.com/final?token=[secret:KEY]" {
		t.Errorf("final URL = %q", got.FinalURL)
	}
}

func TestCache_PutLeavesABinaryBodyAsFetched(t *testing.T) {
	cache, err := OpenCache(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cache.SetScrub(vaultScrub)

	pdf := []byte("%PDF-1.7\x00sk-secret\x00")
	cache.Put("https://example.com/doc.pdf", "", Result{
		Status:      200,
		ContentType: "application/pdf",
		Body:        pdf,
	})
	got, ok := cache.Get("https://example.com/doc.pdf")
	if !ok {
		t.Fatal("expected a hit")
	}
	if string(got.Body) != string(pdf) {
		t.Errorf("body = %q, want the bytes as fetched", got.Body)
	}
}

// writeEntry puts one entry on disk directly, so a test can say what the meta
// holds and when the file was written independently of each other.
func writeEntry(t *testing.T, dir, url, meta string, written time.Time) {
	t.Helper()
	key := cacheKey(url)
	for name, data := range map[string]string{key + ".json": meta, key + ".dat": "body"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, written, written); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCache_PruneDecidesFromTheFilesModTime(t *testing.T) {
	dir := t.TempDir()
	fresh, err := json.Marshal(cacheMeta{Status: 200, Fetched: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	// The meta says the entry is fresh and the file says it was written two
	// hours ago. The sweep believes the file.
	writeEntry(t, dir, "https://example.com/old", string(fresh), time.Now().Add(-2*time.Hour))
	writeEntry(t, dir, "https://example.com/new", string(fresh), time.Now())

	if _, err := OpenCache(dir, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, cacheKey("https://example.com/old")+".json")); !os.IsNotExist(err) {
		t.Errorf("expected the old entry pruned, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, cacheKey("https://example.com/old")+".dat")); !os.IsNotExist(err) {
		t.Errorf("expected the old body pruned, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, cacheKey("https://example.com/new")+".json")); err != nil {
		t.Errorf("expected the fresh entry kept: %v", err)
	}
}

func TestCache_OpenReadsNoEntry(t *testing.T) {
	dir := t.TempDir()
	// A thousand entries whose metas are not JSON at all. A sweep that read
	// them would call every one expired; one that stats them keeps them,
	// which is what says the startup path never opened a file.
	const entries = 1000
	for i := range entries {
		writeEntry(t, dir, fmt.Sprintf("https://example.com/%d", i), "not json", time.Now())
	}
	if _, err := OpenCache(dir, time.Hour); err != nil {
		t.Fatal(err)
	}
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2*entries {
		t.Errorf("dir holds %d files, want %d — the sweep read the entries", len(left), 2*entries)
	}
}

func TestCache_GetReadsTheEntrysOwnTimestamp(t *testing.T) {
	dir := t.TempDir()
	cache, err := OpenCache(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := json.Marshal(cacheMeta{Status: 200, Fetched: time.Now().Add(-2 * time.Hour).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	// Written just now, fetched two hours ago: the file would pass the
	// sweep, and the fetch about to be answered must not.
	writeEntry(t, dir, "https://example.com/", string(stale), time.Now())
	if _, ok := cache.Get("https://example.com/"); ok {
		t.Error("expected a miss on an entry whose meta says it is stale")
	}
}
