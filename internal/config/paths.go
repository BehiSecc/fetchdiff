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
	Providers string
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
		Providers: filepath.Join(abs, "providers.yaml"),
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
	return ensureProvidersTemplate(paths.Providers)
}

func ensureProvidersTemplate(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if _, writeErr := file.WriteString(providersTemplate); writeErr != nil {
			file.Close()
			return fmt.Errorf("write provider template: %w", writeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close provider template: %w", closeErr)
		}
		return nil
	}
	if !os.IsExist(err) {
		return fmt.Errorf("create provider template: %w", err)
	}
	info, statErr := os.Lstat(path)
	if statErr != nil {
		return fmt.Errorf("inspect provider configuration: %w", statErr)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("provider configuration must be a regular file: %s", path)
	}
	if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
		return fmt.Errorf("secure provider configuration: %w", chmodErr)
	}
	return nil
}

const providersTemplate = `# FetchDiff notification providers.
# Compatible with ProjectDiscovery Notify. Uncomment only providers you use.
# This file may contain secrets and is protected with 0600 permissions.

# slack:
#   - id: "slack"
#     slack_channel: "recon"
#     slack_username: "FetchDiff"
#     slack_format: "{{data}}"
#     slack_webhook_url: ""

# discord:
#   - id: "discord"
#     discord_channel: "changes"
#     discord_username: "FetchDiff"
#     discord_format: "{{data}}"
#     discord_webhook_url: ""

# telegram:
#   - id: "telegram"
#     telegram_api_key: ""
#     telegram_chat_id: ""
#     telegram_format: "{{data}}"
#     telegram_parsemode: "Markdown"

# pushover:
#   - id: "pushover"
#     pushover_user_key: ""
#     pushover_api_token: ""
#     pushover_format: "{{data}}"
#     pushover_devices: []

# smtp:
#   - id: "email"
#     smtp_server: ""
#     smtp_username: ""
#     smtp_password: ""
#     from_address: ""
#     smtp_cc: []
#     smtp_format: "{{data}}"
#     subject: "FetchDiff change detected"
#     smtp_html: false
#     smtp_disable_starttls: false

# googlechat:
#   - id: "googlechat"
#     key: ""
#     token: ""
#     space: ""
#     google_chat_format: "{{data}}"

# teams:
#   - id: "teams"
#     teams_webhook_url: ""
#     teams_format: "{{data}}"

# gotify:
#   - id: "gotify"
#     gotify_host: ""
#     gotify_port: "80"
#     gotify_token: ""
#     gotify_format: "{{data}}"
#     gotify_disabletls: false
#     gotify_title: "FetchDiff"

# custom:
#   - id: "webhook"
#     custom_webhook_url: ""
#     custom_method: POST
#     custom_format: '{"text":{{dataJsonString}}}'
#     custom_headers:
#       Content-Type: application/json
`
