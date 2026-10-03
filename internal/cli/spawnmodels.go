package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/profile"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// spawnableModels is the models a spawn may name: the /model picker's
// choices — the provider's catalog, with the session's model ahead of it
// where the catalog does not list it — and after them every model the
// configuration names for an agent, so a model the person set for a role is
// one the orchestrator can name too. It is in the picker's order and
// spelling, then the configuration's, each id once.
//
// A strict catalog is the list and nothing else: a profile that declares the
// only models it may send to refuses every other name at the request, so
// offering a configured one it does not declare is offering a failure.
// See docs/capabilities/subagents.md#the-model-is-offered-the-models-it-can-name.
func spawnableModels(cfg config.Config, agents *agentProfiles, catalog []string, sessionModel string, strict bool) []string {
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || strings.EqualFold(id, config.InheritModel) || slices.Contains(out, id) {
			return
		}
		out = append(out, id)
	}
	if !strict && sessionModel != "" && !slices.Contains(catalog, sessionModel) {
		add(sessionModel)
	}
	for _, id := range catalog {
		add(id)
	}
	if strict {
		return out
	}
	add(cfg.Agents.Model)
	depths := make([]string, 0, len(cfg.Agents.Depths))
	for depth := range cfg.Agents.Depths {
		depths = append(depths, depth)
	}
	// Depth keys are numbers written as strings: "10" belongs after "9".
	slices.SortFunc(depths, func(a, b string) int {
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		return strings.Compare(a, b)
	})
	for _, depth := range depths {
		add(cfg.Agents.Depths[depth].Model)
	}
	for _, role := range slices.Sorted(maps.Keys(cfg.Agents.Profiles)) {
		add(cfg.Agents.Profiles[role].Model)
	}
	if agents != nil {
		agents.mu.RLock()
		for _, role := range slices.Sorted(maps.Keys(agents.definitions)) {
			add(agents.definitions[role].ProfileModel())
		}
		agents.mu.RUnlock()
	}
	return out
}

// spawnModels is one session's answer to what a spawn may name, read live:
// the session's model moves under /model and a provider switch, and a
// profile saved from the manager can bring a model of its own.
type spawnModels struct {
	env    *sessionEnv
	agents *agentProfiles
	prices *pricing.Table
}

// strict reports whether the session's provider is a profile that declares
// the only models it may send to.
func (m spawnModels) strict() bool {
	if m.env.prov == nil {
		return false
	}
	name := m.env.prov.Name()
	for _, p := range profile.Loaded() {
		if strings.EqualFold(p.Name, name) {
			return p.StrictModels
		}
	}
	return false
}

// endpoint is the provider's own list where it may be asked: never for a
// strict catalog, whose list is the one it declared.
func (m spawnModels) endpoint() *endpointModels {
	if m.strict() {
		return nil
	}
	return m.env.endpointModels
}

// ids is spawnableModels over the session as it stands now. The catalog is
// the one the picker opened on, the provider's the session started with,
// because that is the provider a child is sent to.
func (m spawnModels) ids() []string {
	var catalog []string
	if m.env.prov != nil {
		catalog = provider.KnownModels(m.env.prov.Name())
	}
	return spawnableModels(m.env.cfg, m.agents, catalog, m.env.currentModel(), m.strict())
}

// offer is the list as spawn_agent's model argument describes it, each id
// with the per-Mtok price the picker prints beside it.
func (m spawnModels) offer() subagent.Offer {
	ids := m.ids()
	models := make([]subagent.SpawnModel, len(ids))
	for i, id := range ids {
		models[i] = subagent.SpawnModel{ID: id}
		if m.prices == nil {
			continue
		}
		if in, out, ok := m.prices.Cost(id, 1_000_000, 1_000_000); ok {
			models[i].Price = fmt.Sprintf("$%.2f in / $%.2f out per Mtok", in, out)
		}
	}
	return subagent.Offer{Models: models, Endpoint: m.endpoint() != nil}
}

// check is the supervisor's model check: an id on the list passes, one the
// endpoint lists passes, and any other is refused with the list, so the
// model that named it corrects itself in the same turn. A listing that
// fails lets the spawn through with a note, because a check that could not
// be made is not a reason to refuse what may well be a good name.
// See docs/capabilities/subagents.md#the-model-is-offered-the-models-it-can-name.
func (m spawnModels) check(model string) (string, error) {
	ids := m.ids()
	if slices.Contains(ids, model) {
		return "", nil
	}
	refusal := fmt.Sprintf("model %q is not one this session can run", model)
	if endpoint := m.endpoint(); endpoint != nil {
		names, err := endpoint.forSpawn()
		if err != nil {
			return fmt.Sprintf("Its model %s could not be checked: the provider's model list could not be read (%v), so if the endpoint does not serve it the agent fails on its first request.", model, err), nil
		}
		if slices.Contains(names, model) {
			return "", nil
		}
		refusal += ", and the provider's endpoint does not list it"
	}
	if len(ids) == 0 {
		return "", nil
	}
	return "", fmt.Errorf("%s; name one of %s, or leave model out to take the default", refusal, strings.Join(ids, ", "))
}

// offerOn puts the list as it stands now on spawn_agent's definition: in
// the toolset the next request carries, and in defs, which it answers with
// for a caller still assembling one. The definitions are built before the
// toolset is edited, because the edit runs under the lock the session's
// model is read under.
func (m spawnModels) offerOn(profiles subagent.Profiles, defs []provider.Tool) []provider.Tool {
	fresh := subagent.Definitions(profiles, m.offer())
	if m.env.replaceTools != nil {
		m.env.replaceTools(func(current []provider.Tool) []provider.Tool { return replaceDefinitions(current, fresh) })
	}
	return replaceDefinitions(defs, fresh)
}

// followSpawnModels rebuilds spawn_agent after a /model switch and a provider
// switch, so the next request offers the list the session now has.
func followSpawnModels(env *sessionEnv, spawn spawnModels, profiles func() subagent.Profiles) {
	switchModel, switchProvider := env.switchModel, env.switchProvider
	if switchModel != nil {
		env.switchModel = func(name string) {
			switchModel(name)
			spawn.offerOn(profiles(), nil)
		}
	}
	if switchProvider != nil {
		env.switchProvider = func(name string) error {
			if err := switchProvider(name); err != nil {
				return err
			}
			spawn.offerOn(profiles(), nil)
			return nil
		}
	}
}

// checkedSpawnPreview puts the session's model check in front of the spawn
// card's preview. A model the session cannot run is refused there, before the
// card and before a slot: no answer the person gives makes it one the
// provider serves, and the refusal names the ones it does, so the model
// corrects itself in the same turn. The endpoint's list, where it is asked,
// is asked once, on the first name outside the session's own, and the wait is
// bounded by the lister's timeout.
// See docs/capabilities/subagents.md#the-model-is-offered-the-models-it-can-name.
func checkedSpawnPreview(sup *subagent.Supervisor, preview chat.GatedPreviewFunc) chat.GatedPreviewFunc {
	return func(args json.RawMessage) (chat.GatedPreview, error) {
		if _, err := sup.CheckModel(args); err != nil {
			return chat.GatedPreview{}, err
		}
		return preview(args)
	}
}
