package migrate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/storage"
)

// The macOS `~/Library` layout, retired.
//
// shhh used to read `~/Library/Application Support/shhh` on macOS and the XDG
// directories everywhere else, which made "where are my settings" a question
// with two answers and no way to tell from the outside which one a given
// machine would give. It was also the one directory that mixed the two kinds
// of state the rest of the product keeps apart: `config.toml` is a file a
// person edits, `shhh.db` is a database they never open, and both were in
// there together. So there is one layout now — `~/.config/shhh` for settings,
// `~/.local/share/shhh` for state, `~/.cache/shhh` for what can be
// re-fetched — on every platform, XDG variables honoured
// (docs/capabilities/configuration.md#one-layout-everywhere).
//
// Nothing reads the old directory any more. That is what makes this a
// migration and not a fallback: a Mac that still has one is a Mac whose
// settings and history shhh is no longer finding, and the point of the check
// is to say so in those words rather than to quietly start reading two places
// again.

// appleSupport and appleCaches are the two retired roots, relative to a home
// directory. They are named as literals rather than asked of
// os.UserCacheDir, which on macOS answers the very directory being retired.
var (
	appleSupport = filepath.Join("Library", "Application Support", "shhh")
	appleCaches  = filepath.Join("Library", "Caches", "shhh")
)

// configNames are the entries in the old directory that belong with the
// settings. Everything else in there was state, so the rule is a list of what
// a person edits rather than a list of what a database is called: a file this
// build has never heard of is far more likely to be new state than a new
// setting, and state is the destination that loses nothing by being wrong.
var configNames = map[string]bool{
	"config.toml":    true,
	"providers.toml": true,
	"providers":      true,
}

// legacyAppleDirs detects a machine still holding the retired `~/Library`
// directories, and plans moving each thing in them to where the new layout
// looks for it. The checkout is not part of the question: these directories
// are the user's, and one shhh install has one of them.
func legacyAppleDirs(string) (Pending, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Pending{}, false
	}
	configDir, dataDir, cacheDir, ok := currentDirs()
	if !ok {
		return Pending{}, false
	}

	roots := []relocation{{
		from: filepath.Join(home, appleSupport),
		route: func(name string) string {
			if configNames[name] {
				return configDir
			}
			return dataDir
		},
	}, {
		from:  filepath.Join(home, appleCaches),
		route: func(string) string { return cacheDir },
	}}

	moves, conflicts := planMoves(roots)
	if len(moves) == 0 && len(conflicts) == 0 {
		return Pending{}, false
	}

	p := Pending{
		Name:        "the macOS ~/Library directories",
		Summary:     appleSummary(roots, moves, conflicts),
		Consequence: appleConsequence(moves, conflicts),
		Steps:       appleSteps(moves, conflicts),
	}
	// Nothing to move is nothing to offer. What is left in that case is
	// conflicts, and a conflict is the reader's to settle — a key that would
	// only try the old directories again and find them still occupied is an
	// offer that cannot be honoured (invariant 5).
	if len(moves) > 0 {
		p.Apply = func() ([]string, error) { return applyMoves(moves, roots) }
	}
	return p, true
}

// currentDirs is where the three kinds of state live now. A machine that
// cannot answer any one of them is a machine with nowhere to migrate to, and
// planning a move to a path shhh could not name would be worse than not
// offering one.
func currentDirs() (configDir, dataDir, cacheDir string, ok bool) {
	paths := config.Paths()
	if len(paths) == 0 {
		return "", "", "", false
	}
	dataDir, err := storage.Dir()
	if err != nil {
		return "", "", "", false
	}
	cacheDir, err = pricing.CacheDir()
	if err != nil {
		return "", "", "", false
	}
	return filepath.Dir(paths[0]), dataDir, cacheDir, true
}

// appleSummary is the one line the doctor row shows. It leads with the
// directory rather than the count, because the directory is the thing the
// reader has to recognise.
func appleSummary(roots []relocation, moves, conflicts []move) string {
	found := make([]string, 0, len(roots))
	for _, root := range roots {
		if _, err := os.Stat(root.from); err == nil {
			found = append(found, shortHome(root.from))
		}
	}
	summary := joinWords(found)
	switch {
	case len(moves) == 0:
		return summary + " · " + countOf(len(conflicts), "conflict to settle", "conflicts to settle")
	case len(conflicts) == 0:
		return summary + " · " + countOf(len(moves), "entry", "entries") + " to move"
	}
	return summary + " · " + countOf(len(moves), "entry", "entries") + " to move, " +
		countOf(len(conflicts), "conflict", "conflicts")
}

// appleConsequence is what leaving it costs, said as what the reader will
// find missing rather than as a description of the directory layout. Which
// sentence it is depends on what is actually in there: a machine whose
// config.toml is still in the old place has settings that are not being read,
// and that is a much sharper thing to say than "some files did not move".
//
// A machine with nothing but conflicts gets the sentence about conflicts,
// because that is the one where the reader has something to decide rather
// than something to press.
func appleConsequence(moves, conflicts []move) string {
	settings, state := false, false
	for _, m := range moves {
		if configNames[filepath.Base(m.from)] {
			settings = true
			continue
		}
		state = true
	}
	switch {
	case settings && state:
		return "shhh is reading none of it: every setting in there is on its default, and the history, snippets and metrics in there are not being counted"
	case settings:
		return "shhh is not reading those settings — every one of them is on its default until they move"
	case state:
		return "the history, snippets, memories and metrics in there are not being read; shhh has started a fresh store beside them"
	case len(conflicts) > 0:
		return "shhh is reading the new copies; the old ones are being kept, and only you can say which of each pair is the one you want"
	}
	return "nothing is being read from there, and nothing in it is needed"
}

// appleSteps are the lines behind `[f]`: every move, then every conflict with
// the reason it is a conflict.
//
// A conflict is the case worth writing carefully, because it is the one shhh
// will not resolve and it is not rare. The first command run after an upgrade
// opens the store, which creates an empty one at the new path — so the very
// next doctor run finds a real database in the old place and a fresh one in
// the new place, and picking the wrong one silently discards a history. Each
// side is therefore named with what is in it, and the last line says what to
// do about it: with size and date on both, "which of these is mine" is a
// question the reader can answer at a glance.
func appleSteps(moves, conflicts []move) []string {
	steps := make([]string, 0, len(moves)+len(conflicts)+1)
	for _, m := range moves {
		steps = append(steps, shortHome(m.from)+"  →  "+shortHome(m.to))
	}
	for _, c := range conflicts {
		steps = append(steps, describe(c.from)+"  ✗  "+describe(c.to)+" is already there")
	}
	if len(conflicts) > 0 {
		steps = append(steps,
			"a conflict is left alone: remove or rename whichever of the pair you do not want, then run this again")
	}
	return steps
}

// describe is a path with enough beside it to tell two copies of the same
// thing apart — how big it is and when it was last written.
func describe(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return shortHome(path)
	}
	if info.IsDir() {
		return shortHome(path) + "/"
	}
	return fmt.Sprintf("%s (%s, %s)", shortHome(path), bytesOf(info.Size()), info.ModTime().Format("2 Jan"))
}
