package main

import (
	"embed"
	"html/template"
	"net/http"
	"strings"
)

//go:embed static/kasir.html
var kasirFS embed.FS

// handleKasir: halaman kasir di dalam admin layout (sidebar+header).
// static/kasir.html = full page lama. Kita ekstrak: form section (formView+
// payView) dan script, lalu render dalam card di layout admin.
func (s *srv) handleKasir(w http.ResponseWriter, r *http.Request) {
	if !s.requireSession(w, r) {
		return
	}
	b, err := kasirFS.ReadFile("static/kasir.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	raw := string(b)

	scrStart := strings.Index(raw, "<script>")
	if scrStart < 0 {
		http.Error(w, "kasir.html invalid", 500)
		return
	}
	script := raw[scrStart:]

	// ambil dari <div id="formView"> sampai tepat sebelum </div> penutup
	// terakhir sebelum <script> — tanpa membawa div wrapper card lama.
	start := strings.Index(raw, `<div id="formView">`)
	if start < 0 || start > scrStart {
		http.Error(w, "kasir.html invalid", 500)
		return
	}
	inner := raw[start:scrStart]
	inner = strings.TrimSpace(inner)
	// buang SATU penutup </div> terakhir (punya card lama)
	inner = strings.TrimSuffix(inner, "</div>")
	inner = strings.TrimSpace(inner)
	// buang logo/sub yang tidak relevan di admin
	inner = strings.Replace(inner, `<div class="logo">Pay<span>pan</span> Kasir</div>`, "", 1)
	inner = strings.Replace(inner, `<div class="sub">Buat tagihan QRIS · by JhopanStore</div>`, "", 1)
	// hint left-align
	inner = strings.Replace(inner, `class="hint"`, `class="hint" style="text-align:left"`, 1)

	s.renderPage(w, "kasir", "Kasir", r.URL.Query().Get("m"), func() template.HTML {
		var sb strings.Builder
		sb.WriteString(`<div class="card" style="max-width:460px;margin:0 auto">
<h2 style="margin:0 0 12px">Buat Tagihan</h2>`)
		sb.WriteString(inner)
		sb.WriteString(`</div>`)
		sb.WriteString(script)
		return template.HTML(sb.String())
	})
}
