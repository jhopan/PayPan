package main

import (
	"embed"
	"html/template"
	"net/http"
	"strings"
)

//go:embed static/*
var staticFS embed.FS

func (s *srv) staticHandler(w http.ResponseWriter, r *http.Request) {
	// belokkan ke embedded static (css/js admin)
	path := strings.TrimPrefix(r.URL.Path, "/static/")
	b, err := staticFS.ReadFile("static/" + path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(path, ".css") {
		w.Header().Set("Content-Type", "text/css")
	}
	w.Write(b)
}

// ---------- login ----------

func (s *srv) handleLogin(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if r.Method == http.MethodPost {
		r.ParseForm()
		if r.FormValue("pass") == s.adminPass() {
			tok := sessions.newSession()
			http.SetCookie(w, &http.Cookie{
				Name: "paypan_session", Value: tok, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
			})
			http.Redirect(w, r, "/admin", 302)
			return
		}
		msg = "Password salah"
	}
	tmpl, _ := template.New("l").Parse(`<!doctype html><html lang="id"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>Login — Paypan</title>
<style>body{font-family:system-ui;background:#f2f4f8;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0}
.c{background:#fff;padding:32px;border-radius:16px;box-shadow:0 2px 16px rgba(0,0,0,.09);width:320px}
h1{font-size:20px;margin:0 0 16px}input{width:100%;box-sizing:border-box;padding:10px;margin:6px 0 12px;border:1px solid #ccc;border-radius:8px}
button{width:100%;padding:10px;background:#1a7f37;color:#fff;border:0;border-radius:8px;font-weight:600;cursor:pointer}
.e{color:#b00;font-size:14px}</style></head><body><div class="c">
<h1>Paypan Admin</h1>
<form method="post"><input type="password" name="pass" placeholder="Password admin" autofocus>
<button>Login</button></form>{{if .}}<p class="e">{{.}}</p>{{end}}</div></body></html>`)
	tmpl.Execute(w, msg)
}

func (s *srv) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("paypan_session"); err == nil {
		sessions.drop(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "paypan_session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/admin/login", 302)
}

// ---------- admin pages ----------

var adminTmpl = template.Must(template.New("a").Parse(`<!doctype html><html lang="id"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}} — Paypan</title>
<style>
body{font-family:system-ui;background:#f2f4f8;margin:0}
header{background:#101828;color:#fff;padding:12px 24px;display:flex;justify-content:space-between;align-items:center}
header a{color:#94b8ff;text-decoration:none;font-size:14px}
.wrap{max-width:960px;margin:24px auto;padding:0 16px}
.card{background:#fff;border-radius:12px;padding:20px;margin-bottom:20px;box-shadow:0 1px 6px rgba(0,0,0,.06)}
h2{margin:0 0 12px;font-size:17px}
table{width:100%;border-collapse:collapse;font-size:14px}
th,td{padding:8px 10px;border-bottom:1px solid #eee;text-align:left}
th{color:#667085;font-weight:600;background:#f9fafb}
code{background:#f2f4f8;padding:2px 6px;border-radius:6px;font-size:13px}
input,select{padding:8px;border:1px solid #ccc;border-radius:8px;margin:4px 0}
button{padding:8px 14px;background:#1a7f37;color:#fff;border:0;border-radius:8px;cursor:pointer;font-weight:600}
button.del{background:#b42318}
.badge{padding:3px 10px;border-radius:999px;font-size:12px;font-weight:600}
.paid{background:#d4edda;color:#186a3b}.pending{background:#fff3cd;color:#8a6d00}
.expired{background:#f8d7da;color:#8a1c1c}
.money{font-variant-numeric:tabular-nums;text-align:right}
.flash{background:#d4edda;color:#186a3b;padding:10px 14px;border-radius:8px;margin-bottom:14px}
small{color:#667085}
.nav{display:flex;gap:8px;margin-bottom:16px}
.nav a{padding:8px 16px;border-radius:8px;background:#fff;color:#101828;text-decoration:none;font-weight:600;font-size:14px;border:1px solid #e4e7ec}
.nav a.on{background:#101828;color:#fff}
</style></head><body>
<header><b>Paypan</b> <small style="color:#94a3b8">payment gateway</small><a href="/admin/logout">Logout</a></header>
<div class="wrap">
<div class="nav">
<a href="/admin" class="{{if eq .Tab "dash"}}on{{end}}">Dashboard</a>
<a href="/admin/apps" class="{{if eq .Tab "apps"}}on{{end}}">Aplikasi & Token</a>
<a href="/admin/config" class="{{if eq .Tab "config"}}on{{end}}">Konfigurasi</a>
</div>
{{if .Flash}}<div class="flash">{{.Flash}}</div>{{end}}
{{.Body}}
</div></body></html>`))
type pageData struct {
	Title string
	Tab   string
	Flash string
	Body  func() template.HTML
}

func (s *srv) renderPage(w http.ResponseWriter, tab, title, flash string, body func() template.HTML) {
	adminTmpl.Execute(w, map[string]any{
		"Tab": tab, "Title": title, "Flash": flash, "Body": body(),
	})
}

func (s *srv) handleAdminHome(w http.ResponseWriter, r *http.Request) {
	if !s.requireSession(w, r) {
		return
	}
	var stats struct{ Paid, Pending, Expired, Pay, Unmatch int }
	s.db.QueryRow("SELECT SUM(status='paid'),SUM(status='pending'),SUM(status='expired') FROM orders").Scan(&stats.Paid, &stats.Pending, &stats.Expired)
	s.db.QueryRow("SELECT COUNT(*) FROM payments").Scan(&stats.Pay)
	s.db.QueryRow("SELECT COUNT(*) FROM unmatched").Scan(&stats.Unmatch)

	orders := s.recentOrders(15)
	pays := s.recentPayments(10)
	unm := s.recentUnmatched(8)

	s.renderPage(w, "dash", "Dashboard", r.URL.Query().Get("m"), func() template.HTML {
		// render inline string-builder: sederhana, tanpa template ganda
		var b strings.Builder
		b.WriteString(`<div class="card"><h2>Ringkasan</h2><table><tr><th>Order lunas</th><th>Pending</th><th>Expired</th><th>Notif diterima</th><th>Unmatched</th></tr><tr>`)
		b.WriteString(`<td class="money">` + itoa(stats.Paid) + `</td><td class="money">` + itoa(stats.Pending) + `</td><td class="money">` + itoa(stats.Expired) + `</td><td class="money">` + itoa(stats.Pay) + `</td><td class="money">` + itoa(stats.Unmatch) + `</td></tr></table></div>`)
		b.WriteString(`<div class="card"><h2>Order terakhir</h2><table><tr><th>ID</th><th>Status</th><th>Price</th><th>Total</th></tr>`)
		for _, o := range orders {
			b.WriteString(`<tr><td><code>` + o.ID + `</code></td><td><span class="badge ` + o.Status + `">` + o.Status + `</span></td><td class="money">` + itoa64(o.Price) + `</td><td class="money">` + itoa64(o.Total) + `</td></tr>`)
		}
		b.WriteString(`</table></div>`)
		b.WriteString(`<div class="card"><h2>Notif terakhir</h2><table><tr><th>App</th><th>Judul</th><th>Amount</th></tr>`)
		for _, p := range pays {
			amt := "—"
			if p.Amount.Valid {
				amt = itoa64(p.Amount.Int64)
			}
			b.WriteString(`<tr><td><code>` + p.Pkg + `</code></td><td>` + p.Title + `</td><td class="money">` + amt + `</td></tr>`)
		}
		b.WriteString(`</table></div>`)
		if len(unm) > 0 {
			b.WriteString(`<div class="card"><h2>Unmatched</h2><table><tr><th>Amount</th><th>Alasan</th></tr>`)
			for _, u := range unm {
				b.WriteString(`<tr><td class="money">` + itoa64(u.Amount) + `</td><td>` + u.Reason + `</td></tr>`)
			}
			b.WriteString(`</table></div>`)
		}
		return template.HTML(b.String())
	})
}

func (s *srv) handleAdminApps(w http.ResponseWriter, r *http.Request) {
	if !s.requireSession(w, r) {
		return
	}
	flash := ""
	if r.Method == http.MethodPost {
		r.ParseForm()
		act := r.FormValue("act")
		switch act {
		case "add":
			name := strings.TrimSpace(r.FormValue("name"))
			if name != "" {
				s.db.Exec("INSERT INTO apps(name,token,scopes,active,created_at) VALUES(?,?,?,1,strftime('%s','now'))",
					name, genToken(), r.FormValue("scopes"))
				flash = "Aplikasi '" + name + "' dibuat"
			}
		case "rotate":
			var id int64
			fmt_Sscan(r.FormValue("id"), &id)
			s.db.Exec("UPDATE apps SET token=? WHERE rowid=?", genToken(), id)
			flash = "Token di-rotate"
		case "toggle":
			var id int64
			fmt_Sscan(r.FormValue("id"), &id)
			s.db.Exec("UPDATE apps SET active=1-active WHERE rowid=?", id)
			flash = "Status aplikasi diubah"
		case "del":
			var id int64
			fmt_Sscan(r.FormValue("id"), &id)
			s.db.Exec("DELETE FROM apps WHERE rowid=?", id)
			flash = "Aplikasi dihapus"
		}
	}
	apps := listApps(s.db)
	s.renderPage(w, "apps", "Aplikasi & Token", flash, func() template.HTML {
		var b strings.Builder
		b.WriteString(`<div class="card"><h2>Tambah aplikasi</h2>
<form method="post"><input type="hidden" name="act" value="add">
<input name="name" placeholder="Nama aplikasi (mis. Toko Web, Bot Telegram)" required>
<select name="scopes"><option value="both">notif + order</option><option value="notif">notif saja</option><option value="order">order saja</option></select>
<button>Tambah</button></form></div>
<div class="card"><h2>Daftar aplikasi</h2><table><tr><th>Nama</th><th>Token</th><th>Scope</th><th>Status</th><th>Aksi</th></tr>`)
		for _, a := range apps {
			st := "aktif"
			if !a.Active {
				st = "nonaktif"
			}
			b.WriteString(`<tr><td>` + a.Name + `</td><td><code>` + a.Token + `</code></td><td>` + a.Scopes + `</td><td>` + st + `</td>
<td><form method="post" style="display:inline"><input type="hidden" name="act" value="rotate"><input type="hidden" name="id" value="` + itoa64(a.ID) + `"><button>Rotate</button></form>
<form method="post" style="display:inline"><input type="hidden" name="act" value="toggle"><input type="hidden" name="id" value="` + itoa64(a.ID) + `"><button>On/Off</button></form>
<form method="post" style="display:inline"><input type="hidden" name="act" value="del"><input type="hidden" name="id" value="` + itoa64(a.ID) + `"><button class="del">Hapus</button></form></td></tr>`)
		}
		b.WriteString(`</table><small>Token dipakai di app HP (field Token) dan header <code>Authorization: Bearer &lt;token&gt;</code> untuk API.</small></div>`)
		return template.HTML(b.String())
	})
}

func (s *srv) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireSession(w, r) {
		return
	}
	flash := ""
	if r.Method == http.MethodPost {
		r.ParseForm()
		switch r.FormValue("act") {
		case "qris":
			q := strings.TrimSpace(r.FormValue("qris"))
			if strings.Contains(q, "010211") && strings.Contains(q, "5802ID") && strings.Contains(q, "6304") {
				os_WriteFile("qris_base.txt", []byte(q), 0600)
				flash = "QRIS statis tersimpan"
			} else {
				flash = "Payload tidak dikenali (butuh 010211, 5802ID, 6304)"
			}
		case "pass":
			np := r.FormValue("newpass")
			if len(np) >= 6 {
				s.setAdminPass(np)
				flash = "Password admin diganti"
			} else {
				flash = "Password minimal 6 karakter"
			}
		case "tg":
			s.db.Exec("INSERT INTO settings(key,value) VALUES('tg_token',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", r.FormValue("tgtoken"))
			s.db.Exec("INSERT INTO settings(key,value) VALUES('tg_chat',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", r.FormValue("tgchat"))
			s.loadTGFromDB()
			flash = "Telegram tersimpan"
		}
	}
	cur := s.readQris()
	var tgToken, tgChat string
	s.db.QueryRow("SELECT value FROM settings WHERE key='tg_token'").Scan(&tgToken)
	s.db.QueryRow("SELECT value FROM settings WHERE key='tg_chat'").Scan(&tgChat)

	s.renderPage(w, "config", "Konfigurasi", flash, func() template.HTML {
		var b strings.Builder
		b.WriteString(`<div class="card"><h2>QRIS statis (template)</h2>
<form method="post"><input type="hidden" name="act" value="qris">
<textarea name="qris" rows="4" style="width:100%;box-sizing:border-box;font-family:monospace;font-size:12px;padding:8px" placeholder="000201010211...">` + cur + `</textarea>
<button>Simpan QRIS</button> <small>Scan QR statis lo dengan scanner, paste raw text di sini.</small></form></div>`)
		b.WriteString(`<div class="card"><h2>Notifikasi Telegram (opsional)</h2>
<form method="post"><input type="hidden" name="act" value="tg">
<input name="tgtoken" placeholder="Bot token" value="` + tgToken + `" style="width:100%">
<input name="tgchat" placeholder="Chat ID tujuan" value="` + tgChat + `" style="width:100%">
<button>Simpan Telegram</button></form></div>`)
		b.WriteString(`<div class="card"><h2>Password admin</h2>
<form method="post"><input type="hidden" name="act" value="pass">
<input type="password" name="newpass" placeholder="Password baru (min 6)" style="width:100%">
<button>Ganti Password</button></form></div>`)
		return template.HTML(b.String())
	})
}
