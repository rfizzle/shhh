package components

// The recovery surfaces' own tests (S-106). The golden files beside them
// capture the whole render; these assert the rules the render must not drift
// from — the grid the row sits on, what the card refuses to claim, and the
// one thing the key prompt must never do.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestRecoveryRow_SitsOnTheActivityGrid(t *testing.T) {
	row := RecoveryRow{
		Verb: VerbModel, Subject: "gpt-4o", Qualifier: "401 unauthorized",
		Outcome: "key ···4f9c rejected", Duration: "0.3s",
	}
	// Indexed by rune: the glyph field holds one, and the columns below are
	// character cells, not bytes.
	line := []rune(ansi.Strip(strings.Split(row.View(110), "\n")[0]))
	// Pointer (2) + rail (1) is blank, then the glyph, then the verb in its
	// 8 columns: a failure reads as part of the turn because it is on the
	// same grid as the call that failed.
	if got := string(line[:ptrWidth+railWidth]); strings.TrimSpace(got) != "" {
		t.Errorf("the pointer and rail columns should be blank, got %q", got)
	}
	if got := string(line[ptrWidth+railWidth : ptrWidth+railWidth+glyphWidth]); got != "✗ " {
		t.Errorf("glyph field = %q, want the ✗ of a broken call", got)
	}
	verb := string(line[leadWidth-verbWidth : leadWidth])
	if verb != "model   " {
		t.Errorf("verb field = %q, want model padded to %d columns", verb, verbWidth)
	}
	if !strings.HasSuffix(string(line), "key ···4f9c rejected  0.3s") {
		t.Errorf("the outcome and duration should close the row, got %q", string(line))
	}
}

func TestRecoveryRow_GlyphsSayTheState(t *testing.T) {
	for _, tc := range []struct {
		state RecoveryState
		glyph string
	}{
		{RecoveryBroken, "✗"},
		{RecoveryStalled, "⚠"},
		{RecoveryStopped, "⊘"},
	} {
		row := RecoveryRow{State: tc.state, Verb: VerbModel, Subject: "gpt-4o"}
		if got := ansi.Strip(row.View(80)); !strings.Contains(got, tc.glyph) {
			t.Errorf("state %v should render %q, got %q", tc.state, tc.glyph, got)
		}
	}
}

func TestRecoveryRow_DetailIsBounded(t *testing.T) {
	row := RecoveryRow{
		Verb: VerbModel, Subject: "gpt-4o",
		Detail:    []string{"one", "two", "three", "four", "five"},
		MaxDetail: 2,
	}
	got := ansi.Strip(row.View(80))
	if strings.Contains(got, "three") {
		t.Errorf("the detail body should stop at MaxDetail, got:\n%s", got)
	}
	if !strings.Contains(got, "two") {
		t.Errorf("the detail body should reach MaxDetail, got:\n%s", got)
	}
}

func TestRecoveryRow_OutcomeNeverClips(t *testing.T) {
	row := RecoveryRow{
		Verb: VerbModel, Subject: "a-very-long-model-name-that-will-not-fit-here",
		Qualifier: "429 rate limited", Outcome: "retry in 38s", Duration: "0.3s",
	}
	line := ansi.Strip(strings.Split(row.View(60), "\n")[0])
	if !strings.Contains(line, "retry in 38s") {
		t.Errorf("the outcome is the reason to read the row and must survive, got %q", line)
	}
	if !strings.Contains(line, "…") {
		t.Errorf("the target should clip instead, got %q", line)
	}
}

func TestRecoveryRow_KeyStrokes(t *testing.T) {
	row := RecoveryRow{Keys: []KeyOffer{{Key: "[k]"}, {Key: "[p]"}}}
	got := strings.Join(row.KeyStrokes(), ",")
	if got != "k,p" {
		t.Errorf("KeyStrokes() = %q, want the bare keys", got)
	}
}

func TestProviderCard_NamesEveryPlaceAndWhatWasThere(t *testing.T) {
	card := ProviderCard{
		Places: []ProviderPlace{
			{Label: "env", Detail: "SHHH_API_KEY, OPENAI_API_KEY — unset"},
			{Label: "config", Detail: "~/.config/shhh/config.toml — no provider api_key"},
			{Label: "profiles", Detail: "no .toml in ~/.config/shhh/providers"},
			{Label: "local", Emphasis: "localhost:11434", Detail: "llama3.3", Found: true},
		},
		Likely: "the local runtime is already answering",
		Keys:   []KeyOffer{{Key: "[enter]", Label: "setup wizard"}},
	}
	got := ansi.Strip(card.View(96))
	if !strings.Contains(got, "shhh looked in four places:") {
		t.Errorf("the card should count the places in words, got:\n%s", got)
	}
	for _, want := range []string{"env", "config", "profiles", "local", "llama3.3", "the local runtime is already answering", "[enter] setup wizard"} {
		if !strings.Contains(got, want) {
			t.Errorf("the card should say %q, got:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "✓ local") {
		t.Errorf("a place that had something should be marked, got:\n%s", got)
	}
	if !strings.Contains(got, "✗ env") {
		t.Errorf("a place that had nothing should be marked, got:\n%s", got)
	}
}

func TestProviderCard_ClaimsOnlyTheKeysItOffers(t *testing.T) {
	card := &ProviderCard{Keys: []KeyOffer{{Key: "[p]", Label: "paste a key"}}}
	if done, _ := card.Update(tea.KeyPressMsg{Code: 'z', Text: "z"}); done {
		t.Error("a key the card does not offer should not resolve it")
	}
	done, result := card.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if !done || result != "p" {
		t.Errorf("[p] should resolve the card, got done=%v result=%v", done, result)
	}
	esc := &ProviderCard{Keys: []KeyOffer{{Key: "[p]"}}}
	done, result = esc.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !done || result != "" {
		t.Errorf("esc should decline, got done=%v result=%v", done, result)
	}
}

func TestSecretPrompt_MasksAndNeverEchoes(t *testing.T) {
	p := &SecretPrompt{Prompt: "Paste a key for openai", Hint: "OPENAI_API_KEY"}
	for _, r := range "sk-secret" {
		p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	got := ansi.Strip(p.View(80))
	if strings.Contains(got, "sk-secret") {
		t.Errorf("the prompt must never render the key, got:\n%s", got)
	}
	if !strings.Contains(got, strings.Repeat("•", 9)) {
		t.Errorf("the prompt should mask a bullet per rune, got:\n%s", got)
	}
	if p.Len() != 9 {
		t.Errorf("Len() = %d, want 9", p.Len())
	}
	p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if p.Len() != 8 {
		t.Errorf("backspace should delete one rune, Len() = %d", p.Len())
	}
	done, result := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || result != "sk-secre" {
		t.Errorf("enter should resolve to what was typed, got %v", result)
	}
}

func TestSecretPrompt_EscResolvesToNothing(t *testing.T) {
	p := &SecretPrompt{}
	p.Update(tea.KeyPressMsg{Code: 'a', Text: "abc"})
	done, result := p.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !done || result != "" {
		t.Errorf("esc declines and keeps the old key, got done=%v result=%v", done, result)
	}
}

// The retry countdown (S-107). The rules it has to keep are §10c's: a
// bar that states its number, and cells that drain rather than fill.
func TestRetryWait_StatesItsNumberAndItsBound(t *testing.T) {
	w := RetryWait{
		Pct: 60, Text: "retry in 12s", Note: "attempt 2 of 3",
		Keys: []KeyOffer{
			{Key: "[m]", Label: "finish this turn on gpt-4o-mini"},
			{Key: "[esc]", Label: "stop and keep the 3 edits"},
		},
	}
	view := ansi.Strip(w.View(80))
	for _, want := range []string{"▰", "▱", "retry in 12s", "attempt 2 of 3", "[m]", "[esc]"} {
		if !strings.Contains(view, want) {
			t.Errorf("the countdown should say %q, got:\n%s", want, view)
		}
	}
	// Both lines sit in the detail body's column, under the row that stalled.
	for _, line := range strings.Split(view, "\n") {
		if !strings.HasPrefix(line, "    ") {
			t.Errorf("every line indents to the detail body, got %q", line)
		}
	}
}

func TestRetryWait_Drains(t *testing.T) {
	full := ansi.Strip(RetryWait{Pct: 100, Text: "retry in 20s"}.View(80))
	part := ansi.Strip(RetryWait{Pct: 25, Text: "retry in 5s"}.View(80))
	if strings.Count(full, "▰") <= strings.Count(part, "▰") {
		t.Errorf("a countdown loses cells as it runs down:\n%s\n%s", full, part)
	}
	if strings.Count(part, "▱") == 0 {
		t.Error("a partly drained countdown should show the cells it has given up")
	}
}
