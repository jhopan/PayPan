package main

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
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

// ---------- kode unik 001-999 ----------

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

// next ambil kode bebas; randomOrder=true mengacak urutan awal.
func (p *codePool) next(db *sql.DB) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// reload used dari DB setiap kali (sumber kebenaran = DB)
	rows, err := db.Query("SELECT code FROM orders WHERE status='pending'")
	if err != nil {
		return 0, err
	}
	p.used = make(map[int]bool)
	for rows.Next() {
		var c int
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return 0, err
		}
		p.used[c] = true
	}
	rows.Close()

	free := make([]int, 0, 999)
	for c := 1; c <= 999; c++ {
		if !p.used[c] {
			free = append(free, c)
		}
	}
	if len(free) == 0 {
		return 0, fmt.Errorf("pool kode habis (999 order pending)")
	}
	return free[rand.Intn(len(free))], nil
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

	// 2. match exact: order pending dengan total = amount, belum expired
	now := time.Now().Unix()
	var oid string
	err = s.db.QueryRow(
		"SELECT id FROM orders WHERE status='pending' AND total=? AND expires_at>? LIMIT 1",
		*n.Amount, now).Scan(&oid)
	switch {
	case err == sql.ErrNoRows:
		s.db.Exec("INSERT OR IGNORE INTO unmatched(payment_id,amount,reason,received_at) VALUES(?,?,?,?)",
			n.ID, *n.Amount, "no pending order", now)
		go s.notifyTG(fmt.Sprintf("⚠️ Notif tidak cocok: Rp%d (%s) — tidak ada order pending", *n.Amount, n.Source))
		s.writeJSON(w, 200, map[string]any{"ok": true, "matched": false})
	case err != nil:
		s.writeJSON(w, 500, map[string]string{"error": err.Error()})
	default:
		_, err = s.db.Exec("UPDATE orders SET status='paid', paid_at=? WHERE id=? AND status='pending'",
			now, oid)
		if err != nil {
			s.writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		var price, code int64
		s.db.QueryRow("SELECT price,code FROM orders WHERE id=?", oid).Scan(&price, &code)
		go s.notifyTG(fmt.Sprintf("✅ LUNAS Rp%d (order %s, kode %03d)", *n.Amount, oid, code))
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
	var q orderReq
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil || q.Price <= 0 {
		s.writeJSON(w, 400, map[string]string{"error": "price>0 wajib"})
		return
	}
	if q.Price > 9_000_000 {
		s.writeJSON(w, 400, map[string]string{"error": "price terlalu besar (max 9.000.000; kode 1-999)"})
		return
	}

	code, err := newCodePool().next(s.db)
	if err != nil {
		s.writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	total := q.Price + int64(code)
	oid := newOrderID()
	now := time.Now().Unix()
	exp := now + 15*60 // 15 menit
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
	rand.Read(b)
	return hex.EncodeToString(b)
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

// expireWorker: tandai order pending lewat 15 menit sebagai expired.
func (s *srv) expireWorker() {
	for range time.Tick(30 * time.Second) {
		s.db.Exec("UPDATE orders SET status='expired' WHERE status='pending' AND expires_at<strftime('%s','now')")
	}
}

// notifyTG kirim pesan Telegram (opsional; diam kalau token kosong).
func (s *srv) notifyTG(msg string) {
	if s.tgToken == "" || s.tgChat == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"chat_id": s.tgChat, "text": msg,
	})
	req, _ := http.NewRequest("POST",
		"https://api.telegram.org/bot"+s.tgToken+"/sendMessage",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	s.httpc.Do(req)
}
