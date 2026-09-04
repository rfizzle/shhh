package chat

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/project"
)

// The screen names what the checkout was not allowed to load, and offers the
// answer. Without it a session in a fresh clone is quietly smaller than the
// same session in a trusted one, with nothing on screen to say why.
func TestStartScreenNamesWhatWasWithheld(t *testing.T) {
	info := startFixture()
	info.Gate = StartGate{Path: ".shhh/quality.json"}
	info.Trust = Trust{Withheld: []string{"skills", "quality suites"}}
	m := startModel(t, info)

	screen := m.renderStartScreen(110)
	for _, want := range []string{"trust", "withheld", "skills, quality suites", "/trust"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the screen does not say %q:\n%s", want, screen)
		}
	}
	// The gate line is the one that would otherwise lie: a configured gate
	// that was withheld must not read as one nobody wrote.
	if strings.Contains(screen, "not configured") {
		t.Errorf("a withheld gate read as an absent one:\n%s", screen)
	}
	// A checkout that was trusted, or that declares nothing, says nothing
	// here: a row that reads the same on every session is a row nobody
	// reads by the third one.
	if got := m.WithStartScreen(startFixture()).renderStartScreen(110); strings.Contains(got, "trust ") {
		t.Errorf("a trusted checkout drew a trust row:\n%s", got)
	}
}

// An answer given once and overtaken by an edit reads differently from one
// that was never given.
func TestWithheldWordSeparatesEditedFromUnanswered(t *testing.T) {
	if w := (Trust{Withheld: []string{"skills"}}).word(); w != "withheld" {
		t.Errorf("unanswered = %q", w)
	}
	if w := (Trust{Withheld: []string{"skills"}, Changed: true}).word(); w != "changed" {
		t.Errorf("edited = %q", w)
	}
	if (Trust{}).withholding() {
		t.Error("a session with nothing withheld reported some")
	}
	if !(Trust{Withheld: []string{string(project.KindGate)}}).withholds(project.KindGate) {
		t.Error("the gate is in the list and was not found")
	}
}

// /status says it in words too: the terminals that have no rail also have no
// start screen left once the session is under way.
func TestStatusCommandNamesWhatWasWithheld(t *testing.T) {
	info := startFixture()
	info.Trust = Trust{Withheld: []string{"skills", "MCP servers"}, Changed: true}
	m := startModel(t, info)
	text, _ := m.statusCommand()
	for _, want := range []string{"Withheld", "changed since you trusted it", "skills and MCP servers", "/trust"} {
		if !strings.Contains(text, want) {
			t.Errorf("/status missing %q:\n%s", want, text)
		}
	}
	quiet := startModel(t, startFixture())
	if got, _ := quiet.statusCommand(); strings.Contains(got, "Withheld") {
		t.Errorf("a trusted checkout was reported as withholding:\n%s", got)
	}
}

// /trust is answered by the session that wired it, and says where the answer
// lives when nothing did.
func TestTrustCommandGoesToTheSessionsAnswer(t *testing.T) {
	info := startFixture()
	var got []string
	info.Trust = Trust{Manage: func(args []string) string {
		got = args
		return "recorded"
	}}
	m := startModel(t, info)
	if out := m.trustCommand([]string{"off"}); out != "recorded" {
		t.Errorf("trustCommand = %q", out)
	}
	if strings.Join(got, ",") != "off" {
		t.Errorf("args = %v", got)
	}
	bare := startModel(t, startFixture())
	if out := bare.trustCommand(nil); !strings.Contains(out, "shhh doctor trust") {
		t.Errorf("a session with no answer to give said %q", out)
	}
}
