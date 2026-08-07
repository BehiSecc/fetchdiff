package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const dataDirEnv = "FETCHDIFF_DATA_DIR"

type Paths struct {
	Root      string
	Database  string
	Snapshots string
}

func ResolvePaths(override string) (Paths, error) {
	root := override
	if root == "" {
		root = os.Getenv(dataDirEnv)
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve home directory: %w", err)
		}
		root = filepath.Join(home, ".fetchdiff")
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve data directory: %w", err)
	}
	return Paths{
		Root:      abs,
		Database:  filepath.Join(abs, "state.db"),
		Snapshots: filepath.Join(abs, "snapshots", "sha256"),
	}, nil
}

func EnsurePaths(paths Paths) error {
	for _, dir := range []string{paths.Root, paths.Snapshots} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("secure %s: %w", dir, err)
		}
	}
	return nil
}
