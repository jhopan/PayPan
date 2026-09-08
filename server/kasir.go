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
// Konten kasir diambil dari static/kasir.html — hanya bagian form + script,
// wrapper html/body/card lama dibuang.
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

	// ambil isi antara <div id="formView"> ... sebelum <script>
	// (form + payview), lalu script dipisah untuk ditempel di akhir
	start := strings.Index(raw, `<div id="formView">`)
	scrStart := strings.Index(raw, "<script>")
	if start < 0 || scrStart < 0 {
		http.Error(w, "kasir.html invalid", 500)
		return
	}
	inner := raw[start:scrStart]
	// buang penutup </div> card terakhir (sudah disediakan wrapper admin)
	inner = strings.TrimSpace(inner)
	inner = strings.TrimSuffix(inner, "</div>")
	// bersihkan elemen yang tidak relevan di dalam admin (logo/sub)
	inner = strings.Replace(inner, `<div class="logo">Pay<span>pan</span> Kasir</div>`, "", 1)
	inner = strings.Replace(inner, `<div class="sub">Buat tagihan QRIS · by JhopanStore</div>`, "", 1)
	// hint jadi text-align:left biar konsisten dengan form
	inner = strings.Replace(inner, `class="hint"`, `class="hint" style="text-align:left"`, 1)
	script := raw[scrStart:]

	s.renderPage(w, "kasir", "Kasir", r.URL.Query().Get("m"), func() template.HTML {
		var sb strings.Builder
		sb.WriteString(`<div class="card" style="max-width:420px;margin:0 auto">
<h2 style="text-align:left">Buat Tagihan</h2>
<div id="formView" style="text-align:left">`)
		sb.WriteString(inner)
		sb.WriteString(`</div>`)
		sb.WriteString(script)
		return template.HTML(sb.String())
	})
}
