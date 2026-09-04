package llm

import (
	"fmt"

	"github.com/fundus-app/fundus/internal/config"
)

// Registry resolves provider names from configuration.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry instantiates every configured provider. fakeFactory builds the
// "fake" provider type (it lives outside this package because it needs to know
// the triage output shape).
func NewRegistry(cfg *config.Config, fakeFactory func(name string) Provider) (*Registry, error) {
	r := &Registry{providers: map[string]Provider{}}
	for name, pc := range cfg.Providers {
		switch pc.Type {
		case "openai":
			r.providers[name] = NewOpenAI(OpenAIOptions{
				Name:          name,
				BaseURL:       pc.BaseURL,
				APIKey:        pc.ResolveAPIKey(),
				Headers:       pc.Headers,
				Structured:    StructuredMode(pc.Structured),
				Transcription: pc.TranscriptionMode(),
				WebSearch:     pc.WebSearchMode(),
			})
		case "fake":
			if fakeFactory == nil {
				return nil, fmt.Errorf("provider %s: fake provider not available", name)
			}
			r.providers[name] = fakeFactory(name)
		default:
			return nil, fmt.Errorf("provider %s: unknown type %q", name, pc.Type)
		}
	}
	return r, nil
}

// Get returns a provider by name.
func (r *Registry) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", name)
	}
	return p, nil
}

// Add registers a provider, replacing any with the same name.
func (r *Registry) Add(p Provider) { r.providers[p.Name()] = p }

// Names lists configured providers.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.providers))
	for n := range r.providers {
		out = append(out, n)
	}
	return out
}
