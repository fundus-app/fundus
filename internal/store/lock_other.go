//go:build !unix

package store

import (
	"errors"
	"os"
)

// ErrLocked means another process holds the data directory.
var ErrLocked = errors.New("data directory is locked by another process")

// lockDir is a no-op on platforms without flock; the daemon must not be
// started twice on the same data directory there.
func lockDir(dir string) (*os.File, error) { return nil, nil }

func unlockDir(f *os.File) error { return nil }
