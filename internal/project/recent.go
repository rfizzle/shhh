package project

// Recently modified files. The command palette's FILES group is the
// paths this session touched plus what the checkout itself changed most
// recently — the second half is this walk, and it is the same bounded walk
// the survey uses: the same skipped directories, the same depth and entry
// ceilings, so a checkout with a million files costs what one with a thousand
// costs. It runs when the palette opens and never per keystroke (the
// rule for dynamic sources).

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RecentFile is one modified path, relative to the directory walked and
// slash-separated whatever the platform is, with the time it was written.
type RecentFile struct {
	Path string
	Mod  time.Time
}

// RecentFiles returns the most recently modified files under dir, newest
// first, at most limit of them. It never fails: a directory it cannot read is
// skipped, and an unreadable root returns nothing rather than an error — the
// palette's FILES group is an offer, not a report.
func RecentFiles(dir string, limit int) []RecentFile {
	if limit <= 0 {
		return nil
	}
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil
		}
		dir = wd
	}

	var out []RecentFile
	visited := 0
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is skipped rather than fatal.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if visited++; visited > maxWalkEntries {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path == dir {
				return nil
			}
			name := d.Name()
			if skipDirs[name] || strings.HasPrefix(name, ".") || depth(dir, path) > maxWalkDepth {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		out = append(out, RecentFile{Path: filepath.ToSlash(rel), Mod: info.ModTime()})
		return nil
	})

	// Newest first, ties broken by path so the list is stable between two
	// opens of the palette rather than by whatever order the walk produced.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Mod.Equal(out[j].Mod) {
			return out[i].Mod.After(out[j].Mod)
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
