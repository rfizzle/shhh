package cli

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// sessionEnv is the provider-and-prompt setup shared by the interactive chat
// TUI and headless print mode: resolved model, initial messages, and a stream
// closure over the session's provider.
// modelListTimeout bounds a query to the endpoint's own catalog — the
// /model picker's, and the window probe below it; a gateway that is slow or
// down should cost the user a beat, not the session.
const modelListTimeout = 10 * time.Second

// modelListerFor adapts a provider that can enumerate its endpoint into the
// chat model's lazy lister. Providers without the capability return nil, and
// the picker keeps the curated catalog.
func modelListerFor(p provider.Provider) func(context.Context) ([]string, error) {
	lister, ok := p.(provider.ModelLister)
	if !ok {
		return nil
	}
	return func(ctx context.Context) ([]string, error) {
		ctx, cancel := context.WithTimeout(ctx, modelListTimeout)
		defer cancel()
		return lister.ListModels(ctx)
	}
}

// endpointModels is the endpoint's own model list, asked lazily and shared
// by the two things that want it: the /model picker, and the check on a
// model a spawn names. Neither asks at startup, and a list one of them read
// is the other's without a second request.
//
// A failure is remembered for the spawn's check and not for the picker. A
// spawn that could not check is let through with a note, and asking again
// on every later spawn would put the same wait in front of each of them;
// the picker is a person asking, and a person who opens it again after a
// failure is asking to be asked again — which is what it has always done.
// See docs/capabilities/subagents.md#the-model-is-offered-the-models-it-can-name.
type endpointModels struct {
	fetch func(context.Context) ([]string, error)

	// mu is held across the request, so a spawn and the picker asking at
	// once make one request between them rather than two.
	mu       sync.Mutex
	listed   bool
	names    []string
	spawnErr error
}

// newEndpointModels shares fetch, or answers nil where there is nothing to
// ask, which the picker reads as a provider with no list of its own.
func newEndpointModels(fetch func(context.Context) ([]string, error)) *endpointModels {
	if fetch == nil {
		return nil
	}
	return &endpointModels{fetch: fetch}
}

// picker is the list as the /model picker's lazy lister, nil where there is
// no endpoint to ask.
func (e *endpointModels) picker() func(context.Context) ([]string, error) {
	if e == nil {
		return nil
	}
	return func(ctx context.Context) ([]string, error) { return e.list(ctx, false) }
}

// forSpawn is the list as the spawn's check reads it: asked at most once,
// bounded by the lister's own timeout, and a failure kept.
func (e *endpointModels) forSpawn() ([]string, error) {
	return e.list(context.Background(), true)
}

func (e *endpointModels) list(ctx context.Context, spawn bool) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.listed {
		return e.names, nil
	}
	if spawn && e.spawnErr != nil {
		return nil, e.spawnErr
	}
	names, err := e.fetch(ctx)
	if err != nil {
		if spawn {
			e.spawnErr = err
		}
		return nil, err
	}
	e.listed, e.names = true, names
	return names, nil
}

// endpointWindowsFor asks an endpoint that can report the context length it
// serves each model at, and hands the session a lookup over the answer.
// Providers without the capability return nil and the session reads the
// table.
//
// The query runs once, in the background, and the lookup answers "not known"
// until it lands: the session asks for the window on every frame, so it
// cannot be a question that waits on a network — and nothing goes wrong while
// the answer is missing, because the table and the family floor are behind it
// and the first trim is many turns away in any case. A failure is dropped for
// the same reason it is not logged: nobody asked for this.
func endpointWindowsFor(p provider.Provider) func(string) (int64, bool) {
	endpoint, ok := p.(provider.ModelWindower)
	if !ok {
		return nil
	}
	var (
		mu      sync.RWMutex
		windows map[string]int64
	)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), modelListTimeout)
		defer cancel()
		got, err := endpoint.ModelWindows(ctx)
		if err != nil || len(got) == 0 {
			return
		}
		mu.Lock()
		windows = got
		mu.Unlock()
	}()
	return func(model string) (int64, bool) {
		mu.RLock()
		defer mu.RUnlock()
		w, ok := windows[strings.ToLower(model)]
		return w, ok
	}
}
