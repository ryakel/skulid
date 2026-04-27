package httpx

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"strings"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

// Renderer parses each page template against the shared layout independently
// so the per-page {{define "body"}} blocks don't clash in a single namespace.
type Renderer struct {
	pages map[string]*template.Template
}

func NewRenderer() (*Renderer, error) {
	funcs := template.FuncMap{
		"fmtTime": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.Local().Format("2006-01-02 15:04:05")
		},
		"fmtTimePtr": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return "—"
			}
			return t.Local().Format("2006-01-02 15:04:05")
		},
		"jsonStr": func(b []byte) string {
			s := string(b)
			if strings.TrimSpace(s) == "" {
				return "{}"
			}
			return s
		},
		"join":  strings.Join,
		"int64": func(v int64) string { return fmt.Sprintf("%d", v) },
		"deref": func(p *int64) string {
			if p == nil {
				return ""
			}
			return fmt.Sprintf("%d", *p)
		},
		"strOr": func(v, def string) string {
			if strings.TrimSpace(v) == "" {
				return def
			}
			return v
		},
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict needs an even number of args")
			}
			m := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				k, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				m[k] = values[i+1]
			}
			return m, nil
		},
	}

	layout, err := templateFS.ReadFile("templates/layout.html")
	if err != nil {
		return nil, err
	}

	r := &Renderer{pages: map[string]*template.Template{}}
	if err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := strings.TrimPrefix(path, "templates/")
		if name == "layout.html" {
			return nil
		}
		raw, err := templateFS.ReadFile(path)
		if err != nil {
			return err
		}
		t, err := template.New("layout").Funcs(funcs).Parse(string(layout))
		if err != nil {
			return err
		}
		if _, err := t.Parse(string(raw)); err != nil {
			return fmt.Errorf("parse %s: %w", name, err)
		}
		key := strings.TrimSuffix(name, ".html")
		r.pages[key] = t
		return nil
	}); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Renderer) Render(w io.Writer, name string, data any) error {
	t, ok := r.pages[name]
	if !ok {
		return fmt.Errorf("template %q not found", name)
	}
	// Login is a self-contained page (no layout); render its definition directly.
	if name == "login" {
		return t.ExecuteTemplate(w, "login", data)
	}
	return t.ExecuteTemplate(w, "layout", data)
}
