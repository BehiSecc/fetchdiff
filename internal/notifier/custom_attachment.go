package notifier

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

type customAttachmentSender struct{ sender *customSender }

func newCustomAttachmentSender(sender *customSender) *customAttachmentSender {
	return &customAttachmentSender{sender: sender}
}

func (s *customAttachmentSender) SendAttachment(ctx context.Context, message string, attachment Attachment) error {
	s.sender.counter++
	payload, err := s.sender.payload(message, "")
	if err != nil {
		return err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("data", payload); err != nil {
		return fmt.Errorf("write custom notification data: %w", err)
	}
	part, err := createAttachmentPart(writer, "file", attachment)
	if err != nil {
		return fmt.Errorf("create custom notification attachment: %w", err)
	}
	if _, err := part.Write(attachment.Data); err != nil {
		return fmt.Errorf("write custom notification attachment: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish custom notification attachment: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, s.sender.option.CustomMethod, s.sender.option.CustomWebhookURL, &body)
	if err != nil {
		return fmt.Errorf("create custom notification request for id %s: %w", s.sender.option.ID, err)
	}
	for name, value := range s.sender.option.CustomHeaders {
		if !strings.EqualFold(name, "Content-Type") {
			request.Header.Set(name, value)
		}
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := s.sender.client.Do(request)
	if err != nil {
		return fmt.Errorf("send custom notification for id %s: %w", s.sender.option.ID, err)
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(io.LimitReader(response.Body, 32<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("custom notification for id %s returned HTTP %s: %s", s.sender.option.ID, response.Status, strings.TrimSpace(string(content)))
	}
	return nil
}
