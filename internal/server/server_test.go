package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPerPageValue(t *testing.T) {
	if perPageValue("10") != 10 {
		t.Fatalf("expected 10")
	}
	if perPageValue("35") != 30 {
		t.Fatalf("invalid should default to 30")
	}
	if perPageValue("") != 30 {
		t.Fatalf("empty should default to 30")
	}
}

func TestDefaultSort(t *testing.T) {
	if defaultSort("relevance") != "relevance" {
		t.Fatalf("expected relevance")
	}
	if defaultSort("other") != "newest" {
		t.Fatalf("expected newest")
	}
}

func TestPageURL(t *testing.T) {
	u, _ := url.Parse("http://example.com/ui/items?q=go")
	got := pageURL(u, 2)
	if got != "http://example.com/ui/items?q=go&page=2" && got != "http://example.com/ui/items?page=2&q=go" {
		t.Fatalf("unexpected url: %s", got)
	}
}

func TestParseTagInput(t *testing.T) {
	got := parseTagInput(" Go,news;go\nweb ")
	if len(got) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(got))
	}
	if got[0] != "go" || got[1] != "news" || got[2] != "web" {
		t.Fatalf("unexpected tags: %#v", got)
	}
}

func TestParseTagFilters(t *testing.T) {
	values := url.Values{}
	values.Add("tag", "Go")
	values.Add("tag", "news")
	values.Set("tags", "web,go")
	got := parseTagFilters(values)
	if len(got) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(got))
	}
	if got[0] != "go" || got[1] != "news" || got[2] != "web" {
		t.Fatalf("unexpected tags: %#v", got)
	}
}

func TestSelectedTagSet(t *testing.T) {
	set := selectedTagSet([]string{"go", "news"})
	if !set["go"] || !set["news"] {
		t.Fatalf("expected selected tags to be true: %#v", set)
	}
	if set["web"] {
		t.Fatalf("unexpected selected tag: %#v", set)
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	got := normalizeWhitespace("  hello \n  world\tgo  ")
	if got != "hello world go" {
		t.Fatalf("unexpected normalized text: %q", got)
	}
}

func TestTruncateUTF8(t *testing.T) {
	got := truncateUTF8("あいうえお", 7)
	if got != "あい" {
		t.Fatalf("expected UTF-8 safe truncation, got %q", got)
	}
}

func TestCreateItemPrefillTitleTruncation(t *testing.T) {
	input := strings.Repeat("a", 600)
	got := truncateUTF8(strings.TrimSpace(input), 500)
	if len(got) != 500 {
		t.Fatalf("expected title truncated to 500 bytes, got %d", len(got))
	}
}

func TestCreateItemPrefillExcerptTruncation(t *testing.T) {
	input := strings.Repeat("b", 300)
	got := truncateUTF8(strings.TrimSpace(input), 200)
	if len(got) != 200 {
		t.Fatalf("expected excerpt truncated to 200 bytes, got %d", len(got))
	}
}

func TestCreateItemPrefillTrimSpace(t *testing.T) {
	got := truncateUTF8(strings.TrimSpace("  hello world  "), 500)
	if got != "hello world" {
		t.Fatalf("expected trimmed title, got %q", got)
	}
}

func TestCreateItemPrefillUTF8TitleTruncation(t *testing.T) {
	// 500 bytes should safely truncate multi-byte characters
	input := strings.Repeat("日", 200) // 200 chars × 3 bytes = 600 bytes
	got := truncateUTF8(strings.TrimSpace(input), 500)
	if len(got) > 500 {
		t.Fatalf("expected title ≤ 500 bytes, got %d", len(got))
	}
	// Should be valid UTF-8 (no broken runes)
	runes := []rune(got)
	if len(runes) != 166 { // 498 bytes / 3 bytes per rune = 166 full runes
		t.Fatalf("expected 166 runes (valid UTF-8), got %d", len(runes))
	}
}

func TestCreateItemPrefillEmptyValuesArePassthrough(t *testing.T) {
	title := truncateUTF8(strings.TrimSpace(""), 500)
	excerpt := truncateUTF8(strings.TrimSpace(""), 200)
	if title != "" {
		t.Fatalf("expected empty title, got %q", title)
	}
	if excerpt != "" {
		t.Fatalf("expected empty excerpt, got %q", excerpt)
	}
}

func TestQuickAddNotice(t *testing.T) {
	if quickAddNotice("created") == "" {
		t.Fatalf("created state should return notice")
	}
	if quickAddNotice("exists") == "" {
		t.Fatalf("exists state should return notice")
	}
	if quickAddNotice("other") != "" {
		t.Fatalf("unexpected notice for unknown state")
	}
}

func TestSettingsNotice(t *testing.T) {
	msg, cls := settingsNotice("export_success")
	if msg == "" || cls != "notice" {
		t.Fatalf("expected export_success notice, got msg=%q class=%q", msg, cls)
	}
	msg, cls = settingsNotice("export_failed")
	if msg == "" || cls != "error" {
		t.Fatalf("expected export_failed error, got msg=%q class=%q", msg, cls)
	}
	msg, cls = settingsNotice("unknown")
	if msg != "" || cls != "" {
		t.Fatalf("expected empty notice for unknown status")
	}
}

func TestGoogleSheetURL(t *testing.T) {
	if got := googleSheetURL(""); got != "" {
		t.Fatalf("expected empty url for blank id")
	}
	if got := googleSheetURL("sheet-id-123"); got != "https://docs.google.com/spreadsheets/d/sheet-id-123/edit" {
		t.Fatalf("unexpected sheet url: %q", got)
	}
}

func TestExtractHTTPURLFromText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "plain text", in: "hello world", want: ""},
		{name: "http url", in: "check http://example.com/page", want: "http://example.com/page"},
		{name: "https with punctuation", in: "see (https://example.com/path?q=1).", want: "https://example.com/path?q=1"},
		{name: "non http scheme ignored", in: "ftp://example.com", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractHTTPURLFromText(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestQuickAddContentPreview(t *testing.T) {
	got := quickAddContentPreview("https://example.com/abc", "Read https://example.com/abc later")
	if got != "Read later" {
		t.Fatalf("unexpected content preview: %q", got)
	}
}

func TestQuickAddContentPreviewTruncatesTo200Runes(t *testing.T) {
	input := strings.Repeat("あ", 220)
	got := quickAddContentPreview("", input)
	if len([]rune(got)) != 200 {
		t.Fatalf("expected 200 runes, got %d", len([]rune(got)))
	}
}

func TestSanitizeQuickAddContentNormalizesWhitespace(t *testing.T) {
	got := sanitizeQuickAddContent("  one \n two\tthree  ")
	if got != "one two three" {
		t.Fatalf("unexpected sanitized content: %q", got)
	}
}

func TestSanitizeQuickAddContentTruncatesTo200Runes(t *testing.T) {
	input := strings.Repeat("a", 220)
	got := sanitizeQuickAddContent(input)
	if len([]rune(got)) != 200 {
		t.Fatalf("expected 200 runes, got %d", len([]rune(got)))
	}
}

func TestSanitizeQuickAddContentInputUsesByteLimit(t *testing.T) {
	got := sanitizeQuickAddContentInput("  abc\n def  ", 7)
	if got != "abc def" {
		t.Fatalf("unexpected sanitized content input: %q", got)
	}
	got = sanitizeQuickAddContentInput("123456789", 4)
	if got != "1234" {
		t.Fatalf("expected byte-limited content, got %q", got)
	}
}

func TestHandleUIQuickAddShareTargetRedirectsWithURLFallback(t *testing.T) {
	s := newAuthTestServer()
	form := "title=Example+Article&text=Read+https%3A%2F%2Fexample.com%2Fabc+later"
	req := httptest.NewRequest(http.MethodPost, "/ui/quick-add/share-target", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	s.handleUIQuickAddShareTarget(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("failed to parse redirect location %q: %v", loc, err)
	}
	if u.Path != "/ui/quick-add" {
		t.Fatalf("expected redirect path /ui/quick-add, got %q", u.Path)
	}
	if got := u.Query().Get("url"); got != "https://example.com/abc" {
		t.Fatalf("expected extracted url, got %q", got)
	}
	if got := u.Query().Get("title"); got != "Example Article" {
		t.Fatalf("expected title to be forwarded, got %q", got)
	}
	if got := u.Query().Get("content"); got != "Read later" {
		t.Fatalf("expected content preview, got %q", got)
	}
}

func TestCORSRejectsDisallowedOrigin(t *testing.T) {
	s := newAuthTestServer()
	s.cfg.CORSAllowOrigins = []string{"chrome-extension://allowed-extension-id"}

	called := false
	handler := s.cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "https://api.example.test/v1/items", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
	if called {
		t.Fatalf("expected next handler not to be called")
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin must not receive allow-origin header")
	}
}

func TestCORSAllowsConfiguredOriginPreflight(t *testing.T) {
	s := newAuthTestServer()
	allowed := "chrome-extension://allowed-extension-id"
	s.cfg.CORSAllowOrigins = []string{allowed}

	handler := s.cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodOptions, "https://api.example.test/v1/items", nil)
	req.Header.Set("Origin", allowed)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != allowed {
		t.Fatalf("expected allow-origin %q, got %q", allowed, rr.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSAllowsSameHostOrigin(t *testing.T) {
	s := newAuthTestServer()
	s.cfg.CORSAllowOrigins = nil

	called := false
	handler := s.cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "https://api.example.test/v1/items", nil)
	req.Header.Set("Origin", "https://api.example.test")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !called {
		t.Fatalf("expected next handler to be called")
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "https://api.example.test" {
		t.Fatalf("expected same-host origin to be allowed")
	}
}
