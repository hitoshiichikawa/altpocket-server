package server

import (
	"net/url"
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
