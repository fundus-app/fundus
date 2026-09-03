package llm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveTranscribe speaks a sentence with espeak-ng and sends it through the
// real OpenAI transcription endpoint. Skipped unless FUNDUS_LIVE_TESTS=1, an
// OPENAI_API_KEY and espeak-ng are present; it costs a fraction of a cent.
func TestLiveTranscribe(t *testing.T) {
	if os.Getenv("FUNDUS_LIVE_TESTS") != "1" {
		t.Skip("set FUNDUS_LIVE_TESTS=1 to run against a real provider")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("no OPENAI_API_KEY")
	}
	if _, err := exec.LookPath("espeak-ng"); err != nil {
		t.Skip("espeak-ng not installed")
	}
	wav := filepath.Join(t.TempDir(), "say.wav")
	if out, err := exec.Command("espeak-ng", "-v", "en", "-s", "150", "-w", wav, "Remind me to write the Fundus release notes on Friday.").CombinedOutput(); err != nil {
		t.Fatalf("espeak-ng: %v: %s", err, out)
	}
	audio, err := os.ReadFile(wav)
	if err != nil {
		t.Fatal(err)
	}
	model := os.Getenv("FUNDUS_LIVE_TRANSCRIBE_MODEL")
	if model == "" {
		model = "gpt-4o-mini-transcribe"
	}
	p := NewOpenAI(OpenAIOptions{Name: "live", BaseURL: "https://api.openai.com/v1", APIKey: key, Transcription: "audio"})
	text, err := p.Transcribe(context.Background(), &TranscribeRequest{Model: model, Audio: audio, MIME: "audio/wav", Hints: []string{"Fundus"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("transcript: %q", text)
	low := strings.ToLower(text)
	if !strings.Contains(low, "release notes") || !strings.Contains(low, "friday") {
		t.Fatalf("transcript lost the sentence: %q", text)
	}
}
