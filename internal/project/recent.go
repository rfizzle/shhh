package project

// Recently modified files. The command palette's FILES group and the
// draft's file-mention menu are the paths this session touched plus what
// the checkout itself changed most recently — the second half is this
// walk, and it is bounded the way the survey's is: the same skipped
// directories, the same depth and entry ceilings, so a checkout with a
// million files costs what one with a thousand costs. It runs when a menu
// opens and never per keystroke (the rule for dynamic sources). What
// .gitignore ignores stays out (ignore.go): these lists are offers of
// files a person might name, and a build artefact is never that.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RecentFile is one modified path, relative to the first directory walked
// and slash-separated whatever the platform is, with the time it was
// written.
type RecentFile struct {
	Path string
	Mod  time.Time
}

// RecentFiles returns the most recently modified files under dir, newest
// first, at most limit of them. It never fails: a directory it cannot read is
// skipped, and an unreadable root returns nothing rather than an error — the
// menus this feeds are an offer, not a report.
func RecentFiles(dir string, limit int) []RecentFile {
	return RecentFilesIn([]string{dir}, limit)
}

// RecentFilesIn walks several roots — the working directory first, then
// whatever the session added to its scope — and returns their most
// recently modified files, newest first, at most limit of them. Every path
// is relative to the first root, because that is the directory a path
// written into a sentence is read against.
func RecentFilesIn(dirs []string, limit int) []RecentFile {
	if limit <= 0 || len(dirs) == 0 {
		return nil
	}
	base := dirs[0]
	if base == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil
		}
		base = wd
	}

	w := &recentWalk{base: base}
	seen := map[string]bool{}
	for i, dir := range dirs {
		if i == 0 {
			dir = base
		}
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		w.walk(dir, 0, LoadIgnore(dir))
	}

	// Newest first, ties broken by path so the list is stable between two
	// opens of the menu rather than by whatever order the walk produced.
	out := w.out
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

// recentWalk carries the walk's shared budget and output across roots, so
// several scope directories cost what one does.
type recentWalk struct {
	base    string
	visited int
	out     []RecentFile
}

// walk descends one directory, carrying the ignore rules collected from
// the directories above it. It is recursive rather than filepath.WalkDir
// because the rules are a property of the path down, and a stack the
// language maintains is simpler than one maintained beside a flat walk.
func (w *recentWalk) walk(dir string, depth int, rules Ignore) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, d := range entries {
		if w.visited++; w.visited > maxWalkEntries {
			return
		}
		name := d.Name()
		full := filepath.Join(dir, name)
		if d.IsDir() {
			if skipDirs[name] || strings.HasPrefix(name, ".") || depth+1 > maxWalkDepth {
				continue
			}
			if rules.Ignored(full, true) {
				continue
			}
			w.walk(full, depth+1, rules.Descend(full))
			continue
		}
		if !d.Type().IsRegular() {
			continue
		}
		if rules.Ignored(full, false) {
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(w.base, full)
		if err != nil {
			continue
		}
		w.out = append(w.out, RecentFile{Path: filepath.ToSlash(rel), Mod: info.ModTime()})
	}
}
