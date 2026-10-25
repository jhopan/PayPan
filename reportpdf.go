package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// handleWebhookRetry: kirim ulang webhook untuk satu order (aksi admin).
// Berguna kalau penerima sempat mati dan antrean sudah menyerah.
func (s *srv) handleWebhookRetry(w http.ResponseWriter, r *http.Request) {
	if !s.requireSession(w, r) {
		return
	}
	orderID := r.FormValue("order_id")
	if orderID == "" {
		http.Redirect(w, r, "/admin", 302)
		return
	}

	var price, code, total int64
	var paidAt *int64
	err := s.db.QueryRow("SELECT price,code,total,paid_at FROM orders WHERE id=?", orderID).
		Scan(&price, &code, &total, &paidAt)
	if err != nil {
		http.Error(w, "order tidak ditemukan", 404)
		return
	}
	if paidAt == nil {
		http.Error(w, "order belum lunas — tidak ada yang dikirim", 400)
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"event": "order.paid",
		"order": map[string]any{
			"id": orderID, "price": price, "code": code, "total": total, "paid_at": *paidAt,
		},
	})

	rows, err := s.db.Query("SELECT rowid,url,secret FROM webhooks WHERE active=1")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type wh struct {
		id     int64
		url    string
		secret string
	}
	var hooks []wh
	for rows.Next() {
		var h wh
		if rows.Scan(&h.id, &h.url, &h.secret) == nil {
			hooks = append(hooks, h)
		}
	}
	rows.Close()

	if len(hooks) == 0 {
		http.Error(w, "tidak ada webhook aktif", 400)
		return
	}

	now := time.Now().Unix()
	for _, h := range hooks {
		// reset penanda supaya fireWebhooks mau mengantrekan lagi
		s.db.Exec("UPDATE orders SET webhook_fired=0 WHERE id=?", orderID)
		s.db.Exec(`INSERT INTO webhook_queue(order_id,hook_id,url,secret,payload,attempts,next_at,created_at)
			VALUES(?,?,?,?,?,0,?,?)`, orderID, h.id, h.url, h.secret, string(payload), now, now)
		s.db.Exec("UPDATE orders SET webhook_fired=1 WHERE id=?", orderID)
	}
	s.audit(s.adminUser(), "webhook.retry", orderID)
	http.Redirect(w, r, "/admin/tx/"+orderID+"?m="+url.QueryEscape("Webhook dimasukkan ulang ke antrean"), 302)
}

// handleReportPDF: laporan bulanan dalam bentuk teks siap cetak (print-friendly HTML).
// PDF asli butuh dependency besar; halaman ini bisa disimpan sebagai PDF dari browser.
func (s *srv) handleReportPDF(w http.ResponseWriter, r *http.Request) {
	if !s.requireSession(w, r) {
		return
	}
	month := r.URL.Query().Get("bulan")
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	from, to := monthRange(month)
	if from == 0 {
		http.Error(w, "format bulan salah", 400)
		return
	}

	rows, err := s.db.Query(`
		SELECT o.id,o.price,o.code,o.total,o.label,o.buyer,COALESCE(a.name,''),COALESCE(o.paid_at,0)
		FROM orders o LEFT JOIN apps a ON a.rowid=o.created_by
		WHERE o.status='paid' AND o.paid_at>=? AND o.paid_at<?
		ORDER BY o.paid_at`, from, to)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var b bytes.Buffer
	var count int64
	var sum int64
	title := time.Unix(from, 0).Format("Januari 2006")

	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>Laporan ` + title + `</title>
<style>body{font-family:system-ui,sans-serif;margin:24px;color:#101828}
h1{font-size:20px;margin:0 0 4px}.sub{color:#667085;font-size:13px;margin-bottom:18px}
table{width:100%;border-collapse:collapse;font-size:12px}
th,td{border-bottom:1px solid #e4e7ec;padding:6px 8px;text-align:left}
th{background:#f9fafb;font-weight:600}.r{text-align:right}
tfoot td{font-weight:700;border-top:2px solid #101828}
@media print{@page{margin:12mm}}</style></head><body>
<h1>Laporan Transaksi Paypan</h1>
<div class="sub">Periode: ` + title + ` · dicetak ` + time.Now().Format("02 Jan 2006 15:04") + `</div>
<table><thead><tr><th>Waktu</th><th>ID</th><th>Untuk apa</th><th>Pemesan</th><th>Diminta oleh</th><th class="r">Total</th></tr></thead><tbody>`)

	for rows.Next() {
		var id, label, buyer, app string
		var price, code, total, paidAt int64
		if rows.Scan(&id, &price, &code, &total, &label, &buyer, &app, &paidAt) != nil {
			continue
		}
		count++
		sum += total
		b.WriteString(`<tr><td>` + time.Unix(paidAt, 0).Format("02/01 15:04") + `</td><td>` + esc(id) +
			`</td><td>` + escOrDash(label) + `</td><td>` + escOrDash(buyer) +
			`</td><td>` + escOrDash(app) + `</td><td class="r">` + rp(total) + `</td></tr>`)
	}
	b.WriteString(`</tbody><tfoot><tr><td colspan="5">Total ` + fmt.Sprintf("%d", count) +
		` transaksi</td><td class="r">` + rp(sum) + `</td></tr></tfoot></table>
<p style="margin-top:18px;font-size:11px;color:#98a2b3">Paypan · JhopanStore</p>
</body></html>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b.Bytes())
}
