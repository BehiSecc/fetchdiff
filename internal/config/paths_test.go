package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAndEnsurePaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	paths, err := ResolvePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	if paths.Root != root {
		t.Fatalf("root = %q, want %q", paths.Root, root)
	}
	if err := EnsurePaths(paths); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(paths.Snapshots)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("snapshot directory mode = %o, want 700", got)
	}
}
