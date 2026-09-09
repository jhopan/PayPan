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
	"strconv"
	"time"
)

// ---------- webhook LUNAS ----------

// fireWebhooks dipanggil (goroutine) saat order jadi paid: dari match notif
// maupun manual mark-paid. Fire-and-forget per URL aktif; result dicatat.
func (s *srv) fireWebhooks(orderID string, total int64) {
		rows, err := s.db.Query("SELECT rowid,url,secret FROM webhooks WHERE active=1")
	if err != nil {
		return
	}
	type wh struct {
		id     int64
		url    string
		secret string
	}
	var urls []wh
	for rows.Next() {
		var w wh
		if rows.Scan(&w.id, &w.url, &w.secret) == nil {
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
			code := s.deliverWebhook(w.url, payload, w.secret, w.id)
			s.db.Exec("INSERT INTO webhook_log(order_id,url,code,at) VALUES(?,?,?,strftime('%s','now'))",
				orderID, w.url, code)
			// retry sederhana: selain 2xx coba 2x lagi dengan jeda
			if code < 200 || code >= 300 {
				for i := 0; i < 2; i++ {
					time.Sleep(30 * time.Second)
					code = s.deliverWebhook(w.url, payload, w.secret, w.id)
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

func (s *srv) deliverWebhook(url string, payload []byte, secret string, hookID int64) int {
	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		log.Println("webhook bad url:", err)
		return -1
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Paypan-Event", "order.paid")
	req.Header.Set("X-Paypan-Hook-ID", strconv.FormatInt(hookID, 10))
	// HMAC per-webhook: tiap webhook punya secret sendiri.
	// Penerima verifikasi: HMAC_SHA256(secret_webhook_ini, raw_body) == X-Paypan-Signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	req.Header.Set("X-Paypan-Signature", hex.EncodeToString(mac.Sum(nil)))
	resp, err := s.httpc.Do(req)
	if err != nil {
		return -1
	}
	resp.Body.Close()
	return resp.StatusCode
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
