package main

import (
	"html/template"
	"net/http"
	"strings"
	"time"
)

// handleAdminLaporan: laporan penjualan per bulan + hapus data.
func (s *srv) handleAdminLaporan(w http.ResponseWriter, r *http.Request) {
	if !s.requireSession(w, r) {
		return
	}
	flash := ""
	if r.Method == http.MethodPost {
		actor := s.adminUser()
		switch r.FormValue("act") {
		case "purge_month":
			month := r.FormValue("month") // format YYYY-MM
			if len(month) == 7 && month[4] == '-' {
				start, err := time.Parse("2006-01", month)
				if err != nil {
					flash = "Format bulan salah"
					break
				}
				from := start.Unix()
				to := start.AddDate(0, 1, 0).Unix() - 1
				// hapus data transaksional bulan tsb (order, payments, unmatched, webhook_log)
				r1 := s.deleteRange("DELETE FROM orders WHERE created_at BETWEEN ? AND ?", from, to)
				r2 := s.deleteRange("DELETE FROM payments WHERE received_at BETWEEN ? AND ?", from, to)
				s.deleteRange("DELETE FROM unmatched WHERE received_at BETWEEN ? AND ?", from, to)
				s.deleteRange(`DELETE FROM webhook_log WHERE at BETWEEN ? AND ?`, from, to)
				s.audit(actor, "data.purge", "bulan "+month+" ("+itoa64(r1+int64(r2))+" baris)")
				flash = "Data bulan " + month + " dihapus"
			} else {
				flash = "Pilih bulan dulu"
			}
		case "purge_all":
			if r.FormValue("confirm") != "HAPUS" {
				flash = "Ketik HAPUS untuk konfirmasi"
				break
			}
			// hapus SEMUA data transaksional (order/payments/unmatched/webhook_log).
			// apps, settings, audit TIDAK disentuh.
			db := s.db
			db.Exec("DELETE FROM orders")
			db.Exec("DELETE FROM payments")
			db.Exec("DELETE FROM unmatched")
			db.Exec("DELETE FROM webhook_log")
			s.audit(actor, "data.purge_all", "semua data transaksional dihapus")
			flash = "Semua data transaksional dihapus"
		}
	}

	// rekap per bulan (dari orders paid, fallback created_at)
	type monthRow struct {
		Month   string
		Paid    int64
		Expired int64
		Revenue int64
	}
	var rows []monthRow
	dbRows, err := s.db.Query(`
		SELECT strftime('%Y-%m', COALESCE(paid_at, expires_at), 'unixepoch') AS m,
		       SUM(status='paid'), SUM(status='expired'),
		       COALESCE(SUM(CASE WHEN status='paid' THEN total END),0)
		FROM orders
		GROUP BY m ORDER BY m DESC LIMIT 24`)
	if err == nil {
		for dbRows.Next() {
			var mr monthRow
			dbRows.Scan(&mr.Month, &mr.Paid, &mr.Expired, &mr.Revenue)
			rows = append(rows, mr)
		}
		dbRows.Close()
	}
	// tambahkan bulan yang hanya punya payments (tanpa order paid)
	payRows, _ := s.db.Query(`
		SELECT strftime('%Y-%m', received_at, 'unixepoch'), COUNT(*)
		FROM payments GROUP BY 1 ORDER BY 1 DESC LIMIT 24`)
	payCount := map[string]int64{}
	if payRows != nil {
		for payRows.Next() {
			var m string
			var cnt int64
			payRows.Scan(&m, &cnt)
			payCount[m] = cnt
		}
		payRows.Close()
	}
	for m := range payCount {
		found := false
		for i := range rows {
			if rows[i].Month == m {
				found = true
				break
			}
		}
		if !found {
			rows = append(rows, monthRow{Month: m})
		}
	}
	// urutkan desc
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[j].Month > rows[i].Month {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}

	curMonth := time.Now().Format("2006-01")

	s.renderPage(w, "laporan", "Laporan", flash, func() template.HTML {
		var b strings.Builder
		b.WriteString(`<div class="card"><h2>Rekap per bulan</h2><table>
<tr><th>Bulan</th><th class="money">Lunas</th><th class="money">Expired</th><th class="money">Pendapatan (lunas)</th></tr>`)
		if len(rows) == 0 {
			b.WriteString(`<tr><td colspan="4" class="empty">Belum ada data</td></tr>`)
		}
		for _, mr := range rows {
			label := mr.Month
			if t, err := time.Parse("2006-01", mr.Month); err == nil {
				label = t.Format("January 2006")
			}
			b.WriteString(`<tr><td><b>` + label + `</b></td><td class="money">` + itoa64(mr.Paid) + `</td><td class="money">` + itoa64(mr.Expired) + `</td><td class="money">` + rp(mr.Revenue) + `</td></tr>`)
		}
		b.WriteString(`</table><small>Hanya order <b>paid</b> dihitung pendapatan.</small></div>`)

		b.WriteString(`<div class="card"><h2>Hapus data per bulan</h2>
<form method="post" style="display:flex;gap:10px;align-items:center;flex-wrap:wrap">
<input type="hidden" name="act" value="purge_month">
<input type="month" name="month" value="` + curMonth + `" style="margin:0" required>
<button class="del">Hapus Data Bulan Ini</button></form>
<small>Menghapus order, payments, unmatched, dan webhook log bulan tersebut. Tidak bisa dibatalkan.</small></div>`)

		b.WriteString(`<div class="card"><h2>Hapus SEMUA data transaksional</h2>
<form method="post"><input type="hidden" name="act" value="purge_all">
<label style="font-size:13px;color:#344054;font-weight:600">Ketik HAPUS untuk konfirmasi</label>
<input name="confirm" placeholder="HAPUS" style="width:200px" required>
<button class="del">Hapus Semua</button></form>
<small>Menghapus semua order, payments, unmatched, webhook log. Aplikasi & token, pengaturan, dan audit log TIDAK terhapus.</small></div>`)
		return template.HTML(b.String())
	})
}

func (s *srv) deleteRange(query string, from, to int64) int64 {
	res, err := s.db.Exec(query, from, to)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}
