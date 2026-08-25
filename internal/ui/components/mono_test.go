package components

// The mono conformance walk (S-095). DESIGN-TUI.md's first invariant —
// colour never carries meaning alone — is enforced here rather than asserted
// in prose: every surface renders each of its states with the mono palette
// on, the ANSI is stripped off, and the resulting plain texts must all
// differ. Two states that were only ever a hue apart collapse to the same
// string and the surface fails.

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/rfizzle/shhh/internal/diff"
)

// monoOn turns the mono palette on for one test and restores what was there.
func monoOn(t *testing.T) {
	t.Helper()
	was := mono
	SetMono(true)
	t.Cleanup(func() { SetMono(was) })
}

// withColorProfile forces lipgloss to actually emit ANSI, which it does not do
// for a test binary's non-terminal stdout. The palette assertions need the
// escape codes to be there to check them.
func withColorProfile(t *testing.T, p termenv.Profile) {
	t.Helper()
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(p)
	t.Cleanup(func() { lipgloss.SetColorProfile(was) })
}

type monoState struct {
	name string
	view string
}

type monoSurface struct {
	name   string
	states []monoState
}

// monoFixtures renders every surface's states with the current palette. The
// fixtures deliberately hold everything else constant — same verb, same
// target, same label — so that the only thing left to tell two states apart
// is what the state itself contributes.
func monoFixtures() []monoSurface {
	const w = 72

	row := func(mut func(*ActivityRow)) string {
		r := ActivityRow{Kind: ActivityTool, Verb: "read", Target: "internal/agent/loop.go"}
		mut(&r)
		return r.View(w)
	}

	hunk := func(kind diff.Kind) []diff.Hunk {
		return []diff.Hunk{{
			OldStart: 1, OldCount: 1, NewStart: 1, NewCount: 1,
			Lines: []diff.Line{{Kind: kind, Text: "value := compute()", OldNo: 1, NewNo: 1}},
		}}
	}
	diffLine := func(kind diff.Kind) string {
		return strings.Join(UnifiedLines(hunk(kind), w, UnifiedOpts{LineNumbers: true, Emphasis: true}), "\n")
	}

	card := func(mut func(*ApprovalCard)) string {
		c := ApprovalCard{
			Variant:  ApprovalCommand,
			Title:    "Approve command",
			Headline: "Assistant wants to run: go test ./...",
			Question: "Run this command?",
		}
		mut(&c)
		return c.View(w)
	}

	agents := func(state AgentState, status string) string {
		l := AgentList{Rows: []AgentRow{{State: state, Name: "writer-1", Task: "docs", Status: status}}}
		return l.View(w)
	}

	staged := func(checked bool) string {
		s := NewMultiSelect("Stage files", []SelectOption{{Label: "internal/agent/loop.go"}})
		s.Checked[0] = checked
		return s.View(w)
	}

	meter := func(pct int) string {
		return Meter{Pct: pct, Cells: MeterCellsVitals, Tone: MeterPressure, Label: "ctx"}.View()
	}

	return []monoSurface{
		{"cockpit mode segment", []monoState{
			// The mode word is held constant on purpose: the glyph has to
			// carry the difference on its own.
			{"permissive", Cockpit{Mode: "mode", ModeKind: CockpitPermissive, CtxPct: -1}.View(w)},
			{"gated", Cockpit{Mode: "mode", ModeKind: CockpitGated, CtxPct: -1}.View(w)},
			{"checking", Cockpit{Mode: "mode", ModeKind: CockpitChecking, CtxPct: -1}.View(w)},
		}},
		{"activity row state", []monoState{
			{"done", row(func(r *ActivityRow) { r.Outcome = OutcomeOK; r.Duration = "1.1s" })},
			{"queued", row(func(r *ActivityRow) {
				r.State, r.Outcome, r.Duration = ActivityQueued, OutcomeQueued, NoDuration
			})},
			{"running", row(func(r *ActivityRow) { r.State, r.Outcome = ActivityRunning, OutcomeRunning })},
			{"checking", row(func(r *ActivityRow) { r.State, r.Outcome = ActivityChecking, OutcomeChecking })},
			{"failed", row(func(r *ActivityRow) { r.State, r.Outcome = ActivityFailed, OutcomeExit(1) })},
			// The two denials are the case the invariant is really about: the
			// component colours them differently, so the decider has to be a
			// word as well (§6d).
			{"denied by you", row(func(r *ActivityRow) {
				r.State, r.Outcome, r.Duration = ActivityDenied, OutcomeBy(OutcomeDenied, "you"), NoDuration
			})},
			{"denied by rule", row(func(r *ActivityRow) {
				r.State, r.ByRule = ActivityDenied, true
				r.Outcome, r.Duration = OutcomeBy(OutcomeDenied, "auto"), NoDuration
			})},
		}},
		{"activity row kind", []monoState{
			{"tool", row(func(r *ActivityRow) { r.Outcome = OutcomeOK })},
			{"command", row(func(r *ActivityRow) { r.Kind, r.Outcome = ActivityCommand, OutcomeOK })},
			{"edit", row(func(r *ActivityRow) { r.Kind, r.Outcome = ActivityEdit, OutcomeOK })},
			{"sub-agent", row(func(r *ActivityRow) { r.Kind, r.Outcome = ActivitySubagent, OutcomeOK })},
		}},
		{"diff line kind", []monoState{
			{"context", diffLine(diff.Context)},
			{"addition", diffLine(diff.Add)},
			{"deletion", diffLine(diff.Del)},
		}},
		{"approval severity", []monoState{
			{"no warnings", card(func(c *ApprovalCard) {
				c.AllowAlways, c.AlwaysHint = true, "a: always allow commands this session"
			})},
			{"warned", card(func(c *ApprovalCard) {
				c.Warnings = []string{"deletes files recursively (rm -rf)"}
			})},
			{"contained", card(func(c *ApprovalCard) {
				c.Containment = "contained · workspace profile · network on"
			})},
		}},
		{"approval variant", []monoState{
			{"command", card(func(c *ApprovalCard) {})},
			{"edit", card(func(c *ApprovalCard) {
				c.Variant, c.Title, c.Hunks = ApprovalEdit, "Approve edit", hunk(diff.Add)
			})},
			{"generic", card(func(c *ApprovalCard) {
				c.Variant, c.Title, c.Summary = ApprovalGeneric, "Approve tool", "fetch https://example.com"
			})},
		}},
		{"staged checkbox", []monoState{
			{"unstaged", staged(false)},
			{"staged", staged(true)},
		}},
		{"agent lane state", []monoState{
			{"current", agents(AgentCurrent, "working")},
			{"running", agents(AgentRunning, "working")},
			{"blocked", agents(AgentBlocked, "working")},
			{"done", agents(AgentDone, "working")},
			{"failed", agents(AgentFailed, "working")},
		}},
		{"context meter pressure", []monoState{
			{"healthy", meter(40)},
			{"pressured", meter(75)},
			{"critical", meter(95)},
		}},
	}
}

// TestMonoConformance is the invariant check: with the palette down to two
// greys, no two states of a surface may render to the same plain text.
func TestMonoConformance(t *testing.T) {
	monoOn(t)
	for _, s := range monoFixtures() {
		seen := map[string]string{}
		for _, st := range s.states {
			plain := strings.TrimRight(ansi.Strip(st.view), " \n")
			if plain == "" {
				t.Errorf("%s/%s: rendered nothing to tell it apart by", s.name, st.name)
				continue
			}
			if prev, dup := seen[plain]; dup {
				t.Errorf("%s: %q and %q are identical once colour is stripped — the state needs a glyph or a word:\n%s",
					s.name, prev, st.name, plain)
				continue
			}
			seen[plain] = st.name
		}
	}
}

// sgrParams pulls the parameter list out of every SGR escape in s.
var sgrPattern = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// allowedMonoSGR is what a mono render may emit: the attribute codes that
// carry weight and shape, and the two greys (plus the selection background)
// of tokens/colors.css. Anything else is a colour that survived the swap.
func allowedMonoSGR(params string) bool {
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "", "0", "1", "2", "3", "4", "7", "22", "23", "24", "27", "39", "49":
			continue
		case "38", "48":
			// 256-colour foreground/background: 38;5;N.
			if i+2 >= len(fields) || fields[i+1] != "5" {
				return false
			}
			switch fields[i+2] {
			case string(MonoFg), string(MonoDim), string(MonoBg):
				i += 2
				continue
			}
			return false
		default:
			// A bare 30–37/90–97 colour is still a colour.
			if n, err := strconv.Atoi(fields[i]); err == nil && n >= 30 {
				return false
			}
			return false
		}
	}
	return true
}

// TestMonoRendersTwoGreys checks the other half of the claim: mono does not
// merely keep states distinguishable, it actually strips the palette. Every
// escape a surface emits must be an attribute or one of the mono shades.
func TestMonoRendersTwoGreys(t *testing.T) {
	withColorProfile(t, termenv.ANSI256)
	monoOn(t)
	for _, s := range monoFixtures() {
		for _, st := range s.states {
			for _, m := range sgrPattern.FindAllStringSubmatch(st.view, -1) {
				if !allowedMonoSGR(m[1]) {
					t.Errorf("%s/%s emits SGR %q, which is not one of the two greys", s.name, st.name, m[1])
				}
			}
		}
	}
}

// TestMonoLeavesTheFullPaletteIntact guards the swap itself: turning mono off
// restores the colours, so the check above is testing a real change.
func TestMonoLeavesTheFullPaletteIntact(t *testing.T) {
	withColorProfile(t, termenv.ANSI256)
	was := mono
	t.Cleanup(func() { SetMono(was) })

	SetMono(false)
	colored := ActivityRow{Kind: ActivityEdit, Verb: "edit", Target: "loop.go", Outcome: OutcomeOK}.View(60)
	if Mono() {
		t.Fatal("mono should be off")
	}
	if Palette != FullPalette {
		t.Fatal("the full palette should be back")
	}
	var offPalette bool
	for _, m := range sgrPattern.FindAllStringSubmatch(colored, -1) {
		if !allowedMonoSGR(m[1]) {
			offPalette = true
		}
	}
	if !offPalette {
		t.Fatal("with mono off the row should render in colours the mono check would reject")
	}

	SetMono(true)
	if !Mono() || Palette != MonoPalette {
		t.Fatal("mono should be on with the mono palette")
	}
}

// TestMonoDeclinesSyntaxHighlighting covers the one source of colour the
// palette does not own: chroma's themes. Mono declines them rather than
// pretending to strip them.
func TestMonoDeclinesSyntaxHighlighting(t *testing.T) {
	withColorProfile(t, termenv.ANSI256)
	monoOn(t)
	syntax := func(line string) []Segment {
		return []Segment{{Text: line, Color: "#ff0000"}}
	}
	hunks := []diff.Hunk{{
		OldStart: 1, OldCount: 0, NewStart: 1, NewCount: 1,
		Lines: []diff.Line{{Kind: diff.Add, Text: "x := 1", NewNo: 1}},
	}}
	out := strings.Join(UnifiedLines(hunks, 60, UnifiedOpts{Syntax: syntax}), "\n")
	for _, m := range sgrPattern.FindAllStringSubmatch(out, -1) {
		if !allowedMonoSGR(m[1]) {
			t.Fatalf("mono diff kept a syntax colour: SGR %q", m[1])
		}
	}
	if !strings.Contains(ansi.Strip(out), "+x := 1") {
		t.Fatalf("the line should still read as an addition, got %q", ansi.Strip(out))
	}
}

// TestMonoFromEnv covers the environment half of the switch: NO_COLOR turns
// mono on for the session regardless of its value, and so does a terminal
// with no attributes at all.
func TestMonoFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"nothing set", map[string]string{}, false},
		{"NO_COLOR empty is not set", map[string]string{"NO_COLOR": ""}, false},
		{"NO_COLOR any value", map[string]string{"NO_COLOR": "0"}, true},
		{"dumb terminal", map[string]string{"TERM": "dumb"}, true},
		{"ordinary terminal", map[string]string{"TERM": "xterm-256color"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := monoFromEnv(func(k string) string { return tc.env[k] })
			if got != tc.want {
				t.Fatalf("monoFromEnv(%v) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}
