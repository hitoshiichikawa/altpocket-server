package ui

import (
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type Renderer struct {
	templates map[string]*template.Template
}

func New(templateDir string) (*Renderer, error) {
	assetVersion := os.Getenv("ASSET_VERSION")
	if assetVersion == "" {
		assetVersion = time.Now().UTC().Format("20060102150405")
	}

	funcMap := template.FuncMap{
		"assetVersion": func() string {
			return assetVersion
		},
	}

	layout := filepath.Join(templateDir, "layout.html")
	items := filepath.Join(templateDir, "items.html")
	detail := filepath.Join(templateDir, "item_detail.html")
	quickAdd := filepath.Join(templateDir, "quick_add.html")

	itemsTpl, err := template.New("layout.html").Funcs(funcMap).ParseFiles(layout, items)
	if err != nil {
		return nil, err
	}
	detailTpl, err := template.New("layout.html").Funcs(funcMap).ParseFiles(layout, detail)
	if err != nil {
		return nil, err
	}
	quickAddTpl, err := template.New("layout.html").Funcs(funcMap).ParseFiles(layout, quickAdd)
	if err != nil {
		return nil, err
	}
	return &Renderer{templates: map[string]*template.Template{
		"items":     itemsTpl,
		"detail":    detailTpl,
		"quick_add": quickAddTpl,
	}}, nil
}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) error {
	tpl, ok := r.templates[name]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return nil
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return tpl.ExecuteTemplate(w, "layout", data)
}
