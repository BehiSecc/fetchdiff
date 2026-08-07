package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/projectdiscovery/notify/pkg/providers/custom"
	"github.com/projectdiscovery/notify/pkg/providers/discord"
	"github.com/projectdiscovery/notify/pkg/providers/googlechat"
	"github.com/projectdiscovery/notify/pkg/providers/gotify"
	"github.com/projectdiscovery/notify/pkg/providers/pushover"
	"github.com/projectdiscovery/notify/pkg/providers/slack"
	"github.com/projectdiscovery/notify/pkg/providers/smtp"
	"github.com/projectdiscovery/notify/pkg/providers/teams"
	"github.com/projectdiscovery/notify/pkg/providers/telegram"
	"gopkg.in/yaml.v3"
)

const ChunkLimit = 1800

type providerSender interface {
	Send(message, cliFormat string) error
}

type Config struct {
	Slack      []*slack.Options      `yaml:"slack,omitempty"`
	Discord    []*discord.Options    `yaml:"discord,omitempty"`
	Telegram   []*telegram.Options   `yaml:"telegram,omitempty"`
	Pushover   []*pushover.Options   `yaml:"pushover,omitempty"`
	SMTP       []*smtp.Options       `yaml:"smtp,omitempty"`
	GoogleChat []*googlechat.Options `yaml:"googlechat,omitempty"`
	Teams      []*teams.Options      `yaml:"teams,omitempty"`
	Gotify     []*gotify.Options     `yaml:"gotify,omitempty"`
	Custom     []*custom.Options     `yaml:"custom,omitempty"`
}

type Message struct {
	Text string            `json:"text"`
	Data map[string]string `json:"data,omitempty"`
}

type Filter struct {
	Providers []string
	IDs       []string
}

type Result struct {
	Key      string
	Provider string
	ID       string
	Err      error
}

type destination struct {
	key      string
	provider string
	id       string
	sender   providerSender
	sprig    bool
	secrets  []string
}

type Client struct {
	destinations map[string]destination
	keys         []string
}

func Load(path string) (*Client, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read provider configuration: %w", err)
	}
	var config Config
	if err := yaml.Unmarshal(content, &config); err != nil {
		return nil, fmt.Errorf("parse provider configuration: %w", err)
	}
	return New(config)
}

func New(config Config) (*Client, error) {
	client := &Client{destinations: make(map[string]destination)}
	var errs []error

	for _, option := range config.Slack {
		if option == nil {
			errs = append(errs, errors.New("slack contains an empty entry"))
			continue
		}
		if err := client.requireID("slack", option.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		if option.SlackThreads {
			if option.SlackToken == "" || option.SlackChannel == "" {
				errs = append(errs, fmt.Errorf("slack:%s requires slack_token and slack_channel for threads", option.ID))
				continue
			}
		} else if !strings.HasPrefix(option.SlackWebHookURL, "https://hooks.slack.com/services/") {
			errs = append(errs, fmt.Errorf("slack:%s has an invalid slack_webhook_url", option.ID))
			continue
		}
		sender, err := slack.New([]*slack.Options{option}, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("slack:%s: %w", option.ID, err))
			continue
		}
		client.add("slack", option.ID, sender, false, option.SlackWebHookURL, option.SlackToken)
	}

	for _, option := range config.Discord {
		if option == nil {
			errs = append(errs, errors.New("discord contains an empty entry"))
			continue
		}
		if err := client.requireID("discord", option.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := requireHTTPURL("discord", option.ID, option.DiscordWebHookURL); err != nil {
			errs = append(errs, err)
			continue
		}
		sender, err := discord.New([]*discord.Options{option}, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("discord:%s: %w", option.ID, err))
			continue
		}
		client.add("discord", option.ID, sender, false, option.DiscordWebHookURL)
	}

	for _, option := range config.Telegram {
		if option == nil {
			errs = append(errs, errors.New("telegram contains an empty entry"))
			continue
		}
		if err := client.requireID("telegram", option.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		if option.TelegramAPIKey == "" || option.TelegramChatID == "" {
			errs = append(errs, fmt.Errorf("telegram:%s requires telegram_api_key and telegram_chat_id", option.ID))
			continue
		}
		sender, err := telegram.New([]*telegram.Options{option}, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("telegram:%s: %w", option.ID, err))
			continue
		}
		client.add("telegram", option.ID, sender, false, option.TelegramAPIKey, option.TelegramChatID)
	}

	for _, option := range config.Pushover {
		if option == nil {
			errs = append(errs, errors.New("pushover contains an empty entry"))
			continue
		}
		if err := client.requireID("pushover", option.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		if option.PushoverApiToken == "" || option.UserKey == "" {
			errs = append(errs, fmt.Errorf("pushover:%s requires pushover_api_token and pushover_user_key", option.ID))
			continue
		}
		sender, err := pushover.New([]*pushover.Options{option}, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("pushover:%s: %w", option.ID, err))
			continue
		}
		client.add("pushover", option.ID, sender, false, option.PushoverApiToken, option.UserKey)
	}

	for _, option := range config.SMTP {
		if option == nil {
			errs = append(errs, errors.New("smtp contains an empty entry"))
			continue
		}
		if err := client.requireID("smtp", option.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		if option.Server == "" || option.FromAddress == "" || len(option.SMTPCC) == 0 {
			errs = append(errs, fmt.Errorf("smtp:%s requires smtp_server, from_address, and at least one smtp_cc recipient", option.ID))
			continue
		}
		sender, err := smtp.New([]*smtp.Options{option}, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("smtp:%s: %w", option.ID, err))
			continue
		}
		client.add("smtp", option.ID, sender, false, option.Username, option.Password)
	}

	for _, option := range config.GoogleChat {
		if option == nil {
			errs = append(errs, errors.New("googlechat contains an empty entry"))
			continue
		}
		if err := client.requireID("googlechat", option.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		if option.Space == "" || option.Key == "" || option.Token == "" {
			errs = append(errs, fmt.Errorf("googlechat:%s requires space, key, and token", option.ID))
			continue
		}
		sender, err := googlechat.New([]*googlechat.Options{option}, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("googlechat:%s: %w", option.ID, err))
			continue
		}
		client.add("googlechat", option.ID, sender, false, option.Key, option.Token)
	}

	for _, option := range config.Teams {
		if option == nil {
			errs = append(errs, errors.New("teams contains an empty entry"))
			continue
		}
		if err := client.requireID("teams", option.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		parts := strings.Split(option.TeamsWebHookURL, "/webhookb2/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			errs = append(errs, fmt.Errorf("teams:%s has an invalid teams_webhook_url", option.ID))
			continue
		}
		sender, err := teams.New([]*teams.Options{option}, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("teams:%s: %w", option.ID, err))
			continue
		}
		client.add("teams", option.ID, sender, false, option.TeamsWebHookURL)
	}

	for _, option := range config.Gotify {
		if option == nil {
			errs = append(errs, errors.New("gotify contains an empty entry"))
			continue
		}
		if err := client.requireID("gotify", option.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		if option.GotifyHost == "" || option.GotifyPort == "" || option.GotifyToken == "" {
			errs = append(errs, fmt.Errorf("gotify:%s requires gotify_host, gotify_port, and gotify_token", option.ID))
			continue
		}
		sender, err := gotify.New([]*gotify.Options{option}, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("gotify:%s: %w", option.ID, err))
			continue
		}
		client.add("gotify", option.ID, sender, false, option.GotifyToken)
	}

	for _, option := range config.Custom {
		if option == nil {
			errs = append(errs, errors.New("custom contains an empty entry"))
			continue
		}
		if err := client.requireID("custom", option.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := requireHTTPURL("custom", option.ID, option.CustomWebhookURL); err != nil {
			errs = append(errs, err)
			continue
		}
		if option.CustomMethod == "" {
			errs = append(errs, fmt.Errorf("custom:%s requires custom_method", option.ID))
			continue
		}
		if _, err := http.NewRequest(option.CustomMethod, option.CustomWebhookURL, nil); err != nil {
			errs = append(errs, fmt.Errorf("custom:%s has an invalid method or URL", option.ID))
			continue
		}
		sender, err := custom.New([]*custom.Options{option}, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("custom:%s: %w", option.ID, err))
			continue
		}
		secrets := []string{option.CustomWebhookURL}
		for _, value := range option.CustomHeaders {
			secrets = append(secrets, value)
		}
		client.add("custom", option.ID, sender, option.CustomSprig != "", secrets...)
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	sort.Strings(client.keys)
	return client, nil
}

func (c *Client) Keys() []string {
	return append([]string(nil), c.keys...)
}

func (c *Client) Count() int { return len(c.keys) }

func (c *Client) Has(key string) bool {
	_, ok := c.destinations[key]
	return ok
}

func (c *Client) Select(filter Filter) []string {
	providers := stringSet(filter.Providers)
	ids := stringSet(filter.IDs)
	var selected []string
	for _, key := range c.keys {
		destination := c.destinations[key]
		if len(providers) > 0 && !providers[strings.ToLower(destination.provider)] {
			continue
		}
		if len(ids) > 0 && !ids[strings.ToLower(destination.id)] {
			continue
		}
		selected = append(selected, key)
	}
	return selected
}

func (c *Client) Send(ctx context.Context, key string, message Message) error {
	destination, ok := c.destinations[key]
	if !ok {
		return fmt.Errorf("notification destination %q is not configured", key)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload := message.Text
	if destination.sprig {
		data := make(map[string]string, len(message.Data)+1)
		for key, value := range message.Data {
			data[key] = value
		}
		data["data"] = message.Text
		encoded, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("encode custom notification data: %w", err)
		}
		payload = string(encoded)
	}
	if err := destination.sender.Send(payload, ""); err != nil {
		return errors.New(redact(err.Error(), destination.secrets))
	}
	return nil
}

func (c *Client) SendAll(ctx context.Context, message Message, filter Filter) []Result {
	keys := c.Select(filter)
	results := make([]Result, 0, len(keys))
	for _, key := range keys {
		destination := c.destinations[key]
		results = append(results, Result{Key: key, Provider: destination.provider, ID: destination.id, Err: c.Send(ctx, key, message)})
	}
	return results
}

func SplitMessage(message string, limit int) []string {
	if limit <= 0 {
		limit = ChunkLimit
	}
	runes := []rune(message)
	if len(runes) <= limit {
		return []string{message}
	}
	count := (len(runes) + limit - 1) / limit
	chunks := make([]string, 0, count)
	for index := 0; index < count; index++ {
		start := index * limit
		end := start + limit
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, fmt.Sprintf("FetchDiff · %d/%d\n\n%s", index+1, count, string(runes[start:end])))
	}
	return chunks
}

func (c *Client) add(provider, id string, sender providerSender, sprig bool, secrets ...string) {
	key := provider + ":" + id
	if _, exists := c.destinations[key]; exists {
		// Validation reports duplicates from New after construction.
		return
	}
	cleanSecrets := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			cleanSecrets = append(cleanSecrets, secret)
		}
	}
	c.destinations[key] = destination{key: key, provider: provider, id: id, sender: sender, sprig: sprig, secrets: cleanSecrets}
	c.keys = append(c.keys, key)
}

func (c *Client) requireID(provider, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%s provider entry requires a non-empty id", provider)
	}
	if _, exists := c.destinations[provider+":"+id]; exists {
		return fmt.Errorf("%s provider id %q is duplicated", provider, id)
	}
	return nil
}

func requireHTTPURL(provider, id, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s:%s has an invalid webhook URL", provider, id)
	}
	return nil
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[strings.ToLower(strings.TrimSpace(value))] = true
	}
	return result
}

func redact(message string, secrets []string) string {
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return message
}
