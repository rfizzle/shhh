package receipt

import (
	"testing"

	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/tools"
)

// An edit's receipt carries the head of its change — the file, the first
// line it changed and the whole edit's count — read from the diff the
// caller holds and never from the tool's sentence about it.
func TestReceipt_AnEditCarriesItsHunkHead(t *testing.T) {
	added := []diff.Hunk{
		{OldStart: 40, NewStart: 40, Lines: []diff.Line{
			{Kind: diff.Context, Text: "switch k {", OldNo: 40, NewNo: 40},
			{Kind: diff.Add, Text: `case "c":`, NewNo: 41},
			{Kind: diff.Add, Text: "m.copy()", NewNo: 42},
		}},
		{OldStart: 90, NewStart: 92, Lines: []diff.Line{
			{Kind: diff.Del, Text: "old()", OldNo: 90},
			{Kind: diff.Add, Text: "new()", NewNo: 92},
		}},
	}
	removed := []diff.Hunk{{OldStart: 7, NewStart: 7, Lines: []diff.Line{
		{Kind: diff.Context, Text: "a", OldNo: 6, NewNo: 6},
		{Kind: diff.Del, Text: "gone", OldNo: 7},
	}}}
	modeOnly := []diff.Hunk{{OldStart: 1, NewStart: 1, Lines: []diff.Line{{Kind: diff.Context, Text: "a", OldNo: 1, NewNo: 1}}}}

	cases := []struct {
		name   string
		call   Call
		want   *HunkHead
		marked string
	}{
		{"the first changed line, past the context, and every hunk's count",
			Call{Name: tools.EditFileName, Args: `{"path":"internal/ui/keys.go"}`, Result: "edited 1 file", Hunks: added},
			&HunkHead{Path: "internal/ui/keys.go", Line: 41, Change: diff.Add, Text: `case "c":`, Added: 3, Removed: 1},
			`+ case "c":`},
		{"a removed line is numbered on the old side",
			Call{Name: tools.EditFileName, Args: `{"path":"a.go"}`, Hunks: removed},
			&HunkHead{Path: "a.go", Line: 7, Change: diff.Del, Text: "gone", Removed: 1},
			"- gone"},
		{"a caller holding the change and not the arguments names the file",
			Call{Name: tools.WriteFileName, Path: "b.go", Hunks: removed},
			&HunkHead{Path: "b.go", Line: 7, Change: diff.Del, Text: "gone", Removed: 1},
			"- gone"},
		{"a change with no changed line has no head",
			Call{Name: tools.EditFileName, Args: `{"path":"a.sh"}`, Hunks: modeOnly}, nil, ""},
		{"the result is never read for one",
			Call{Name: tools.EditFileName, Args: `{"path":"a.go"}`, Result: "@@ -1 +1 @@\n-a\n+b"}, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Build(tc.call).Hunk
			switch {
			case got == nil || tc.want == nil:
				if got != tc.want {
					t.Errorf("head %+v, want %+v", got, tc.want)
				}
			case *got != *tc.want || got.Marked() != tc.marked:
				t.Errorf("head %+v marked %q, want %+v %q", *got, got.Marked(), *tc.want, tc.marked)
			}
		})
	}
}
