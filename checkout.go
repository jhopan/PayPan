package main

import (
	"embed"
	"html/template"
	"net/http"
	"os"
	"strings"

	"github.com/skip2/go-qrcode"
)

//go:embed static/*
var checkoutStaticFS embed.FS

func (s *srv) checkoutStaticRef() embed.FS { return checkoutStaticFS }

// GET /pay/{id} — halaman checkout: QR dinamis + polling status.
func (s *srv) handleCheckout(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/pay/")
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	var status string
	var total int64
	err := s.db.QueryRow("SELECT status,total FROM orders WHERE id=?", id).Scan(&status, &total)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// QR dinamis hanya untuk order yang masih pending; sisanya tanpa QR
	qrData := ""
	if status == "pending" {
		data, err := os.ReadFile("qris_base.txt")
		if err != nil {
			http.Error(w, "qris_base.txt belum diset (Konfigurasi → QRIS)", 500)
			return
		}
		qrData, err = buildDynamicQR(strings.TrimSpace(string(data)), total)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_, err = qrcode.Encode(qrData, qrcode.Medium, 512)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	tmpl, _ := template.New("p").Parse(checkoutHTML)
	tmpl.Execute(w, map[string]any{"ID": id, "Status": status, "Total": total, "QR": qrData != ""})
}

// GET /pay/{id}/qr.png — gambar QR dinamis per order (pending saja).
func (s *srv) handleQR(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/pay/"), "/qr.png")
	var status string
	var total int64
	err := s.db.QueryRow("SELECT status,total FROM orders WHERE id=?", id).Scan(&status, &total)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if status != "pending" {
		http.Error(w, "QR tidak tersedia", 404)
		return
	}
	data, err := os.ReadFile("qris_base.txt")
	if err != nil {
		http.Error(w, "qris_base.txt belum diset", 500)
		return
	}
	qrData, err := buildDynamicQR(strings.TrimSpace(string(data)), total)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	png, err := qrcode.Encode(qrData, qrcode.Medium, 512)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(png)
}

const checkoutHTML = `<!doctype html>
<html lang="id"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Checkout</title>
<style>
body{font-family:system-ui,sans-serif;background:#f6f7f9;margin:0;display:flex;justify-content:center;padding:24px}
.card{background:#fff;border-radius:16px;max-width:380px;width:100%;padding:24px;box-shadow:0 2px 12px rgba(0,0,0,.08);text-align:center}
h1{font-size:18px;margin:0 0 4px} .amount{font-size:32px;font-weight:700;margin:8px 0}
.qr{width:260px;height:260px;margin:12px auto;border:1px solid #eee;border-radius:12px;padding:8px}
.badge{display:inline-block;padding:6px 14px;border-radius:999px;font-weight:600;font-size:14px}
.pending{background:#fff3cd;color:#8a6d00} .paid{background:#d4edda;color:#186a3b}
.expired{background:#f8d7da;color:#8a1c1c} small{color:#888}
.paid .qr,.expired .qr{filter:grayscale(1);opacity:.5}
</style></head><body>
<div class="card {{.Status}}">
<h1>Pembayaran QRIS</h1>
<div class="amount">Rp {{.Total}}</div>
<span class="badge {{.Status}}" id="badge">{{if eq .Status "paid"}}LUNAS{{else if eq .Status "expired"}}KADALUARSA{{else}}MENUNGGU PEMBAYARAN{{end}}</span>
<div><img class="qr" id="qr" src="/pay/{{.ID}}/qr.png" alt="QRIS" {{if not .QR}}style="display:none"{{end}}></div>
{{if .QR}}<small>Scan dengan aplikasi e-wallet / m-banking.<br>Halaman update otomatis.</small>
{{else}}<small>QR tidak tersedia untuk order ini.</small>{{end}}
<div style="margin-top:16px;font-size:11px;color:#98a2b3">Powered by <b>JhopanStore</b></div>
</div>
<script>
(function poll(){
fetch('/api/order/{{.ID}}').then(r=>r.json()).then(j=>{
 if(j.status==='paid'){document.getElementById('badge').textContent='LUNAS';location.reload();}
 else if(j.status==='expired'){location.reload();}
 else setTimeout(poll,3000);
}).catch(()=>setTimeout(poll,5000));
})();
</script>
</body></html>`
