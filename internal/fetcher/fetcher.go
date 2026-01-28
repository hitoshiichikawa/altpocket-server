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
	Title        string
	Excerpt      string
	ContentFull  string
	ContentSearch string
	ContentBytes int
}

type Fetcher struct {
	Client           *http.Client
	MaxBytes         int64
	ContentFullLimit int
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
	text := strings.TrimSpace(doc.Text())
	text = strings.Join(strings.Fields(text), " ")
	contentFull := truncateUTF8(text, f.ContentFullLimit)
	contentSearch := truncateUTF8(contentFull, f.ContentSearchLimit)
	excerpt := truncateUTF8(contentFull, 200)

	return Result{
		Title:        title,
		Excerpt:      excerpt,
		ContentFull:  contentFull,
		ContentSearch: contentSearch,
		ContentBytes: len([]byte(contentFull)),
	}, nil
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
