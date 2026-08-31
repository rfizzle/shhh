package report

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// Every primitive is exercised at eighty columns and again at sixty, because
// the whole point of a width is what it does to a row that no longer fits.
func TestRender_EveryPrimitiveAtTwoWidths(t *testing.T) {
	r := everything()
	for _, width := range []int{80, 60} {
		got := r.Render(width)
		for _, want := range []string{
			"shhh example — 2 sources · 1 profile",
			"SOURCES",
			"✓ openai", "✗ anthropic", "⊘ corp-gw",
			"[unusable]",
			"base url:",
			"⚠ providers.toml could not be read",
			"1 failed · 2 passed",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("at w%d the report does not carry %q:\n%s", width, want, got)
			}
		}
		for _, line := range strings.Split(got, "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("at w%d a line is %d columns wide: %q", width, w, line)
			}
		}
	}
}

// Every glyph in the vocabulary is one column. The rule is checkable rather
// than aspirational: a wide glyph arriving would shift every target in every
// listing by a column, and a row is laid out on the assumption that the marks
// are the doctor's own.
func TestState_EveryGlyphIsOneColumn(t *testing.T) {
	seen := map[string]State{}
	for _, s := range []State{Pass, Warn, Fail, Skip, Run, Queue} {
		g := s.Glyph()
		if w := lipgloss.Width(g); w != 1 {
			t.Errorf("the glyph for state %d (%q) is %d columns", s, g, w)
		}
		if other, dup := seen[g]; dup {
			t.Errorf("states %d and %d share the glyph %q", other, s, g)
		}
		seen[g] = s
	}
}

// A row that cannot fit gives up its target and keeps its outcome: the
// outcome is the reason the row is being read.
func TestRow_ClipsTheTargetAndNeverTheOutcome(t *testing.T) {
	r := Report{Sections: []Section{{Rows: []Row{{
		State:   Fail,
		Name:    "endpoint",
		Subject: strings.Repeat("a very long subject ", 10),
		Outcome: "unreachable",
	}}}}}
	got := r.Render(50)
	if !strings.Contains(got, "[unreachable]") {
		t.Fatalf("the outcome was clipped away:\n%s", got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("the target was not clipped:\n%s", got)
	}
	if w := lipgloss.Width(strings.Split(got, "\n")[0]); w > 50 {
		t.Fatalf("the row is %d columns wide: %q", w, got)
	}
}

// A name wider than its column pushes its own target right rather than being
// cut: the column is a floor the targets line up on, and a clipped name is
// the one field a reader cannot reconstruct.
func TestRow_AWideNameKeepsEveryCharacter(t *testing.T) {
	got := Report{Sections: []Section{{NameWidth: 8, Rows: []Row{
		{State: Pass, Name: "short", Subject: "a"},
		{State: Pass, Name: "a-name-far-wider-than-the-column", Subject: "b"},
	}}}}.Render(80)
	if !strings.Contains(got, "a-name-far-wider-than-the-column  b") {
		t.Fatalf("the name was clipped to the column:\n%s", got)
	}
}

// A section sizes its name column to its own longest name, capped, so one
// wide vocabulary does not push every other section's targets across the
// screen.
func TestSection_SizesItsOwnNameColumn(t *testing.T) {
	got := Report{Sections: []Section{
		{Header: "SHORT", Rows: []Row{{State: Pass, Name: "a", Subject: "x"}}},
		{Header: "LONG", Rows: []Row{{State: Pass, Name: "aaaaaaaaaaaa", Subject: "y"}}},
	}}.Render(80)
	if !strings.Contains(got, "✓ a  x") {
		t.Fatalf("the short section borrowed the long one's column:\n%s", got)
	}
	if !strings.Contains(got, "✓ aaaaaaaaaaaa  y") {
		t.Fatalf("the long section's column is wrong:\n%s", got)
	}
}

// An empty state is what is absent and one way out, in the same row shape
// every listing draws.
func TestEmpty_NamesWhatIsAbsentAndOneWayOut(t *testing.T) {
	got := Report{Sections: []Section{{Rows: []Row{
		Empty("no history yet", "run `shhh <prompt>` to record one"),
	}}}}.Render(80)
	if got != "  ⊘ no history yet · run `shhh <prompt>` to record one" {
		t.Fatalf("empty state = %q", got)
	}
}

// A confirmation is the verb and the thing it happened to, and nothing else.
func TestDone_IsTheVerbAndTheThing(t *testing.T) {
	if got := (Report{Sections: []Section{{Rows: []Row{Done("saved snippet", "ports")}}}}).Render(80); got != "  ✓ saved snippet ports" {
		t.Fatalf("confirmation = %q", got)
	}
}

// A note wraps where a row clips: a diagnostic cut short is a diagnostic
// nobody can act on.
func TestNote_WrapsRatherThanClips(t *testing.T) {
	long := "the file at /etc/shhh/providers.toml declares an endpoint with no base_url, which every request to it would need"
	got := Report{Notes: []Note{{State: Warn, Text: long}}}.Render(60)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("the note did not wrap:\n%s", got)
	}
	if !strings.HasPrefix(lines[0], "  ⚠ ") || !strings.HasPrefix(lines[1], "    ") {
		t.Fatalf("continuation lines sit under the first:\n%s", got)
	}
	if !strings.Contains(got, "base_url") {
		t.Fatalf("the note lost its end:\n%s", got)
	}
}

// A pipe, a dumb terminal and NO_COLOR all get the bytes Render produces:
// there is one plain rendering and colour is added to it, never a
// downsampling of it (docs/interface/principles.md#colour-never-carries-meaning-alone).
func TestFprint_IsByteIdenticalToRenderAndCarriesNoEscapes(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	if err := Fprint(&buf, everything()); err != nil {
		t.Fatal(err)
	}
	if want := everything().Render(FallbackWidth) + "\n"; buf.String() != want {
		t.Fatalf("a NO_COLOR stream is not the plain render:\n%q\n%q", buf.String(), want)
	}
	if strings.Contains(buf.String(), "\x1b") {
		t.Fatalf("the output carries an escape code: %q", buf.String())
	}
}

// A destination with no width to give takes the fallback rather than
// whatever the terminal on the other side of the process happens to be.
func TestWidth_FallsBackWhereThereIsNothingToMeasure(t *testing.T) {
	if got := Width(&bytes.Buffer{}); got != FallbackWidth {
		t.Fatalf("a buffer should measure %d, got %d", FallbackWidth, got)
	}
	if got := Width(os.Stdin); got <= 0 {
		t.Fatalf("a stream that cannot be sized should still be positive, got %d", got)
	}
}

// everything is a report using every primitive at once, so one fixture
// covers the title, sections, rows, key/value blocks, notes and the tally.
func everything() Report {
	return Report{
		Title:   "shhh example",
		Subject: "2 sources · 1 profile",
		Sections: []Section{
			{Header: "SOURCES", Rows: []Row{
				{State: Pass, Name: "openai", Subject: "key in env", Detail: "OPENAI_API_KEY"},
				{State: Fail, Name: "anthropic", Subject: "no key", Outcome: "unusable",
					Consequence: "a session that picks this provider stops before its first turn",
					Fix:         []string{"export ANTHROPIC_API_KEY=…"}},
				{State: Skip, Name: "corp-gw", Subject: "CORP_GW_KEY unset"},
			}},
			{Header: "PROFILE corp-gw", Pairs: []Pair{
				{Key: "base url", Value: "gw.example.com/v1"},
				{Key: "auth", Value: "CORP_GW_KEY"},
			}},
		},
		Notes: []Note{{State: Warn, Text: "providers.toml could not be read"}},
		Tally: "1 failed · 2 passed",
	}
}
