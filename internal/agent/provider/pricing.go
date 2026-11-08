package provider

import "context"

// Pricing fills in the cost of calls the API did not price itself, from a
// model registry: Anthropic's API reports none, nor do most local servers.
type Pricing struct {
	Provider
	Registry *Registry
	// Entry is the agent.providers name, for entries scoped to it.
	Entry string
}

// WithPricing wraps p; a nil registry uses hm's own table.
func WithPricing(p Provider, models *Registry, name string) Provider {
	if models == nil {
		models = Builtin()
	}
	return &Pricing{Provider: p, Registry: models, Entry: name}
}

func (p *Pricing) Stream(ctx context.Context, req Request, onEvent func(Event)) (Response, error) {
	resp, err := p.Provider.Stream(ctx, req, onEvent)
	if err != nil || resp.Usage.Cost != 0 {
		return resp, err
	}
	// The model that answered first: a fallback may cost differently.
	for _, model := range []string{resp.Model, req.Model} {
		if model == "" {
			continue
		}
		if m, ok := p.Registry.Lookup(p.Entry, model); ok && m.Price.known() {
			resp.Usage.Cost = m.Cost(resp.Usage)
			break
		}
	}
	return resp, nil
}
