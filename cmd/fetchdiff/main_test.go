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
