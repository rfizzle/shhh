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
// first — ConfigFile the settings a checkout keeps beside them, HooksFile
// the commands it asks a session to run at its own seams, PromptsDir the
// wordings it replaces shhh's own with, and TodoProfileDir the profile its
// backlog is written in and worked under, all relative to the checkout.
//
// The wordings are a directory of files by convention rather than keys,
// because the settings file may not point at a file anywhere on the machine
// and a checkout's wording has to live in the checkout to travel with it.
// The profile is at a fixed path for the same reason, and a checkout has at
// most one: a name would be a second thing to keep in step with the
// settings, and the file is right there.
const (
	StateDir       = ".shhh"
	ContextFile    = ".shhh/project.md"
	ConfigFile     = ".shhh/config.toml"
	HooksFile      = ".shhh/hooks.json"
	PromptsDir     = ".shhh/prompts"
	TodoProfileDir = ".shhh/todo/profile"
)

// Root is the directory a checkout's shhh state belongs to: the nearest
// ancestor that already holds a shhh directory; without one, the enclosing
// repository root; and the directory itself when there is neither.
// Everything keyed on "this project" — the backlog, an offer already
// refused — is keyed on it, which is what makes those the project's rather
// than a session's.
//
// The shhh directory comes first because it is the one marker somebody put
// there to say where shhh's state goes. A repository is a fact about the
// code, and it is the right boundary only for the parts of shhh that are
// about code — a run that ends in a commit asks InRepo for that answer
// directly. A monorepo whose services each keep their own shhh directory
// has as many projects as it has of those, and reading the repository root
// over them would give every service one backlog.
//
// The last answer is what a directory with neither marker gets, and it is a
// guess rather than a boundary: a caller that must not guess asks RootFound.
func Root(dir string) string {
	root, _ := RootFound(dir)
	return root
}

// RootFound is Root with the half some callers need beside it: whether
// anything in the tree actually marked the boundary, or whether the
// directory itself was assumed for want of a marker. A caller that treats
// the assumed answer as a found one reads the enclosing directory as though
// it were part of this project.
func RootFound(dir string) (root string, found bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir, false
	}
	return projectRoot(abs)
}

// projectRoot is RootFound over an absolute path.
func projectRoot(abs string) (root string, discovered bool) {
	repo, state := nearest(abs)
	switch {
	case state != "":
		return state, true
	case repo != "":
		return repo, true
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
// entry ends the walk, so a shhh directory above a repository root is never
// read as this project's: it belongs to whatever encloses the checkout, and
// a clone dropped inside somebody's own shhh-managed directory would
// otherwise take that directory's backlog.
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
// other half was ever there.
//
// What a cut file keeps is its head and its end — whole sections from the
// end wherever they fit — with the middle gone and a note saying so where it
// went. A document that leads with its shape keeps its rules at the end, so
// a cut that only kept the head dropped exactly the sections a reader gets
// corrected on — the gotchas, the testing rules, the conventions — while
// keeping the overview it could have guessed.
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
		text, cut, middle := f.Text, false, 0
		if over > 0 {
			room := len(text) - over
			if room < 0 {
				room = 0
			}
			over -= len(text) - room
			text, middle = cutToFit(text, room)
			cut = true
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
		case cut && middle > 0:
			fmt.Fprintf(&b, "\nCut to fit the budget — %d of its %d bytes: its head and its end, with %d bytes dropped from the middle where the note below stands.\n%s",
				len(f.Text)-middle, len(f.Text), middle, text)
		case cut:
			fmt.Fprintf(&b, "\nCut to fit the budget — this is the first %d of %d bytes.\n%s", len(text), len(f.Text), text)
		default:
			b.WriteString("\n" + text)
		}
	}
	return b.String()
}

// cutNoticeBudget is the room cutToFit holds back for the note it puts at
// the cut. The note is part of the file's allowance rather than an extra on
// top of it: the budget is a bound on what reaches the model, and a bound
// that quietly grows by a line per cut file is not one.
const cutNoticeBudget = 160

// cutNotice is what stands where the middle of a file was. Without it the
// head and the tail read as one continuous document, and a model told to
// follow the section it is looking at cannot tell that the section before it
// was ever there. It names where the text picks up again so the gap can be
// closed by reading the file, which the model can do.
func cutNotice(dropped int, resumes string) string {
	note := fmt.Sprintf("\n\n[%d bytes cut from the middle of this file to fit the budget. It resumes %s.]\n\n",
		dropped, resumes)
	if len(note) > cutNoticeBudget {
		// A heading long or strange enough to overrun the reservation loses
		// its half of the sentence rather than the cut losing its bound.
		note = fmt.Sprintf("\n\n[%d bytes cut from the middle of this file to fit the budget.]\n\n", dropped)
	}
	return note
}

// cutToFit reduces s to at most n bytes and reports how many it dropped from
// the middle — zero when it could only cut the head off.
//
// It keeps the end of the file in half of what is left and fills the rest
// with the head. Whole sections wherever they fit, because half a section
// read out of order is worse than no section: it has no heading to say what
// it governs, and its first sentence usually depends on the one above.
func cutToFit(s string, n int) (string, int) {
	if n >= len(s) {
		return s, 0
	}
	if n <= 0 {
		return "", 0
	}
	tail, resumes := lastSections(s, n/2)
	head := trimToLine(s, n-len(tail)-cutNoticeBudget)
	// Neither half is worth having alone: a tail with no head starts the
	// file in the middle of nowhere, and a head that leaves no room for the
	// note is the plain cut this is a refinement of.
	if tail == "" || head == "" {
		return trimToLine(s, n), 0
	}
	dropped := len(s) - len(head) - len(tail)
	return head + cutNotice(dropped, resumes) + tail, dropped
}

// lastSections returns the longest suffix of s that fits in max bytes, and a
// phrase saying where that suffix starts.
//
// It prefers a suffix that begins at a heading, at whatever depth: the
// longest one that fits, since the tail is where a document that leads with
// its shape keeps its rules, and a subsection the note names is not read as
// the section above it. A file whose last section is itself larger than max
// — this repository's own is — would otherwise keep nothing from the end at
// all, so it falls back to whole lines and the note says the text resumes
// part-way through.
func lastSections(s string, max int) (tail, resumes string) {
	heads := headingOffsets(s)
	for _, off := range heads {
		if len(s)-off <= max {
			return s[off:], fmt.Sprintf("at %q", headingText(s[off:]))
		}
	}
	tail = tailToLine(s, max)
	if tail == "" {
		return "", ""
	}
	under := "the file"
	for _, off := range heads {
		if off < len(s)-len(tail) {
			under = fmt.Sprintf("%q", headingText(s[off:]))
		}
	}
	return tail, "part-way through " + under
}

// tailToLine is trimToLine from the other end: at most n bytes of s, forward
// to the start of the first whole line that fits.
func tailToLine(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n <= 0 {
		return ""
	}
	s = s[len(s)-n:]
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
		return s[i+1:]
	}
	return ""
}

// headingOffsets is the offset of every ATX heading line in s, in order.
//
// A heading inside a fenced code block is not one. Instruction files are full
// of shell examples, and a comment line in one would otherwise be taken for a
// section boundary — which would resume the text in the middle of a code
// block, with its fence opened above the cut and never closed.
//
// A fence is closed by the marker that opened it, which is what Markdown
// does: the other marker inside an open block is content, and treating it as
// a close would reopen the document in the middle of an example.
func headingOffsets(s string) []int {
	var out []int
	fence := ""
	for off := 0; off < len(s); {
		line, next := s[off:], len(s)
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line, next = line[:i], off+i+1
		}
		trimmed := strings.TrimSpace(line)
		switch marker := fenceMarker(trimmed); {
		case fence == "" && marker != "":
			fence = marker
		case fence != "" && marker == fence:
			fence = ""
		case fence == "" && headingLevel(line) > 0:
			out = append(out, off)
		}
		off = next
	}
	return out
}

// fenceMarker is the fence a line opens or closes, or empty for an ordinary
// line.
func fenceMarker(trimmed string) string {
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return "```"
	case strings.HasPrefix(trimmed, "~~~"):
		return "~~~"
	}
	return ""
}

// headingLevel is the ATX heading level of a line, or zero for anything else.
// The hashes must start the line and be followed by a space: an indented one
// is inside a list or a code block, and `#comment` is not a heading.
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}

// headingText is the first line of s, capped so that the note quoting it
// stays inside cutNoticeBudget however long the heading is.
func headingText(s string) string {
	line := s
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if len(line) > 60 {
		line = strings.ToValidUTF8(line[:60], "") + "…"
	}
	return line
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
