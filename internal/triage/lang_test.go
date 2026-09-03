package triage

import "testing"

func TestGuessLanguage(t *testing.T) {
	cases := map[string]string{
		"I must send the 2025 tax return by 15 October, otherwise there is a late fee.":   "English",
		"Ich sollte mir irgendwann ansehen, ob man die Heizungsdaten visualisieren kann.": "German",
		"Fundus zum Einhorn machen: Backlinks kassieren.":                                 "",
		"hm":                       "",
		"Deploy the Grafana panel": "",
		"Il faut que je vérifie le deuxième string de l'onduleur avec les données du jour.": "French",
		"note_01ABC task_02DEF": "",
	}
	for text, want := range cases {
		if got := guessLanguage(text); got != want {
			t.Errorf("%q: got %q, want %q", text, got, want)
		}
	}
}
