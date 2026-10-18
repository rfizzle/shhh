package components

import (
	"reflect"
	"testing"
)

func TestWrap_BreaksBetweenWordsAndLeavesALongWordWhole(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		width int
		want  []string
	}{
		{"fits", "one two", 10, []string{"one two"}},
		{"breaks between words", "one two three", 7, []string{"one two", "three"}},
		{"a long word is not cut", "a internal/ui/components b", 8, []string{"a", "internal/ui/components", "b"}},
		{"a hyphen is not a break", "ports 8000-9999 open", 11, []string{"ports", "8000-9999", "open"}},
		{"no words is one empty row", "   ", 8, []string{""}},
		{"no room leaves the text", "a b", 0, []string{"a b"}},
	}
	for _, c := range cases {
		if got := Wrap(c.text, c.width); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Wrap(%q, %d) = %q, want %q", c.name, c.text, c.width, got, c.want)
		}
	}
}

func TestWrapHang_TheFirstRowIsNarrowerThanTheRest(t *testing.T) {
	got := WrapHang("alpha beta gamma delta", 10, 16)
	want := []string{"alpha beta", "gamma delta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WrapHang = %q, want %q", got, want)
	}
	// A first word that does not fit beside the head opens the next row.
	got = WrapHang("unbreakable x", 4, 20)
	want = []string{"", "unbreakable x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WrapHang (no room beside the head) = %q, want %q", got, want)
	}
}

func TestWrapParagraphs_KeepsTheBreaksTheTextHas(t *testing.T) {
	got := WrapParagraphs("one two three\n\nfour", 7)
	want := []string{"one two", "three", "", "four"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WrapParagraphs = %q, want %q", got, want)
	}
}

func TestClipLeft_KeepsTheTailAndMarksTheCut(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"internal/ui/card.go", 19, "internal/ui/card.go"},
		{"internal/ui/card.go", 9, "…/card.go"},
		{"internal/ui/card.go", 1, "…"},
		{"internal/ui/card.go", 0, ""},
	}
	for _, c := range cases {
		if got := ClipLeft(c.in, c.width); got != c.want {
			t.Errorf("ClipLeft(%q, %d) = %q, want %q", c.in, c.width, got, c.want)
		}
	}
}
