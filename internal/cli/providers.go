package cli

// `shhh providers` — what this machine can talk to, and whether it is ready
// to. A gateway profile fails in quiet ways (an env var that isn't exported,
// a model the catalog no longer serves, a rewrite rule whose path stopped
// matching), so the listing is written to make each of those visible without
// starting a session.
//
// It is a report like every other listing
// (docs/interface/surfaces.md#outside-the-tui): the readiness of a provider is
// a glyph and a word, not a `WARNING:` in the middle of a sentence, and a
// profile that would not load is one row with the fix under it rather than an
// error printed once here and once again on the way in.

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/profile"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/spf13/cobra"
)

// ownsProfileErrors marks the command that reports profile-load failures
// itself. The startup loop in the root command prints them for every other
// command, which is right — a session starting on a half-loaded set of
// providers should say so — but here it would print the same file twice on
// one screen, once to stderr and once as a row.
const ownsProfileErrors = "owns-profile-errors"

func newProvidersCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "providers",
		Short: "List providers and check gateway profiles",
		Long: "Show every provider this machine can resolve — the built-in ones and any gateway profile in " +
			"<config-dir>/providers.toml or <config-dir>/providers/*.toml — with each profile's endpoints, " +
			"auth, declared models, and rewrite rules.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{ownsProfileErrors: "yes"},
		RunE: func(cmd *cobra.Command, args []string) error {
			dirs := profile.Dirs(config.Paths())
			profiles, errs := profile.Load(dirs)
			prices := loadPricing()
			if asJSON {
				return writeJSON(cmd, providersJSON(profiles, errs, prices))
			}
			names := provider.Available()
			sort.Strings(names)
			r := providersReport(names, sourceRows(dirs, profiles, errs), profiles, prices,
				profileWayOut(dirs))
			if plan := profile.Plan(dirs); plan.NeedsWork() {
				r.Notes = append(r.Notes, report.Note{State: report.Warn, Text: fmt.Sprintf(
					"beside %s, %s now redundant; `shhh providers migrate` folds them in",
					plan.Target, countOf(len(plan.Redundant()), "file is", "files are"))})
			}
			return report.Fprint(cmd.OutOrStdout(), r)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the providers, sources and profiles as JSON")
	cmd.AddCommand(newProvidersMigrateCmd())
	return cmd
}

// providersReport is the whole listing: what can be resolved, where profiles
// were looked for, and one section per profile. It is handed the resolvable
// names and the source rows rather than reading them, so the listing can be
// rendered against a machine other than this one.
func providersReport(names []string, sources []report.Row, profiles []profile.Profile,
	prices *pricing.Table, wayOut string) report.Report {
	byName := map[string]profile.Profile{}
	for _, p := range profiles {
		byName[p.Name] = p
	}
	builtIn := 0
	rows := make([]report.Row, 0, len(names))
	for _, name := range names {
		if p, ok := byName[name]; ok {
			rows = append(rows, providerRow(name, p.Routes()[0]))
			continue
		}
		builtIn++
		rows = append(rows, builtinRow(name))
	}

	r := report.Report{
		Title:    "shhh providers",
		Subject:  fmt.Sprintf("%d built in · %s", builtIn, countOf(len(profiles), "profile", "profiles")),
		Sections: []report.Section{{Header: "PROVIDERS", Rows: rows}},
	}
	if len(sources) > 0 {
		r.Sections = append(r.Sections, report.Section{Header: "SOURCES", Rows: sources})
	}
	for _, p := range profiles {
		r.Sections = append(r.Sections, profileSections(p, prices)...)
	}
	if len(profiles) == 0 {
		r.Sections = append(r.Sections, report.Section{Rows: []report.Row{
			report.Empty("no gateway profiles", wayOut),
		}})
	}
	return r
}

// profileWayOut names the file a first profile would be written to. A machine
// with no config directory at all — no HOME and no XDG_CONFIG_HOME, which is
// a bare container or a `su -` shell — has nowhere to name, and says the
// shape of the file instead of naming a path that does not exist.
func profileWayOut(dirs []string) string {
	files := profile.Files(dirs)
	if len(files) == 0 {
		return "set HOME or XDG_CONFIG_HOME first"
	}
	return "write " + files[0] + " to add one"
}

// builtinRow reads whether a built-in provider has a key to use, from the
// same variables the dialects read in the order they read them.
func builtinRow(name string) report.Row {
	vars := resolve.KeyVars(name)
	for _, v := range vars {
		if strings.TrimSpace(os.Getenv(v)) != "" {
			return report.Row{State: report.Pass, Name: name, Subject: "key in env", Detail: v}
		}
	}
	return report.Row{State: report.Fail, Name: name, Subject: "no key",
		Detail: strings.Join(vars, " or ") + " unset"}
}

// providerRow reads a gateway profile's readiness off its default endpoint: a
// literal key is ready, a named variable is ready when it is exported, and a
// profile that named neither is reachable only if the gateway wants no auth.
func providerRow(name string, e profile.Endpoint) report.Row {
	switch {
	case e.APIKey != "":
		return report.Row{State: report.Pass, Name: name, Subject: "literal api_key", Detail: e.BaseURL}
	case e.APIKeyEnv == "":
		return report.Row{State: report.Pass, Name: name, Subject: "no auth configured", Detail: e.BaseURL}
	case os.Getenv(e.APIKeyEnv) == "":
		return report.Row{State: report.Skip, Name: name, Subject: e.APIKeyEnv + " unset", Detail: e.BaseURL}
	}
	return report.Row{State: report.Pass, Name: name, Subject: "key in env", Detail: e.APIKeyEnv}
}

// sourceRows is the search order, each path with what was found at it — and
// each file that would not load as its own row, so a broken providers.toml is
// reported where it was read from and exactly once.
func sourceRows(dirs []string, profiles []profile.Profile, errs []error) []report.Row {
	held := map[string]int{}
	for _, p := range profiles {
		held[p.Path]++
	}
	var rows []report.Row
	for i, dir := range dirs {
		for _, path := range []string{profile.Files(dirs)[i], dir} {
			rows = append(rows, sourceRow(path, held))
		}
	}
	for _, err := range errs {
		path, detail := splitProfileError(err)
		// The fault goes on the consequence line rather than into the target:
		// the target is what clips when a path is long, and the reason a
		// profile did not load is the one thing this row exists to say.
		rows = append(rows, report.Row{State: report.Fail, Subject: path, Outcome: "would not load",
			Consequence: detail,
			Fix:         []string{"the other profiles still load; this file is skipped until it parses"}})
	}
	return rows
}

// sourceRow says what one search location holds. A directory counts the
// profiles read out of the files inside it.
func sourceRow(path string, held map[string]int) report.Row {
	info, err := os.Stat(path)
	if err != nil {
		return report.Row{State: report.Skip, Subject: path, Detail: "absent"}
	}
	n := held[path]
	if info.IsDir() {
		n = 0
		for p, count := range held {
			if strings.HasPrefix(p, path+string(os.PathSeparator)) {
				n += count
			}
		}
	}
	if n == 0 {
		return report.Row{State: report.Skip, Subject: path, Detail: "no profiles"}
	}
	return report.Row{State: report.Pass, Subject: path, Detail: countOf(n, "profile", "profiles")}
}

// splitProfileError separates the file a load error names from the rest of
// the sentence, so the path can lead the row the way every other source does.
func splitProfileError(err error) (string, string) {
	text := err.Error()
	if path, rest, ok := strings.Cut(text, ": "); ok {
		return path, rest
	}
	return text, ""
}

// profileSections is one profile as sections: where it points, what it
// declares, and what it rewrites. Each is a section rather than an indented
// sub-block because a mis-globbed route is exactly the quiet failure this
// listing exists to catch, and it has to be findable.
func profileSections(p profile.Profile, prices *pricing.Table) []report.Section {
	routes := p.Routes()
	head := report.Section{
		Header: "PROFILE " + p.Name,
		Pairs:  []report.Pair{{Key: "file", Value: p.Path}},
	}
	if len(routes) > 1 {
		head.Pairs = append(head.Pairs, report.Pair{Key: "routing",
			Value: fmt.Sprintf("%d endpoints; a model no endpoint claims goes to the default", len(routes))})
	}
	for _, e := range routes {
		head.Rows = append(head.Rows, endpointRow(e))
	}

	models := modelRows(routes, prices)
	if len(models) == 0 {
		// A profile that declares nothing leans entirely on the gateway's own
		// catalog, and gets no pricing or context metadata from it. That is a
		// reading, not an absence, so it is a row rather than a missing
		// section — the quiet failures are what this listing is for.
		models = []report.Row{{State: report.Skip, Subject: "none declared",
			Detail: "discovery only; no pricing or context metadata"}}
	}
	sections := []report.Section{head, {Header: "MODELS " + p.Name, Rows: models}}
	if rules := ruleRows(routes); len(rules) > 0 {
		sections = append(sections, report.Section{Header: "RULES " + p.Name, Rows: rules})
	}
	return sections
}

// endpointRow is one address: its dialect and where it points, with what it
// matches and how it authenticates behind them. The glyph is the auth, since
// that is the half of an endpoint that silently stops working.
func endpointRow(e profile.Endpoint) report.Row {
	row := providerRow(e.Label, e)
	row.Subject, row.Detail = e.API, e.BaseURL
	row.Outcome = endpointAuth(e)
	if len(e.Match) > 0 {
		row.Detail = joinDetail(row.Detail, "matches "+strings.Join(e.Match, ", "))
	}
	if e.DiscoveryOff() {
		row.Consequence = "catalog off — the declared models are the whole list"
	} else if e.ModelsPath != "" {
		row.Consequence = "catalog " + e.ModelsPath
	}
	if len(e.Headers) > 0 {
		keys := make([]string, 0, len(e.Headers))
		for k := range e.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		row.Fix = append(row.Fix, "headers "+strings.Join(keys, ", "))
	}
	return row
}

// endpointAuth is the outcome field: how this endpoint authenticates, in the
// fewest words that tell a set variable from an unset one.
func endpointAuth(e profile.Endpoint) string {
	switch {
	case e.APIKey != "":
		return "literal key"
	case e.APIKeyEnv == "":
		return "no auth"
	case os.Getenv(e.APIKeyEnv) == "":
		return e.APIKeyEnv + " unset"
	}
	return e.APIKeyEnv
}

// modelRows are the declared models across every endpoint, each with the
// metadata that is actually known. A figure the profile did not declare and
// the public table has never heard of is left out rather than printed as a
// dash: a stat that cannot be reported is not a stat
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
func modelRows(routes []profile.Endpoint, prices *pricing.Table) []report.Row {
	var rows []report.Row
	for _, e := range routes {
		for _, m := range e.Models {
			row := report.Row{State: report.Pass, Name: m.ID, Outcome: sourceCell(m, prices)}
			var facts []string
			if window := contextCell(m, prices); window != "" {
				facts = append(facts, window)
			}
			if price := priceCell(m, prices); price != "" {
				facts = append(facts, price)
			}
			if shape := reasoningCell(m, prices); shape != "" {
				facts = append(facts, shape)
			}
			if len(routes) > 1 {
				facts = append(facts, "via "+e.Label)
			}
			if len(facts) > 0 {
				row.Subject, row.Detail = facts[0], strings.Join(facts[1:], " · ")
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// ruleRows are the rewrites, with each rule's note as the consequence line
// under it — the note is why the rule exists, which is the thing a reader
// checking a gateway needs.
func ruleRows(routes []profile.Endpoint) []report.Row {
	var rows []report.Row
	for _, e := range routes {
		for _, r := range e.Rewrite {
			scope := "all models"
			if r.When.Model != "" {
				scope = r.When.Model
			}
			rows = append(rows, report.Row{
				State: report.Run, Name: r.Direction,
				Subject: joinDetail(r.Op, r.Path), Detail: scope,
				Outcome: e.Label, Consequence: r.Note,
			})
		}
	}
	return rows
}

// newProvidersMigrateCmd folds every profile file into one providers.toml.
// It writes nothing without being asked twice — once by being run, once by
// --prune before it removes anything it has replaced.
func newProvidersMigrateCmd() *cobra.Command {
	var prune bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Fold every gateway profile into one providers.toml",
		Long: "Read every profile this machine has — <config-dir>/providers.toml and <config-dir>/providers/*.toml, " +
			"in load order — and write them as one file of [[provider]] blocks. The originals are left in place " +
			"unless --prune is given.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{ownsProfileErrors: "yes"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			plan := profile.Plan(profile.Dirs(config.Paths()))

			r := report.Report{Title: "shhh providers migrate"}
			for _, err := range plan.Errors {
				path, detail := splitProfileError(err)
				r.Notes = append(r.Notes, report.Note{State: report.Fail,
					Text: joinDetail(path, detail) + " — left where it is"})
			}
			for _, s := range plan.Shadowed {
				r.Notes = append(r.Notes, report.Note{State: report.Skip,
					Text: fmt.Sprintf("%s in %s — %s already declares that name", s.Name, s.Path, s.Kept)})
			}

			switch {
			case len(plan.Profiles) == 0:
				r.Sections = append(r.Sections, report.Section{Rows: []report.Row{
					report.Empty("no gateway profiles to migrate", "shhh providers"),
				}})
				return report.Fprint(out, r)
			case !plan.NeedsWork():
				r.Subject = "already one file"
				r.Sections = append(r.Sections, report.Section{Rows: []report.Row{{
					State: report.Pass, Subject: plan.Target,
					Detail: countOf(len(plan.Profiles), "provider", "providers"),
				}}})
				return report.Fprint(out, r)
			}

			r.Subject = fmt.Sprintf("%s into %s · %s redundant",
				countOf(len(plan.Profiles), "provider", "providers"), plan.Target,
				countOf(len(plan.Redundant()), "file", "files"))
			folded := report.Section{Header: "FOLDING IN"}
			for _, p := range plan.Profiles {
				folded.Rows = append(folded.Rows, report.Row{State: report.Run, Name: p.Name, Subject: p.Path})
			}
			r.Sections = append(r.Sections, folded)

			if dryRun {
				r.Title = "shhh providers migrate — would write"
				r.Subject = ""
				r.Sections = append(r.Sections, report.Section{
					Header: plan.Target, Body: profile.Encode(plan.Profiles)})
				return report.Fprint(out, r)
			}
			if err := plan.Write(); err != nil {
				return err
			}
			written := report.Section{Rows: []report.Row{report.Done("wrote", plan.Target)}}
			if !prune {
				for _, path := range plan.Redundant() {
					written.Rows = append(written.Rows, report.Row{State: report.Skip,
						Subject: path, Detail: "still loads, now redundant"})
				}
				r.Notes = append(r.Notes, report.Note{State: report.Warn,
					Text: "`shhh providers migrate --prune` removes the redundant files"})
				r.Sections = append(r.Sections, written)
				return report.Fprint(out, r)
			}
			removed, pruneErr := plan.Prune()
			for _, path := range removed {
				written.Rows = append(written.Rows, report.Done("removed", path))
			}
			r.Sections = append(r.Sections, written)
			if err := report.Fprint(out, r); err != nil {
				return err
			}
			return pruneErr
		},
	}
	cmd.Flags().BoolVar(&prune, "prune", false, "remove the files the new providers.toml replaces")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the file that would be written and change nothing")
	return cmd
}

// The metadata cells fall back to the public pricing table, which is the
// point of the fallback: a profile only has to declare what the public table
// gets wrong or has never heard of. Each returns "" where nothing is known,
// because an unreportable stat is left out rather than dashed.

func contextCell(m profile.Model, prices *pricing.Table) string {
	window := m.ContextWindow
	if window == 0 && prices != nil {
		window, _ = prices.ContextWindow(m.ID)
	}
	switch {
	case window == 0:
		return ""
	case window >= 1_000_000:
		return fmt.Sprintf("%.1fM ctx", float64(window)/1_000_000)
	case window >= 1_000:
		return fmt.Sprintf("%dk ctx", window/1_000)
	}
	return fmt.Sprintf("%d ctx", window)
}

// priceCell is the pair of per-million-token prices as one field, in the unit
// model cards publish them in.
func priceCell(m profile.Model, prices *pricing.Table) string {
	if m.Cost.HasPricing() {
		return fmt.Sprintf("$%s / $%s", modelPrice(m.Cost.Input), modelPrice(m.Cost.Output))
	}
	if in, out, ok := publicCost(prices, m.ID); ok {
		return fmt.Sprintf("$%s / $%s", modelPrice(in), modelPrice(out))
	}
	return ""
}

// modelPrice is a per-million-token price as a model card writes one: the
// cents are dropped on a round figure, so a column of them reads as prices
// rather than as decimals. It is not the spend formatter — a price is what a
// model costs and a spend is what was paid, and they round differently.
func modelPrice(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.2f", v)
}

// reasoningCell is the shape a thinking level takes on the model and the
// rungs above high it has, from the profile when it said, otherwise from the
// table and the family floor — the same answer the providers act on.
func reasoningCell(m profile.Model, prices *pricing.Table) string {
	var caps provider.Capabilities
	if e, ok := prices.Entry(m.ID); ok && (e.ReasoningKnown || e.SupportsReasoning) {
		caps = provider.Capabilities{Known: true, Reasoning: e.SupportsReasoning, Adaptive: e.AdaptiveThinking,
			Legacy: e.LegacyThinking, AlwaysOn: e.ThinkingAlwaysOn, XHigh: e.XHighEffort, Max: e.MaxEffort}
	} else {
		caps = provider.CapabilitiesFor(m.ID)
	}
	switch {
	case !caps.Known:
		return ""
	case !caps.Reasoning:
		return "no thinking"
	}
	shape := "effort"
	if !caps.Adaptive && !caps.Legacy {
		// The public table marks some models reasoning-capable without
		// saying how; the family floor knows the shape, and it is what
		// the provider sends.
		if floor := provider.CapabilitiesFor(m.ID); floor.Adaptive || floor.Legacy {
			caps.Adaptive, caps.Legacy = floor.Adaptive, floor.Legacy
		}
	}
	if caps.Adaptive {
		shape = "adaptive"
	} else if caps.Legacy {
		shape = "budget"
	}
	var extra []string
	if caps.XHigh {
		extra = append(extra, "xhigh")
	}
	if caps.Max {
		extra = append(extra, "max")
	}
	if caps.AlwaysOn {
		extra = append(extra, "always on")
	}
	if len(extra) > 0 {
		shape += " (" + strings.Join(extra, ", ") + ")"
	}
	return shape
}

func sourceCell(m profile.Model, prices *pricing.Table) string {
	switch {
	case m.Cost.HasPricing():
		return "profile"
	case hasPublicCost(prices, m.ID):
		return "public table"
	default:
		return "unpriced"
	}
}

// publicCost reads a model's public per-million-token prices.
func publicCost(prices *pricing.Table, model string) (float64, float64, bool) {
	if prices == nil {
		return 0, 0, false
	}
	in, out, ok := prices.Cost(model, 1_000_000, 1_000_000)
	return in, out, ok
}

func hasPublicCost(prices *pricing.Table, model string) bool {
	_, _, ok := publicCost(prices, model)
	return ok
}
