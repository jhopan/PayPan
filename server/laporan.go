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
		case "del_tx":
			id := r.FormValue("id")
			if s.deleteTx(id) {
				s.audit(actor, "tx.delete", id)
				flash = "Transaksi " + id + " dihapus"
			} else {
				flash = "Gagal hapus (tidak ditemukan)"
			}
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

	// daftar transaksi paid terbaru (bisa difilter per bulan) + sumber dari payments
	filterMonth := r.URL.Query().Get("bulan")
	listQ := `SELECT o.id, o.total, COALESCE((SELECT p.source FROM payments p WHERE p.amount=o.total ORDER BY p.received_at DESC LIMIT 1),''), o.paid_at
		FROM orders o WHERE o.status='paid'`
	var listArgs []any
	if filterMonth != "" && len(filterMonth) == 7 {
		start, err := time.Parse("2006-01", filterMonth)
		if err == nil {
			listQ += ` AND paid_at BETWEEN ? AND ?`
			listArgs = append(listArgs, start.Unix(), start.AddDate(0, 1, 0).Unix()-1)
		}
	}
	listQ += ` ORDER BY paid_at DESC LIMIT 50`
	txRows, err := s.db.Query(listQ, listArgs...)
	type txRow struct {
		ID, Source string
		Total      int64
		PaidAt     int64
	}
	var txs []txRow
	if err == nil {
		for txRows.Next() {
			var t txRow
			txRows.Scan(&t.ID, &t.Total, &t.Source, &t.PaidAt)
			txs = append(txs, t)
		}
		txRows.Close()
	}

	// rekap harian (30 hari terakhir, hanya paid)
	dayRows, err := s.db.Query(`
		SELECT date(paid_at,'unixepoch'), COUNT(*), SUM(total)
		FROM orders WHERE status='paid' AND paid_at > strftime('%s','now') - 30*86400
		GROUP BY 1 ORDER BY 1 DESC LIMIT 30`)
	type dayRow struct {
		Day     string
		Count   int64
		Revenue int64
	}
	var days []dayRow
	if err == nil {
		for dayRows.Next() {
			var d dayRow
			dayRows.Scan(&d.Day, &d.Count, &d.Revenue)
			days = append(days, d)
		}
		dayRows.Close()
	}

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

		// rekap harian 30 hari
		b.WriteString(`<div class="card"><h2>Rekap harian (30 hari terakhir)</h2><table>
<tr><th>Tanggal</th><th class="money">Transaksi</th><th class="money">Pendapatan</th></tr>`)
		if len(days) == 0 {
			b.WriteString(`<tr><td colspan="3" class="empty">Belum ada transaksi</td></tr>`)
		}
		for _, d := range days {
			t, _ := time.Parse("2006-01-02", d.Day)
			b.WriteString(`<tr><td>` + t.Format("02 Jan 2006") + `</td><td class="money">` + itoa64(d.Count) + `</td><td class="money">` + rp(d.Revenue) + `</td></tr>`)
		}
		b.WriteString(`</table></div>`)

		// daftar transaksi paid (filter per bulan)
		b.WriteString(`<div class="card"><h2>Transaksi Lunas</h2>
<form method="get" style="display:flex;gap:10px;align-items:center;margin-bottom:10px">
<input type="month" name="bulan" value="` + (func() string {
			if filterMonth != "" {
				return filterMonth
			}
			return curMonth
		})() + `" style="margin:0">
<button class="sec">Filter</button>
<a class="pg" href="/admin/laporan" style="margin-left:8px">Semua</a></form>
<table><tr><th>Waktu dibayar</th><th>Sumber</th><th>ID Order</th><th class="money">Total</th><th>Aksi</th></tr>`)
		if len(txs) == 0 {
			b.WriteString(`<tr><td colspan="5" class="empty">Tidak ada transaksi lunas</td></tr>`)
		}
		for _, t := range txs {
			b.WriteString(`<tr><td><small>` + timeFmt(t.PaidAt) + `</small></td><td>` + esc(t.Source) + `</td><td><code>` + t.ID + `</code></td><td class="money"><b>` + rp(t.Total) + `</b></td><td style="white-space:nowrap"><a class="pg" href="/admin/tx/` + t.ID + `">detail</a> <form method="post" class="inline" onsubmit="return confirm('Hapus transaksi ` + t.ID + `?')"><input type="hidden" name="act" value="del_tx"><input type="hidden" name="id" value="` + t.ID + `"><button class="del">Hapus</button></form></td></tr>`)
		}
		b.WriteString(`</table><small>Maks 50 transaksi terbaru.`)
		if filterMonth != "" {
			b.WriteString(` Filter: ` + filterMonth)
		}
		b.WriteString(`</small></div>`)

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
