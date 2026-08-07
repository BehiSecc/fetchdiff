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
	providerInfo, err := os.Stat(paths.Providers)
	if err != nil {
		t.Fatal(err)
	}
	if got := providerInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("provider config mode = %o, want 600", got)
	}
	content, err := os.ReadFile(paths.Providers)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) == 0 {
		t.Fatal("provider template is empty")
	}
}

func TestEnsurePathsDoesNotOverwriteProviderConfig(t *testing.T) {
	paths, err := ResolvePaths(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsurePaths(paths); err != nil {
		t.Fatal(err)
	}
	const custom = "discord: []\n"
	if err := os.WriteFile(paths.Providers, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePaths(paths); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(paths.Providers)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != custom {
		t.Fatalf("provider config was overwritten: %q", content)
	}
	info, err := os.Stat(paths.Providers)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("provider config mode = %o, want 600", got)
	}
}
