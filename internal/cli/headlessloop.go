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
