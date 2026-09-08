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
		sb.WriteString(`<style>
.kasir{max-width:420px;margin:0 auto;text-align:center}
.kasir .amt{font-size:34px;font-weight:800;color:#101828;margin:4px 0 10px}
.kasir .qrbox{background:#fff;border:1px solid #e4e7ec;border-radius:14px;padding:14px;display:inline-block;margin:10px 0}
.kasir .qrbox img{width:240px;height:240px;display:block}
.kasir .done{background:#f0fdf4;border:1px solid #bbf7d0;border-radius:12px;padding:16px;margin-top:14px}
.kasir .done .ok{font-size:40px;line-height:1}
.kasir .hint{color:#667085;font-size:13px;text-align:center}
.kasir .order-id{font-family:monospace;font-size:11px;color:#98a2b3;margin-top:8px}
.kasir button{margin-top:12px}
</style>
<div class="kasir">`)
		sb.WriteString(inner)
		sb.WriteString(`</div>`)
		sb.WriteString(script)
		return template.HTML(sb.String())
	})
}
