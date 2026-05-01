package fetcher

import (
	"bytes"
	"context"
	"errors"
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

func TestFetchTruncatesOversizedResponse(t *testing.T) {
	large := "<html><head><title>Big</title></head><body><p>" +
		strings.Repeat("a", 2000) +
		"</p><p>tail-marker</p></body></html>"
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(large))),
				Header:     http.Header{},
			}, nil
		}),
	}
	f := New(120, 1024, 512)
	f.Client = client
	parsed, err := f.Fetch(context.Background(), "http://example.com/large")
	if err != nil {
		t.Fatalf("expected oversized response to be truncated, got error: %v", err)
	}
	if parsed.Title != "Big" {
		t.Fatalf("title mismatch: %s", parsed.Title)
	}
	if parsed.ContentBytes == 0 {
		t.Fatalf("expected non-empty content after truncation")
	}
	if strings.Contains(parsed.ContentFull, "tail-marker") {
		t.Fatalf("expected tail content to be truncated, got %q", parsed.ContentFull)
	}
}

func TestFetchFallsBackToMetaTitleAndDescription(t *testing.T) {
	body := []byte(`<html><head>
<title></title>
<meta property="og:title" content="Meta title">
<meta name="description" content="Meta description fallback">
</head><body><div id="root"></div></body></html>`)
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

	parsed, err := f.Fetch(context.Background(), "https://example.com/meta")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Title != "Meta title" {
		t.Fatalf("expected title from meta, got %q", parsed.Title)
	}
	if parsed.ContentFull != "Meta description fallback" {
		t.Fatalf("expected content from meta description, got %q", parsed.ContentFull)
	}
}

func TestFetchReturnsErrNoContentWhenExtractedAndMetaAreEmpty(t *testing.T) {
	body := []byte("<html><head><title>Empty</title></head><body><div></div></body></html>")
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

	_, err := f.Fetch(context.Background(), "https://example.com/empty")
	if err != ErrNoContent {
		t.Fatalf("expected ErrNoContent, got %v", err)
	}
}

func TestFetchReturnsErrNoContentWhenTitleIsMissing(t *testing.T) {
	body := []byte(`<html><head><title></title></head><body><article><p>Body exists.</p></article></body></html>`)
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

	_, err := f.Fetch(context.Background(), "https://example.com/title-missing")
	if err != ErrNoContent {
		t.Fatalf("expected ErrNoContent, got %v", err)
	}
}

// TestFetchRejectsIPLiteralURLs covers Requirement 1 AC-6 + Requirement 3
// AC-1 at the Fetch entry point: when the URL is an IP literal pointing into
// a blocked range, Fetch must return an error that satisfies
// errors.Is(err, ErrBlockedIP) without performing any HTTP work.
func TestFetchRejectsIPLiteralURLs(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/",
		"http://[::1]/",
		"http://10.0.0.1/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://[fc00::1]/",
		"http://[fe80::1]/",
		"http://255.255.255.255/",
		"http://0.0.0.0/",
		"http://[::ffff:127.0.0.1]/",
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			// Arrange: real Fetcher with default transport. We do NOT swap in
			// roundTripFunc here because the early literal check must fire
			// before any RoundTrip call.
			f := New(1_000_000, 1024, 512)

			// Act.
			_, err := f.Fetch(context.Background(), u)

			// Assert.
			if err == nil {
				t.Fatalf("expected blocked_ip error for %q, got nil", u)
			}
			if !errors.Is(err, ErrBlockedIP) {
				t.Fatalf("expected ErrBlockedIP for %q, got %v", u, err)
			}
		})
	}
}

// TestFetchAllowsRoundTripFuncOverride confirms that swapping Client to a
// roundTripFunc-based client (the existing test pattern) still works for
// public-IP-literal-style URLs — the SSRF guard lives in DialContext on the
// default Transport, so a fully replaced Client bypasses it. This is the
// expected behavior documented in OQ-1's option (b) bypass-by-Client-swap.
// (Requirement 4 AC-2: existing roundTripFunc tests must remain green.)
func TestFetchAllowsRoundTripFuncOverride(t *testing.T) {
	// Arrange.
	body := []byte("<html><head><title>OK</title></head><body><p>fine</p></body></html>")
	f := New(1_000_000, 1024, 512)
	f.Client = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
				Header:     http.Header{"Content-Type": []string{"text/html"}},
			}, nil
		}),
	}

	// Act: use a public hostname so the early literal check is a no-op.
	parsed, err := f.Fetch(context.Background(), "http://example.com/")

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error with roundTripFunc override: %v", err)
	}
	if parsed.Title != "OK" {
		t.Fatalf("title mismatch: %q", parsed.Title)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
