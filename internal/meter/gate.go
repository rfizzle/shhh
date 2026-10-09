package meter

// The provider gate. A gated provider is an ordinary provider.Provider that
// happens to tell the ledger what went through it, so a feature holding one
// needs to know nothing about spend accounting — which is the point. Wiring a
// feature to an ungated provider is the only way to escape the meter, and
// that is a visible thing to do at the one place providers are handed out.

import (
	"context"

	"github.com/rfizzle/shhh/internal/provider"
)

// gated wraps a provider, attributing every request to one origin. The model
// comes from each request's own options rather than from the session, because
// the classifier, the summary and a sub-agent all routinely run somewhere
// cheaper than the session model.
type gated struct {
	inner  provider.Provider
	ledger *Ledger
	origin Origin
	// fallbackModel prices a request whose options name no model, which is
	// what a caller that leaves the provider's default in place sends.
	fallbackModel string
}

// For returns a provider that bills everything it streams to source. Use
// ForOrigin where several requesters share a source and it matters which one
// spent.
func (l *Ledger) For(p provider.Provider, source Source) provider.Provider {
	return l.ForOrigin(p, Origin{Source: source})
}

// ForOrigin returns a provider that bills everything it streams to one named
// requester — a single sub-agent, rather than sub-agents as a class.
func (l *Ledger) ForOrigin(p provider.Provider, o Origin) provider.Provider {
	if p == nil {
		return nil
	}
	return (&gated{inner: p, ledger: l, origin: o}).wrap()
}

// wrap is the gate as a type that answers each optional interface the way
// its inner provider does.
//
// Wrapping must not cost the session a capability it had. The /model picker
// discovers a live catalogue by asking whether the provider implements
// ModelLister, and the classifier asks whether it implements Decider, so the
// gate has to answer both questions the same way its inner provider would —
// hence a type per combination rather than one that always implements both
// and returns nothing.
func (g *gated) wrap() provider.Provider {
	_, lists := g.inner.(provider.ModelLister)
	_, decides := g.inner.(provider.Decider)
	switch {
	case lists && decides:
		return &gatedListerDecider{gated: g}
	case lists:
		return &gatedLister{gated: g}
	case decides:
		return &gatedDecider{gated: g}
	}
	return g
}

// core is the gate under any of its types, or nil for a provider that is
// not one.
func core(p provider.Provider) *gated {
	switch g := p.(type) {
	case *gated:
		return g
	case *gatedLister:
		return g.gated
	case *gatedDecider:
		return g.gated
	case *gatedListerDecider:
		return g.gated
	}
	return nil
}

// WithFallbackModel names the model to price against when a request does not
// name one itself.
func WithFallbackModel(p provider.Provider, model string) provider.Provider {
	g := core(p)
	if g == nil {
		return p
	}
	next := *g
	next.fallbackModel = model
	return next.wrap()
}

func (g *gated) Name() string { return g.inner.Name() }

func (g *gated) StreamCompletion(ctx context.Context, messages []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	if err := g.ledger.AllowRequest(); err != nil {
		return nil, err
	}
	events, err := g.inner.StreamCompletion(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	model := opts.Model
	if model == "" {
		model = g.fallbackModel
	}

	out := make(chan provider.StreamEvent)
	go func() {
		defer close(out)
		for ev := range events {
			// Every provider reports usage once, on the event that ends the
			// stream, so this adds rather than replaces. A stream the caller
			// abandons still bills what the provider already reported: it was
			// spent whether or not anyone read the answer.
			if ev.Usage != nil {
				g.ledger.Record(g.origin, model, *ev.Usage)
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				// The reader is gone. Drain the rest so the provider's own
				// goroutine finishes and any usage it still owes is billed.
				for rest := range events {
					if rest.Usage != nil {
						g.ledger.Record(g.origin, model, *rest.Usage)
					}
				}
				return
			}
		}
	}()
	return out, nil
}

func (g *gated) listModels(ctx context.Context) ([]string, error) {
	return g.inner.(provider.ModelLister).ListModels(ctx)
}

func (g *gated) offersDecisions(model string) bool {
	return g.inner.(provider.Decider).OffersDecisions(model)
}

// decide is a Decisions API request through the gate: the same cap a
// completion meets, and the request billed to this gate's origin. The API
// charges for input alone, so only the input is recorded — whatever else a
// response's usage carries is not something anybody was billed for.
func (g *gated) decide(ctx context.Context, req provider.DecisionRequest) (provider.DecisionResult, error) {
	if err := g.ledger.AllowRequest(); err != nil {
		return provider.DecisionResult{}, err
	}
	res, err := g.inner.(provider.Decider).Decide(ctx, req)
	if err != nil {
		return res, err
	}
	model := req.Model
	if model == "" {
		model = g.fallbackModel
	}
	g.ledger.Record(g.origin, model, provider.Usage{PromptTokens: res.Usage.PromptTokens})
	return res, nil
}

// gatedLister is the gate over a provider that can enumerate its endpoint,
// gatedDecider over one that can answer the Decisions API, and
// gatedListerDecider over one that can do both.
type (
	gatedLister        struct{ *gated }
	gatedDecider       struct{ *gated }
	gatedListerDecider struct{ *gated }
)

func (g *gatedLister) ListModels(ctx context.Context) ([]string, error) { return g.listModels(ctx) }

func (g *gatedDecider) OffersDecisions(model string) bool { return g.offersDecisions(model) }
func (g *gatedDecider) Decide(ctx context.Context, req provider.DecisionRequest) (provider.DecisionResult, error) {
	return g.decide(ctx, req)
}

func (g *gatedListerDecider) ListModels(ctx context.Context) ([]string, error) {
	return g.listModels(ctx)
}
func (g *gatedListerDecider) OffersDecisions(model string) bool { return g.offersDecisions(model) }
func (g *gatedListerDecider) Decide(ctx context.Context, req provider.DecisionRequest) (provider.DecisionResult, error) {
	return g.decide(ctx, req)
}
