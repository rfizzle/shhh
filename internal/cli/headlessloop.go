package cli

// The loop's settings, for the two surfaces with no screen.
//
// A scripted run and a served session each own a loop and no Model, so they
// cannot hand the screen a Wiring. They build the loop part of one instead and
// apply it through the function the screen's constructor applies it with
// (chat.ApplyLoop), so what a loop is set to is written down once rather than
// three times. The retry bound and the tree check are the screen's alone and
// are left unset here.
// See docs/architecture.md#the-screen-is-handed-its-wiring-as-one-value.

import (
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// headlessLoop is the loop part of the wiring a tail passes: the executor it
// has finished building, the round cap it resolved, and the steering, the
// progress clocks, the scrub, the skills kept whole and the trim's archive
// that every surface is given the same way.
func headlessLoop(cfg config.Config, env *sessionEnv, session chatSession, ts *toolset, rounds int, exec agent.ToolExecutor) chat.Wiring {
	w := chat.Wiring{
		Executor:        exec,
		MaxToolRounds:   rounds,
		Steering:        steering(cfg, env.prompts),
		ProgressCalls:   cfg.Behavior.ProgressIntervalCalls,
		ProgressElapsed: time.Duration(cfg.Behavior.ProgressIntervalSeconds) * time.Second,
		Secrets:         chat.Secrets{Scrub: session.vault.ScrubMessage},
	}
	if session.skills.Len() > 0 {
		w.Skills = session.skills
	}
	// An unattended run recovers its window at every round boundary rather
	// than ahead of a person's request, so it trims far more often than a
	// session does; the id the placeholder names is one this surface's
	// evidence tool reads.
	// See docs/capabilities/evidence.md#a-trim-makes-the-same-promise.
	w.Evidence.Keep = ts.evidence.Keep
	return w
}

// loopKeys are the live keys a loop's settings carry (chat.ApplySettings):
// what a served session takes again at each turn's start.
var loopKeys = []string{
	"behavior.max_tool_rounds", "behavior.check_in_interval_rounds", "behavior.check_in_max_doublings",
	"behavior.progress_interval_calls", "behavior.progress_interval_seconds",
}

// retakeLoop is a served session's turn boundary for its settings. A served
// session has no settings screen, so what it takes is what the file holds
// now: the file is read again, every live key the loop carries is read from
// it the way the settings screen's take reads it (liveTakers), and the loop
// is set through the applier the screen uses, so a written round limit or
// check-in interval reaches the session at its next turn
// (docs/interface/surfaces.md#the-settings-screen). A key the session's
// readers ask for at the call — a flow's model, the readings' cadence — is
// held for them as the screen holds it (heldKey). Everything else stays as
// the session opened on it. flag and set are the --max-rounds it was served
// with, which outranks the file's round cap here as it did at the open. A
// file that does not read leaves the session as it was.
func retakeLoop(a *agent.Agent, opened chat.Wiring, env *sessionEnv, dir string, flag int, set bool) func() {
	return func() {
		cfg, _, err := loadLayeredConfig(dir)
		if err != nil {
			return
		}
		w := opened
		for _, key := range loopKeys {
			liveTakers[key](cfg, nil, &w)
		}
		w.MaxToolRounds = maxRoundsFor(cfg, flag, set)
		chat.ApplySettings(a, w)
		if env == nil {
			return
		}
		for _, s := range config.Settings() {
			if heldKey(s.Key) {
				v, _ := config.Value(cfg, s.Key)
				env.flows.set(s.Key, v)
			}
		}
	}
}
