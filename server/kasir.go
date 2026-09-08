package main

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed static/kasir.html
var kasirFS embed.FS

// handleKasir: halaman kasir di dalam admin (butuh session).
// Order dibuat lewat /api/kasir/order (session-auth), bukan token master.
func (s *srv) handleKasir(w http.ResponseWriter, r *http.Request) {
	if !s.requireSession(w, r) {
		return
	}
	b, err := kasirFS.ReadFile("static/kasir.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, err := template.New("k").Parse(string(b))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tmpl.Execute(w, nil)
}
