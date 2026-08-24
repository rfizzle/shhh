package web

import (
	"os"
	"path/filepath"
	"runtime"
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
