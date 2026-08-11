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
	for _, command := range [][]string{{"list"}, {"show", "production-js"}, {"history", "production-js"}, {"diff", "production-js"}, {"status"}, {"doctor"}} {
		if output := run(command...); strings.TrimSpace(output) == "" {
			t.Fatalf("%s returned no output", strings.Join(command, " "))
		}
	}
}

func TestForceCheckAllAndRemoveTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.WriteString(w, `const path = "`+request.URL.Path+`";`)
	}))
	defer server.Close()

	dataDir := filepath.Join(t.TempDir(), "state")
	run := func(args ...string) (string, error) {
		var output bytes.Buffer
		command := newRootCommand(&output, &output)
		command.SetArgs(append([]string{"--data-dir", dataDir}, args...))
		err := command.Execute()
		return output.String(), err
	}
	for _, name := range []string{"alpha", "beta"} {
		if output, err := run("add", server.URL+"/"+name+".js", "--name", name, "--every", "24h"); err != nil {
			t.Fatalf("add %s: %v\n%s", name, err, output)
		}
	}
	output, err := run("check", "--force")
	if err != nil {
		t.Fatalf("force check all: %v\n%s", err, output)
	}
	if !strings.Contains(output, "alpha unchanged") || !strings.Contains(output, "beta unchanged") {
		t.Fatalf("force check output:\n%s", output)
	}
	output, err = run("remove", "alpha")
	if err != nil || !strings.Contains(output, "Removed target alpha") {
		t.Fatalf("remove: %v\n%s", err, output)
	}
	output, err = run("list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, output)
	}
	if strings.Contains(output, "alpha") || !strings.Contains(output, "beta") {
		t.Fatalf("list after remove:\n%s", output)
	}
	if output, err = run("history", "alpha"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("removed history: %v\n%s", err, output)
	}
}

func TestAddSupportsWeekIntervals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.WriteString(w, `const interval = "two-weeks";`)
	}))
	defer server.Close()
	dataDir := filepath.Join(t.TempDir(), "state")

	var output bytes.Buffer
	command := newRootCommand(&output, &output)
	command.SetArgs([]string{"--data-dir", dataDir, "add", server.URL + "/app.js", "--name", "fortnightly", "--every", "2w"})
	if err := command.Execute(); err != nil {
		t.Fatalf("add with week interval: %v\n%s", err, output.String())
	}
	output.Reset()
	command = newRootCommand(&output, &output)
	command.SetArgs([]string{"--data-dir", dataDir, "show", "fortnightly"})
	if err := command.Execute(); err != nil {
		t.Fatalf("show fortnightly: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "Interval:") || !strings.Contains(output.String(), "2w") {
		t.Fatalf("show output:\n%s", output.String())
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

func TestChangedCheckDeliversDiscordHTMLReport(t *testing.T) {
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
	var delivered, attachment string
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		reader, err := request.MultipartReader()
		if err != nil {
			t.Errorf("multipart reader: %v", err)
			return
		}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("next part: %v", err)
				return
			}
			content, _ := io.ReadAll(part)
			if part.FormName() == "payload_json" {
				delivered = string(content)
			}
			if part.FormName() == "files[0]" {
				attachment = string(content)
			}
		}
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
	providerConfig := "discord:\n  - id: webhook\n    discord_webhook_url: " + webhook.URL + "\n    discord_format: '{{data}}'\n"
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
	if strings.Contains(delivered, "return 2") || strings.Contains(delivered, "@@") || !strings.Contains(delivered, "Report: fetchdiff-app-") {
		t.Fatalf("delivered notification is not the concise report summary:\n%s", delivered)
	}
	if !strings.Contains(attachment, "return 2") || !strings.Contains(attachment, "Full changes") || !strings.Contains(attachment, "<!doctype html>") {
		t.Fatalf("Discord attachment does not contain the HTML diff report")
	}
}
