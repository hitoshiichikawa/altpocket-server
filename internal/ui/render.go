package ui

import (
	"html/template"
	"net/http"
	"path/filepath"
)

type Renderer struct {
	templates map[string]*template.Template
}

func New(templateDir string) (*Renderer, error) {
	layout := filepath.Join(templateDir, "layout.html")
	items := filepath.Join(templateDir, "items.html")
	detail := filepath.Join(templateDir, "item_detail.html")

	itemsTpl, err := template.ParseFiles(layout, items)
	if err != nil {
		return nil, err
	}
	detailTpl, err := template.ParseFiles(layout, detail)
	if err != nil {
		return nil, err
	}
	return &Renderer{templates: map[string]*template.Template{
		"items":  itemsTpl,
		"detail": detailTpl,
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
