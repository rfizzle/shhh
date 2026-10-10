package config

// The reference section of the configuration document, written from the
// table rather than by hand. A default stated in prose and a default in the
// code are two places to be wrong, and the one that goes stale is the prose,
// because nothing fails when it does. This makes the failure a test.
//
// Only the region between the two markers is generated. Everything around it
// — what the section is for, the sentence about the resolution order — is a
// person's, and a regeneration leaves it alone.

import (
	"fmt"
	"os"
	"strings"
)

// The lines that bracket the generated region. They are HTML comments so a
// rendered page does not show them, and they say what to run rather than
// only "do not edit": a reader who has already edited inside the region needs
// to know how to get their change into the table.
const (
	referenceBegin = "<!-- BEGIN generated settings reference — written by `make docs` from the settings table; edit the table, not this. -->"
	referenceEnd   = "<!-- END generated settings reference -->"

	refusedBegin = "<!-- BEGIN generated refused keys — written by `make docs` from projectRefusals in internal/config/project.go; edit the list, not this. -->"
	refusedEnd   = "<!-- END generated refused keys -->"
)

// Reference is the settings reference: one table per section of the file, in
// the file's own order, with what each key takes, what stands when nothing
// sets it, when a running session takes a new value, and what it decides.
func Reference() string {
	var b strings.Builder
	b.WriteString(referenceBegin + "\n")
	group := ""
	for _, s := range settings {
		if g := s.Group(); g != group {
			group = g
			fmt.Fprintf(&b, "\n**`[%s]`**\n\n", group)
			b.WriteString("| Key | Takes | Default | A session takes it | What it decides |\n")
			b.WriteString("|---|---|---|---|---|\n")
		}
		// The wildcard is `*` in the table because that is what a key match
		// is against; in a document it is the word a person writes there.
		key := strings.TrimPrefix(s.shown(), group+".")
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n",
			key, cell(takes(s)), cell(quoteDefault(s.Default)), s.Taken, cell(decides(s)))
	}
	b.WriteString("\n" + referenceEnd)
	return b.String()
}

// cell escapes what would end a column early. Nothing in the table holds a
// pipe today; a description that grew one would silently shift every column
// after it rightwards, which is the kind of breakage a generated document
// makes nobody look at.
func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// takes is the second column: the shape, and for a word the words themselves.
func takes(s Setting) string {
	if len(s.Values) == 0 {
		return s.Kind.String()
	}
	quoted := make([]string, len(s.Values))
	for i, v := range s.Values {
		quoted[i] = "`" + v + "`"
	}
	return s.Kind.String() + ": " + strings.Join(quoted, ", ")
}

// quoteDefault marks up a default that is a value a person could type and
// leaves one that is a sentence alone, so `1h` reads as something to write in
// the file and `on when a summary model is set` reads as what happens.
func quoteDefault(d string) string {
	if strings.ContainsAny(d, " (") {
		return d
	}
	return "`" + d + "`"
}

// decides is the last column: the sentence, and for the keys with a rank above
// the file, what outranks it. A reader looking up why a value is not in force
// is looking at this row, so every combination that names something has a
// branch — a key with a flag and no variable is still a key the command line
// answers for, and a row that left that out would send the reader to the file.
func decides(s Setting) string {
	out := s.Desc
	switch {
	case s.Flag != "" && s.Env != "":
		out += fmt.Sprintf(" `%s` and `%s` are read ahead of the file.", s.Flag, s.Env)
	case s.Flag != "":
		out += fmt.Sprintf(" `%s` is read ahead of the file.", s.Flag)
	case s.Env != "":
		out += fmt.Sprintf(" `%s` is read ahead of the file.", s.Env)
	}
	if s.Secret {
		out += " It is a credential: the listing says whether it is set, never what it is."
	}
	return out
}

// ReferenceIn is the document with the generated region replaced, and whether
// that changed anything. A document with no markers is an error rather than a
// silent append: the region has to sit where the section around it explains
// it, and a generator that guessed would put a table under the wrong heading.
func ReferenceIn(doc string) (string, bool, error) {
	i := strings.Index(doc, referenceBegin)
	j := strings.Index(doc, referenceEnd)
	if i < 0 || j < i {
		return "", false, fmt.Errorf("the settings reference markers are not in the document")
	}
	out := doc[:i] + Reference() + doc[j+len(referenceEnd):]
	return out, out != doc, nil
}

// WriteReference rewrites the generated region of the document at path and
// reports whether it had drifted. `make docs` calls it to write; the test
// calls the same function and fails on a change it had to make, so the doc
// and the table cannot disagree about a default for longer than one run of
// the suite.
func WriteReference(path string, write bool) (stale bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out, changed, err := ReferenceIn(string(raw))
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return false, nil
	}
	if !write {
		return true, nil
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	return true, os.WriteFile(path, []byte(out), mode)
}

// RefusedTable is the table of keys a checkout may not set, written from the
// list the loader refuses by. Keys that share a reason share a row, in the
// order the list first gives them. A key that is itself a setting is written
// as the key; one that only heads a table, or a file the checkout keeps
// instead, is written as the table.
func RefusedTable() string {
	var b strings.Builder
	b.WriteString(refusedBegin + "\n\n")
	b.WriteString("| Key | Why not |\n|---|---|\n")
	var reasons []string
	keys := map[string][]string{}
	for _, r := range projectRefusals {
		if _, ok := keys[r.Reason]; !ok {
			reasons = append(reasons, r.Reason)
		}
		shown := "`" + r.Key + "`"
		if !isSetting(r.Key) {
			shown = "`[" + r.Key + "]`"
		}
		keys[r.Reason] = append(keys[r.Reason], shown)
	}
	for _, reason := range reasons {
		fmt.Fprintf(&b, "| %s | %s |\n", cell(strings.Join(keys[reason], ", ")), cell(reason))
	}
	b.WriteString("\n" + refusedEnd)
	return b.String()
}

func isSetting(key string) bool {
	for _, s := range settings {
		if s.Key == key {
			return true
		}
	}
	return false
}

// RefusedIn is the document with the generated refused-keys region replaced.
func RefusedIn(doc string) (string, bool, error) {
	i := strings.Index(doc, refusedBegin)
	j := strings.Index(doc, refusedEnd)
	if i < 0 || j < i {
		return "", false, fmt.Errorf("the refused keys markers are not in the document")
	}
	out := doc[:i] + RefusedTable() + doc[j+len(refusedEnd):]
	return out, out != doc, nil
}

// WriteRefused is WriteReference for the refused-keys table.
func WriteRefused(path string, write bool) (stale bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out, changed, err := RefusedIn(string(raw))
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return false, nil
	}
	if !write {
		return true, nil
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	return true, os.WriteFile(path, []byte(out), mode)
}
