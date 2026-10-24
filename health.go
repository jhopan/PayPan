package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// handleHealthz: endpoint monitoring ringan. Tidak butuh auth, tidak bocorkan data.
// 200 = sehat, 503 = ada komponen bermasalah.
func (s *srv) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	status := "ok"
	code := http.StatusOK
	detail := map[string]any{}

	// DB harus bisa di-query
	var one int
	if err := s.db.QueryRow("SELECT 1").Scan(&one); err != nil || one != 1 {
		status = "db_error"
		code = http.StatusServiceUnavailable
	}

	// QRIS harus terpasang (kalau kosong, invoice tidak bisa dibuat)
	if s.qris == "" {
		detail["qris"] = "missing"
		status = "degraded"
		if code == http.StatusOK {
			code = http.StatusServiceUnavailable
		}
	} else {
		detail["qris"] = "ok"
	}

	// antrean webhook yang menunggu (indikator backlog)
	var backlog int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM webhook_queue").Scan(&backlog); err == nil {
		detail["webhook_backlog"] = backlog
	}

	// order pending yang sudah lewat expiry (harusnya cepat dibereskan worker)
	var stuck int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM orders WHERE status='pending' AND expires_at<strftime('%s','now')").Scan(&stuck); err == nil {
		detail["pending_stuck"] = stuck
		if stuck > 50 {
			status = "degraded"
			if code == http.StatusOK {
				code = http.StatusServiceUnavailable
			}
		}
	}

	detail["status"] = status
	detail["time"] = time.Now().Unix()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(detail)
}
