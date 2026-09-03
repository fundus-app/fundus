package llm

import "context"

// Fake is a scripted provider for tests and for running without a model.
type Fake struct {
	ProviderName string
	Fn           func(ctx context.Context, req *Request) (*Response, error)
	Calls        []*Request
	// TranscribeFn makes the fake a Transcriber; nil means "cannot".
	TranscribeFn func(ctx context.Context, req *TranscribeRequest) (string, error)
	Transcribed  []*TranscribeRequest
}

// Transcribe implements Transcriber when TranscribeFn is set.
func (f *Fake) Transcribe(ctx context.Context, req *TranscribeRequest) (string, error) {
	f.Transcribed = append(f.Transcribed, req)
	if f.TranscribeFn == nil {
		return "", &Error{Provider: f.Name(), Message: "this provider cannot transcribe"}
	}
	return f.TranscribeFn(ctx, req)
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
