package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
)

// ---------- API Invoice terstandarisasi ----------
//
// Kontrak respons seragam untuk integrator:
//   sukses : {"ok":true,  "data":{...}}
//   gagal  : {"ok":false, "error":"pesan"}
//
// Semua endpoint butuh Bearer token scope "order" (bot/website).
// Order = invoice. pay_url bisa langsung ditampilkan ke customer.

func respOK(w http.ResponseWriter, data any) {
	s := map[string]any{"ok": true, "data": data}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

func respErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}

// invoiceView: bentuk data invoice yang dikirim ke integrator.
func (s *srv) invoiceView(id string) (any, error) {
	var status string
	var price, total, code int64
	var createdAt, expiresAt int64
	var paidAt sql.NullInt64
	err := s.db.QueryRow(
		"SELECT status,price,total,code,created_at,expires_at,paid_at FROM orders WHERE id=?", id).
		Scan(&status, &price, &total, &code, &createdAt, &expiresAt, &paidAt)
	if err != nil {
		return nil, err
	}
	view := map[string]any{
		"id":         id,
		"status":     status, // pending | paid | expired | refunded
		"price":      price,
		"code":       code,
		"total":      total,
		"created_at": createdAt,
		"expires_at": expiresAt,
		"pay_url":    "/pay/" + id,        // halaman checkout siap tampil
		"qr_url":     "/pay/" + id + "/qr.png", // gambar QR dinamis
	}
	if paidAt.Valid {
		view["paid_at"] = paidAt.Int64
	}
	return view, nil
}

// POST /api/invoice — buat invoice.
func (s *srv) handleInvoiceCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respErr(w, 405, "method not allowed")
		return
	}
	if _, ok := s.authScope(r, "order"); !ok {
		respErr(w, 401, "unauthorized")
		return
	}
	var q orderReq
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil || q.Price <= 0 {
		respErr(w, 400, "body JSON {price:int, label:string} wajib")
		return
	}
	if msg := validatePrice(q.Price); msg != "" {
		respErr(w, 400, msg)
		return
	}
	oid, code, total, exp, err := s.claimOrder(q.Price)
	if err != nil {
		respErr(w, 503, err.Error())
		return
	}
	qrData, err := buildDynamicQR(s.qris, total)
	if err != nil {
		respErr(w, 500, err.Error())
		return
	}
	respOK(w, map[string]any{
		"id": oid, "price": q.Price, "code": code, "total": total,
		"status": "pending", "expires_at": exp,
		"pay_url": "/pay/" + oid,
		"qr_url":  "/pay/" + oid + "/qr.png",
		"qr":      qrData,
	})
}

// GET /api/invoice/{id} — status + detail.
func (s *srv) handleInvoiceGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respErr(w, 405, "method not allowed")
		return
	}
	if _, ok := s.authScope(r, "order"); !ok {
		respErr(w, 401, "unauthorized")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/invoice/")
	view, err := s.invoiceView(id)
	if err != nil {
		respErr(w, 404, "invoice tidak ditemukan")
		return
	}
	respOK(w, view)
}

// POST /api/invoice/{id}/cancel — batalkan order pending (kode balik pool).
func (s *srv) handleInvoiceCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respErr(w, 405, "method not allowed")
		return
	}
	if _, ok := s.authScope(r, "order"); !ok {
		respErr(w, 401, "unauthorized")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/invoice/"), "/cancel")
	res, err := s.db.Exec("UPDATE orders SET status='expired' WHERE id=? AND status='pending'", id)
	if err != nil {
		respErr(w, 500, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		respErr(w, 409, "bukan pending / tidak ditemukan")
		return
	}
	s.audit(s.adminUser(), "invoice.cancel", id)
	respOK(w, map[string]any{"id": id, "status": "expired"})
}

// POST /api/invoice/{id}/refund — tandai paid sebagai refunded (dana sudah dikembalikan manual).
func (s *srv) handleInvoiceRefund(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respErr(w, 405, "method not allowed")
		return
	}
	if _, ok := s.authScope(r, "order"); !ok {
		respErr(w, 401, "unauthorized")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/invoice/"), "/refund")
	res, err := s.db.Exec("UPDATE orders SET status='refunded' WHERE id=? AND status='paid'", id)
	if err != nil {
		respErr(w, 500, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		respErr(w, 409, "bukan paid / tidak ditemukan")
		return
	}
	s.audit(s.adminUser(), "invoice.refund", id)
	respOK(w, map[string]any{"id": id, "status": "refunded"})
}
