package systemd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

const DefaultUnitPath = "/etc/systemd/system/fetchdiff.service"

type Account struct {
	Name string
	Home string
}

type CommandRunner func(context.Context, string, ...string) error

type InstallOptions struct {
	UserName   string
	Executable string
	UnitPath   string
	Enable     bool
	Force      bool
	Account    *Account
	Run        CommandRunner
}

type InstallResult struct {
	UnitPath string
	User     string
	Changed  bool
	Enabled  bool
}

func Install(ctx context.Context, options InstallOptions) (InstallResult, error) {
	account := options.Account
	if account == nil {
		resolved, err := resolveAccount(options.UserName)
		if err != nil {
			return InstallResult{}, err
		}
		account = &resolved
	}
	if err := validateAccount(*account); err != nil {
		return InstallResult{}, err
	}
	if err := validateDataDirectory(filepath.Join(account.Home, ".fetchdiff")); err != nil {
		return InstallResult{}, err
	}
	executable := options.Executable
	if executable == "" {
		resolved, err := os.Executable()
		if err != nil {
			return InstallResult{}, fmt.Errorf("resolve FetchDiff executable: %w", err)
		}
		executable = resolved
	}
	executable, err := filepath.Abs(executable)
	if err != nil {
		return InstallResult{}, fmt.Errorf("resolve FetchDiff executable: %w", err)
	}
	if err := validateExecutable(executable); err != nil {
		return InstallResult{}, err
	}
	unitPath := options.UnitPath
	if unitPath == "" {
		unitPath = DefaultUnitPath
	}
	run := options.Run
	if run == nil {
		if _, err := exec.LookPath("systemctl"); err != nil {
			return InstallResult{}, errors.New("systemctl is required to install the FetchDiff service")
		}
		run = runCommand
		if err := run(ctx, "systemctl", "--version"); err != nil {
			return InstallResult{}, fmt.Errorf("verify systemd: %w", err)
		}
	}
	content, err := RenderUnit(*account, executable)
	if err != nil {
		return InstallResult{}, err
	}
	changed, err := writeUnit(unitPath, content, options.Force)
	if err != nil {
		return InstallResult{}, err
	}
	if err := run(ctx, "systemctl", "daemon-reload"); err != nil {
		return InstallResult{}, fmt.Errorf("reload systemd: %w", err)
	}
	if options.Enable {
		if err := run(ctx, "systemctl", "enable", "fetchdiff.service"); err != nil {
			return InstallResult{}, fmt.Errorf("enable FetchDiff service: %w", err)
		}
		if err := run(ctx, "systemctl", "restart", "fetchdiff.service"); err != nil {
			return InstallResult{}, fmt.Errorf("start FetchDiff service: %w", err)
		}
		if err := run(ctx, "systemctl", "is-enabled", "--quiet", "fetchdiff.service"); err != nil {
			return InstallResult{}, fmt.Errorf("verify FetchDiff service is enabled: %w", err)
		}
		if err := run(ctx, "systemctl", "is-active", "--quiet", "fetchdiff.service"); err != nil {
			return InstallResult{}, fmt.Errorf("verify FetchDiff service is running: %w", err)
		}
	}
	return InstallResult{UnitPath: unitPath, User: account.Name, Changed: changed, Enabled: options.Enable}, nil
}

func RenderUnit(account Account, executable string) ([]byte, error) {
	if err := validateAccount(account); err != nil {
		return nil, err
	}
	if strings.ContainsAny(executable, "\r\n") {
		return nil, errors.New("executable path contains a newline")
	}
	dataDir := filepath.Join(account.Home, ".fetchdiff")
	unit := fmt.Sprintf(`[Unit]
Description=FetchDiff URL monitor
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=%s
Environment="HOME=%s"
Environment="FETCHDIFF_DATA_DIR=%s"
ExecStart="%s" --data-dir "%s" watch
Restart=on-failure
RestartSec=10
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full

[Install]
WantedBy=multi-user.target
`, account.Name, escapeUnit(account.Home), escapeUnit(dataDir), escapeUnit(executable), escapeUnit(dataDir))
	return []byte(unit), nil
}

func resolveAccount(name string) (Account, error) {
	if name == "" {
		name = os.Getenv("SUDO_USER")
	}
	var current *user.User
	var err error
	if name == "" {
		current, err = user.Current()
	} else {
		current, err = user.Lookup(name)
	}
	if err != nil {
		return Account{}, fmt.Errorf("resolve service user: %w", err)
	}
	return Account{Name: current.Username, Home: current.HomeDir}, nil
}

func validateAccount(account Account) error {
	if strings.TrimSpace(account.Name) == "" || strings.ContainsAny(account.Name, " \t\r\n\\\"") {
		return errors.New("service user is invalid")
	}
	if account.Name == "root" {
		return errors.New("FetchDiff service must run as a non-root user")
	}
	if !filepath.IsAbs(account.Home) || strings.ContainsAny(account.Home, "\r\n") {
		return errors.New("service user home directory is invalid")
	}
	return nil
}

func validateDataDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("FetchDiff data directory does not exist: %s (run fetchdiff init as the service user first)", path)
		}
		return fmt.Errorf("inspect FetchDiff data directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("FetchDiff data directory must be a real directory: %s", path)
	}
	return nil
}

func validateExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect FetchDiff executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("FetchDiff executable is not a regular file: %s", path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("FetchDiff executable is not executable: %s", path)
	}
	return nil
}

func writeUnit(path string, content []byte, force bool) (bool, error) {
	info, statErr := os.Lstat(path)
	if statErr == nil && !info.Mode().IsRegular() {
		return false, fmt.Errorf("service file must be a regular file: %s", path)
	}
	existing, err := os.ReadFile(path)
	if statErr == nil && err == nil {
		if bytes.Equal(existing, content) {
			return false, nil
		}
		if !force {
			return false, fmt.Errorf("service file already exists with different content: %s (use --force to replace it)", path)
		}
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return false, fmt.Errorf("inspect existing service file: %w", statErr)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read existing service file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create service directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".fetchdiff.service-*.tmp")
	if err != nil {
		return false, fmt.Errorf("create temporary service file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return false, fmt.Errorf("set service permissions: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return false, fmt.Errorf("write service file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return false, fmt.Errorf("sync service file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("close service file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return false, fmt.Errorf("install service file: %w", err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return false, fmt.Errorf("open service directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return false, fmt.Errorf("sync service directory: %w", err)
	}
	return true, nil
}

func runCommand(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("%w: %s", err, message)
		}
		return err
	}
	return nil
}

func escapeUnit(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return strings.ReplaceAll(value, "%", "%%")
}
