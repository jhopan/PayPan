package main

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// handleExport: unduh transaksi sebagai CSV.
// Query: ?from=YYYY-MM-DD&to=YYYY-MM-DD&status=paid (opsional)
// Tanpa from/to = 30 hari terakhir.
func (s *srv) handleExport(w http.ResponseWriter, r *http.Request) {
	if !s.requireSession(w, r) {
		return
	}

	loc := time.Local
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -30)
	// to eksklusif: awal hari BESOK, supaya transaksi hari ini ikut
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)

	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, loc); err == nil {
			from = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, loc); err == nil {
			to = t.AddDate(0, 0, 1) // inklusif sampai akhir hari
		}
	}
	status := r.URL.Query().Get("status")

	q := `SELECT o.id,o.price,o.code,o.total,o.status,o.label,o.buyer,o.created_at,o.expires_at,COALESCE(o.paid_at,0),COALESCE(a.name,'')
		FROM orders o LEFT JOIN apps a ON a.rowid=o.created_by WHERE o.created_at>=? AND o.created_at<?`
	args := []any{from.Unix(), to.Unix()}
	if status != "" {
		q += " AND o.status=?"
		args = append(args, status)
	}
	q += " ORDER BY o.created_at DESC"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		http.Error(w, "gagal query: "+err.Error(), 500)
		return
	}
	defer rows.Close()

	name := fmt.Sprintf("paypan-transaksi-%s_%s.csv", from.Format("20060102"), to.AddDate(0, 0, -1).Format("20060102"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	// BOM supaya Excel membaca UTF-8 dengan benar
	w.Write([]byte("\xEF\xBB\xBF"))

	cw := csv.NewWriter(w)
	defer cw.Flush()
	cw.Write([]string{"ID", "Waktu dibuat", "Status", "Harga", "Kode", "Total", "Untuk apa", "Pemesan", "Diminta oleh", "Dibayar pada", "Link"})

	fmtTime := func(ts int64) string {
		if ts == 0 {
			return ""
		}
		return time.Unix(ts, 0).In(loc).Format("2006-01-02 15:04:05")
	}

	for rows.Next() {
		var id, st, label, buyer, app string
		var price, code, total, createdAt, expiresAt, paidAt int64
		if rows.Scan(&id, &price, &code, &total, &st, &label, &buyer, &createdAt, &expiresAt, &paidAt, &app) != nil {
			continue
		}
		cw.Write([]string{
			id,
			fmtTime(createdAt),
			st,
			strconv.FormatInt(price, 10),
			strconv.FormatInt(code, 10),
			strconv.FormatInt(total, 10),
			label,
			buyer,
			app,
			fmtTime(paidAt),
			"/pay/" + id,
		})
	}

	s.audit(s.adminUser(), "export.csv", fmt.Sprintf("%s s/d %s status=%q", from.Format("2006-01-02"), to.Format("2006-01-02"), status))
}
