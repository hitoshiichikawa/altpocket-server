package ui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPageTitleFormat(t *testing.T) {
	r, err := New("../../templates")
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}

	tests := []struct {
		name     string
		template string
		data     map[string]interface{}
		want     string
	}{
		{
			"home shows login title",
			"home",
			map[string]interface{}{"Title": "ログイン"},
			"<title>ログイン | altpocket</title>",
		},
		{
			"register shows registration title",
			"register",
			map[string]interface{}{"Title": "アカウント登録"},
			"<title>アカウント登録 | altpocket</title>",
		},
		{
			"items shows article list title",
			"items",
			map[string]interface{}{
				"Title":      "記事一覧",
				"Page":       1,
				"TotalPages": 1,
				"PerPage":    30,
				"Sort":       "newest",
			},
			"<title>記事一覧 | altpocket</title>",
		},
		{
			"quick_add shows quick add title",
			"quick_add",
			map[string]interface{}{"Title": "クイック追加"},
			"<title>クイック追加 | altpocket</title>",
		},
		{
			"settings shows settings title",
			"settings",
			map[string]interface{}{"Title": "設定"},
			"<title>設定 | altpocket</title>",
		},
		{
			"detail shows article title",
			"detail",
			map[string]interface{}{"Title": "テスト記事タイトル"},
			"<title>テスト記事タイトル | altpocket</title>",
		},
		{
			"detail shows untitled fallback",
			"detail",
			map[string]interface{}{"Title": "(無題)"},
			"<title>(無題) | altpocket</title>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			if err := r.Render(rr, tc.template, tc.data); err != nil {
				t.Fatalf("render error: %v", err)
			}
			body := rr.Body.String()
			if !strings.Contains(body, tc.want) {
				// Show first 500 chars for debugging
				end := len(body)
				if end > 500 {
					end = 500
				}
				t.Errorf("expected %q in response body, got:\n%s", tc.want, body[:end])
			}
		})
	}
}
