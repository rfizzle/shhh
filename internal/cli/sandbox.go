package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// sandboxPolicy builds the session containment policy (S-062): workspace is
// the current directory, profile and extensions come from config.
func sandboxPolicy(cfg config.Config) (sandbox.Policy, error) {
	profile, err := sandbox.ParseProfile(cfg.Sandbox.Profile)
	if err != nil {
		return sandbox.Policy{}, fmt.Errorf("config sandbox.profile: %w", err)
	}
	ws, err := os.Getwd()
	if err != nil {
		return sandbox.Policy{}, err
	}
	return sandbox.Policy{
		Workspace:  ws,
		Profile:    profile,
		DenyExtra:  cfg.Sandbox.DenyExtra,
		WriteExtra: cfg.Sandbox.WriteExtra,
	}, nil
}

// buildContainment assembles the containment setup agent sessions run
// commands through: a wrapped runner when a mechanism is available, plus the
// status line and doctor report either way. A wrap or spawn failure surfaces
// as the command's error result — a contained command never falls back to
// running bare.
func buildContainment(cfg config.Config) (chat.Containment, error) {
	policy, err := sandboxPolicy(cfg)
	if err != nil {
		return chat.Containment{}, err
	}
	avail := sandbox.Detect()
	c := chat.Containment{Report: sandbox.Report(avail, policy)}
	if !avail.OK {
		c.Status = "unconfined — " + avail.Detail
		return c, nil
	}
	c.Status = fmt.Sprintf("contained: %s (%s profile)", avail.Mechanism, policy.Profile)
	c.Run = func(ctx context.Context, command string) (string, int) {
		argv, err := sandbox.Wrap(avail, policy, command)
		if err != nil {
			return "sandbox: " + err.Error(), -1
		}
		return runner.RunCaptureArgv(ctx, argv)
	}
	return c, nil
}
