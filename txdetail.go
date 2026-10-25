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
	Secret string
	Active bool
}

func (s *srv) listWebhooks() []whRow {
	rows, err := s.db.Query("SELECT rowid,url,secret,active FROM webhooks ORDER BY rowid")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []whRow
	for rows.Next() {
		var w whRow
		if rows.Scan(&w.ID, &w.URL, &w.Secret, &w.Active) == nil {
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
			if s.setOrderStatus(id, "paid", "refunded", s.adminUser(), "refund oleh admin") {
				s.audit(s.adminUser(), "order.refund", id)
				flash = "Order di-refund (status refunded, kode bebas setelah cooldown)"
			} else {
				flash = "Gagal: order bukan paid"
			}
		case "expire":
			if s.setOrderStatus(id, "pending", "expired", s.adminUser(), "di-expire manual oleh admin") {
				s.audit(s.adminUser(), "order.expire", id)
				flash = "Order di-expire"
			} else {
				flash = "Gagal: order bukan pending"
			}
		}
	}

	var status, label, buyer string
	var price, total, code int64
	var createdAt, expiresAt int64
	var paidAt sql.NullInt64
	var appName sql.NullString
	err := s.db.QueryRow(
		`SELECT o.status,o.price,o.total,o.code,o.created_at,o.expires_at,o.paid_at,o.label,o.buyer,a.name
		 FROM orders o LEFT JOIN apps a ON a.rowid=o.created_by WHERE o.id=?`, id).
		Scan(&status, &price, &total, &code, &createdAt, &expiresAt, &paidAt, &label, &buyer, &appName)
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

	s.renderPage(w, r, "tx", "Detail Transaksi", flash, func() template.HTML {
		var b strings.Builder
		b.WriteString(`<div class="card"><h2>Order <code>` + id + `</code></h2>
<table><tr><th>Status</th><td><span class="badge ` + status + `">` + status + `</span></td></tr>
<tr><th>Untuk apa</th><td>` + escOrDash(label) + `</td></tr>
<tr><th>Pemesan</th><td>` + escOrDash(buyer) + `</td></tr>
<tr><th>Diminta oleh</th><td>` + escOrDash(appName.String) + `</td></tr>
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
<form method="post" action="/admin/webhook/retry" class="inline"><input type="hidden" name="order_id" value="` + esc(id) + `"><button class="sec">Kirim ulang webhook</button></form>
</div>
<small>Link checkout publik: <code>/pay/` + id + `</code></small></div>`)
		// riwayat status
		evs := s.orderEvents(id)
		b.WriteString(`<div class="card"><h2>Riwayat status</h2>`)
		if len(evs) == 0 {
			b.WriteString(`<p style="font-size:13px;color:#98a2b3;margin:0">Belum ada perubahan status tercatat.</p>`)
		} else {
			b.WriteString(`<table><tr><th>Waktu</th><th>Perubahan</th><th>Oleh</th><th>Catatan</th></tr>`)
			for _, e := range evs {
				from := e.From
				if from == "" {
					from = "—"
				}
				b.WriteString(`<tr><td><small>` + fm(e.At) + `</small></td><td><code>` + esc(from) + `</code> → <span class="badge ` + esc(e.To) + `">` + esc(e.To) + `</span></td><td>` + esc(e.Actor) + `</td><td><small>` + escOrDash(e.Note) + `</small></td></tr>`)
			}
			b.WriteString(`</table>`)
		}
		b.WriteString(`</div>`)

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
