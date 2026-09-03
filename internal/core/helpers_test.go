package core

import (
	"os"
	"path/filepath"
)

func removeSnapshot(dir string) error {
	err := os.Remove(filepath.Join(dir, "snapshots", "state.json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
