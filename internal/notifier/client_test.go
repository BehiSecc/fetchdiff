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
	client, err := New(Config{Custom: []*custom.Options{{
		ID: "webhook", CustomWebhookURL: server.URL, CustomMethod: http.MethodPost, CustomFormat: `{"text":{{dataJsonString}}}`,
	}}})
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
