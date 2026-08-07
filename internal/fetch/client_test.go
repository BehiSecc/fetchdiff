package fetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

func noSleep(context.Context, time.Duration) error { return nil }

func TestFetchConditionalAndNotModified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` && r.Header.Get("If-Modified-Since") != "" {
			w.Header().Set("ETag", `"v1"`)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		_, _ = io.WriteString(w, "hello")
	}))
	defer server.Close()

	client := New(Options{Sleep: noSleep})
	first, err := client.Fetch(context.Background(), Request{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Fetch(context.Background(), Request{URL: server.URL, ETag: first.ETag, LastModified: first.LastModified})
	if err != nil {
		t.Fatal(err)
	}
	if !second.NotModified || second.StatusCode != http.StatusNotModified {
		t.Fatalf("response = %#v", second)
	}
}

func TestFetchDecodesGzipAndBrotli(t *testing.T) {
	for _, encoding := range []string{"gzip", "br"} {
		t.Run(encoding, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Encoding", encoding)
				if encoding == "gzip" {
					zw := gzip.NewWriter(w)
					_, _ = zw.Write([]byte("decoded"))
					_ = zw.Close()
					return
				}
				zw := brotli.NewWriter(w)
				_, _ = zw.Write([]byte("decoded"))
				_ = zw.Close()
			}))
			defer server.Close()
			response, err := New(Options{Sleep: noSleep}).Fetch(context.Background(), Request{URL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if string(response.Content) != "decoded" {
				t.Fatalf("content = %q", response.Content)
			}
		})
	}
}

func TestFetchRetries429(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "later", http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	response, err := New(Options{MaxRetries: 3, Sleep: noSleep}).Fetch(context.Background(), Request{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if response.Attempts != 3 || string(response.Content) != "ok" {
		t.Fatalf("response = %#v", response)
	}
}

func TestFetchRecordsEffectiveURL(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/final.js", http.StatusFound)
	}))
	defer source.Close()
	response, err := New(Options{Sleep: noSleep}).Fetch(context.Background(), Request{URL: source.URL})
	if err != nil {
		t.Fatal(err)
	}
	if response.EffectiveURL != destination.URL+"/final.js" {
		t.Fatalf("effective URL = %q", response.EffectiveURL)
	}
}

func TestFetchStopsAtRedirectLimitWithoutRetrying(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(nil)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, server.URL, http.StatusFound)
	})
	defer server.Close()
	_, err := New(Options{MaxRedirects: 2, MaxRetries: 3, Sleep: noSleep}).Fetch(context.Background(), Request{URL: server.URL})
	if err == nil {
		t.Fatal("expected redirect error")
	}
	var fetchErr *Error
	if !errors.As(err, &fetchErr) || fetchErr.Fingerprint != "redirect" {
		t.Fatalf("error = %v", err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
}

func TestFetchRetryExhaustion(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	_, err := New(Options{MaxRetries: 2, Sleep: noSleep}).Fetch(context.Background(), Request{URL: server.URL})
	if err == nil {
		t.Fatal("expected retry exhaustion")
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
}

func TestFetchRejectsOversizedDecodedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.Copy(w, bytes.NewReader(make([]byte, 11)))
	}))
	defer server.Close()
	_, err := New(Options{MaxBodyBytes: 10, Sleep: noSleep}).Fetch(context.Background(), Request{URL: server.URL})
	if err == nil {
		t.Fatal("expected size error")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	if got := parseRetryAfter("5", now); got != 5*time.Second {
		t.Fatalf("seconds delay = %s", got)
	}
	header := now.Add(4 * time.Second).Format(http.TimeFormat)
	if got := parseRetryAfter(header, now); got != 4*time.Second {
		t.Fatalf("date delay = %s, header %s", got, strconv.Quote(header))
	}
}
