package notifier

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/projectdiscovery/notify/pkg/providers/slack"
	providersmtp "github.com/projectdiscovery/notify/pkg/providers/smtp"
	"github.com/projectdiscovery/notify/pkg/providers/telegram"
	"gopkg.in/yaml.v3"
)

var testAttachment = Attachment{Name: "report.html", ContentType: "text/html; charset=utf-8", Data: []byte("<!doctype html><title>Full changes</title>")}

func TestTelegramAttachmentDelivery(t *testing.T) {
	var fields map[string]string
	var document string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/bottest-token/sendDocument" {
			t.Errorf("path = %q", request.URL.Path)
		}
		reader, err := request.MultipartReader()
		if err != nil {
			t.Errorf("multipart: %v", err)
			return
		}
		fields = map[string]string{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("part: %v", err)
				return
			}
			content, _ := io.ReadAll(part)
			if part.FormName() == "document" {
				document = string(content)
			} else {
				fields[part.FormName()] = string(content)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	defer server.Close()
	sender := newTelegramAttachmentSender(&telegram.Options{TelegramAPIKey: "test-token", TelegramChatID: "-10001:42", TelegramFormat: "{{data}}", TelegramParseMode: "None"})
	sender.baseURL = server.URL
	if err := sender.SendAttachment(context.Background(), "changed", testAttachment); err != nil {
		t.Fatal(err)
	}
	if fields["chat_id"] != "-10001" || fields["message_thread_id"] != "42" || fields["caption"] != "changed" {
		t.Fatalf("fields = %#v", fields)
	}
	if document != string(testAttachment.Data) {
		t.Fatalf("document = %q", document)
	}
}

func TestSlackAttachmentDelivery(t *testing.T) {
	var uploaded, comment, channel, authorization string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/files.getUploadURLExternal":
			authorization = request.Header.Get("Authorization")
			_ = request.ParseForm()
			if request.Form.Get("filename") != "report.html" {
				t.Errorf("filename = %q", request.Form.Get("filename"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "upload_url": server.URL + "/upload", "file_id": "F123"})
		case "/upload":
			content, _ := io.ReadAll(request.Body)
			uploaded = string(content)
			w.WriteHeader(http.StatusOK)
		case "/api/files.completeUploadExternal":
			_ = request.ParseForm()
			comment, channel = request.Form.Get("initial_comment"), request.Form.Get("channel_id")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	sender := newSlackAttachmentSender(&slack.Options{SlackToken: "xoxb-test", SlackChannel: "C123", SlackFormat: "{{data}}"})
	sender.apiURL = server.URL + "/api"
	if err := sender.SendAttachment(context.Background(), "changed", testAttachment); err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer xoxb-test" || uploaded != string(testAttachment.Data) || comment != "changed" || channel != "C123" {
		t.Fatalf("auth=%q uploaded=%q comment=%q channel=%q", authorization, uploaded, comment, channel)
	}
}

func TestSMTPAttachmentDelivery(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	messages := make(chan string, 1)
	go serveTestSMTP(listener, messages)
	sender := newSMTPAttachmentSender(&providersmtp.Options{Server: listener.Addr().String(), FromAddress: "from@example.com", SMTPCC: []string{"to@example.com"}, Subject: "FetchDiff change", DisableStartTLS: true})
	if err := sender.SendAttachment(context.Background(), "changed", testAttachment); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-messages:
		if !strings.Contains(message, "report.html") || !strings.Contains(message, base64.StdEncoding.EncodeToString(testAttachment.Data)) {
			t.Fatalf("SMTP message lacks attachment:\n%s", message)
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP message was not received")
	}
}

func serveTestSMTP(listener net.Listener, messages chan<- string) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()
	reader, writer := bufio.NewReader(connection), bufio.NewWriter(connection)
	write := func(value string) { _, _ = writer.WriteString(value); _ = writer.Flush() }
	write("220 localhost ESMTP\r\n")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"):
			write("250-localhost\r\n250 8BITMIME\r\n")
		case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
			write("250 OK\r\n")
		case command == "DATA":
			write("354 End data\r\n")
			var data strings.Builder
			for {
				line, err = reader.ReadString('\n')
				if err != nil {
					return
				}
				if line == ".\r\n" {
					break
				}
				data.WriteString(line)
			}
			messages <- data.String()
			write("250 queued\r\n")
		case command == "QUIT":
			write("221 bye\r\n")
			return
		default:
			write("250 OK\r\n")
		}
	}
}

func TestCustomMultipartAttachmentDelivery(t *testing.T) {
	var data, file, header string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		header = request.Header.Get("X-Test")
		reader, err := request.MultipartReader()
		if err != nil {
			t.Errorf("multipart: %v", err)
			return
		}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("part: %v", err)
				return
			}
			content, _ := io.ReadAll(part)
			if part.FormName() == "data" {
				data = string(content)
			}
			if part.FormName() == "file" {
				file = string(content)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	config := "custom:\n  - id: upload\n    custom_webhook_url: " + server.URL + "\n    custom_method: POST\n    custom_format: '{{data}}'\n    custom_multipart: true\n    custom_headers:\n      X-Test: yes\n"
	var parsed Config
	if err := yamlUnmarshalForTest([]byte(config), &parsed); err != nil {
		t.Fatal(err)
	}
	client, err := New(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Send(context.Background(), "custom:upload", Message{Text: "changed", Attachment: &testAttachment}); err != nil {
		t.Fatal(err)
	}
	if data != "changed" || file != string(testAttachment.Data) || header != "yes" {
		t.Fatalf("data=%q file=%q header=%q", data, file, header)
	}
}

func yamlUnmarshalForTest(content []byte, value any) error { return yaml.Unmarshal(content, value) }
