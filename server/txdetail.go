package main

import (
	"database/sql"
	"html/template"
	"net/http"
	"strings"
	"time"
)

type whRow struct {
	ID     int64
	URL    string
	Active bool
}

func (s *srv) listWebhooks() []whRow {
	rows, err := s.db.Query("SELECT rowid,url,active FROM webhooks ORDER BY rowid")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []whRow
	for rows.Next() {
		var w whRow
		if rows.Scan(&w.ID, &w.URL, &w.Active) == nil {
			out = append(out, w)
		}
	}
	return out
}

// GET /admin/tx/{id} — detail transaksi: order + notif yang match + aksi manual.
func (s *srv) handleTxDetail(w http.ResponseWriter, r *http.Request) {
	if !s.requireSession(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/tx/")
	flash := ""
	if r.Method == http.MethodPost {
		switch r.FormValue("act") {
		case "markpaid":
			if s.manualPaid(id) {
				s.audit(s.adminUser(), "order.markpaid", id)
				flash = "Order ditandai LUNAS manual"
			} else {
				flash = "Gagal (bukan pending / tidak ada)"
			}
		case "refund":
			res, err := s.db.Exec("UPDATE orders SET status='refunded' WHERE id=? AND status='paid'", id)
			if err == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					s.audit(s.adminUser(), "order.refund", id)
					flash = "Order di-refund (dikembalikan pending tidak; status refunded)"
				}
			}
		case "expire":
			s.db.Exec("UPDATE orders SET status='expired' WHERE id=? AND status='pending'", id)
			s.audit(s.adminUser(), "order.expire", id)
			flash = "Order di-expire-kan"
		}
	}

	var status string
	var price, total, code int64
	var createdAt, expiresAt int64
	var paidAt sql.NullInt64
	err := s.db.QueryRow(
		"SELECT status,price,total,code,created_at,expires_at,paid_at FROM orders WHERE id=?", id).
		Scan(&status, &price, &total, &code, &createdAt, &expiresAt, &paidAt)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}

	// notif yang men-trigger (cari payment dengan amount = total, terdekat paid_at)
	var payPkg, payTitle, payText string
	var payAt sql.NullInt64
	s.db.QueryRow(`SELECT pkg,IFNULL(title,''),IFNULL(text,''),received_at FROM payments
	               WHERE amount=? ORDER BY received_at DESC LIMIT 1`, total).
		Scan(&payPkg, &payTitle, &payText, &payAt)

	// riwayat webhook utk order ini
	type whLog struct{ At, URL, Code string }
	var whs []whLog
	rows, _ := s.db.Query("SELECT at,url,code FROM webhook_log WHERE order_id=? ORDER BY id DESC LIMIT 8", id)
	for rows.Next() {
		var at int64
		var u string
		var c int64
		rows.Scan(&at, &u, &c)
		whs = append(whs, whLog{time.Unix(at, 0).Format("02 Jan 15:04"), u, itoa64(c)})
	}
	rows.Close()

	fm := func(ts int64) string {
		if ts <= 0 {
			return "—"
		}
		return time.Unix(ts, 0).Format("02 Jan 2006 15:04")
	}

	s.renderPage(w, "tx", "Detail Transaksi", flash, func() template.HTML {
		var b strings.Builder
		b.WriteString(`<div class="card"><h2>Order <code>` + id + `</code></h2>
<table><tr><th>Status</th><td><span class="badge ` + status + `">` + status + `</span></td></tr>
<tr><th>Harga</th><td class="money">Rp ` + itoa64(price) + `</td></tr>
<tr><th>Kode unik</th><td>+` + itoa64(code) + `</td></tr>
<tr><th>Total dibayar</th><td class="money"><b>Rp ` + itoa64(total) + `</b></td></tr>
<tr><th>Dibuat</th><td>` + fm(createdAt) + `</td></tr>
<tr><th>Expired</th><td>` + fm(expiresAt) + `</td></tr>
<tr><th>Dibayar</th><td>` + fm(paidAt.Int64) + `</td></tr></table>
<div style="margin-top:12px">
<form method="post" class="inline"><input type="hidden" name="act" value="markpaid"><button>Tandai LUNAS manual</button></form>
<form method="post" class="inline"><input type="hidden" name="act" value="refund"><button class="sec">Refund (paid→refunded)</button></form>
<form method="post" class="inline"><input type="hidden" name="act" value="expire"><button class="del">Expire-kan</button></form>
</div>
<small>Link checkout publik: <code>/pay/` + id + `</code></small></div>`)
		b.WriteString(`<div class="card"><h2>Notif pembayaran terkait</h2>
<table><tr><th>App</th><th>Judul</th><th>Isi</th><th>Diterima</th></tr>
<tr><td><code>` + esc(payPkg) + `</code></td><td>` + esc(payTitle) + `</td><td>` + esc(payText) + `</td><td>` + fm(payAt.Int64) + `</td></tr></table></div>`)
		if len(whs) > 0 {
			b.WriteString(`<div class="card"><h2>Webhook terkirim</h2><table><tr><th>Waktu</th><th>URL</th><th>HTTP</th></tr>`)
			for _, w := range whs {
				b.WriteString(`<tr><td>` + w.At + `</td><td><code>` + w.URL + `</code></td><td>` + w.Code + `</td></tr>`)
			}
			b.WriteString(`</table></div>`)
		}
		return template.HTML(b.String())
	})
}
