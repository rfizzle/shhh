package resolve

// Where shhh looked for a provider, and what it found there (S-106,
// DESIGN-TUI.md §17b).
//
// "SHHH_API_KEY or OPENAI_API_KEY is not set" is true and useless: it names
// two of the four places and none of the findings, so the reader has to go and
// check each one by hand — which is exactly the work the program just did.
// Survey does the same walk the resolution does and reports it, so the card
// can name every place, say what was there, and point at the one that is
// probably the fix.
//
// The walk is deliberately cheap: three environment lookups, a stat of each
// config path, a listing of the profile directories, and one bounded request
// to a local endpoint. It runs once, on a session that has already failed to
// start, and never on the happy path.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/profile"
)

// localProbeTimeout bounds the request to a local model runtime. It is a
// loopback request to a port that is either listening or not, so a quarter of
// a second is generous; a machine where it is not is a machine where waiting
// longer would not have helped either.
const localProbeTimeout = 250 * time.Millisecond

// DefaultLocalBaseURL is the endpoint a local runtime serves by default —
// Ollama's, which is also what the openai-compatible provider defaults to.
const DefaultLocalBaseURL = "http://localhost:11434/v1"

// PlaceKind names one place in the search, so a caller can act on a finding
// rather than re-parse its label.
type PlaceKind string

const (
	// PlaceEnv is SHHH_API_KEY and the provider's own variable.
	PlaceEnv PlaceKind = "env"
	// PlaceConfig is the config files, in search order.
	PlaceConfig PlaceKind = "config"
	// PlaceProfiles is the gateway profile directories (S-084).
	PlaceProfiles PlaceKind = "profiles"
	// PlaceLocal is a model runtime already listening on this machine.
	PlaceLocal PlaceKind = "local"
)

// Place is one place that was looked in. Found means something was there —
// not that it worked, which is a different question and one only a request
// can answer.
type Place struct {
	Kind PlaceKind
	// Found says whether the place held anything at all.
	Found bool
	// Finding is what was there, in a few words: the endpoint that answered,
	// the profile that loaded. Empty when nothing was.
	Finding string
	// Detail is the rest of the sentence — the variables that were unset, the
	// paths that were checked, the models the endpoint serves.
	Detail string
}

// Survey is the whole walk: every place, in search order, plus the sentence
// naming the likely fix and the local endpoint if one answered.
type Survey struct {
	// Provider is the provider name the session resolved to, which is what
	// decides the second environment variable that was checked.
	Provider string
	Places   []Place
	// Likely names the place that is probably the way in, phrased as a
	// sentence. Empty when nothing found was close enough to point at.
	Likely string
	// LocalBaseURL is the endpoint that answered, for the offer to use it.
	// Empty when nothing did.
	LocalBaseURL string
	// LocalModel is the first model that endpoint serves, so the offer can
	// name what it would start on rather than promising a model list.
	LocalModel string
}

// SurveyOpts is what the walk needs that it cannot read off the machine: the
// provider that was being resolved, whether the config file supplied a key,
// and where to look for config and profiles.
type SurveyOpts struct {
	// Provider is the resolved provider name; empty falls back to the default.
	Provider string
	// ConfigAPIKey is the key the loaded config supplied, if any.
	ConfigAPIKey string
	// ConfigPaths are the config files in search order (config.Paths()).
	ConfigPaths []string
	// LocalBaseURL overrides the endpoint probed for a local runtime.
	LocalBaseURL string
	// HTTPClient overrides the client the local probe uses.
	HTTPClient *http.Client
}

// keyVars are the environment variables a provider's key can come from, in
// the order the dialects read them.
func keyVars(providerName string) []string {
	vars := []string{"SHHH_API_KEY"}
	switch providerName {
	case "anthropic":
		vars = append(vars, "ANTHROPIC_API_KEY")
	case "gemini":
		vars = append(vars, "GEMINI_API_KEY")
	case "openrouter":
		vars = append(vars, "OPENROUTER_API_KEY")
	case "openai", "openai-responses":
		vars = append(vars, "OPENAI_API_KEY")
	}
	return vars
}

// SurveyPlaces runs the walk. It never returns an error: every place that
// could not be read is a finding of its own, and a survey that failed to
// report would leave the card saying nothing at all.
func SurveyPlaces(ctx context.Context, opts SurveyOpts) Survey {
	providerName := opts.Provider
	if providerName == "" {
		providerName = DefaultProvider
	}
	s := Survey{Provider: providerName}
	s.Places = append(s.Places,
		surveyEnv(providerName),
		surveyConfig(opts),
		surveyProfiles(opts),
		surveyLocal(ctx, opts, &s),
	)
	s.Likely = likelyFix(s)
	return s
}

// surveyEnv reports which of the variables the dialects read are set, naming
// the ones that are not. A set variable is reported by its name and the last
// four characters of its value — enough to tell one key from another, never
// enough to be one.
func surveyEnv(providerName string) Place {
	vars := keyVars(providerName)
	var set, unset []string
	for _, name := range vars {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			set = append(set, name+" ···"+tail(value))
			continue
		}
		unset = append(unset, name)
	}
	if len(set) > 0 {
		place := Place{Kind: PlaceEnv, Found: true, Finding: strings.Join(set, ", ")}
		if len(unset) > 0 {
			place.Detail = strings.Join(unset, ", ") + " unset"
		}
		return place
	}
	return Place{Kind: PlaceEnv, Detail: strings.Join(unset, ", ") + " — unset"}
}

// surveyConfig reports the config file that supplied a key, or every path
// that was checked and what was wrong with each: absent, or present with no
// provider key in it.
func surveyConfig(opts SurveyOpts) Place {
	if key := strings.TrimSpace(opts.ConfigAPIKey); key != "" {
		return Place{Kind: PlaceConfig, Found: true, Finding: "provider.api_key ···" + tail(key)}
	}
	if len(opts.ConfigPaths) == 0 {
		return Place{Kind: PlaceConfig, Detail: "no config directory on this machine"}
	}
	for _, path := range opts.ConfigPaths {
		if _, err := os.Stat(path); err == nil {
			return Place{Kind: PlaceConfig, Detail: display(path) + " — no provider api_key"}
		}
	}
	return Place{Kind: PlaceConfig, Detail: display(opts.ConfigPaths[0]) + " — no such file"}
}

// surveyProfiles reports the gateway profiles that loaded (S-084) and whether
// each one's key is resolvable, because a profile with an unexported variable
// is the failure that looks most like no provider at all.
func surveyProfiles(opts SurveyOpts) Place {
	dirs := profile.Dirs(opts.ConfigPaths)
	loaded := profile.Loaded()
	if len(loaded) == 0 {
		loaded, _ = profile.Load(dirs)
	}
	if len(loaded) == 0 {
		// Two places hold profiles now — the one file and the directory
		// beside it — and naming only one of them would send someone to
		// write a file where shhh is not the one that looks first.
		detail := "no " + strings.Join(displayAll(profile.Files(dirs)), ", ") + " or .toml in " + strings.Join(displayAll(dirs), ", ")
		if len(dirs) == 0 {
			detail = "no profile directory on this machine"
		}
		return Place{Kind: PlaceProfiles, Detail: detail}
	}
	var ready, missing []string
	for _, p := range loaded {
		if p.Key() != "" {
			ready = append(ready, p.Name)
			continue
		}
		if p.APIKeyEnv != "" {
			missing = append(missing, p.Name+" ("+p.APIKeyEnv+" unset)")
			continue
		}
		ready = append(ready, p.Name)
	}
	sort.Strings(ready)
	sort.Strings(missing)
	if len(ready) > 0 {
		place := Place{Kind: PlaceProfiles, Found: true, Finding: strings.Join(ready, ", ")}
		if len(missing) > 0 {
			place.Detail = "also " + strings.Join(missing, ", ")
		}
		return place
	}
	return Place{Kind: PlaceProfiles, Detail: strings.Join(missing, ", ")}
}

// surveyLocal asks a local model runtime whether it is there. The probe is
// the same GET /models the openai-compatible dialect uses for its catalog, so
// an endpoint that answers it is an endpoint shhh can already talk to.
func surveyLocal(ctx context.Context, opts SurveyOpts, s *Survey) Place {
	baseURL := opts.LocalBaseURL
	if baseURL == "" {
		baseURL = DefaultLocalBaseURL
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: localProbeTimeout}
	}
	models, err := probeLocal(ctx, client, baseURL)
	if err != nil {
		return Place{Kind: PlaceLocal, Detail: hostOf(baseURL) + " — nothing listening"}
	}
	s.LocalBaseURL = baseURL
	place := Place{Kind: PlaceLocal, Found: true, Finding: hostOf(baseURL)}
	if len(models) == 0 {
		place.Detail = "answering, but serving no models yet"
		return place
	}
	s.LocalModel = models[0]
	place.Detail = strings.Join(models[:min(len(models), 3)], ", ")
	if len(models) > 3 {
		place.Detail += fmt.Sprintf(", +%d more", len(models)-3)
	}
	return place
}

// probeLocal reads an openai-compatible catalog, bounded by the caller's
// client timeout and by the context it was given.
func probeLocal(ctx context.Context, client *http.Client, baseURL string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, localProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return decodeModelIDs(resp)
}

// likelyFix picks the place to point at: the first one that found something,
// phrased as what to do about it. Nothing found means nothing to point at,
// and the card says so by omission rather than by guessing.
func likelyFix(s Survey) string {
	for _, p := range s.Places {
		if !p.Found {
			continue
		}
		switch p.Kind {
		case PlaceEnv:
			return "a key is exported but " + s.Provider + " still refused to start — check it is the right provider's key"
		case PlaceConfig:
			return "the config file has a key but " + s.Provider + " still refused to start — check provider.default"
		case PlaceProfiles:
			return "a gateway profile is ready — start on it with --provider " + p.Finding
		case PlaceLocal:
			return "the local runtime is already answering — that is the quickest way in"
		}
	}
	return ""
}

// tail is the last four characters of a secret.
func tail(secret string) string {
	runes := []rune(strings.TrimSpace(secret))
	if len(runes) > 4 {
		runes = runes[len(runes)-4:]
	}
	return string(runes)
}

// display shortens a path under the home directory to ~/…, because the card
// is read at a glance and an absolute path under a long username is not.
func display(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rel, relErr := filepath.Rel(home, path); relErr == nil && !strings.HasPrefix(rel, "..") {
		return "~" + string(filepath.Separator) + rel
	}
	return path
}

func displayAll(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, display(p))
	}
	return out
}

// hostOf is the endpoint without its scheme or path — `localhost:11434`.
func hostOf(baseURL string) string {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")
	if i := strings.Index(trimmed, "/"); i > 0 {
		trimmed = trimmed[:i]
	}
	return trimmed
}

// decodeModelIDs reads the ids out of an openai-compatible catalog. Only the
// wrapped shape is accepted here: this is a probe, not the discovery path
// (internal/profile owns the shapes gateways return), and a body that is not
// this shape is a body from something that is not a model runtime.
func decodeModelIDs(resp *http.Response) ([]string, error) {
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
