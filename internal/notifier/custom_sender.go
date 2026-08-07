package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/Masterminds/sprig"
	"github.com/projectdiscovery/notify/pkg/providers/custom"
	"github.com/projectdiscovery/notify/pkg/utils"
)

type customSender struct {
	option  *custom.Options
	client  *http.Client
	counter int
}

func newCustomSender(option *custom.Options) *customSender {
	return &customSender{option: option, client: &http.Client{Timeout: 30 * time.Second}}
}

func (s *customSender) Send(message, cliFormat string) error {
	s.counter++
	payload, err := s.payload(message, cliFormat)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(s.option.CustomMethod, s.option.CustomWebhookURL, bytes.NewBufferString(payload))
	if err != nil {
		return fmt.Errorf("create custom notification request for id %s: %w", s.option.ID, err)
	}
	for name, value := range s.option.CustomHeaders {
		request.Header.Set(name, value)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("send custom notification for id %s: %w", s.option.ID, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("custom notification for id %s returned HTTP %s", s.option.ID, response.Status)
	}
	return nil
}

func (s *customSender) payload(message, cliFormat string) (string, error) {
	if s.option.CustomSprig != "" {
		var data map[string]any
		if err := json.Unmarshal([]byte(message), &data); err != nil {
			return "", fmt.Errorf("decode custom notification data for id %s: %w", s.option.ID, err)
		}
		tmpl, err := template.New("custom").Funcs(sprig.TxtFuncMap()).Parse(s.option.CustomSprig)
		if err != nil {
			return "", fmt.Errorf("parse custom template for id %s: %w", s.option.ID, err)
		}
		var output bytes.Buffer
		if err := tmpl.Execute(&output, data); err != nil {
			return "", fmt.Errorf("execute custom template for id %s: %w", s.option.ID, err)
		}
		return output.String(), nil
	}
	if strings.Contains(s.option.CustomFormat, "{{dataJsonString}}") {
		encoded, err := json.Marshal(message)
		if err != nil {
			return "", fmt.Errorf("encode custom notification for id %s: %w", s.option.ID, err)
		}
		return strings.ReplaceAll(s.option.CustomFormat, "{{dataJsonString}}", string(encoded)), nil
	}
	return utils.FormatMessage(message, utils.SelectFormat(cliFormat, s.option.CustomFormat), s.counter), nil
}
