package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// transcribeServer fakes both transcription paths of an OpenAI-compatible
// endpoint and records what it received.
func transcribeServer(t *testing.T, audioStatus int) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/audio/transcriptions":
			if audioStatus != 200 {
				w.WriteHeader(audioStatus)
				_, _ = io.WriteString(w, `{"error":{"message":"no such endpoint"}}`)
				return
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("multipart: %v", err)
			}
			f, hdr, err := r.FormFile("file")
			if err != nil {
				t.Errorf("file: %v", err)
				return
			}
			data, _ := io.ReadAll(f)
			seen = append(seen, "audio:"+r.FormValue("model")+":"+hdr.Filename+":"+string(data)+":"+r.FormValue("prompt")+":"+r.FormValue("language"))
			_, _ = io.WriteString(w, `{"text":"  hello from audio  "}`)
		case "/chat/completions":
			var body struct {
				Model    string `json:"model"`
				Messages []struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			parts := string(body.Messages[len(body.Messages)-1].Content)
			seen = append(seen, "chat:"+body.Model+":"+parts)
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hello from chat"}}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestTranscribeAudioEndpoint(t *testing.T) {
	srv, seen := transcribeServer(t, 200)
	p := NewOpenAI(OpenAIOptions{Name: "t", BaseURL: srv.URL, APIKey: "k", Transcription: "audio"})
	text, err := p.Transcribe(context.Background(), &TranscribeRequest{Model: "m", Audio: []byte("RIFFwav"), MIME: "audio/wav", Language: "en", Hints: []string{"Fundus", "Deye"}})
	if err != nil || text != "hello from audio" {
		t.Fatalf("got %q, %v", text, err)
	}
	if len(*seen) != 1 || (*seen)[0] != "audio:m:audio.wav:RIFFwav:Fundus, Deye:en" {
		t.Fatalf("server saw %v", *seen)
	}
}

func TestTranscribeChatPath(t *testing.T) {
	srv, seen := transcribeServer(t, 200)
	p := NewOpenAI(OpenAIOptions{Name: "t", BaseURL: srv.URL, Transcription: "chat"})
	text, err := p.Transcribe(context.Background(), &TranscribeRequest{Model: "g", Audio: []byte("abc"), MIME: "audio/wav"})
	if err != nil || text != "hello from chat" {
		t.Fatalf("got %q, %v", text, err)
	}
	if len(*seen) != 1 || !strings.HasPrefix((*seen)[0], "chat:g:") || !strings.Contains((*seen)[0], `"input_audio"`) || !strings.Contains((*seen)[0], `"format":"wav"`) {
		t.Fatalf("server saw %v", *seen)
	}
}

func TestTranscribeFallsBackToChatOn404(t *testing.T) {
	srv, seen := transcribeServer(t, 404)
	p := NewOpenAI(OpenAIOptions{Name: "t", BaseURL: srv.URL, Transcription: "audio"})
	text, err := p.Transcribe(context.Background(), &TranscribeRequest{Model: "m", Audio: []byte("abc"), MIME: "audio/wav"})
	if err != nil || text != "hello from chat" {
		t.Fatalf("got %q, %v", text, err)
	}
	if len(*seen) != 1 || !strings.HasPrefix((*seen)[0], "chat:") {
		t.Fatalf("server saw %v", *seen)
	}
}

func TestTranscribeNone(t *testing.T) {
	p := NewOpenAI(OpenAIOptions{Name: "t", BaseURL: "http://127.0.0.1:1", Transcription: "none"})
	if _, err := p.Transcribe(context.Background(), &TranscribeRequest{Model: "m", Audio: []byte("abc")}); err == nil {
		t.Fatal("expected an error")
	}
}
