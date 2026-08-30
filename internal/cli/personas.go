package cli

import (
	"os"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// personaKind is the drafter's lean for a session.
func personaKind(session chatSession) persona.Kind {
	if session.conversation {
		return persona.KindChat
	}
	return persona.KindCode
}

// buildPersonas wires /agents new: the drafter on the session's own model
// (a profile is a judgement about the work, not a status line), and a save
// that writes the file and makes the role spawnable in this session.
// See docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation.
func buildPersonas(session chatSession, env *sessionEnv, agents *agentProfiles, sup *subagent.Supervisor, ledger *meter.Ledger) chat.Personas {
	kind := personaKind(session)
	drafter := persona.NewDrafter(ledger.For(env.prov, meter.SourcePersona), persona.Config{Model: env.modelName})
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	p := chat.Personas{
		Kind:      kind,
		Enabled:   drafter.Enabled(),
		Draft:     drafter.Draft,
		Models:    provider.KnownModels(env.prov.Name()),
		GlobalDir: persona.Dir(persona.ScopeGlobal, cwd),
	}
	if kind == persona.KindCode {
		p.ProjectDir = persona.Dir(persona.ScopeProject, cwd)
	}
	p.Existing = func() []string {
		var builtins []string
		for name := range sup.Profiles() {
			builtins = append(builtins, string(name))
		}
		return persona.Existing(agents.definitions, builtins...)
	}
	p.Save = func(scope persona.Scope, d persona.Draft, overwrite bool) (string, error) {
		if kind == persona.KindChat {
			// Chat has no project state; the card never offers one, and
			// this is the guarantee behind the card.
			scope = persona.ScopeGlobal
		}
		path, err := persona.Write(persona.Dir(scope, cwd), d, kind, overwrite)
		if err != nil {
			return path, err
		}
		def, err := config.LoadAgentFile(path)
		if err != nil {
			return path, err
		}
		prof, err := profileFromDefinition(def)
		if err != nil {
			return path, err
		}
		agents.definitions[def.Name] = def
		agents.profiles[prof.Name] = prof
		sup.AddProfile(prof)
		// The spawn tool's role enum is the profiles at the time it was
		// built; rebuild it so the next request can name the new one.
		env.replaceTools(func(defs []provider.Tool) []provider.Tool {
			fresh := subagent.Definitions(sup.Profiles())
			byName := map[string]provider.Tool{}
			for _, t := range fresh {
				byName[t.Name] = t
			}
			out := make([]provider.Tool, 0, len(defs))
			for _, t := range defs {
				if f, ok := byName[t.Name]; ok {
					t = f
				}
				out = append(out, t)
			}
			return out
		})
		return path, nil
	}
	return p
}
