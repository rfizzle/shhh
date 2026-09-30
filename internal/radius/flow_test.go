package radius

import (
	"strings"
	"testing"
)

// The blast-radius reading takes the verb behind a shell flow word, as the
// safety table and the destruction reading do: a delete behind `then` is a
// delete of what it names, and the words that open and close the construct
// leave nothing unaccounted for.
func TestOutline_AFlowWordIsReadPast(t *testing.T) {
	cases := []struct {
		command string
		want    []string
	}{
		{"if true; then rm -rf build; fi", []string{"build"}},
		{"if false; then :; else mkdir out; fi", []string{"out"}},
		{"while false; do touch stamp; done", []string{"stamp"}},
		{"! rm -rf dist", []string{"dist"}},
		{"{ rm -rf dist; }", []string{"dist"}},
	}
	for _, c := range cases {
		got := Outline(c.command)
		if strings.Join(paths(got), ",") != strings.Join(c.want, ",") {
			t.Errorf("Outline(%q) writes = %v, want %v", c.command, paths(got), c.want)
		}
		if len(got.Unresolved) > 0 {
			t.Errorf("Outline(%q) left %q unaccounted for", c.command, got.Unresolved)
		}
	}
	if got := Outline("if true; then rm -rf build; fi"); !got.Recursive {
		t.Errorf("a recursive delete behind then was not read as one: %+v", got)
	}
}

// The destruction reading cuts a command at the parentheses between commands,
// as the safety table does, and leaves a substitution's and a quoted one
// alone.
func TestParens(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"case x in x) rm -rf /", []string{"case x in x", " rm -rf /"}},
		{"if(rm -rf /)", []string{"if", "rm -rf /", ""}},
		{"rm -rf $(pwd)/x", []string{"rm -rf $(pwd)/x"}},
		{"echo $((1 + (2)))", []string{"echo $((1 + (2)))"}},
		{`echo "a)b" 'c(d'`, []string{`echo "a)b" 'c(d'`}},
		{`find . \( -name a \) -delete`, []string{`find . \( -name a \) -delete`}},
	}
	for _, c := range cases {
		if got := parens(c.text); strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("parens(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}
