package fetcher

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFetchSuccess(t *testing.T) {
	body := []byte("<html><head><title>Title</title></head><body>Hello world</body></html>")
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
				Header:     http.Header{"Content-Type": []string{"text/html"}},
			}, nil
		}),
	}
	f := New(1_000_000, 1024, 512)
	f.Client = client
	parsed, err := f.Fetch(context.Background(), "http://example.com/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Title != "Title" {
		t.Fatalf("title mismatch: %s", parsed.Title)
	}
	if !strings.Contains(parsed.ContentFull, "Hello world") {
		t.Fatalf("content missing")
	}
	if parsed.ContentBytes == 0 {
		t.Fatalf("content bytes should be > 0")
	}

}

func TestFetchBadStatus(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(bytes.NewReader(nil)),
				Header:     http.Header{},
			}, nil
		}),
	}
	f := New(1_000_000, 1024, 512)
	f.Client = client
	_, err := f.Fetch(context.Background(), "http://example.com/404")
	if err != ErrBadStatus {
		t.Fatalf("expected ErrBadStatus, got %v", err)
	}
}

func TestFetchTooLarge(t *testing.T) {
	large := strings.Repeat("a", 2000)
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(large))),
				Header:     http.Header{},
			}, nil
		}),
	}
	f := New(100, 1024, 512)
	f.Client = client
	_, err := f.Fetch(context.Background(), "http://example.com/large")
	if err != ErrTooLarge {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
