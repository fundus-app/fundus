package store

import "testing"

// FuzzParseRecord makes sure arbitrary log lines never panic the parser.
func FuzzParseRecord(f *testing.F) {
	f.Add([]byte(`{"seq":1,"prev":"","hash":"x","txn":{}}`))
	f.Add([]byte(`{"seq":"1"}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, line []byte) {
		_, _ = parseRecord(line)
	})
}
