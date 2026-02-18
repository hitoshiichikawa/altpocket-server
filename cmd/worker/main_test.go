package main

import (
	"errors"
	"testing"

	"altpocket/internal/fetcher"
)

func TestClassifyFetchErrorNoContent(t *testing.T) {
	if got := classifyFetchError(fetcher.ErrNoContent); got != "no_content" {
		t.Fatalf("expected no_content, got %q", got)
	}
}

func TestClassifyFetchErrorUnknown(t *testing.T) {
	if got := classifyFetchError(errors.New("unknown")); got != "fetch_failed" {
		t.Fatalf("expected fetch_failed, got %q", got)
	}
}
