package project

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// contextFilenames are the recognized project-context files, in precedence
// order within a directory: shhh's own file inside its state directory wins
// over the generic AGENTS.md convention, which wins over the CLAUDE.md the
// rest of the field writes. One directory contributes one file, so a
// checkout that keeps the same instructions under two names is not told them
// twice. The state directory is a directory now — the backlog and the skills
// live in it — so the context file moved inside it; a checkout still holding
// the old single file is reported by the doctor rather than read here
// (docs/capabilities/configuration.md#a-migration-is-a-doctor-check).
var contextFilenames = []string{filepath.Join(".shhh", "project.md"), "AGENTS.md", "CLAUDE.md"}

// StateDir is the checkout's shhh directory, ContextFile the context file
// inside it — where `shhh init --project` writes and what a session reads
// first — and ConfigFile the settings a checkout keeps beside them, all
// relative to the checkout.
const (
	StateDir    = ".shhh"
	ContextFile = ".shhh/project.md"
	ConfigFile  = ".shhh/config.toml"
)

// Root is the directory a checkout's shhh state belongs to: the enclosing
// repository root; without one, the nearest ancestor that already holds a
// shhh directory; and the directory itself when there is neither. Everything
// keyed on "this project" — the backlog, an offer already refused — is
// keyed on it, which is what makes those the project's rather than a
// session's.
//
// The middle answer is what a project with no repository needs. Falling
// straight to the directory means two terminals opened two levels apart in
// one project key their state on two different directories, and the symptom
// is a backlog that is empty in one of them and full in the other, with
// nothing on screen to say why.
func Root(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	root, _ := projectRoot(abs)
	return root
}

// projectRoot is Root's answer with the half some callers need beside it:
// whether anything in the tree actually marked the boundary, or whether the
// directory itself was assumed for want of a marker. A caller that treats
// the assumed answer as a found one reads the enclosing directory as though
// it were part of this project.
func projectRoot(abs string) (root string, discovered bool) {
	repo, state := nearest(abs)
	switch {
	case repo != "":
		return repo, true
	case state != "":
		return state, true
	}
	return abs, false
}

// InRepo reports whether dir is inside a git working tree. It is the same
// walk Root makes, asked for the half of the answer a caller that must have
// a repository needs — a run that ends in a commit, and nothing else.
func InRepo(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	repo, _ := nearest(abs)
	return repo != ""
}

// nearest walks up from abs, itself included, for the closest ancestor
// holding .git and the closest holding the shhh state directory. A .git
// entry ends the walk, so a repository is never overruled by a state
// directory nearer the leaf: the two are found together only because the
// second answer is worth nothing until the first has come back empty.
//
// The state directory must be a directory. A checkout still holding the old
// single-file .shhh is a doctor migration, and reading it as a root would
// key a project on a file's parent for as long as the migration is unmade
// (docs/capabilities/configuration.md#a-migration-is-a-doctor-check).
func nearest(abs string) (repo, state string) {
	for probe := abs; ; {
		if _, err := os.Stat(filepath.Join(probe, ".git")); err == nil {
			return probe, state
		}
		if state == "" {
			if info, err := os.Stat(filepath.Join(probe, StateDir)); err == nil && info.IsDir() {
				state = probe
			}
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", state
		}
		probe = parent
	}
}

// FindFrom returns the path and contents of the nearest project-context
// file, walking up from dir. The path is what the start screen names: a
// session that says what it read is a session whose system prompt is not a
// secret.
//
// The directory is the caller's to state. Every caller has one — a session
// its working directory, a sub-agent its worktree — and reading the process
// here instead would make the answer depend on where the binary was started
// rather than on what it was asked about.
func FindFrom(dir string) (path, content string) {
	// A caller that could not name its directory has not named the root of
	// the walk either, and walking up from "" would read the process's
	// directory while claiming to have read somewhere stated.
	if dir == "" {
		return "", ""
	}
	for {
		if ins, ok := readInstruction(dir); ok {
			return ins.Path, ins.Text
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", ""
}

// InstructionNames lists the recognised instruction filenames as a surface
// says them, in precedence order. Both surfaces that name them — the start
// screen's context note and the doctor's project row — say the same sentence
// about a checkout that has written none, and a fourth name should reach the
// screen without a second edit remembering to put it there.
func InstructionNames() string {
	names := make([]string, 0, len(contextFilenames))
	for _, n := range contextFilenames {
		names = append(names, filepath.ToSlash(n))
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// FindContextFrom is FindFrom for the callers that want only the text.
func FindContextFrom(dir string) string {
	_, content := FindFrom(dir)
	return content
}

// Instruction is one instruction file read into the system prompt: where it
// was found, how a surface should print that path, and what it says.
type Instruction struct {
	// Path is absolute. Display is the same path as a reader should see it —
	// relative to the project root where it sits inside it, home-abbreviated
	// where it does not, which is what the user's own file looks like.
	Path    string
	Display string
	Text    string
}

// Instructions collects every instruction file a session standing in dir is
// told to obey, outermost first: the user's own file, then one file per
// directory from the project root down to dir itself.
//
// The order is the point of the list. A nested directory refines what the
// root said rather than repeating it, so the nearest file comes last and has
// the last word where two of them disagree — which is the order a model
// reading top to bottom takes them in. A single file, which is all this used
// to return, cannot express that: a monorepo whose root says how the build
// works and whose service directory says how that service differs had to
// pick one of the two to be read.
//
// user is the path of the user's own instructions file, or empty for none.
// That file is the user's own writing and is read wherever shhh runs; the
// checkout's files are the checkout's, and they carry exactly the trust the
// project context file already carried and nothing more
// (docs/capabilities/configuration.md#project-context-is-opt-in-and-lives-with-the-project).
//
// A `@path` line inside one of these files is text like any other line: no
// import is followed, and a file that expects one is read as what it says
// rather than as what it points at.
func Instructions(dir, user string) []Instruction {
	// A caller that could not name its directory has not named the root of
	// the walk either.
	if dir == "" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	// The walk ends at the directory this project's state is keyed on: the
	// repository root, or with no repository the nearest ancestor holding a
	// shhh directory. Above that is whatever encloses the project — a
	// directory of unrelated checkouts, or a home directory — and a file
	// there was not written about this one.
	root, discovered := projectRoot(abs)
	var found []Instruction
	if ins, ok := readInstructionFile(user); ok {
		found = append(found, ins)
	}
	found = append(found, projectInstructions(abs, root, discovered)...)
	// Paths are stated from the root, not from the working directory: with
	// the set collected root first, working-directory-relative paths would
	// print the outer ones as ../.. climbs, and the same file would be named
	// differently in two sessions opened at two depths of one checkout.
	for i := range found {
		found[i].Display = relativeTo(root, found[i].Path)
	}
	return found
}

// projectInstructions walks the checkout from abs up to stop and returns what
// it found in the order the prompt states it, root first. discovered says
// whether stop is a boundary something in the tree marked or one assumed for
// want of any marker, which is the only thing that licenses reading above it.
func projectInstructions(abs, stop string, discovered bool) []Instruction {
	var found []Instruction
	for probe := abs; ; {
		if ins, ok := readInstruction(probe); ok {
			found = append(found, ins)
		}
		if probe == stop {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	// Nothing inside the project says anything, and nothing in the tree
	// marked where the project begins: with neither a repository nor a shhh
	// directory the walk stopped at the working directory, which is a guess.
	// The nearest file above it is still the answer, the way it was when a
	// single file was the whole answer — a project told to look no further
	// than a directory that says nothing is a project read as bare.
	//
	// A boundary something did mark is not reopened here, however little was
	// found inside it. Climbing past a repository root that simply has no
	// instruction file would put a sibling checkout's AGENTS.md, or the one
	// sitting in a home directory, into this project's system prompt.
	if !discovered && len(found) == 0 {
		if path, text := FindFrom(filepath.Dir(stop)); path != "" {
			found = append(found, Instruction{Path: path, Text: text})
		}
	}
	slices.Reverse(found)
	return found
}

// readInstruction reads the one file a directory contributes: the first
// recognised name that is there and says something.
func readInstruction(dir string) (Instruction, bool) {
	for _, name := range contextFilenames {
		if ins, ok := readInstructionFile(filepath.Join(dir, name)); ok {
			return ins, true
		}
	}
	return Instruction{}, false
}

// readInstructionFile reads one path, refusing a file with nothing in it. An
// empty file is stepped over rather than taken, because taking it would put
// a heading in the prompt with nothing under it and hide the next name in
// the same directory behind it.
func readInstructionFile(path string) (Instruction, bool) {
	if path == "" {
		return Instruction{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return Instruction{}, false
	}
	return Instruction{Path: path, Text: string(data)}, true
}

// instructionPreamble tells the model what the section under it is and how to
// read a disagreement between two files in it. It states no path: every file
// is named by its own heading, and naming the set twice invites the two to
// drift apart.
const instructionPreamble = "# Project instructions\n" +
	"These files were written for whoever works in this project, and they are yours to follow. " +
	"They are listed outermost first, so the last one is the nearest to the working directory and has the last word wherever two of them disagree."

// InstructionBlock renders collected instruction files as one system-prompt
// section: each file's text under a heading naming its path, in the order
// Instructions returned them.
//
// budget bounds the files' bytes; a budget of zero or less is no bound at
// all. Over it the outermost files are cut first,
// because the nearest file is the one describing the directory the session
// was opened in, and every cut is stated in the heading above it. A silent
// cut leaves a model following half an instruction with nothing to say the
// other half was ever there — and the half it loses is the end of the file,
// which is where a document that leads with its shape keeps its rules.
// See docs/capabilities/configuration.md#project-context-is-opt-in-and-lives-with-the-project.
func InstructionBlock(files []Instruction, budget int) string {
	if len(files) == 0 {
		return ""
	}
	total := 0
	for _, f := range files {
		total += len(f.Text)
	}
	over := 0
	if budget > 0 && total > budget {
		over = total - budget
	}

	var b strings.Builder
	b.WriteString(instructionPreamble)
	if over > 0 {
		fmt.Fprintf(&b, " They came to %d bytes against a budget of %d, so they were cut from the outermost inwards; a heading below says so wherever its file was cut.", total, budget)
	}
	for _, f := range files {
		text, cut := f.Text, false
		if over > 0 {
			room := len(text) - over
			if room < 0 {
				room = 0
			}
			over -= len(text) - room
			text, cut = trimToLine(text, room), true
		}
		// The trailing newline every text file ends with goes before the next
		// heading, and dropping it is a layout decision rather than a cut.
		// Deciding "was this cut" by comparing lengths after it would report
		// a truncation for every whole file, which is a prompt telling the
		// model its own instructions are incomplete when they are not.
		text = strings.TrimRight(text, "\n")
		b.WriteString("\n\n## " + f.Display)
		switch {
		case cut && text == "":
			fmt.Fprintf(&b, "\nNot read: none of its %d bytes fit in what was left of the budget.", len(f.Text))
		case cut:
			fmt.Fprintf(&b, "\nCut to fit the budget — this is the first %d of %d bytes.\n%s", len(text), len(f.Text), text)
		default:
			b.WriteString("\n" + text)
		}
	}
	return b.String()
}

// trimToLine cuts s to at most n bytes, back to the end of the last whole
// line that fits. Cutting mid-line hands the model half a sentence and, in a
// list, half an instruction that still reads as a whole one; cutting
// mid-rune hands it a byte no decoder accepts, which is what the last
// fallback is for.
func trimToLine(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n <= 0 {
		return ""
	}
	s = s[:n]
	if i := strings.LastIndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return strings.ToValidUTF8(s, "")
}
