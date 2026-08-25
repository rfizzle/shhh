package changeset

// Undo (S-100). Taking a turn back is reading the store's before-content and
// putting it on disk — deliberately not a git operation, so it works in a
// directory that was never a repository and never touches the user's index
// or stash.
//
// Planning and applying are separate because the interesting part happens
// between them. The plan reads each file as it is now and compares it with
// what the turn left behind; a file that no longer matches has drifted, and
// the workspace holds something the record never saw. Nothing is overwritten
// on that basis alone: the plan says which files drifted, the user chooses,
// and Apply is told what they chose.
//
// Every restore produces its own Record, so the undo can be recorded as a
// changeset of its own — which is what makes an undo reviewable, and
// undoable.

import (
	"os"
	"path/filepath"
)

// undoFileMode is what a restored file is created with when the workspace has
// no mode to reuse — the file was deleted by the turn, so there is nothing
// left to read a mode from.
const undoFileMode os.FileMode = 0o644

// UndoFile is one file's part in an undo: the record that says what to put
// back, and the file as it is on disk right now.
type UndoFile struct {
	Record Record
	// Now is the file's current content; NowExists distinguishes an empty
	// file from a missing one, the same way the record does.
	Now       string
	NowExists bool
	// Drifted says the workspace no longer holds what the turn left behind,
	// so restoring would discard a change the record never saw.
	Drifted bool
}

// Path is the file the record is about.
func (f UndoFile) Path() string { return f.Record.Path }

// Removes reports a file the undo deletes rather than rewrites — one the
// turn created, which has no before-content to put back.
func (f UndoFile) Removes() bool { return !f.Record.BeforeExists }

// UndoPlan is what undoing a turn would do to the workspace. It is a
// snapshot: the disk was read when the plan was built, so a plan held across
// a long confirmation describes the workspace as it was when it was offered.
type UndoPlan struct {
	Turn  int64
	Files []UndoFile
}

// PlanUndo builds the plan for turn t, restricted to paths when it is
// non-empty — the selection review stages — and covering every record
// otherwise. Records keep their turn order, so the plan reads in the order
// the turn wrote.
func PlanUndo(t Turn, paths []string) UndoPlan {
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	plan := UndoPlan{Turn: t.N}
	for _, r := range t.Records {
		if len(want) > 0 && !want[r.Path] {
			continue
		}
		f := UndoFile{Record: r}
		if data, err := os.ReadFile(r.Path); err == nil {
			f.Now, f.NowExists = string(data), true
		}
		f.Drifted = f.NowExists != r.AfterExists || f.Now != r.After
		plan.Files = append(plan.Files, f)
	}
	return plan
}

// Empty reports a plan with nothing in it.
func (p UndoPlan) Empty() bool { return len(p.Files) == 0 }

// Drifted names the files that changed since the turn, in plan order.
func (p UndoPlan) Drifted() []string {
	var out []string
	for _, f := range p.Files {
		if f.Drifted {
			out = append(out, f.Path())
		}
	}
	return out
}

// Restores is how many files the undo would write back — content the turn
// replaced, plus files the turn deleted.
func (p UndoPlan) Restores() int {
	n := 0
	for _, f := range p.Files {
		if !f.Removes() {
			n++
		}
	}
	return n
}

// Removes is how many files the undo would delete: the ones the turn created.
func (p UndoPlan) Removes() int {
	n := 0
	for _, f := range p.Files {
		if f.Removes() {
			n++
		}
	}
	return n
}

// Touches is how many files the undo would actually act on. Without force a
// drifted file is left alone, so the count the confirm states is this one and
// not the file count.
func (p UndoPlan) Touches(force bool) int {
	if force {
		return len(p.Files)
	}
	return len(p.Files) - len(p.Drifted())
}

// UndoFailure is one file the undo could not restore, and why.
type UndoFailure struct {
	Path string
	Err  error
}

// UndoOutcome is what an undo actually did.
type UndoOutcome struct {
	// Records are the reverse edits, in the order they were applied — the
	// undo's own changeset.
	Records []Record
	// Skipped names the drifted files left exactly as they were.
	Skipped []string
	// Failed names the files whose restore errored. A failure stops that
	// file, not the undo: the rest of the turn still comes back.
	Failed []UndoFailure
}

// Apply restores the plan's files. A drifted file is skipped unless force
// says to overwrite it, which is the choice the confirm put to the user.
//
// The Records it returns describe the undo as an edit in its own right —
// from the file as it was found to the content the turn had replaced — so
// the caller can record them and the undo becomes reviewable like any turn.
func (p UndoPlan) Apply(force bool) UndoOutcome {
	var out UndoOutcome
	for _, f := range p.Files {
		if f.Drifted && !force {
			out.Skipped = append(out.Skipped, f.Path())
			continue
		}
		rec, err := f.restore()
		if err != nil {
			out.Failed = append(out.Failed, UndoFailure{Path: f.Path(), Err: err})
			continue
		}
		out.Records = append(out.Records, rec)
	}
	return out
}

// restore puts one file back and returns the record of having done so. The
// reverse record's before-side is the file as it was found, not what the turn
// wrote — with force that is the drifted content, which is exactly what a
// second undo would need to restore.
func (f UndoFile) restore() (Record, error) {
	r := f.Record
	rev := Record{
		Path:         r.Path,
		Before:       f.Now,
		After:        r.Before,
		BeforeExists: f.NowExists,
		AfterExists:  r.BeforeExists,
		Agent:        MainAgent,
		Origin:       Approved,
		Track:        r.Track,
	}
	if !r.BeforeExists {
		// The turn created the file; taking the turn back removes it. A file
		// already gone is the state undo wanted, not an error.
		if err := os.Remove(r.Path); err != nil && !os.IsNotExist(err) {
			return Record{}, err
		}
		return rev, nil
	}
	if dir := filepath.Dir(r.Path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Record{}, err
		}
	}
	if err := os.WriteFile(r.Path, []byte(r.Before), undoMode(r.Path)); err != nil {
		return Record{}, err
	}
	return rev, nil
}

// undoMode keeps the file's own permissions where it still exists — an undo
// restores content, and has no business making a script unexecutable.
func undoMode(path string) os.FileMode {
	if st, err := os.Stat(path); err == nil {
		return st.Mode().Perm()
	}
	return undoFileMode
}
