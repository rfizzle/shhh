package update

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	repoOwner  = "rfizzle"
	repoName   = "shhh"
	cacheTTL   = 24 * time.Hour
	cacheFile  = "update_check.json"
	releaseURL = "https://api.github.com/repos/" + repoOwner + "/" + repoName + "/releases/latest"
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

	cached := readCache()
	if cached != nil && time.Since(cached.CheckedAt) < cacheTTL {
		return compareVersions(currentVersion, cached.Latest)
	}

	latest := fetchLatest()
	if latest == "" {
		return nil
	}

	writeCache(&cacheEntry{Latest: latest, CheckedAt: time.Now()})
	return compareVersions(currentVersion, latest)
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

	cached := readCache()
	if cached != nil && time.Since(cached.CheckedAt) < cacheTTL {
		return
	}

	go func() {
		latest := fetchLatest()
		if latest != "" {
			writeCache(&cacheEntry{Latest: latest, CheckedAt: time.Now()})
		}
	}()
}

func compareVersions(current, latest string) *Result {
	c := strings.TrimPrefix(current, "v")
	l := strings.TrimPrefix(latest, "v")
	if c == l || l == "" {
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
