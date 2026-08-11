package notifier

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/projectdiscovery/notify/pkg/providers/custom"
	"github.com/projectdiscovery/notify/pkg/providers/discord"
)

func TestLoadCommentedTemplateIsDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.yaml")
	if err := os.WriteFile(path, []byte("# discord:\n#   - id: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if client.Count() != 0 {
		t.Fatalf("destinations = %v", client.Keys())
	}
}

func TestDiscordAttachmentDelivery(t *testing.T) {
	var payload, filename, fileContent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !strings.Contains(request.URL.RawQuery, "wait=true") {
			t.Errorf("query = %q", request.URL.RawQuery)
		}
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
			switch part.FormName() {
			case "payload_json":
				payload = string(content)
			case "files[0]":
				filename, fileContent = part.FileName(), string(content)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client, err := New(Config{Discord: []*discord.Options{{ID: "alerts", DiscordWebHookURL: server.URL, DiscordWebHookUsername: "FetchDiff"}}})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Send(context.Background(), "discord:alerts", Message{Text: "changed", Attachment: &Attachment{Name: "report.html", ContentType: "text/html", Data: []byte("<html>full diff</html>")}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, `"content":"changed"`) || !strings.Contains(payload, `"parse":[]`) {
		t.Fatalf("payload = %s", payload)
	}
	if filename != "report.html" || fileContent != "<html>full diff</html>" {
		t.Fatalf("file = %q %q", filename, fileContent)
	}
}

func TestCustomWebhookDeliveryAndFilter(t *testing.T) {
	var requests atomic.Int32
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		content, _ := io.ReadAll(request.Body)
		body = string(content)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := New(Config{Custom: []*CustomOptions{{Options: custom.Options{
		ID: "webhook", CustomWebhookURL: server.URL, CustomMethod: http.MethodPost, CustomFormat: `{"text":{{dataJsonString}}}`,
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	results := client.SendAll(context.Background(), Message{Text: "hello"}, Filter{Providers: []string{"custom"}, IDs: []string{"webhook"}})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %#v", results)
	}
	if requests.Load() != 1 || !strings.Contains(body, "hello") {
		t.Fatalf("requests = %d, body = %q", requests.Load(), body)
	}
	if got := client.Select(Filter{IDs: []string{"missing"}}); len(got) != 0 {
		t.Fatalf("selected = %v", got)
	}
}

func TestDuplicateYAMLKeysFail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.yaml")
	content := "custom:\n  - id: one\ncustom:\n  - id: two\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected duplicate key error")
	}
}

func TestLoadNotifyCompatibleConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.yaml")
	content := `slack:
  - id: slack
    slack_webhook_url: https://hooks.slack.com/services/T000/B000/SECRET
    slack_icon_emoji: ":ghost:"
custom:
  - id: first
    custom_webhook_url: https://example.com/one
    custom_method: POST
    custom_format: "{{data}}"
  - id: second
    custom_webhook_url: https://example.com/two
    custom_method: POST
    custom_format: "{{data}}"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if client.Count() != 3 {
		t.Fatalf("destinations = %v", client.Keys())
	}
}

func TestInvalidTeamsConfigIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.yaml")
	content := "teams:\n  - id: broken\n    teams_webhook_url: https://example.com/no-webhook\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "invalid teams_webhook_url") {
		t.Fatalf("error = %v", err)
	}
}

func TestSplitMessagePreservesContent(t *testing.T) {
	message := strings.Repeat("界", 25)
	chunks := SplitMessage(message, 10)
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d", len(chunks))
	}
	var rebuilt string
	for _, chunk := range chunks {
		_, content, ok := strings.Cut(chunk, "\n\n")
		if !ok {
			t.Fatalf("chunk has no prefix: %q", chunk)
		}
		rebuilt += content
	}
	if rebuilt != message {
		t.Fatalf("rebuilt message differs")
	}
}

func TestWebhookSecretsAreRedactedFromDerivedURLs(t *testing.T) {
	webhook := "https://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz-secret-token"
	message := "delivery failed for discord://abcdefghijklmnopqrstuvwxyz-secret-token@123456789012345678"
	redacted := redact(message, webhookSecrets(webhook))
	if strings.Contains(redacted, "secret-token") || strings.Contains(redacted, "123456789012345678") {
		t.Fatalf("secret remained in error: %s", redacted)
	}
}
