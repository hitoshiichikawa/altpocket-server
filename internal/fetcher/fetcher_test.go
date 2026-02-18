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

func TestFetchXStatusFallsBackToMetaDescription(t *testing.T) {
	body := []byte(`<html><head>
<title></title>
<meta property="og:title" content="Tweet title from meta">
<meta property="og:description" content="Tweet body extracted from og:description.">
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

	parsed, err := f.Fetch(context.Background(), "https://x.com/user/status/123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Title != "Tweet title from meta" {
		t.Fatalf("expected title from meta, got %q", parsed.Title)
	}
	if parsed.ContentFull != "Tweet body extracted from og:description." {
		t.Fatalf("expected content from meta description, got %q", parsed.ContentFull)
	}
}

func TestFetchXStatusFallsBackToOEmbedWhenShellHTMLReturned(t *testing.T) {
	xHTML := []byte(`<html><head><title>X</title></head><body>
<div id="ScriptLoadFailure">
  <span>Something went wrong, but don’t fret — let’s give it another shot.</span>
  <span>Some privacy related extensions may cause issues on x.com. Please disable them and try again.</span>
</div>
</body></html>`)
	oembed := []byte(`{"html":"<blockquote class=\"twitter-tweet\"><p lang=\"en\" dir=\"ltr\">Tweet text from oEmbed fallback</p></blockquote>"}`)

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Host {
			case "x.com":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(xHTML)),
					Header:     http.Header{"Content-Type": []string{"text/html"}},
				}, nil
			case "publish.twitter.com":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(oembed)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			default:
				t.Fatalf("unexpected host: %s", req.URL.Host)
				return nil, nil
			}
		}),
	}
	f := New(1_000_000, 4096, 1024)
	f.Client = client

	parsed, err := f.Fetch(context.Background(), "https://x.com/user/status/123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.ContentFull != "Tweet text from oEmbed fallback" {
		t.Fatalf("expected oEmbed fallback text, got %q", parsed.ContentFull)
	}
}

func TestFetchXStatusLinkOnlyOEmbedFollowsLinkedContent(t *testing.T) {
	xHTML := []byte(`<html><head><title>X</title></head><body>
<div id="ScriptLoadFailure"><span>Something went wrong, but don’t fret — let’s give it another shot.</span></div>
</body></html>`)
	oembed := []byte(`{"html":"<blockquote class=\"twitter-tweet\"><p lang=\"zxx\" dir=\"ltr\"><a href=\"https://t.co/NhTTz2UbKt\">https://t.co/NhTTz2UbKt</a></p></blockquote>"}`)
	linked := []byte(`<html><head><title>Linked page</title></head><body><article><p>This is the linked article content.</p></article></body></html>`)

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Host {
			case "x.com":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(xHTML)),
					Header:     http.Header{"Content-Type": []string{"text/html"}},
				}, nil
			case "publish.twitter.com":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(oembed)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			case "t.co":
				return &http.Response{
					StatusCode: http.StatusFound,
					Body:       io.NopCloser(bytes.NewReader(nil)),
					Header:     http.Header{"Location": []string{"https://example.com/article"}},
				}, nil
			case "example.com":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(linked)),
					Header:     http.Header{"Content-Type": []string{"text/html"}},
				}, nil
			default:
				t.Fatalf("unexpected host: %s", req.URL.Host)
				return nil, nil
			}
		}),
	}
	f := New(1_000_000, 4096, 1024)
	f.Client = client

	parsed, err := f.Fetch(context.Background(), "https://x.com/user/status/123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(parsed.ContentFull, "linked article content") {
		t.Fatalf("expected linked article content, got %q", parsed.ContentFull)
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

func TestIsLikelyLinkOnlyContent(t *testing.T) {
	if !isLikelyLinkOnlyContent("https://t.co/NhTTz2UbKt") {
		t.Fatalf("expected link-only content to be true")
	}
	if isLikelyLinkOnlyContent("Read this https://t.co/NhTTz2UbKt") {
		t.Fatalf("expected text+link content to be false")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
