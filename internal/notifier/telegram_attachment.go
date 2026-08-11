package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/projectdiscovery/notify/pkg/providers/telegram"
	"github.com/projectdiscovery/notify/pkg/utils"
)

type telegramAttachmentSender struct {
	apiKey, chatID, format, parseMode string
	baseURL                           string
	client                            *http.Client
}

func newTelegramAttachmentSender(option *telegram.Options) *telegramAttachmentSender {
	return &telegramAttachmentSender{apiKey: option.TelegramAPIKey, chatID: option.TelegramChatID, format: option.TelegramFormat, parseMode: option.TelegramParseMode, baseURL: "https://api.telegram.org", client: &http.Client{Timeout: 30 * time.Second}}
}

func (s *telegramAttachmentSender) SendAttachment(ctx context.Context, message string, attachment Attachment) error {
	message = utils.FormatMessage(message, utils.SelectFormat("", s.format), 0)
	if len([]rune(message)) > 1024 {
		return fmt.Errorf("Telegram document caption exceeds the 1024-character limit")
	}
	chatID, threadID := telegramDestination(s.chatID)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{"chat_id": chatID, "caption": message}
	if mode := strings.TrimSpace(s.parseMode); mode != "" && !strings.EqualFold(mode, "none") {
		fields["parse_mode"] = mode
	}
	if threadID != "" {
		fields["message_thread_id"] = threadID
	}
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return fmt.Errorf("write Telegram field: %w", err)
		}
	}
	part, err := createAttachmentPart(writer, "document", attachment)
	if err != nil {
		return fmt.Errorf("create Telegram document: %w", err)
	}
	if _, err := part.Write(attachment.Data); err != nil {
		return fmt.Errorf("write Telegram document: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish Telegram document: %w", err)
	}
	endpoint := strings.TrimRight(s.baseURL, "/") + "/bot" + s.apiKey + "/sendDocument"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return fmt.Errorf("create Telegram request: %w", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("send Telegram document: %w", err)
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(content, &result); response.StatusCode < 200 || response.StatusCode >= 300 || err != nil || !result.OK {
		if result.Description == "" {
			result.Description = strings.TrimSpace(string(content))
		}
		return fmt.Errorf("Telegram API returned %s: %s", response.Status, result.Description)
	}
	return nil
}

func telegramDestination(value string) (string, string) {
	chatID, topic, found := strings.Cut(value, ":")
	if !found {
		return value, ""
	}
	if _, err := strconv.ParseInt(topic, 10, 64); err != nil {
		return value, ""
	}
	return chatID, topic
}
