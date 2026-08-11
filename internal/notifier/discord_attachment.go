package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/projectdiscovery/notify/pkg/providers/discord"
	"github.com/projectdiscovery/notify/pkg/utils"
)

type discordAttachmentSender struct {
	webhookURL, username, avatarURL, format string
	threaded                                bool
	threadID                                string
	client                                  *http.Client
}

func newDiscordAttachmentSender(option *discord.Options) *discordAttachmentSender {
	return &discordAttachmentSender{
		webhookURL: option.DiscordWebHookURL, username: option.DiscordWebHookUsername,
		avatarURL: option.DiscordWebHookAvatarURL, format: option.DiscordFormat,
		threaded: option.DiscordThreads, threadID: option.DiscordThreadID,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *discordAttachmentSender) SendAttachment(ctx context.Context, message string, attachment Attachment) error {
	message = utils.FormatMessage(message, utils.SelectFormat("", s.format), 0)
	if len([]rune(message)) > 2000 {
		return fmt.Errorf("Discord message exceeds the 2000-character limit")
	}
	endpoint, err := url.Parse(s.webhookURL)
	if err != nil {
		return fmt.Errorf("parse Discord webhook URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("wait", "true")
	if s.threaded {
		query.Set("thread_id", s.threadID)
	}
	endpoint.RawQuery = query.Encode()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	payload := map[string]any{
		"content":          message,
		"allowed_mentions": map[string]any{"parse": []string{}},
		"attachments":      []map[string]any{{"id": 0, "filename": attachment.Name, "description": "FetchDiff change report"}},
	}
	if s.username != "" {
		payload["username"] = s.username
	}
	if s.avatarURL != "" {
		payload["avatar_url"] = s.avatarURL
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Discord webhook payload: %w", err)
	}
	field, err := writer.CreateFormField("payload_json")
	if err != nil {
		return fmt.Errorf("create Discord payload field: %w", err)
	}
	if _, err := field.Write(encoded); err != nil {
		return fmt.Errorf("write Discord payload: %w", err)
	}
	file, err := createAttachmentPart(writer, "files[0]", attachment)
	if err != nil {
		return fmt.Errorf("create Discord attachment: %w", err)
	}
	if _, err := file.Write(attachment.Data); err != nil {
		return fmt.Errorf("write Discord attachment: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish Discord attachment: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return fmt.Errorf("create Discord webhook request: %w", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("send Discord attachment: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		content, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("Discord webhook returned %s: %s", response.Status, strings.TrimSpace(string(content)))
	}
	return nil
}

func safeAttachmentName(value string) string {
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.", r) {
			return r
		}
		return '-'
	}, value)
	value = strings.Trim(value, ".-")
	if value == "" {
		return "fetchdiff-report.html"
	}
	return value
}
