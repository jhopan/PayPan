package main

import (
	"encoding/json"
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
