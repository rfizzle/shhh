package agent

import (
	"strings"
	"testing"

	"github.com/rfizzle/llm"
)

func TestSessionSteer_NamesTheSenderAndEndsOnTheLine(t *testing.T) {
	got := Steering{}.SessionSteer("session lane-b", "master moved: rebase onto it")
	if !strings.Contains(got, "sent by session lane-b") {
		t.Errorf("the built-in wording names the sender:\n%s", got)
	}
	if !strings.Contains(got, "not by the person at this keyboard") {
		t.Errorf("the built-in wording says it is not the person here:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n\nmaster moved: rebase onto it") {
		t.Errorf("the line follows the wording:\n%s", got)
	}
}

func TestSetSessionSteerWording_AnAppNamesItselfAndNilRestoresTheDefault(t *testing.T) {
	t.Cleanup(func() { SetSessionSteerWording(nil) })
	def := SessionSteerWording()
	if strings.Contains(def, "shhh") {
		t.Errorf("the module default names no app:\n%s", def)
	}
	SetSessionSteerWording(func() string { return "From " + PlaceholderSource + " of my app." })
	if got := (Steering{}).SessionSteer("lane-b", "rebase"); got != "From lane-b of my app.\n\nrebase" {
		t.Errorf("the installed wording is the one in force, got %q", got)
	}
	SetSessionSteerWording(nil)
	if got := SessionSteerWording(); got != def {
		t.Errorf("nil restores the default, got %q", got)
	}
}

func TestSessionSteer_AConfiguredWordingGetsTheSourceAndKeepsTheLine(t *testing.T) {
	s := Steering{SessionSteerText: "From {{source}}, for what it is worth:\n"}
	got := s.SessionSteer("session lane-b", "rebase")
	if got != "From session lane-b, for what it is worth:\n\nrebase" {
		t.Errorf("got %q", got)
	}
	// A wording that places no source still carries the line: the line is
	// the message, and the wording only frames it.
	if got := (Steering{SessionSteerText: "A colleague writes."}).SessionSteer("x", "rebase"); !strings.HasSuffix(got, "rebase") {
		t.Errorf("got %q", got)
	}
}

func TestValidateSessionSteer_TakesTheSourceAndNothingElse(t *testing.T) {
	if err := ValidateSessionSteer(SessionSteerWording()); err != nil {
		t.Errorf("the built-in wording must validate: %v", err)
	}
	if err := ValidateSessionSteer("from {{target}}"); err == nil {
		t.Error("a steer's own placeholder is not this wording's")
	}
}

func TestStartMachineTurn_StartsATurnInTheSessionsOwnVoice(t *testing.T) {
	a := New(nil, nil)
	a.rounds = 4
	a.StartMachineTurn("from another session")
	msgs := a.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser || !last.Machine || last.Content != "from another session" {
		t.Errorf("got %+v", last)
	}
	if a.Rounds() != 0 {
		t.Errorf("a turn starts its round counter again, got %d", a.Rounds())
	}
}
