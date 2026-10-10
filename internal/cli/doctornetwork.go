package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/web"
)

func probeModel(ctx context.Context, cfg config.Config) doctorFinding {
	resolved := resolve.Resolve(resolve.Opts{
		ConfigProvider:  cfg.Provider.Default,
		ConfigModel:     cfg.Provider.Model,
		ConfigReasoning: cfg.Provider.Reasoning,
	})
	survey := resolve.SurveyPlaces(ctx, resolve.SurveyOpts{
		Provider:        resolved.Provider,
		ConfigAPIKey:    cfg.ProviderAPIKey(),
		ConfigAPIKeyEnv: cfg.Provider.APIKeyEnv,
		ConfigPaths:     config.Paths(),
	})
	f := doctorModelFinding(resolved.Provider, resolved.Model, survey)
	// A model decided by an env var or a flag looks exactly like one decided
	// by the config file, which is how `/model default` came to look broken
	// while writing the file correctly. The row that reports the
	// model is the row that has to say who chose it.
	//
	// The row reports provider.model's resolution, and each surface key that
	// is set beside it by name, because a surface that reads its own key runs
	// on a model this row would otherwise never mention.
	// See docs/capabilities/configuration.md#each-surface-can-have-a-model-of-its-own.
	//
	// A key the checkout's own file set is named with that file, in the words
	// `/model` uses, so a project's choice is not read as the person's own.
	proj := ProjectConfigFrom(ctx)
	var set []string
	if cfg.Provider.Model != "" {
		set = append(set, "provider.model = "+cfg.Provider.Model+setInProject("provider.model", proj))
	}
	var surfaces []string
	for _, s := range []string{config.SurfaceCmd, config.SurfaceChat, config.SurfaceCode} {
		if key, model := cfg.SurfaceModel(s); model != "" {
			surfaces = append(surfaces, key+" = "+model+setInProject(key, proj))
		}
	}
	over := resolve.ModelOutranks(resolve.Opts{ConfigModel: cfg.Provider.Model})
	switch {
	case over != "" && len(set)+len(surfaces) > 0:
		f.Detail = joinDetail(f.Detail, over+", overruling "+strings.Join(append(set, surfaces...), ", "))
	case len(surfaces) > 0:
		f.Detail = joinDetail(f.Detail, strings.Join(surfaces, ", ")+" ahead of provider.model")
	}
	// A reasoning level is the other half of what a request asks for,
	// and an unreadable one is a session that will fail to start rather than
	// quietly reason less than it was told to.
	if effort, err := provider.ParseEffort(resolved.Reasoning); err != nil {
		f.Detail = joinDetail(f.Detail, err.Error())
	} else if effort.On() {
		f.Detail = joinDetail(f.Detail, "reasoning "+effort.String())
	}
	return f
}

// probeFlows reads where every bounded call lands for a session started now:
// the provider and model the row above resolved, put through the same chain
// the calls themselves are sent with.
func probeFlows(_ context.Context, cfg config.Config) doctorFinding {
	resolved := resolve.Resolve(resolve.Opts{
		ConfigProvider: cfg.Provider.Default,
		ConfigModel:    cfg.Provider.Model,
	})
	answers := resolveFlows(cfg, resolved.Provider, resolved.Model)
	f := doctorFlows(answers)
	replaced := promptSource("classifier", cfg.Prompts.Classifier, projectPrompts()).path != ""
	for i, a := range answers {
		if a.flow.name == flowClassifier.name {
			f.Fix[i] += classifierBackendNote(cfg, resolved.Provider, a.model, replaced)
		}
	}
	return f
}

// classifierBackendNote is what the classifier's line adds: the backend it
// is asked on, and on the decisions backend whether the model it resolved to
// offers the API — a model that does not is a classifier whose every verdict
// fails closed — and that a replaced wording is not sent there, since it is
// written for a reply that backend does not give. Which models offer it is
// read from the providers' own declarations, the same answer the classifier
// gets when it asks, without building a provider to ask it.
// See docs/capabilities/providers.md#a-bounded-call-asks-for-the-shape-of-its-answer.
func classifierBackendNote(cfg config.Config, provName, model string, replaced bool) string {
	if !decisionsClassifier(cfg) {
		return " · " + agent.BackendCompletion + " backend"
	}
	note := " · " + agent.BackendDecisions + " backend, "
	if provider.DecisionsDeclared(provName, model) {
		note += "offered by this model"
	} else {
		note += "not offered by this model, so every verdict fails closed"
	}
	if replaced {
		note += " · prompts.classifier goes unused there"
	}
	return note
}

// doctorFlows is that reading. A second model on the bill is the question it
// answers, so the row names the models and which link of the chain put each
// flow on one — flow key, cheap key, provider small model, session model —
// and a provider with no small model (a gateway profile, a local endpoint)
// reads as flows on the session's own rather than as nothing.
// See docs/capabilities/providers.md#a-bounded-call-runs-on-the-small-model.
func doctorFlows(answers []flowModel) doctorFinding {
	var models []string
	steps := map[flowStep]int{}
	lines := make([]string, 0, len(answers))
	for _, a := range answers {
		name := a.model
		if name == "" {
			name = "the session's own"
		}
		if !slices.Contains(models, name) {
			models = append(models, name)
		}
		steps[a.step]++
		line := a.flow.name + " — " + name + " · " + a.step.String()
		if a.key != "" {
			line += " " + a.key
		}
		if a.flow.window && a.step != stepSessionModel {
			line += " · when its window holds the conversation"
		}
		lines = append(lines, line)
	}
	subject := countOf(len(models), "model", "models")
	if len(models) == 1 {
		subject = models[0]
	}
	var tally []string
	for _, s := range []flowStep{stepFlowKey, stepCheapKey, stepSmallModel, stepSessionModel} {
		if n := steps[s]; n > 0 {
			tally = append(tally, fmt.Sprintf("%d %s", n, s))
		}
	}
	return doctorFinding{
		Subject:  subject,
		Detail:   strings.Join(tally, " · "),
		Outcome:  "ok",
		Fix:      lines,
		FixLabel: "show the model each flow runs on",
	}
}

// probeSearch reads the web_search backend, and reaches only the one that is
// somebody's own machine. Brave is a paid endpoint on the far side of the
// internet and a diagnostic does not spend a request on it — the model row
// above already reports a key as found rather than as accepted, for the same
// reason. A SearXNG instance is the person's own host, and whether it will
// answer in JSON is not something this side can know without asking.
func probeSearch(ctx context.Context, cfg config.Config) doctorFinding {
	if ctx == nil {
		ctx = context.Background()
	}
	backend := cfg.Web.SearchProvider
	if backend == "" {
		backend = web.ProviderBrave
	}
	switch backend {
	case web.ProviderBrave:
		return doctorSearchBrave(cfg.WebSearchAPIKey() != "")
	case web.ProviderSearXNG:
		endpoint := strings.TrimSpace(cfg.Web.SearchURL)
		if endpoint == "" {
			return doctorSearchNoInstance()
		}
		return doctorSearchInstance(endpoint, searxngCheck(ctx, endpoint))
	}
	return doctorSearchUnknown(backend)
}

// probeHosts reads the host lists a fetch is judged beside: where they are
// cached, and how old each one is. It opens the reading the session would
// and downloads nothing — the row reports what a fetch would find.
func probeHosts(_ context.Context, cfg config.Config) doctorFinding {
	return doctorHosts(hostListsDir(), openReputation(cfg).Lists())
}

// doctorHosts is the row over each list's state: shhh's own, and each
// download's age or why it is not answering. A list older than its window is
// a warning and not a failure — a
// list that cannot answer reads every host as unknown, which is the reading
// that changes nothing
// (docs/capabilities/approvals-and-safety.md#a-host-is-read-against-the-world-before-it-is-judged).
func doctorHosts(dir string, lists []web.ListState) doctorFinding {
	parts := make([]string, 0, len(lists))
	var quiet []string
	for _, st := range lists {
		var word string
		switch {
		case st.Off:
			word = "off"
		case st.From == "binary":
			word = "built in"
		case st.Stale:
			word = "stale " + hostListAge(st.Age)
			quiet = append(quiet, st.Name)
		case st.From == "snapshot":
			word = "shipped " + hostListAge(st.Age)
		case st.From == "download":
			word = hostListAge(st.Age)
		default:
			word = "not fetched"
		}
		// A failed refresh is named, and it is not a list that cannot answer:
		// the copy on disk goes on answering until its window closes, which is
		// what Stale says.
		if st.Failed {
			word += ", last download failed"
		}
		parts = append(parts, st.Name+" "+word)
	}
	subject := "nowhere to cache them"
	if dir != "" {
		subject = shortPath(dir)
	}
	f := doctorFinding{Subject: subject, Detail: strings.Join(parts, " · "), Outcome: "ok"}
	if len(quiet) > 0 {
		f.Outcome, f.State = "stale", components.DoctorWarned
		f.Consequence = strings.Join(quiet, ", ") + " cannot answer; a host only it would have named reads as unknown"
		f.FixLabel = "how a list is fetched"
		f.Fix = []string{
			"a list is downloaded behind the first fetch a session decides once its copy is older than its refresh",
			"a failed download is tried again an hour later; web.reputation_off turns a list off",
		}
	}
	return f
}

// hostListAge is a list's age in the unit a reader compares: hours inside
// two days, days after.
func hostListAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "<1h"
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// searxngCheck is the instance request, a variable so the suite can answer
// for an instance without one running — the same shape the search endpoint
// override takes.
var searxngCheck = web.CheckSearXNG

func doctorSearchBrave(haveKey bool) doctorFinding {
	if !haveKey {
		// A machine with no key is not broken: it has no search. The row
		// states what is not there rather than claiming a fault, the way the
		// container-engine row does for a sandbox nothing asked for.
		return doctorFinding{
			Subject:     "no web search",
			Outcome:     "off",
			State:       components.DoctorSkipped,
			Consequence: "web_search is not registered; a session reads the URLs it is given and the workspace",
			FixLabel:    "show the two backends",
			Fix: []string{
				"shhh config set --global web.search_api_key_env BRAVE_API_KEY   a paid key, named rather than held",
				"shhh config set --global web.search_provider searxng            an instance you run, which takes none",
			},
		}
	}
	return doctorFinding{Subject: web.ProviderBrave, Detail: "key found", Outcome: "ok"}
}

func doctorSearchNoInstance() doctorFinding {
	return doctorFinding{
		Subject: "searxng names no instance", Outcome: "unconfigured",
		State:       components.DoctorWarned,
		Consequence: "web_search is not registered, and the session reads only URLs it is given",
		FixLabel:    "show the setting to fill in",
		Fix:         []string{"shhh config set --global web.search_url https://searx.example.org/search"},
	}
}

// doctorSearchInstance reports what the instance answered. An instance that
// serves its results page instead of JSON is the failure this row exists
// for: the search itself would come back holding nothing, which reads as a
// web with no answer on it rather than as a setting one line away
// (docs/capabilities/evidence.md#search-has-more-than-one-backend).
func doctorSearchInstance(endpoint string, err error) doctorFinding {
	switch {
	case err == nil:
		return doctorFinding{Subject: shortURL(endpoint), Detail: "answers JSON", Outcome: "ok"}
	case errors.Is(err, web.ErrSearXNGFormat):
		return doctorFinding{
			Subject: shortURL(endpoint), Detail: "answers HTML, not JSON", Outcome: "wrong format",
			State:       components.DoctorWarned,
			Consequence: "every search comes back empty, as though the web held nothing",
			FixLabel:    "show the instance setting to change",
			Fix: []string{
				"in the instance's settings.yml, list json under search.formats:",
				"    search:",
				"      formats: [html, json]",
				"then restart the instance",
			},
		}
	}
	return doctorFinding{
		Subject: shortURL(endpoint), Detail: err.Error(), Outcome: "unreachable",
		State:       components.DoctorWarned,
		Consequence: "every search fails until the instance answers",
		FixLabel:    "show the place to check",
		Fix:         []string{"curl -s " + shortURL(endpoint) + "?q=shhh&format=json | head -c 200"},
	}
}

func doctorSearchUnknown(name string) doctorFinding {
	return doctorFinding{
		Subject: "unknown search backend", Detail: name, Outcome: "unusable",
		State:       components.DoctorWarned,
		Consequence: "web_search is not registered at all",
		FixLabel:    "show the backends there are",
		Fix: []string{
			"shhh config set --global web.search_provider " + web.ProviderBrave + "     a paid key",
			"shhh config set --global web.search_provider " + web.ProviderSearXNG + "   an instance you run",
		},
	}
}

// shortURL is a URL cut to the row's width, keeping the host and the head of
// the path — which is what tells two instances apart.
func shortURL(raw string) string {
	const max = 48
	if len(raw) <= max {
		return raw
	}
	return raw[:max-1] + "…"
}

// doctorModelFinding reads the same walk the no-provider card reads:
// the four places a key can come from, and what was in each. A key that was
// found is reported as found and not as accepted — accepting one means
// spending a request on it, and a diagnostic that billed you for running it
// would be a diagnostic nobody runs.
//
// The check is named `model` rather than `provider` because the verb field is
// eight columns and `provider` fills all eight, leaving the target beside it
// with no gap; `model` is the verb a failure row already gives a provider
// failure, so the two rows line up.
func doctorModelFinding(providerName, model string, survey resolve.Survey) doctorFinding {
	f := doctorFinding{Subject: providerName}
	if model != "" {
		f.Detail = model
	}
	for _, place := range survey.Places {
		if !place.Found {
			continue
		}
		switch place.Kind {
		case resolve.PlaceEnv, resolve.PlaceConfig:
			f.Detail = joinDetail(f.Detail, "key "+doctorMasked(place.Finding)+" found")
			f.Outcome = "ok"
			return f
		case resolve.PlaceProfiles:
			f.Detail = joinDetail(f.Detail, "gateway profile "+place.Finding+" is ready")
			f.Outcome = "ok"
			return f
		case resolve.PlaceLocal:
			f.Detail = joinDetail(f.Detail, "a local runtime is answering on "+place.Finding)
			f.Outcome = "ok"
			return f
		}
	}
	f.Detail = joinDetail(f.Detail, "no key in any of the four places")
	f.Outcome = "no key"
	f.State = components.DoctorFailed
	f.Consequence = "no session will start until a key is found — every one exits on \"no provider\""
	f.FixLabel = "show the four places shhh looks"
	f.Fix = doctorKeyPlaces(survey)
	return f
}

// doctorKeyPlaces is the fix behind `[enter]` on a provider with no key: the same
// four places, each with what was there. It is the card's own body
// written as lines, because a fix that only said "set an API key" would be
// telling the reader something they already knew.
func doctorKeyPlaces(survey resolve.Survey) []string {
	lines := make([]string, 0, len(survey.Places)+1)
	for _, place := range survey.Places {
		detail := place.Detail
		if place.Found {
			detail = doctorMasked(place.Finding)
			if place.Detail != "" {
				detail += " · " + place.Detail
			}
		}
		lines = append(lines, fmt.Sprintf("%-9s %s", string(place.Kind), detail))
	}
	if survey.Likely != "" {
		lines = append(lines, "likely    "+survey.Likely)
	}
	return lines
}

// doctorMasked keeps a secret masked wherever the survey already masked it.
// The survey reports a key by its last four characters and never by more
// , and this is the one place in the report a key is mentioned at all,
// so it is worth saying that the masking is inherited rather than reapplied.
func doctorMasked(finding string) string { return finding }
