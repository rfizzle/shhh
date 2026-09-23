package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/rpc"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// The wording a line from another session is framed in is a wording like the
// steer's: a file replaces it, it takes the source and nothing else, and the
// session is built with it.
func TestLoadPrompts_TheSessionSteerTakesItsSource(t *testing.T) {
	got, err := loadPrompts(config.PromptsConfig{
		SessionSteer: writeWording(t, "session_steer.md", "a colleague at "+agent.PlaceholderSource+" writes:"),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.sessionSteer != "a colleague at {{source}} writes:" {
		t.Fatalf("read %q", got.sessionSteer)
	}
	if s := steering(config.Config{}, got); s.SessionSteerText != got.sessionSteer {
		t.Fatalf("the session is not built with it: %+v", s)
	}
	if _, err := loadPrompts(config.PromptsConfig{
		SessionSteer: writeWording(t, "session_steer.md", "judged against "+agent.PlaceholderTarget),
	}, ""); err == nil || !strings.Contains(err.Error(), "{{target}}") {
		t.Fatalf("a substitution the wording does not take must stop the session, got %v", err)
	}
}

// A refusal the settings state is answered without the screen: nothing is
// handed over, so nothing waits on it.
func TestHandInbound_RefuseIsAnsweredBeforeTheScreen(t *testing.T) {
	lines := make(chan chat.InboundLine, 1)
	_, err := handInbound(context.Background(), lines, "refuse", rpc.SendParams{Text: "rebase"})
	if !errors.Is(err, rpc.ErrRefused) {
		t.Fatalf("got %v", err)
	}
	if len(lines) != 0 {
		t.Fatal("a refused line reached the screen")
	}
}

// Otherwise the line goes to the screen with the sender's slot, and what the
// screen answers is what the sender is told — an empty answer being the
// screen's own refusal.
func TestHandInbound_TheScreenSaysWhatBecameOfTheLine(t *testing.T) {
	for word, want := range map[string]error{rpc.TakenHeld: nil, "": rpc.ErrRefused} {
		lines := make(chan chat.InboundLine, 1)
		go func() {
			line := <-lines
			if line.From != "lane-a" || line.Text != "rebase" {
				line.Taken <- "wrong"
				return
			}
			line.Taken <- word
		}()
		got, err := handInbound(context.Background(), lines, "", rpc.SendParams{From: "lane-a", Text: "rebase"})
		if !errors.Is(err, want) || (want == nil && got != word) {
			t.Errorf("screen said %q: got %q, %v", word, got, err)
		}
	}
}

// A slot no running session saves to is named in the refusal, with the
// command that lists the ones that are.
func TestSend_ASlotNobodyHoldsIsRefused(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cmd := newSendCmd()
	cmd.SetArgs([]string{"2020-01-01 00:00:00", "rebase"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `"2020-01-01 00:00:00"`) || !strings.Contains(err.Error(), "shhh sessions") {
		t.Fatalf("got %v", err)
	}
}
