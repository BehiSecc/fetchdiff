package fetch

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
)

const (
	DefaultTimeout      = 30 * time.Second
	DefaultMaxRedirects = 10
	DefaultMaxRetries   = 3
	DefaultMaxBodyBytes = 25 << 20
	DefaultUserAgent    = "fetchdiff/0.1"
)

var errRedirectLimit = errors.New("redirect limit reached")

type Request struct {
	URL          string
	Headers      map[string]string
	ETag         string
	LastModified string
}

type Response struct {
	StatusCode   int
	Status       string
	EffectiveURL string
	ContentType  string
	ETag         string
	LastModified string
	Content      []byte
	NotModified  bool
	Attempts     int
}

type Error struct {
	StatusCode   int
	Status       string
	EffectiveURL string
	Fingerprint  string
	RetryAfter   time.Duration
	Err          error
}

func (e *Error) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("HTTP %s", e.Status)
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

type Client struct {
	httpClient   *http.Client
	maxRetries   int
	maxBodyBytes int64
	userAgent    string
	sleep        func(context.Context, time.Duration) error
}

type Options struct {
	Timeout        time.Duration
	MaxRedirects   int
	MaxRetries     int
	DisableRetries bool
	MaxBodyBytes   int64
	UserAgent      string
	HTTPClient     *http.Client
	Sleep          func(context.Context, time.Duration) error
}

func New(options Options) *Client {
	if options.Timeout <= 0 {
		options.Timeout = DefaultTimeout
	}
	if options.MaxRedirects <= 0 {
		options.MaxRedirects = DefaultMaxRedirects
	}
	if options.DisableRetries || options.MaxRetries < 0 {
		options.MaxRetries = 0
	} else if options.MaxRetries == 0 {
		options.MaxRetries = DefaultMaxRetries
	}
	if options.MaxBodyBytes <= 0 {
		options.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if options.UserAgent == "" {
		options.UserAgent = DefaultUserAgent
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: options.Timeout,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) > options.MaxRedirects {
					return fmt.Errorf("%w after %d redirects", errRedirectLimit, options.MaxRedirects)
				}
				return nil
			},
		}
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	return &Client{httpClient: client, maxRetries: options.MaxRetries, maxBodyBytes: options.MaxBodyBytes, userAgent: options.UserAgent, sleep: sleep}
}

func (c *Client) Fetch(ctx context.Context, request Request) (Response, error) {
	var lastErr error
	for attempt := 1; attempt <= c.maxRetries+1; attempt++ {
		response, err := c.fetchOnce(ctx, request)
		response.Attempts = attempt
		if err == nil {
			return response, nil
		}
		lastErr = err
		if attempt > c.maxRetries || !retryable(err) {
			return response, err
		}
		delay := retryDelay(err, attempt)
		if err := c.sleep(ctx, delay); err != nil {
			return response, err
		}
	}
	return Response{}, lastErr
}

func (c *Client) fetchOnce(ctx context.Context, request Request) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, request.URL, nil)
	if err != nil {
		return Response{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept-Encoding", "gzip, br")
	for name, value := range request.Headers {
		req.Header.Set(name, value)
	}
	if request.ETag != "" {
		req.Header.Set("If-None-Match", request.ETag)
	}
	if request.LastModified != "" {
		req.Header.Set("If-Modified-Since", request.LastModified)
	}

	httpResponse, err := c.httpClient.Do(req)
	if err != nil {
		fingerprint := "network"
		var netErr net.Error
		if errors.Is(err, errRedirectLimit) {
			fingerprint = "redirect"
		} else if errors.As(err, &netErr) && netErr.Timeout() {
			fingerprint = "timeout"
		}
		return Response{}, &Error{Fingerprint: fingerprint, Err: err}
	}
	defer httpResponse.Body.Close()

	response := Response{
		StatusCode:   httpResponse.StatusCode,
		Status:       httpResponse.Status,
		EffectiveURL: httpResponse.Request.URL.String(),
		ContentType:  httpResponse.Header.Get("Content-Type"),
		ETag:         httpResponse.Header.Get("ETag"),
		LastModified: httpResponse.Header.Get("Last-Modified"),
	}
	if httpResponse.StatusCode == http.StatusNotModified {
		response.NotModified = true
		return response, nil
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 || httpResponse.StatusCode == http.StatusPartialContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(httpResponse.Body, 4<<10))
		fetchErr := &Error{
			StatusCode:   httpResponse.StatusCode,
			Status:       httpResponse.Status,
			EffectiveURL: response.EffectiveURL,
			Fingerprint:  "http:" + strconv.Itoa(httpResponse.StatusCode),
			Err:          fmt.Errorf("unexpected HTTP status %s", httpResponse.Status),
		}
		if httpResponse.StatusCode == http.StatusTooManyRequests {
			fetchErr.RetryAfter = parseRetryAfter(httpResponse.Header.Get("Retry-After"), time.Now())
		}
		return response, fetchErr
	}

	reader, err := decodedReader(httpResponse.Body, httpResponse.Header.Get("Content-Encoding"))
	if err != nil {
		return response, &Error{Fingerprint: "encoding", Err: err}
	}
	content, err := io.ReadAll(io.LimitReader(reader, c.maxBodyBytes+1))
	if err != nil {
		return response, &Error{Fingerprint: "body", Err: fmt.Errorf("read response body: %w", err)}
	}
	if int64(len(content)) > c.maxBodyBytes {
		return response, &Error{Fingerprint: "body:too-large", Err: fmt.Errorf("decoded response exceeds %d bytes", c.maxBodyBytes)}
	}
	response.Content = content
	return response, nil
}

func decodedReader(reader io.Reader, encoding string) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		return reader, nil
	case "gzip":
		zr, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("open gzip response: %w", err)
		}
		return zr, nil
	case "br":
		return brotli.NewReader(reader), nil
	default:
		return nil, fmt.Errorf("unsupported content encoding %q", encoding)
	}
}

func retryable(err error) bool {
	var fetchErr *Error
	if !errors.As(err, &fetchErr) {
		return false
	}
	if fetchErr.StatusCode == 0 {
		return fetchErr.Fingerprint == "network" || fetchErr.Fingerprint == "timeout" || fetchErr.Fingerprint == "body"
	}
	return fetchErr.StatusCode == http.StatusRequestTimeout || fetchErr.StatusCode == http.StatusTooManyRequests || fetchErr.StatusCode >= 500
}

func retryDelay(err error, attempt int) time.Duration {
	var fetchErr *Error
	if errors.As(err, &fetchErr) && fetchErr.RetryAfter > 0 {
		if fetchErr.RetryAfter > 30*time.Second {
			return 30 * time.Second
		}
		return fetchErr.RetryAfter
	}
	delay := 250 * time.Millisecond * time.Duration(1<<(attempt-1))
	if delay > 5*time.Second {
		return 5 * time.Second
	}
	return delay
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
