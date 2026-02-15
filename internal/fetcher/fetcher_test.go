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

func TestFetchExtractsReadableArticleContent(t *testing.T) {
	body := []byte(`<html><head><title>Readable</title><script>window.banner='noise'</script></head><body>
<header>Site Header</header>
<nav>Top navigation links</nav>
<main>
  <article>
    <h1>Readable Article</h1>
    <p>This is the actual article body text that users expect to read in the saved content view.</p>
    <p>This second paragraph should also be included in full text extraction output.</p>
  </article>
</main>
<footer>Footer noise</footer>
<script>console.log('noise')</script>
</body></html>`)
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
				Header:     http.Header{"Content-Type": []string{"text/html"}},
			}, nil
		}),
	}
	f := New(1_000_000, 4096, 1024)
	f.Client = client
	parsed, err := f.Fetch(context.Background(), "http://example.com/article")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(parsed.ContentFull, "Readable Article") {
		t.Fatalf("expected article heading in content_full, got %q", parsed.ContentFull)
	}
	if !strings.Contains(parsed.ContentFull, "actual article body text") {
		t.Fatalf("expected article body in content_full, got %q", parsed.ContentFull)
	}
	for _, unwanted := range []string{"Top navigation", "Site Header", "Footer noise", "window.banner", "console.log"} {
		if strings.Contains(parsed.ContentFull, unwanted) {
			t.Fatalf("unexpected non-content text %q in content_full: %q", unwanted, parsed.ContentFull)
		}
	}
}

func TestFetchFallsBackToBodyTextWhenNoSemanticBlocks(t *testing.T) {
	body := []byte("<html><head><title>Fallback</title></head><body>Plain text page without paragraph tags.</body></html>")
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
				Header:     http.Header{"Content-Type": []string{"text/html"}},
			}, nil
		}),
	}
	f := New(1_000_000, 4096, 1024)
	f.Client = client
	parsed, err := f.Fetch(context.Background(), "http://example.com/fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(parsed.ContentFull, "Plain text page without paragraph tags.") {
		t.Fatalf("expected fallback body text, got %q", parsed.ContentFull)
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
