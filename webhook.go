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
	"sync"
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

	// Cegah kirim dobel: hanya satu pemanggil yang boleh mengantrekan.
	// (manualPaid + notif match bisa terjadi hampir bersamaan)
	res, err := s.db.Exec("UPDATE orders SET webhook_fired=1 WHERE id=? AND webhook_fired=0", orderID)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // sudah pernah diantrekan
	}

	// Masukkan ke antrean persisten: worker yang mengirim + retry.
	// Ini menggantikan goroutine + time.Sleep supaya retry tidak hilang saat restart.
	now := time.Now().Unix()
	for _, w := range urls {
		s.db.Exec(`INSERT INTO webhook_queue(order_id,hook_id,url,secret,payload,attempts,next_at,created_at)
			VALUES(?,?,?,?,?,0,?,?)`, orderID, w.id, w.url, w.secret, string(payload), now, now)
	}
}

// webhookWorker: proses antrean webhook. Backoff: 30s, 2m, 5m, 15m, lalu menyerah
// setelah 5 percobaan (dicatat di webhook_log sebagai gagal).
// webhookBackoff: jeda antar percobaan (detik). 4 entri = maks 5 percobaan total.
var webhookBackoff = []int64{30, 120, 300, 900}

func (s *srv) webhookWorker() {
	for range time.Tick(5 * time.Second) {
		now := time.Now().Unix()
		rows, err := s.db.Query(`SELECT id,order_id,hook_id,url,secret,payload,attempts
			FROM webhook_queue WHERE next_at<=? ORDER BY id LIMIT 20`, now)
		if err != nil {
			continue
		}
		type job struct {
			id, hookID, attempts int64
			orderID, url, secret, payload string
		}
		var jobs []job
		for rows.Next() {
			var j job
			if rows.Scan(&j.id, &j.orderID, &j.hookID, &j.url, &j.secret, &j.payload, &j.attempts) == nil {
				jobs = append(jobs, j)
			}
		}
		rows.Close()

		// rows sudah ditutup di atas: jangan pernah menahan koneksi DB
		// selama HTTP call (MaxOpenConns=1 -> seluruh server akan macet).
		// Paralel terbatas: 20 job x 10s timeout serial = 200s (antrean menumpuk).
		sem := make(chan struct{}, 5)
		var wg sync.WaitGroup
		for _, j := range jobs {
			wg.Add(1)
			sem <- struct{}{}
			go func(j struct {
				id, hookID, attempts        int64
				orderID, url, secret, payload string
			}) {
				defer wg.Done()
				defer func() { <-sem }()
				s.processWebhookJob(j.id, j.hookID, j.attempts, j.orderID, j.url, j.secret, j.payload)
			}(j)
		}
		wg.Wait()
	}
}

// processWebhookJob: kirim satu job + update status antrean.
func (s *srv) processWebhookJob(id, hookID, attempts int64, orderID, url, secret, payload string) {
	code := s.deliverWebhook(url, []byte(payload), secret, hookID)
	s.db.Exec("INSERT INTO webhook_log(order_id,url,code,at) VALUES(?,?,?,strftime('%s','now'))",
		orderID, url, code)
	if code >= 200 && code < 300 {
		s.db.Exec("DELETE FROM webhook_queue WHERE id=?", id)
		return
	}
	if int(attempts) >= len(webhookBackoff) {
		// habis percobaan: buang dari antrean, kegagalan tetap tercatat di webhook_log
		s.db.Exec("DELETE FROM webhook_queue WHERE id=?", id)
		return
	}
	delay := webhookBackoff[attempts]
	s.db.Exec("UPDATE webhook_queue SET attempts=attempts+1, next_at=? WHERE id=?", time.Now().Unix()+delay, id)
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
	// goroutine: handler HTTP tidak boleh menunggu webhook (MaxOpenConns=1)
	go s.fireWebhooks(orderID, total)
	return true
}
