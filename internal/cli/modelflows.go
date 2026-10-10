package cli

// The flows the /model picker can point at.
//
// A flow's model is held for the running session by the same store the
// settings screen's flow rows use (flowOverrides), so a choice made in the
// picker reads `session` on /config and ends with the session; the picker
// writes no file
// (docs/capabilities/configuration.md#a-session-can-hold-a-value-no-file-does).

import (
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// modelFlowTargets lists the flows a model can be held for, in the settings
// screen's order, each with the model it runs on now. A flow no interactive
// session asks (sessionless) has nothing for a held model to move, and the
// toolchain drafter reads the profile drafter's key, so neither is offered.
// It is read from the files as they stand now, over what the session holds.
func modelFlowTargets(env *sessionEnv) []chat.FlowTarget {
	cfg, _, err := loadLayeredConfig(workingDir())
	if err != nil {
		return nil
	}
	var out []chat.FlowTarget
	for _, a := range resolveFlows(env.flows.over(cfg), env.provName, env.modelName) {
		if a.flow.sessionless || a.flow.flow == provider.FlowToolchainDrafter {
			continue
		}
		out = append(out, chat.FlowTarget{Key: a.flow.keys[0], Name: a.flow.name, Model: a.model})
	}
	return out
}

// holdFlowModel has the session take model for the flow whose model key is
// key, and tells the record what the rest of the session is asked on.
func holdFlowModel(env *sessionEnv, key, model string) {
	env.flows.set(key, model)
	if env.flowsMoved != nil {
		env.flowsMoved()
	}
}
