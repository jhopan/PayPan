package main

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func itoa(n int) string       { return strconv.Itoa(n) }
func itoa64(n int64) string   { return strconv.FormatInt(n, 10) }
func fmt_Sscan(s string, v *int64) { fmt.Sscan(s, v) }
func os_WriteFile(p string, d []byte, m os.FileMode) { os.WriteFile(p, d, m) }

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
}

type payLite struct {
	Pkg        string
	Title      string
	Amount     sql.NullInt64
	ReceivedAt int64
}

type unmatchLite struct {
	Amount     int64
	Reason     string
	ReceivedAt int64
}

func (s *srv) recentOrders(n int) []orderLite {
	rows, err := s.db.Query("SELECT id,status,price,total,created_at FROM orders ORDER BY created_at DESC LIMIT ?", n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []orderLite
	for rows.Next() {
		var o orderLite
		if rows.Scan(&o.ID, &o.Status, &o.Price, &o.Total, &o.CreatedAt) == nil {
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

func (s *srv) recentUnmatched(n int) []unmatchLite {
	rows, err := s.db.Query("SELECT amount,reason,received_at FROM unmatched ORDER BY received_at DESC LIMIT ?", n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []unmatchLite
	for rows.Next() {
		var u unmatchLite
		if rows.Scan(&u.Amount, &u.Reason, &u.ReceivedAt) == nil {
			out = append(out, u)
		}
	}
	return out
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
