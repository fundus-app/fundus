package store

import (
	"errors"
	"testing"
)

func TestSecondOpenIsRefusedWhileLocked(t *testing.T) {
	dir := t.TempDir()
	l1, _, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("second open: want ErrLocked, got %v", err)
	}
	if err := l1.Close(); err != nil {
		t.Fatal(err)
	}
	l2, _, err := Open(dir)
	if err != nil {
		t.Fatalf("open after close: %v", err)
	}
	l2.Close()
}
