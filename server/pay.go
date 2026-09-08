package main

import (
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---------- QRIS dinamis: inject tag 54 + recompute CRC ----------

// crc16 CCITT-FALSE (poly 0x1021, init 0xFFFF) — standar EMVCo QRIS
func crc16(s string) string {
	crc := uint16(0xFFFF)
	for i := 0; i < len(s); i++ {
		crc ^= uint16(s[i]) << 8
		for b := 0; b < 8; b++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return fmt.Sprintf("%04X", crc)
}

// buildDynamicQR menyuntikkan total ke payload QRIS statis.
// Asumsi: payload statis berisi "010211" (static, tanpa nominal) dan "5802ID".
// Hasil: "010212" + tag 54 disisip sebelum 5802ID + CRC dihitung ulang.
// ponytail: parser TLV penuh diperlukan kalau template QRIS punya tag 54/01 aneh.
func buildDynamicQR(base string, total int64) (string, error) {
	if !strings.Contains(base, "010211") || !strings.Contains(base, "5802ID") {
		return "", fmt.Errorf("payload QRIS statis tidak dikenali (butuh 010211 + 5802ID)")
	}
	amt := fmt.Sprintf("%d.00", total)
	tag54 := fmt.Sprintf("54%02d%s", len(amt), amt)
	s := strings.Replace(base, "010211", "010212", 1)
	s = strings.Replace(s, "5802ID", tag54+"5802ID", 1)
	i := strings.Index(s, "6304")
	if i < 0 {
		return "", fmt.Errorf("payload tanpa CRC 6304")
	}
	body := s[:i+4]
	return body + crc16(body), nil
}

// ---------- kode unik 001-999 per level harga ----------

// Kode unik = 3 digit terakhir yang menjadikan total unik.
// Aturan (per desain user):
//   price 1000 -> 1001, 1002, ... (level 1000)
//   price 2000 -> 2001, 2002, ... (level 2000 — urutan sendiri)
//   price 1500 -> 1501, 1502, ...
// Level = price. Kode dipilih per-price: kode yang belum dipakai order aktif
// dengan price yang sama, diurut dari kecil.
// Anti-bentrok antar level: total (price+code) tidak boleh sama dengan total
// order aktif lain (contoh bentrok: 1000+999=1999 vs 1100+899=1999).

// kode harus unik di antara SEMUA order pending, bukan hanya harga sama.
type codePool struct {
	mu    sync.Mutex
	used  map[int]bool
	shuf  []int
	uidx  int
}

func newCodePool() *codePool {
	return &codePool{used: make(map[int]bool)}
}

// next: ambil kode bebas utk price tertentu.
func (p *codePool) next(db *sql.DB, price int64) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// 1. kode yang sudah dipakai order AKTIF dengan price sama
	rows, err := db.Query(`SELECT code FROM orders WHERE
		price=? AND (
		status='pending'
		OR status='paid'
		OR (status='expired' AND expires_at > strftime('%s','now') - 86400)
		OR (status='refunded' AND COALESCE(paid_at, expires_at) > strftime('%s','now') - 86400))`, price)
	if err != nil {
		return 0, err
	}
	used := make(map[int]bool)
	for rows.Next() {
		var c int
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return 0, err
		}
		used[c] = true
	}
	rows.Close()
	// 2. total yang sudah dipakai order AKTIF level lain (anti-bentrok total)
	usedTotal := make(map[int64]bool)
	rows2, err := db.Query(`SELECT total FROM orders WHERE
		price != ? AND (
		status='pending'
		OR status='paid'
		OR (status='expired' AND expires_at > strftime('%s','now') - 86400)
		OR (status='refunded' AND COALESCE(paid_at, expires_at) > strftime('%s','now') - 86400))`, price)
	if err != nil {
		return 0, err
	}
	for rows2.Next() {
		var t int64
		if err := rows2.Scan(&t); err != nil {
			rows2.Close()
			return 0, err
		}
		usedTotal[t] = true
	}
	rows2.Close()

	for c := 1; c <= 999; c++ {
		if used[c] {
			continue // kode sudah dipakai di level ini
		}
		total := price + int64(c)
		if usedTotal[total] {
			continue // total bentrok dengan level lain yang aktif
		}
		return c, nil
	}
	return 0, fmt.Errorf("pool kode habis utk price %d (999 order aktif dengan harga sama)", price)
}

// ---------- handlers ----------

type srv struct {
	db      *sql.DB
	qris    string
	token   string
	secret  string // sama dengan token; dipisah kalau nanti HMAC
	tgToken string
	tgChat  string
	httpc   *http.Client
}

func (s *srv) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// auth bearer sederhana
func (s *srv) auth(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	return h == "Bearer "+s.token
}

type notifReq struct {
	ID      string `json:"id"`
	Pkg     string `json:"pkg"`
	Title   string `json:"title"`
	Text    string `json:"text"`
	Amount  *int64 `json:"amount"`
	Source  string `json:"source"`
	Created int64  `json:"created_at"`
}

// POST /api/notif — dari device. Simpan payment -> coba match -> response.
func (s *srv) handleNotif(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, 405, map[string]string{"error": "method"})
		return
	}
	appName, ok := s.authScope(r, "notif")
	if !ok {
		s.writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	_ = appName
	var n notifReq
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil || n.ID == "" {
		s.writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}

	// 1. simpan payment (idempotent by id; dobel kirim device = no-op)
	res, err := s.db.Exec(
		"INSERT OR IGNORE INTO payments(id,pkg,title,text,amount,source,created_at,received_at) VALUES(?,?,?,?,?,?,?,?)",
		n.ID, n.Pkg, n.Title, n.Text, n.Amount, n.Source, n.Created, time.Now().Unix())
	if err != nil {
		s.writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	inserted, _ := res.RowsAffected()
	if inserted == 0 {
		s.writeJSON(w, 200, map[string]any{"ok": true, "dup": true})
		return
	}
	// catat aplikasi mana yang mengirim (dari token, diisi authScope)
	r.Header.Set("X-App", appName)
	if n.Amount == nil || *n.Amount <= 0 {
		// bukan pembayaran (tidak ada nominal) — cukup tersimpan di payments
		s.writeJSON(w, 200, map[string]any{"ok": true, "matched": false})
		return
	}

	// lapisan 2: notif non-pembayaran (pencairan/topup/refund) gak boleh match order.
	// cegah skenario "pencairan Rp10.005" kebetulan cocok order Rp10.005.
	low := strings.ToLower(n.Title + " " + n.Text)
	nonPay := []string{"pencairan dana", "pencairan berhasil", "top up saldo", "topup berhasil",
		"deposit berhasil", "penarikan dana", "tarik dana", "refund"}
	for _, p := range nonPay {
		if strings.Contains(low, p) {
			s.writeJSON(w, 200, map[string]any{"ok": true, "matched": false, "ignored": "non-payment"})
			return
		}
	}

	// 2. match exact: order pending dengan total = amount, belum expired
	now := time.Now().Unix()
	var oid string
	oid, err = s.matchPending(*n.Amount, now)
	switch {
	case err == sql.ErrNoRows:
		s.db.Exec("INSERT OR IGNORE INTO unmatched(payment_id,amount,reason,received_at) VALUES(?,?,?,?)",
			n.ID, *n.Amount, "no pending order", now)
		go s.notifyTGResult(fmt.Sprintf("⚠️ Notif tidak cocok: Rp%d (%s) — tidak ada order pending", *n.Amount, n.Source), n.ID)
		s.writeJSON(w, 200, map[string]any{"ok": true, "matched": false})
	case err != nil:
		s.writeJSON(w, 500, map[string]string{"error": err.Error()})
	default:
		// UPDATE ... WHERE status='pending': kalau order sudah dibayar/proses lain
		// di antara SELECT dan UPDATE, rowsAffected=0 -> payment masuk unmatched, bukan dobel-match.
		res, err := s.db.Exec("UPDATE orders SET status='paid', paid_at=? WHERE id=? AND status='pending'",
			now, oid)
		if err != nil {
			s.writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if affected, _ := res.RowsAffected(); affected == 0 {
			s.db.Exec("INSERT OR IGNORE INTO unmatched(payment_id,amount,reason,received_at) VALUES(?,?,?,?)",
				n.ID, *n.Amount, "order sudah diproses", now)
			s.writeJSON(w, 200, map[string]any{"ok": true, "matched": false, "reason": "already processed"})
			return
		}
		var price, code int64
		s.db.QueryRow("SELECT price,code FROM orders WHERE id=?", oid).Scan(&price, &code)
		go s.notifyTGResult(fmt.Sprintf("✅ LUNAS Rp%d (order %s, kode %03d)", *n.Amount, oid, code), oid)
		go s.fireWebhooks(oid, *n.Amount)
		s.writeJSON(w, 200, map[string]any{"ok": true, "matched": true, "order": oid})
	}
}

type orderReq struct {
	Price int64  `json:"price"`
	Label string `json:"label"`
}

// POST /api/order — buat order (dari web/bot/API lain).
func (s *srv) handleOrderCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, 405, map[string]string{"error": "method"})
		return
	}
	if _, ok := s.authScope(r, "order"); !ok {
		s.writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	s.createOrder(w, r)
}

// handleOrderCreateSession: versi kasir — auth via session admin (dipanggil
// setelah session diverifikasi oleh handler /api/kasir/order).
func (s *srv) handleOrderCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, 405, map[string]string{"error": "method"})
		return
	}
	s.createOrder(w, r)
}

func (s *srv) createOrder(w http.ResponseWriter, r *http.Request) {
	var q orderReq
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil || q.Price <= 0 {
		s.writeJSON(w, 400, map[string]string{"error": "price>0 wajib"})
		return
	}
	if q.Price < 1000 {
		s.writeJSON(w, 400, map[string]string{"error": "minimal Rp1.000 (3 digit terakhir dipakai sebagai kode unik 001-999)"})
		return
	}
	if q.Price > 9_000_000 {
		s.writeJSON(w, 400, map[string]string{"error": "price terlalu besar (max 9.000.000; kode 1-999)"})
		return
	}

	code, err := newCodePool().next(s.db, q.Price)
	if err != nil {
		s.writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	total := q.Price + int64(code)
	oid := newOrderID()
	now := time.Now().Unix()
	exp := now + 5*60 // 5 menit window pembayaran
	if _, err := s.db.Exec(
		"INSERT INTO orders(id,price,code,total,status,created_at,expires_at) VALUES(?,?,?,?,'pending',?,?)",
		oid, q.Price, code, total, now, exp); err != nil {
		s.writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	qrData, err := buildDynamicQR(s.qris, total)
	if err != nil {
		s.writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	s.writeJSON(w, 200, map[string]any{
		"id": oid, "price": q.Price, "code": code, "total": total,
		"expires_at": exp, "qr": qrData,
	})
}

// newOrderID: 8 byte random hex — cukup unik, tanpa dep.
func newOrderID() string {
	b := make([]byte, 8)
	crand.Read(b)
	return hex.EncodeToString(b)
}

// matchPending: cari order pending dengan total = amount, belum expired.
// Jika ada LEBIH dari satu order pending dengan nominal sama (misal dua order
// Rp 1.000+kode yang sama — tidak mungkin dengan pool unik, tapi pengaman):
// pilih yang PALING BARU dibuat. Order yang sudah paid/expired tidak akan
// pernah dicocokkan lagi — satu pembayaran hanya pernah match satu order.
func (s *srv) matchPending(amount int64, now int64) (string, error) {
	var oid string
	err := s.db.QueryRow(
		"SELECT id FROM orders WHERE status='pending' AND total=? AND expires_at>? ORDER BY created_at DESC LIMIT 1",
		amount, now).Scan(&oid)
	return oid, err
}

// GET /api/order/{id} — status (polling dari checkout/bot).
func (s *srv) handleOrderStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/order/")
	var status string
	var price, total int64
	var paidAt sql.NullInt64
	err := s.db.QueryRow("SELECT status,price,total,paid_at FROM orders WHERE id=?", id).
		Scan(&status, &price, &total, &paidAt)
	if err == sql.ErrNoRows {
		s.writeJSON(w, 404, map[string]string{"error": "order tidak ada"})
		return
	}
	if err != nil {
		s.writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	s.writeJSON(w, 200, map[string]any{
		"id": id, "status": status, "price": price, "total": total, "paid_at": paidAt.Int64,
	})
}

// expireWorker: tandai order pending lewat window sebagai expired (kode balik ke pool).
func (s *srv) expireWorker() {
	for range time.Tick(10 * time.Second) {
		s.db.Exec("UPDATE orders SET status='expired' WHERE status='pending' AND expires_at<strftime('%s','now')")
	}
}

// notifyTG kirim pesan Telegram ke semua chat terdaftar (dari DB, selalu fresh).
// Gagal kirim dicatat ke audit — jangan diam-diam.
func (s *srv) notifyTG(msg string) {
	s.notifyTGResult(msg, "")
}

// notifyTGResult: sama, tapi link ke order untuk audit detail.
func (s *srv) notifyTGResult(msg, orderID string) {
	s.loadTGFromDB()
	if s.tgToken == "" || s.tgChat == "" {
		return
	}
	for _, chat := range strings.Split(s.tgChat, ",") {
		chat = strings.TrimSpace(chat)
		if chat == "" {
			continue
		}
		ok := false
		for attempt := 0; attempt < 3; attempt++ {
			body, _ := json.Marshal(map[string]any{"chat_id": chat, "text": msg})
			req, err := http.NewRequest("POST",
				"https://api.telegram.org/bot"+s.tgToken+"/sendMessage",
				strings.NewReader(string(body)))
			if err != nil {
				break
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := s.httpc.Do(req)
			if err != nil {
				time.Sleep(3 * time.Second)
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				ok = true
				break
			}
			// 4xx = salah token/chat (tidak ada gunanya retry cepat), 5xx = coba lagi
			if resp.StatusCode < 500 {
				break
			}
			time.Sleep(3 * time.Second)
		}
		if !ok {
			detail := "chat " + chat
			if orderID != "" {
				detail += " order " + orderID
			}
			s.audit("system", "telegram.gagal", detail+": "+msg)
		}
	}
}
