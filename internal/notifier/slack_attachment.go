package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/projectdiscovery/notify/pkg/providers/slack"
	"github.com/projectdiscovery/notify/pkg/utils"
)

type slackAttachmentSender struct {
	token, channel, format string
	apiURL                 string
	client                 *http.Client
}

func newSlackAttachmentSender(option *slack.Options) *slackAttachmentSender {
	return &slackAttachmentSender{token: option.SlackToken, channel: option.SlackChannel, format: option.SlackFormat, apiURL: "https://slack.com/api", client: &http.Client{Timeout: 30 * time.Second}}
}

func (s *slackAttachmentSender) SendAttachment(ctx context.Context, message string, attachment Attachment) error {
	message = utils.FormatMessage(message, utils.SelectFormat("", s.format), 0)
	filename := safeAttachmentName(attachment.Name)
	first, err := s.slackForm(ctx, "/files.getUploadURLExternal", url.Values{"filename": {filename}, "length": {strconv.Itoa(len(attachment.Data))}})
	if err != nil {
		return err
	}
	var upload struct {
		OK        bool   `json:"ok"`
		Error     string `json:"error"`
		UploadURL string `json:"upload_url"`
		FileID    string `json:"file_id"`
	}
	if err := json.Unmarshal(first, &upload); err != nil {
		return fmt.Errorf("decode Slack upload URL response: %w", err)
	}
	if !upload.OK || upload.UploadURL == "" || upload.FileID == "" {
		return fmt.Errorf("Slack files.getUploadURLExternal failed: %s", upload.Error)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upload.UploadURL, bytes.NewReader(attachment.Data))
	if err != nil {
		return fmt.Errorf("create Slack file upload request: %w", err)
	}
	request.Header.Set("Content-Type", attachment.ContentType)
	response, err := s.client.Do(request)
	if err != nil {
		if requestErr, ok := err.(*url.Error); ok {
			err = requestErr.Err
		}
		return fmt.Errorf("upload Slack file: %w", err)
	}
	content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Slack file upload returned %s: %s", response.Status, strings.TrimSpace(string(content)))
	}
	files, _ := json.Marshal([]map[string]string{{"id": upload.FileID, "title": filename}})
	completed, err := s.slackForm(ctx, "/files.completeUploadExternal", url.Values{"files": {string(files)}, "channel_id": {s.channel}, "initial_comment": {message}})
	if err != nil {
		return err
	}
	var completion struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(completed, &completion); err != nil {
		return fmt.Errorf("decode Slack completion response: %w", err)
	}
	if !completion.OK {
		return fmt.Errorf("Slack files.completeUploadExternal failed: %s", completion.Error)
	}
	return nil
}

func (s *slackAttachmentSender) slackForm(ctx context.Context, path string, values url.Values) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.apiURL, "/")+path, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create Slack API request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call Slack API: %w", err)
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Slack API returned %s: %s", response.Status, strings.TrimSpace(string(content)))
	}
	return content, nil
}
