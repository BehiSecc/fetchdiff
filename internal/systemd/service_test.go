package systemd

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fetchdiff")
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func testAccount(t *testing.T) *Account {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".fetchdiff"), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Account{Name: "alice", Home: home}
}

func TestRenderUnit(t *testing.T) {
	content, err := RenderUnit(Account{Name: "alice", Home: "/home/alice"}, "/usr/local/bin/fetchdiff")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{"User=alice", `Environment="HOME=/home/alice"`, `Environment="FETCHDIFF_DATA_DIR=/home/alice/.fetchdiff"`, `ExecStart="/usr/local/bin/fetchdiff" --data-dir "/home/alice/.fetchdiff" watch`, "UMask=0077", "NoNewPrivileges=true", "PrivateTmp=true", "ProtectSystem=full", "WantedBy=multi-user.target"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("unit is missing %q:\n%s", expected, text)
		}
	}
}

func TestInstallIsIdempotentAndEnablesService(t *testing.T) {
	unitPath := filepath.Join(t.TempDir(), "fetchdiff.service")
	var commands []string
	runner := func(_ context.Context, name string, args ...string) error {
		commands = append(commands, strings.Join(append([]string{name}, args...), " "))
		return nil
	}
	options := InstallOptions{
		Executable: testExecutable(t), UnitPath: unitPath, Enable: true,
		Account: testAccount(t), Run: runner,
	}
	first, err := Install(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || !first.Enabled {
		t.Fatalf("first result = %#v", first)
	}
	second, err := Install(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed {
		t.Fatalf("second result = %#v", second)
	}
	expected := []string{
		"systemctl daemon-reload", "systemctl enable fetchdiff.service", "systemctl restart fetchdiff.service", "systemctl is-enabled --quiet fetchdiff.service", "systemctl is-active --quiet fetchdiff.service",
		"systemctl daemon-reload", "systemctl enable fetchdiff.service", "systemctl restart fetchdiff.service", "systemctl is-enabled --quiet fetchdiff.service", "systemctl is-active --quiet fetchdiff.service",
	}
	if !reflect.DeepEqual(commands, expected) {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestInstallRefusesDifferentExistingUnit(t *testing.T) {
	unitPath := filepath.Join(t.TempDir(), "fetchdiff.service")
	if err := os.WriteFile(unitPath, []byte("different"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Install(context.Background(), InstallOptions{
		Executable: testExecutable(t), UnitPath: unitPath,
		Account: testAccount(t), Run: func(context.Context, string, ...string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %v", err)
	}
}

func TestInstallRequiresInitializedDataDirectory(t *testing.T) {
	_, err := Install(context.Background(), InstallOptions{
		Executable: testExecutable(t), UnitPath: filepath.Join(t.TempDir(), "fetchdiff.service"),
		Account: &Account{Name: "alice", Home: t.TempDir()}, Run: func(context.Context, string, ...string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "fetchdiff init") {
		t.Fatalf("error = %v", err)
	}
}
