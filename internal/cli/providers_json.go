package cli

// `shhh providers --json` as data. The report is presentation and its shape
// is free to change with the terminal; a script reads these structs, which
// name the profile's own fields and nothing about how they are drawn.

import (
	"os"

	"sort"
	"strings"

	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/profile"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
)

// providersDoc is the whole answer: what can be resolved, the profiles that
// loaded, and the files that would not.
type providersDoc struct {
	Providers []providerDoc     `json:"providers"`
	Profiles  []profileDoc      `json:"profiles"`
	Errors    []profileErrorDoc `json:"errors,omitempty"`
}

// providerDoc is one resolvable provider name and whether a session starting
// on it would find a key.
type providerDoc struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Ready   bool   `json:"ready"`
	KeyFrom string `json:"key_from,omitempty"`
}

type profileDoc struct {
	Name      string        `json:"name"`
	Path      string        `json:"path"`
	Endpoints []endpointDoc `json:"endpoints"`
}

type endpointDoc struct {
	Label     string     `json:"label"`
	API       string     `json:"api"`
	BaseURL   string     `json:"base_url"`
	Match     []string   `json:"match,omitempty"`
	APIKeyEnv string     `json:"api_key_env,omitempty"`
	KeySet    bool       `json:"key_set"`
	Models    []modelDoc `json:"models,omitempty"`
	Rewrites  []ruleDoc  `json:"rewrites,omitempty"`
}

// modelDoc omits every figure nothing is known about, for the same reason the
// report leaves it out of the row.
type modelDoc struct {
	ID            string   `json:"id"`
	ContextWindow int64    `json:"context_window,omitempty"`
	InputCost     *float64 `json:"input_cost,omitempty"`
	OutputCost    *float64 `json:"output_cost,omitempty"`
	CostSource    string   `json:"cost_source"`
}

type ruleDoc struct {
	Direction string `json:"direction"`
	Op        string `json:"op"`
	Path      string `json:"path"`
	Model     string `json:"model,omitempty"`
	Note      string `json:"note,omitempty"`
}

type profileErrorDoc struct {
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

// providersJSON reads the same machine the report reads.
func providersJSON(profiles []profile.Profile, errs []error, prices *pricing.Table) providersDoc {
	byName := map[string]profile.Profile{}
	for _, p := range profiles {
		byName[p.Name] = p
	}
	names := provider.Available()
	sort.Strings(names)

	doc := providersDoc{Providers: make([]providerDoc, 0, len(names)), Profiles: []profileDoc{}}
	for _, name := range names {
		if p, ok := byName[name]; ok {
			e := p.Routes()[0]
			doc.Providers = append(doc.Providers, providerDoc{
				Name: name, Kind: "profile", Ready: e.Key() != "", KeyFrom: e.APIKeyEnv,
			})
			continue
		}
		entry := providerDoc{Name: name, Kind: "built-in"}
		for _, v := range resolve.KeyVars(name) {
			if strings.TrimSpace(os.Getenv(v)) != "" {
				entry.Ready, entry.KeyFrom = true, v
				break
			}
		}
		doc.Providers = append(doc.Providers, entry)
	}

	for _, p := range profiles {
		entry := profileDoc{Name: p.Name, Path: p.Path, Endpoints: []endpointDoc{}}
		for _, e := range p.Routes() {
			ep := endpointDoc{
				Label: e.Label, API: e.API, BaseURL: e.BaseURL, Match: e.Match,
				APIKeyEnv: e.APIKeyEnv, KeySet: e.Key() != "",
			}
			for _, m := range e.Models {
				ep.Models = append(ep.Models, modelJSON(m, prices))
			}
			for _, r := range e.Rewrite {
				ep.Rewrites = append(ep.Rewrites, ruleDoc{
					Direction: r.Direction, Op: r.Op, Path: r.Path, Model: r.When.Model, Note: r.Note,
				})
			}
			entry.Endpoints = append(entry.Endpoints, ep)
		}
		doc.Profiles = append(doc.Profiles, entry)
	}
	for _, err := range errs {
		path, detail := splitProfileError(err)
		doc.Errors = append(doc.Errors, profileErrorDoc{Path: path, Detail: detail})
	}
	return doc
}

// modelJSON carries a figure only where one is known, from the profile first
// and the public table after.
func modelJSON(m profile.Model, prices *pricing.Table) modelDoc {
	doc := modelDoc{ID: m.ID, ContextWindow: m.ContextWindow, CostSource: sourceCell(m, prices)}
	if doc.ContextWindow == 0 && prices != nil {
		doc.ContextWindow, _ = prices.ContextWindow(m.ID)
	}
	switch {
	case m.Cost.HasPricing():
		in, out := m.Cost.Input, m.Cost.Output
		doc.InputCost, doc.OutputCost = &in, &out
	default:
		if in, out, ok := publicCost(prices, m.ID); ok {
			doc.InputCost, doc.OutputCost = &in, &out
		}
	}
	return doc
}
