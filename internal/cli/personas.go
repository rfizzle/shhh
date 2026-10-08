package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/pricing"
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

// buildPersonas wires /agents new: the drafter on agents.drafter_model, or
// down the bounded-call chain where that is unset, and a save
// that writes the file and makes the role spawnable in this session.
// See docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation.
func buildPersonas(session chatSession, env *sessionEnv, agents *agentProfiles, sup *subagent.Supervisor, ledger *meter.Ledger, prices *pricing.Table) chat.Personas {
	kind := personaKind(session)
	drafter := persona.NewDrafter(ledger.For(env.prov, meter.SourcePersona), persona.Config{ModelAt: env.flowModelAt(env.cfg, flowDrafter), Scrub: session.vault.Scrub})
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
	// Read on every frame the manager draws, so a role saved from the card
	// above appears in the list under it without the session restarting.
	p.Roles = func() []chat.SpawnableRole { return agents.roles(cwd) }
	p.Existing = func() []string {
		var builtins []string
		for name := range sup.Profiles() {
			builtins = append(builtins, string(name))
		}
		return persona.Existing(agents.defs(), builtins...)
	}
	// register reads a profile file and makes the role it defines the one
	// this session spawns: the drafter's save and the manager's editor both
	// end here, so a role written either way reaches the supervisor, the
	// spawn card and the spawn tool the same way. Nothing is changed until
	// the file has loaded, so a file the loader refuses leaves the running
	// role as it was.
	register := func(path string) error {
		def, err := config.LoadAgentFile(path)
		if err != nil {
			return err
		}
		prof, err := profileFromDefinition(def)
		if err != nil {
			return err
		}
		prof.Checkout = fromCheckout(def, cwd)
		// A conversation spawns the roles that read, which is what it was
		// started with; an edit that grants a writing tier is refused
		// rather than let in (docs/capabilities/chat.md#colleagues-not-workers).
		if kind == persona.KindChat && prof.Writes {
			return fmt.Errorf("agent profile %s: grants a tier that writes, and a conversation spawns only roles that read", path)
		}
		// Both maps are written under one hold of mu; AddProfile and offerOn
		// below take their own locks, so mu is released before them.
		agents.mu.Lock()
		agents.definitions[def.Name] = def
		agents.profiles[prof.Name] = prof
		agents.mu.Unlock()
		sup.AddProfile(prof)
		// The spawn tool's role enum is the profiles at the time it was
		// built, and its model list the models they named; rebuild it so
		// the next request can name the new role and the model it brought.
		spawnModels{env: env, agents: agents, prices: prices}.offerOn(sup.Profiles(), nil)
		return nil
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
		return path, register(path)
	}
	p.Reload = register
	p.Open = persona.Open
	// A profile opened from the manager is saved over its own file. A
	// checkout's profile is saved only where it is read: in a checkout the
	// person has trusted
	// (docs/capabilities/subagents.md#a-profile-is-a-file).
	p.SaveOpened = func(src *persona.Source, d persona.Draft) (string, error) {
		if filepath.Dir(src.Path) == config.ProjectAgentDir(cwd) && !projectTrust().Allows() {
			return src.Path, fmt.Errorf("this checkout is not trusted, so its profiles are not saved; trust it with shhh trust first")
		}
		// register refuses a writing role in a conversation, and it runs
		// after the file is written, so the same refusal is asked first.
		if kind == persona.KindChat && d.Writes() {
			return src.Path, fmt.Errorf("agent profile %s: grants a tier that writes, and a conversation spawns only roles that read", src.Path)
		}
		path, err := persona.SaveOpened(src, d)
		if err != nil {
			return path, err
		}
		return path, register(path)
	}
	return p
}
