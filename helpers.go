package main

import (
	"database/sql"
	"os"
	"strconv"
	"strings"
)

func itoa(n int) string       { return strconv.Itoa(n) }
func itoa64(n int64) string   { return strconv.FormatInt(n, 10) }

// esc: escape HTML utk semua teks dari user/device sebelum masuk markup admin
func esc(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;",
		`"`, "&#34;", "'", "&#39;",
	)
	return r.Replace(s)
}

type orderLite struct {
	ID        string
	Status    string
	Price     int64
	Total     int64
	CreatedAt int64
	Label     string
	Buyer     string
	App       string
}

type payLite struct {
	Pkg        string
	Title      string
	Amount     sql.NullInt64
	ReceivedAt int64
}

type unmatchLite struct {
	PaymentID  string
	Amount     int64
	Reason     string
	ReceivedAt int64
	// kandidat order dengan total sama (pending/expired) untuk dicocokkan manual
	Candidates []unmatchCand
}

// unmatchCand: order yang nominalnya cocok dengan notif tak terduga.
type unmatchCand struct {
	ID     string
	Status string
	Price  int64
	Code   int64
	Total  int64
	At     int64
}

func (s *srv) recentOrders(n int) []orderLite {
	rows, err := s.db.Query(`SELECT o.id,o.status,o.price,o.total,o.created_at,o.label,o.buyer,COALESCE(a.name,'')
		FROM orders o LEFT JOIN apps a ON a.rowid=o.created_by
		ORDER BY o.created_at DESC LIMIT ?`, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []orderLite
	for rows.Next() {
		var o orderLite
		if rows.Scan(&o.ID, &o.Status, &o.Price, &o.Total, &o.CreatedAt, &o.Label, &o.Buyer, &o.App) == nil {
			out = append(out, o)
		}
	}
	return out
}

func (s *srv) recentPayments(n int) []payLite {
	rows, err := s.db.Query("SELECT pkg,title,amount,received_at FROM payments ORDER BY received_at DESC LIMIT ?", n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []payLite
	for rows.Next() {
		var p payLite
		if rows.Scan(&p.Pkg, &p.Title, &p.Amount, &p.ReceivedAt) == nil {
			out = append(out, p)
		}
	}
	return out
}

// escOrDash: escape HTML, tampilkan "—" kalau kosong.
func escOrDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "—"
	}
	return esc(v)
}

func (s *srv) recentUnmatched(n int) []unmatchLite {
	rows, err := s.db.Query("SELECT payment_id,amount,reason,received_at FROM unmatched ORDER BY received_at DESC LIMIT ?", n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []unmatchLite
	for rows.Next() {
		var u unmatchLite
		if rows.Scan(&u.PaymentID, &u.Amount, &u.Reason, &u.ReceivedAt) == nil {
			out = append(out, u)
		}
	}
	rows.Close() // WAJIB: pool = 1 koneksi, nested query di dalam loop = deadlock
	// kandidat diambil setelah rows ditutup
	for i := range out {
		out[i].Candidates = s.candidateOrders(out[i].Amount)
	}
	return out
}

// candidateOrders: order dengan total persis sama yang masih bisa ditindaklanjuti.
// Dipakai tombol "Cocokkan" di dashboard untuk notif yang telat masuk.
func (s *srv) candidateOrders(total int64) []unmatchCand {
	rows, err := s.db.Query(
		"SELECT id,status,price,code,total,COALESCE(paid_at,created_at) FROM orders WHERE total=? AND status IN ('pending','expired','refunded') ORDER BY created_at DESC LIMIT 5", total)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []unmatchCand
	for rows.Next() {
		var c unmatchCand
		if rows.Scan(&c.ID, &c.Status, &c.Price, &c.Code, &c.Total, &c.At) == nil {
			out = append(out, c)
		}
	}
	return out
}

// markUnmatchedPaid: cocokkan notif tak terduga ke order tertentu (aksi admin).
// Order harus pending/expired dan nominal harus sama — mencegah salah klik.
func (s *srv) markUnmatchedPaid(paymentID, orderID string) (string, bool) {
	var amount int64
	if err := s.db.QueryRow("SELECT amount FROM unmatched WHERE payment_id=?", paymentID).Scan(&amount); err != nil {
		return "Notif tidak ditemukan", false
	}
	var status string
	var total int64
	if err := s.db.QueryRow("SELECT status,total FROM orders WHERE id=?", orderID).Scan(&status, &total); err != nil {
		return "Order tidak ditemukan", false
	}
	if total != amount {
		return "Nominal tidak sama — dibatalkan", false
	}
	if status == "paid" {
		return "Order sudah lunas", false
	}
	res, err := s.db.Exec("UPDATE orders SET status='paid', paid_at=strftime('%s','now') WHERE id=? AND status<>'paid'", orderID)
	if err != nil {
		return "Gagal update: " + err.Error(), false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "Gagal menandai lunas", false
	}
	s.db.Exec("DELETE FROM unmatched WHERE payment_id=?", paymentID)
	s.audit(s.adminUser(), "unmatched.match", paymentID+" -> "+orderID)
	// goroutine: jangan tahan handler HTTP selama query/HTTP webhook
	go s.fireWebhooks(orderID, total)
	return "Notif dicocokkan ke order " + orderID, true
}

type paidTx struct {
	ID     string
	Source string
	Total  int64
	PaidAt int64
}

// recentPaidFull: transaksi lunas terbaru dgn sumber pembayaran
func (s *srv) recentPaidFull(n int) []paidTx {
	rows, err := s.db.Query(`SELECT o.id, COALESCE(p.source,''), o.total, o.paid_at
		FROM orders o LEFT JOIN payments p ON p.amount = o.total
		WHERE o.status='paid' ORDER BY o.paid_at DESC LIMIT ?`, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []paidTx
	for rows.Next() {
		var t paidTx
		if rows.Scan(&t.ID, &t.Source, &t.Total, &t.PaidAt) == nil {
			out = append(out, t)
		}
	}
	return out
}

// deleteTx: hapus satu order + payment terkait (dipanggil dari dashboard).
func (s *srv) deleteTx(id string) bool {
	res, err := s.db.Exec("DELETE FROM orders WHERE id=?", id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	s.db.Exec("DELETE FROM payments WHERE id LIKE ?", id+"|%")
	s.db.Exec("DELETE FROM webhook_log WHERE order_id=?", id)
	return n > 0
}

// rp: format rupiah ringkas: 17500 -> "17.500"
func rp(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	out = append([]string{s}, out...)
	return strings.Join(out, ".")
}

// readQrisBase: prioritas DB (diset via web), fallback file qris_base.txt.
func (s *srv) readQrisBase() string {
	var q string
	s.db.QueryRow("SELECT value FROM settings WHERE key='qris_base'").Scan(&q)
	if q != "" {
		return q
	}
	b, err := os.ReadFile("qris_base.txt")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// qrisImageBase64: gambar QRIS statis yang diupload admin (PNG/JPG base64) — tampil di checkout.
func (s *srv) qrisImageBase64() string {
	var q string
	s.db.QueryRow("SELECT value FROM settings WHERE key='qris_image'").Scan(&q)
	return q
}

// loadTGFromDB: baca pengaturan telegram dari tabel settings (set via web)
func (s *srv) loadTGFromDB() {
	s.db.QueryRow("SELECT value FROM settings WHERE key='tg_token'").Scan(&s.tgToken)
	s.db.QueryRow("SELECT value FROM settings WHERE key='tg_chat'").Scan(&s.tgChat)
}
