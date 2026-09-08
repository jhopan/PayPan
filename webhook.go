package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// ---------- webhook LUNAS ----------

// fireWebhooks dipanggil (goroutine) saat order jadi paid: dari match notif
// maupun manual mark-paid. Fire-and-forget per URL aktif; result dicatat.
func (s *srv) fireWebhooks(orderID string, total int64) {
	rows, err := s.db.Query("SELECT rowid,url FROM webhooks WHERE active=1")
	if err != nil {
		return
	}
	type wh struct {
		id  int64
		url string
	}
	var urls []wh
	for rows.Next() {
		var w wh
		if rows.Scan(&w.id, &w.url) == nil {
			urls = append(urls, w)
		}
	}
	rows.Close()
	if len(urls) == 0 {
		return
	}

	var price, code int64
	var paidAt sql.NullInt64
	s.db.QueryRow("SELECT price,code,paid_at FROM orders WHERE id=?", orderID).Scan(&price, &code, &paidAt)
	payload, _ := json.Marshal(map[string]any{
		"event": "order.paid",
		"order": map[string]any{
			"id": orderID, "price": price, "code": code, "total": total, "paid_at": paidAt.Int64,
		},
	})

	for _, w := range urls {
		go func(w wh) {
			code := s.deliverWebhook(w.url, payload)
			s.db.Exec("INSERT INTO webhook_log(order_id,url,code,at) VALUES(?,?,?,strftime('%s','now'))",
				orderID, w.url, code)
			// retry sederhana: selain 2xx coba 2x lagi dengan jeda
			if code < 200 || code >= 300 {
				for i := 0; i < 2; i++ {
					time.Sleep(30 * time.Second)
					code = s.deliverWebhook(w.url, payload)
					s.db.Exec("INSERT INTO webhook_log(order_id,url,code,at) VALUES(?,?,?,strftime('%s','now'))",
						orderID, w.url, code)
					if code >= 200 && code < 300 {
						break
					}
				}
			}
		}(w)
	}
}

func (s *srv) deliverWebhook(url string, payload []byte) int {
	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		log.Println("webhook bad url:", err)
		return -1
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Paypan-Event", "order.paid")
	// HMAC signature: penerima bisa verifikasi asli/palsu
	// signature = hex(HMAC_SHA256(secret, body)); secret = webhook secret (settings)
	mac := hmac.New(sha256.New, []byte(s.webhookSecret()))
	mac.Write(payload)
	req.Header.Set("X-Paypan-Signature", hex.EncodeToString(mac.Sum(nil)))
	resp, err := s.httpc.Do(req)
	if err != nil {
		return -1
	}
	resp.Body.Close()
	return resp.StatusCode
}

// webhookSecret: secret bersama utk verifikasi HMAC di sisi penerima
func (s *srv) webhookSecret() string {
	var sec string
	s.db.QueryRow("SELECT value FROM settings WHERE key='webhook_secret'").Scan(&sec)
	if sec == "" {
		// auto-generate sekali
		sec = genToken()
		s.db.Exec("INSERT INTO settings(key,value) VALUES('webhook_secret',?)", sec)
	}
	return sec
}

// manualPaid: tandai order lunas manual (dari halaman detail) + fire webhook.
func (s *srv) manualPaid(orderID string) bool {
	res, err := s.db.Exec(
		"UPDATE orders SET status='paid', paid_at=strftime('%s','now') WHERE id=? AND status='pending'", orderID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false
	}
	var total int64
	s.db.QueryRow("SELECT total FROM orders WHERE id=?", orderID).Scan(&total)
	s.fireWebhooks(orderID, total)
	return true
}
