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
	g := &gated{inner: p, ledger: l, origin: o}
	// Wrapping must not cost the session a capability it had. The /model
	// picker discovers a live catalogue by asking whether the provider
	// implements ModelLister, so the gate has to answer that question the
	// same way its inner provider would — hence two types rather than one
	// that always implements it and returns nothing.
	if _, ok := p.(provider.ModelLister); ok {
		return &gatedLister{gated: g}
	}
	return g
}

// WithFallbackModel names the model to price against when a request does not
// name one itself.
func WithFallbackModel(p provider.Provider, model string) provider.Provider {
	switch g := p.(type) {
	case *gatedLister:
		next := *g.gated
		next.fallbackModel = model
		return &gatedLister{gated: &next}
	case *gated:
		next := *g
		next.fallbackModel = model
		return &next
	}
	return p
}

func (g *gated) Name() string { return g.inner.Name() }

func (g *gated) StreamCompletion(ctx context.Context, messages []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
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

// gatedLister is the gate over a provider that can enumerate its endpoint.
type gatedLister struct{ *gated }

func (g *gatedLister) ListModels(ctx context.Context) ([]string, error) {
	return g.inner.(provider.ModelLister).ListModels(ctx)
}
