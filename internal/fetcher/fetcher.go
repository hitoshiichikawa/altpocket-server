package fetcher

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

var (
	ErrTooLarge     = errors.New("response_too_large")
	ErrTooManyRedir = errors.New("too_many_redirects")
	ErrBadStatus    = errors.New("bad_status")
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

func (f *Fetcher) Fetch(ctx context.Context, url string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		return Result{}, ErrTooLarge
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(buf))
	if err != nil {
		return Result{}, err
	}
	title := strings.TrimSpace(doc.Find("title").First().Text())
	contentText := extractReadableContent(doc)
	contentFull := truncateUTF8(contentText, f.ContentFullLimit)
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
