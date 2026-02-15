package ui

import (
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
)

type Renderer struct {
	templates map[string]*template.Template
}

// BuildRevision can be injected at build time via -ldflags.
var BuildRevision = "dev"

func New(templateDir string) (*Renderer, error) {
	assetVersion := resolveAssetVersion()

	funcMap := template.FuncMap{
		"assetVersion": func() string {
			return assetVersion
		},
	}

	layout := filepath.Join(templateDir, "layout.html")
	home := filepath.Join(templateDir, "home.html")
	register := filepath.Join(templateDir, "register.html")
	items := filepath.Join(templateDir, "items.html")
	detail := filepath.Join(templateDir, "item_detail.html")
	quickAdd := filepath.Join(templateDir, "quick_add.html")

	homeTpl, err := template.New("layout.html").Funcs(funcMap).ParseFiles(layout, home)
	if err != nil {
		return nil, err
	}
	registerTpl, err := template.New("layout.html").Funcs(funcMap).ParseFiles(layout, register)
	if err != nil {
		return nil, err
	}
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
		"home":      homeTpl,
		"register":  registerTpl,
		"items":     itemsTpl,
		"detail":    detailTpl,
		"quick_add": quickAddTpl,
	}}, nil
}

func resolveAssetVersion() string {
	if v := os.Getenv("ASSET_VERSION"); v != "" {
		return v
	}
	if BuildRevision != "" && BuildRevision != "dev" {
		return BuildRevision
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	return "dev"
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
