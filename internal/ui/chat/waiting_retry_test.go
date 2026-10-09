package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// `retry in <n>s` is promised only where a retry is coming: with the bound
// used up, or set to none, the last stretch before the deadline says the turn
// fails there.
func TestWaitingState_RetryClauseKnowsTheBound(t *testing.T) {
	m, step, _ := waitingModel(t)
	step(108 * time.Second)
	if got := statusLine(t, *m); !strings.Contains(got, "silent — retry in 12s") {
		t.Errorf("with the default bound the line reads %q, want a retry", got)
	}

	none := 0
	m.backoff.SetLimit(&none)
	if got := statusLine(t, *m); !strings.Contains(got, "silent — fails in 12s") || strings.Contains(got, "retry") {
		t.Errorf("with provider_retries = 0 the line reads %q, want it to fail at the deadline", got)
	}

	one := 1
	m.backoff.SetLimit(&one)
	if got := statusLine(t, *m); !strings.Contains(got, "retry in 12s") {
		t.Errorf("with one retry left the line reads %q, want a retry", got)
	}
	m.backoff.Next(&provider.Failure{Class: provider.ClassNetwork})
	if got := statusLine(t, *m); !strings.Contains(got, "fails in 12s") {
		t.Errorf("with the bound used up the line reads %q, want it to fail", got)
	}
}
