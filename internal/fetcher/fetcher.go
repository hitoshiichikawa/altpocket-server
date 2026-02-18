package fetcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

var (
	ErrTooLarge     = errors.New("response_too_large")
	ErrTooManyRedir = errors.New("too_many_redirects")
	ErrBadStatus    = errors.New("bad_status")
	ErrNoContent    = errors.New("no_content")
)

type Result struct {
	Title         string
	Excerpt       string
	ContentFull   string
	ContentSearch string
	ContentBytes  int
}

type Fetcher struct {
	Client             *http.Client
	MaxBytes           int64
	ContentFullLimit   int
	ContentSearchLimit int
}

func New(maxBytes int64, contentFullLimit, contentSearchLimit int) *Fetcher {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return ErrTooManyRedir
			}
			return nil
		},
	}
	return &Fetcher{Client: client, MaxBytes: maxBytes, ContentFullLimit: contentFullLimit, ContentSearchLimit: contentSearchLimit}
}

func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Result, error) {
	return f.fetch(ctx, rawURL, 0)
}

func (f *Fetcher) fetch(ctx context.Context, rawURL string, depth int) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "altpocket/1.0")

	resp, err := f.Client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Result{}, ErrBadStatus
	}

	limited := io.LimitReader(resp.Body, f.MaxBytes+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return Result{}, err
	}
	if int64(len(buf)) > f.MaxBytes {
		buf = buf[:f.MaxBytes]
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(buf))
	if err != nil {
		return Result{}, err
	}
	title := pageTitle(doc)
	contentText := extractReadableContent(doc)
	if isXStatusURL(rawURL) && isLikelyXShellContent(contentText) {
		contentText = ""
	}
	contentText = contentFallback(doc, rawURL, contentText)
	if normalizeText(contentText) == "" && isXStatusURL(rawURL) {
		if fallback, err := f.fetchXStatusOEmbedText(ctx, rawURL); err == nil {
			contentText = fallback
		}
	}
	if depth < 1 && isLikelyLinkOnlyContent(contentText) {
		if link := extractFirstHTTPURL(contentText); link != "" && !sameNormalizedURL(rawURL, link) {
			if linked, err := f.fetch(ctx, link, depth+1); err == nil {
				return linked, nil
			}
		}
	}
	contentFull := truncateUTF8(contentText, f.ContentFullLimit)
	if normalizeText(contentFull) == "" {
		return Result{}, ErrNoContent
	}
	searchText := normalizeText(contentFull)
	contentSearch := truncateUTF8(searchText, f.ContentSearchLimit)
	excerpt := truncateUTF8(searchText, 200)

	return Result{
		Title:         title,
		Excerpt:       excerpt,
		ContentFull:   contentFull,
		ContentSearch: contentSearch,
		ContentBytes:  len([]byte(contentFull)),
	}, nil
}

var pruneSelectors = []string{
	"script",
	"style",
	"noscript",
	"template",
	"iframe",
	"canvas",
	"svg",
	"object",
	"embed",
	"nav",
	"aside",
	"footer",
	"form",
	"button",
	"input",
	"select",
	"textarea",
	"[hidden]",
	"[aria-hidden='true']",
	"[role='navigation']",
	"[role='contentinfo']",
	"[role='search']",
	"[style*='display:none']",
	"[style*='visibility:hidden']",
	"[class*='sidebar']",
	"[class*='footer']",
	"[class*='nav']",
	"[class*='menu']",
	"[class*='breadcrumb']",
	"[class*='share']",
	"[class*='social']",
	"[class*='related']",
	"[class*='comment']",
	"[class*='ad-']",
	"[class*='ads']",
	"[id*='sidebar']",
	"[id*='footer']",
	"[id*='nav']",
	"[id*='menu']",
	"[id*='breadcrumb']",
	"[id*='comment']",
	"[id*='ad-']",
	"[id*='ads']",
}

var contentSelectors = []string{
	"article",
	"main",
	"[role='main']",
	"#content",
	"#main",
	".content",
	".main",
	".post-content",
	".entry-content",
	".article-content",
	".article-body",
	".markdown-body",
}

func extractReadableContent(doc *goquery.Document) string {
	pruneNonContent(doc)
	root := selectContentRoot(doc)
	if root.Length() == 0 {
		root = doc.Find("body").First()
	}
	if root.Length() == 0 {
		root = doc.Selection
	}

	blocks := extractBlocks(root)
	if len(blocks) == 0 {
		return normalizeText(root.Text())
	}
	return strings.Join(blocks, "\n\n")
}

func pruneNonContent(doc *goquery.Document) {
	for _, selector := range pruneSelectors {
		doc.Find(selector).Each(func(_ int, s *goquery.Selection) {
			s.Remove()
		})
	}
}

func selectContentRoot(doc *goquery.Document) *goquery.Selection {
	best := doc.Find("body").First()
	bestScore := textScore(best.Text())

	for _, selector := range contentSelectors {
		doc.Find(selector).Each(func(_ int, s *goquery.Selection) {
			score := textScore(s.Text())
			if score > bestScore {
				best = s
				bestScore = score
			}
		})
	}

	return best
}

func extractBlocks(root *goquery.Selection) []string {
	blocks := []string{}
	seen := map[string]struct{}{}

	root.Find("h1,h2,h3,h4,h5,h6,p,li,blockquote,pre").Each(func(_ int, s *goquery.Selection) {
		text := normalizeText(s.Text())
		if text == "" {
			return
		}
		if _, ok := seen[text]; ok {
			return
		}
		seen[text] = struct{}{}
		blocks = append(blocks, text)
	})

	return blocks
}

func normalizeText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func pageTitle(doc *goquery.Document) string {
	title := normalizeText(doc.Find("title").First().Text())
	if title != "" {
		return title
	}
	return firstMetaContent(doc,
		"meta[property='og:title']",
		"meta[name='twitter:title']",
	)
}

func contentFallback(doc *goquery.Document, rawURL, extracted string) string {
	if normalizeText(extracted) != "" {
		return extracted
	}

	if isXStatusURL(rawURL) {
		if desc := metaDescription(doc); desc != "" {
			return desc
		}
	}

	if desc := metaDescription(doc); desc != "" {
		return desc
	}
	return extracted
}

func (f *Fetcher) fetchXStatusOEmbedText(ctx context.Context, rawURL string) (string, error) {
	oembedURL := "https://publish.twitter.com/oembed?omit_script=true&url=" + url.QueryEscape(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, oembedURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "altpocket/1.0")

	resp, err := f.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", ErrBadStatus
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", err
	}
	var payload struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if payload.HTML == "" {
		return "", ErrNoContent
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(payload.HTML))
	if err != nil {
		return "", err
	}
	text := normalizeText(doc.Find("blockquote p").First().Text())
	if text == "" {
		text = normalizeText(doc.Text())
	}
	if text == "" {
		return "", ErrNoContent
	}
	return text, nil
}

func metaDescription(doc *goquery.Document) string {
	return firstMetaContent(doc,
		"meta[property='og:description']",
		"meta[name='twitter:description']",
		"meta[name='description']",
	)
}

func firstMetaContent(doc *goquery.Document, selectors ...string) string {
	for _, selector := range selectors {
		if v, ok := doc.Find(selector).First().Attr("content"); ok {
			if normalized := normalizeText(v); normalized != "" {
				return normalized
			}
		}
	}
	return ""
}

func isXStatusURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	isXHost := host == "x.com" || strings.HasSuffix(host, ".x.com")
	isTwitterHost := host == "twitter.com" || strings.HasSuffix(host, ".twitter.com")
	if !isXHost && !isTwitterHost {
		return false
	}
	return strings.Contains(strings.ToLower(parsed.Path), "/status/")
}

func isLikelyXShellContent(content string) bool {
	candidate := strings.ToLower(normalizeText(content))
	if candidate == "" {
		return false
	}
	phrases := []string{
		"something went wrong, but don't fret",
		"something went wrong, but don’t fret",
		"some privacy related extensions may cause issues on x.com",
		"try again",
	}
	for _, phrase := range phrases {
		if strings.Contains(candidate, phrase) {
			return true
		}
	}
	return false
}

func isLikelyLinkOnlyContent(content string) bool {
	candidate := normalizeText(content)
	if candidate == "" {
		return false
	}
	hasURL := false
	for _, token := range strings.Fields(candidate) {
		if isHTTPURLToken(token) {
			hasURL = true
			continue
		}
		trimmed := strings.Trim(token, "\"'()[]{}<>,.;:-|")
		if trimmed == "" {
			continue
		}
		return false
	}
	return hasURL
}

func extractFirstHTTPURL(content string) string {
	for _, token := range strings.Fields(content) {
		candidate := strings.Trim(token, "\"'()[]{}<>,.;")
		if isHTTPURLToken(candidate) {
			return candidate
		}
	}
	return ""
}

func isHTTPURLToken(token string) bool {
	if token == "" {
		return false
	}
	parsed, err := url.Parse(token)
	if err != nil {
		return false
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return false
	}
	return true
}

func sameNormalizedURL(a, b string) bool {
	pa, errA := url.Parse(a)
	pb, errB := url.Parse(b)
	if errA != nil || errB != nil {
		return strings.EqualFold(strings.TrimSuffix(a, "/"), strings.TrimSuffix(b, "/"))
	}
	return strings.EqualFold(strings.TrimSuffix(pa.String(), "/"), strings.TrimSuffix(pb.String(), "/"))
}

func textScore(text string) int {
	return utf8.RuneCountInString(normalizeText(text))
}

func truncateUTF8(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	b := []byte(s)
	if len(b) <= limit {
		return s
	}
	trunc := b[:limit]
	for len(trunc) > 0 && !utf8.Valid(trunc) {
		trunc = trunc[:len(trunc)-1]
	}
	return string(trunc)
}
