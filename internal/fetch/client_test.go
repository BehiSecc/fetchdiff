package fetch

import (
	"bytes"
	"compress/gzip"
	"context"
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
