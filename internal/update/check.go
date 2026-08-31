package update

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	repoOwner = "rfizzle"
	repoName  = "shhh"
	cacheTTL  = 24 * time.Hour
	// failTTL is how long a failed fetch is remembered. A machine that
	// cannot reach api.github.com — offline, behind a proxy, or rate
	// limited — used to write no cache at all, so the next run paid the
	// full five-second timeout again, and so did the one after that. The
	// window is short enough that a transient failure costs one hour of
	// staleness rather than a day.
	failTTL    = time.Hour
	cacheFile  = "update_check.json"
	releaseURL = "https://api.github.com/repos/" + repoOwner + "/" + repoName + "/releases/latest"
	// ReleasesPage is where a person goes to see what a newer release
	// changed — the release feed's own address is an API endpoint and is no
	// use to a reader.
	ReleasesPage = "https://github.com/" + repoOwner + "/" + repoName + "/releases"
)

type cacheEntry struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

type Result struct {
	Latest    string
	Current   string
	Available bool
}

func Check(currentVersion string) *Result {
	if currentVersion == "dev" || currentVersion == "" {
		return nil
	}

	if cached := readCache(); cached != nil && fresh(cached) {
		return compareVersions(currentVersion, cached.Latest)
	}

	latest := fetchLatest()
	// A failure is recorded too, with an empty Latest: what stops the next
	// run from making the same doomed request is the entry, not its
	// contents. compareVersions reads an empty Latest as "nothing to say".
	writeCache(&cacheEntry{Latest: latest, CheckedAt: time.Now()})
	if latest == "" {
		return nil
	}
	return compareVersions(currentVersion, latest)
}

// Refresh asks the release feed now, whatever the cache says, and records
// the answer. It is the manual trigger behind `shhh update`; Check is the
// routine one. A dev build still has nothing to compare, and a feed that did
// not answer returns nil after writing the failure the way Check does.
func Refresh(currentVersion string) *Result {
	if currentVersion == "dev" || currentVersion == "" {
		return nil
	}
	latest := fetchLatest()
	writeCache(&cacheEntry{Latest: latest, CheckedAt: time.Now()})
	if latest == "" {
		return nil
	}
	return compareVersions(currentVersion, latest)
}

// Latest is the newest released version the cache knows, or "" when it has
// not been asked or the feed did not answer.
func Latest() string {
	if cached := readCache(); cached != nil {
		return cached.Latest
	}
	return ""
}

func CheckCached(currentVersion string) *Result {
	if currentVersion == "dev" || currentVersion == "" {
		return nil
	}

	cached := readCache()
	if cached == nil {
		return nil
	}
	return compareVersions(currentVersion, cached.Latest)
}

func BackgroundCheck(currentVersion string) {
	if currentVersion == "dev" || currentVersion == "" {
		return
	}

	if cached := readCache(); cached != nil && fresh(cached) {
		return
	}

	go func() {
		writeCache(&cacheEntry{Latest: fetchLatest(), CheckedAt: time.Now()})
	}()
}

// fresh reports whether a cache entry still stands. An entry that recorded a
// failure stands for a shorter window than one that recorded an answer.
func fresh(entry *cacheEntry) bool {
	ttl := cacheTTL
	if entry.Latest == "" {
		ttl = failTTL
	}
	return time.Since(entry.CheckedAt) < ttl
}

func compareVersions(current, latest string) *Result {
	c := "v" + strings.TrimPrefix(current, "v")
	l := "v" + strings.TrimPrefix(latest, "v")
	if !semver.IsValid(c) || !semver.IsValid(l) || semver.Compare(l, c) <= 0 {
		return nil
	}
	return &Result{
		Latest:    latest,
		Current:   current,
		Available: true,
	}
}

func fetchLatest() string {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(releaseURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	defer resp.Body.Close()

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return ""
	}
	return release.TagName
}

func cachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "shhh", cacheFile)
}

func readCache() *cacheEntry {
	data, err := os.ReadFile(cachePath())
	if err != nil {
		return nil
	}
	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}
	return &entry
}

func writeCache(entry *cacheEntry) {
	path := cachePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
