package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BehiSecc/fetchdiff/internal/config"
)

func TestHelpDoesNotCreateState(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "state")
	var output bytes.Buffer
	command := newRootCommand(&output, &output)
	command.SetArgs([]string{"--data-dir", dataDir, "--help"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("help created state directory: %v", err)
	}
}

func TestInitIsIdempotentAndPreservesProviders(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "state")
	run := func() string {
		t.Helper()
		var output bytes.Buffer
		command := newRootCommand(&output, &output)
		command.SetArgs([]string{"--data-dir", dataDir, "init"})
		if err := command.Execute(); err != nil {
			t.Fatalf("init: %v\n%s", err, output.String())
		}
		return output.String()
	}
	if output := run(); !strings.Contains(output, "FetchDiff initialized") {
		t.Fatalf("init output:\n%s", output)
	}
	providers := filepath.Join(dataDir, "providers.yaml")
	const custom = "# keep this\n"
	if err := os.WriteFile(providers, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	run()
	content, err := os.ReadFile(providers)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != custom {
		t.Fatalf("providers file was overwritten: %q", content)
	}
}

func TestParseHeaders(t *testing.T) {
	headers, err := parseHeaders([]string{"Authorization: Bearer value", "X-Test: yes"})
	if err != nil {
		t.Fatal(err)
	}
	if headers["Authorization"] != "Bearer value" || headers["X-Test"] != "yes" {
		t.Fatalf("headers = %#v", headers)
	}
	if _, err := parseHeaders([]string{"missing separator"}); err == nil {
		t.Fatal("expected malformed header error")
	}
}

func TestFormatBytes(t *testing.T) {
	if got := formatBytes(188416); got != "184.0 KB" {
		t.Fatalf("size = %q", got)
	}
}

func TestCommandWorkflow(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		version := requests.Add(1)
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("ETag", `"v`+string(rune('0'+version))+`"`)
		if version == 1 {
			_, _ = io.WriteString(w, `function value(){return 1}`)
			return
		}
		_, _ = io.WriteString(w, `function value(){return 2}`)
	}))
	defer server.Close()

	dataDir := filepath.Join(t.TempDir(), "state")
	run := func(args ...string) string {
		t.Helper()
		var output bytes.Buffer
		command := newRootCommand(&output, &output)
		command.SetArgs(append([]string{"--data-dir", dataDir}, args...))
		if err := command.Execute(); err != nil {
			t.Fatalf("fetchdiff %s: %v\n%s", strings.Join(args, " "), err, output.String())
		}
		return output.String()
	}

	if output := run("add", server.URL+"/app.js", "--name", "production-js", "--every", "24h"); !strings.Contains(output, "Baseline created") {
		t.Fatalf("add output:\n%s", output)
	}
	if output := run("check", "production-js", "--force"); !strings.Contains(output, "function value()") || !strings.Contains(output, "return 2") {
		t.Fatalf("check output:\n%s", output)
	}
	for _, command := range [][]string{{"list"}, {"history", "production-js"}, {"diff", "production-js"}, {"status"}, {"doctor"}} {
		if output := run(command...); strings.TrimSpace(output) == "" {
			t.Fatalf("%s returned no output", strings.Join(command, " "))
		}
	}
}

func TestNotifyTestUsesConfiguredCustomWebhook(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		content, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(content), "notification test") {
			t.Errorf("unexpected body: %s", content)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	dataDir := filepath.Join(t.TempDir(), "state")
	paths, err := config.ResolvePaths(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.EnsurePaths(paths); err != nil {
		t.Fatal(err)
	}
	providerConfig := "custom:\n  - id: webhook\n    custom_webhook_url: " + server.URL + "\n    custom_method: POST\n    custom_format: '{{data}}'\n"
	if err := os.WriteFile(paths.Providers, []byte(providerConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := newRootCommand(&output, &output)
	command.SetArgs([]string{"--data-dir", dataDir, "notify-test", "--provider", "custom", "--id", "webhook"})
	if err := command.Execute(); err != nil {
		t.Fatalf("notify-test: %v\n%s", err, output.String())
	}
	if requests.Load() != 1 || !strings.Contains(output.String(), "✓ custom:webhook") {
		t.Fatalf("requests = %d, output = %s", requests.Load(), output.String())
	}
}

func TestChangedCheckDeliversFullDiff(t *testing.T) {
	var fetches atomic.Int32
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		if fetches.Add(1) == 1 {
			_, _ = io.WriteString(w, `function value(){return 1}`)
			return
		}
		_, _ = io.WriteString(w, `function value(){return 2}`)
	}))
	defer resource.Close()
	var delivered string
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		content, _ := io.ReadAll(request.Body)
		delivered += string(content)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	dataDir := filepath.Join(t.TempDir(), "state")
	paths, err := config.ResolvePaths(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.EnsurePaths(paths); err != nil {
		t.Fatal(err)
	}
	providerConfig := "custom:\n  - id: webhook\n    custom_webhook_url: " + webhook.URL + "\n    custom_method: POST\n    custom_format: '{{data}}'\n"
	if err := os.WriteFile(paths.Providers, []byte(providerConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		var output bytes.Buffer
		command := newRootCommand(&output, &output)
		command.SetArgs(append([]string{"--data-dir", dataDir}, args...))
		if err := command.Execute(); err != nil {
			t.Fatalf("fetchdiff %s: %v\n%s", strings.Join(args, " "), err, output.String())
		}
		return output.String()
	}
	run("add", resource.URL+"/app.js", "--name", "app", "--every", "24h")
	output := run("check", "app", "--force")
	if !strings.Contains(output, "Notifications sent: 1") {
		t.Fatalf("check output:\n%s", output)
	}
	if !strings.Contains(delivered, "return 2") || !strings.Contains(delivered, "@@") {
		t.Fatalf("delivered notification does not contain full diff:\n%s", delivered)
	}
}
