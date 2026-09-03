package llm

import "context"

// Fake is a scripted provider for tests and for running without a model.
type Fake struct {
	ProviderName string
	Fn           func(ctx context.Context, req *Request) (*Response, error)
	Calls        []*Request
}

func (f *Fake) Name() string {
	if f.ProviderName == "" {
		return "fake"
	}
	return f.ProviderName
}

func (f *Fake) Complete(ctx context.Context, req *Request) (*Response, error) {
	f.Calls = append(f.Calls, req)
	if f.Fn == nil {
		return &Response{Content: "{}", Model: "fake"}, nil
	}
	return f.Fn(ctx, req)
}
