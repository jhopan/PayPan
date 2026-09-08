package main

import (
	"embed"
	"encoding/base64"
	"html/template"
	"net/http"
	"os"
	"strings"

	"github.com/skip2/go-qrcode"
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
		ip := clientIP(r)
		if !limiter.allow(ip) {
			msg = "Terlalu banyak percobaan. Coba lagi dalam 10 menit."
			tmpl, _ := template.New("l").Parse(loginHTML())
			tmpl.Execute(w, msg)
			return
		}
		r.ParseForm()
		if r.FormValue("user") == s.adminUser() && r.FormValue("pass") == s.adminPass() {
			limiter.reset(ip)
			tok := sessions.newSession()
			http.SetCookie(w, &http.Cookie{
				Name: "paypan_session", Value: tok, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
			})
			s.audit(s.adminUser(), "admin.login", "ip "+ip)
			http.Redirect(w, r, "/admin", 302)
			return
		}
		limiter.hit(ip)
		s.audit("anon", "login.gagal", "ip "+ip)
		msg = "Username atau password salah"
	}
	tmpl, _ := template.New("l").Parse(loginHTML())
	tmpl.Execute(w, msg)
}

func loginHTML() string { return `<!doctype html><html lang="id"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>Login — Paypan</title>
<style>body{font-family:system-ui;background:#f2f4f8;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0}
.c{background:#fff;padding:36px;border-radius:16px;box-shadow:0 2px 16px rgba(0,0,0,.09);width:340px}
.logo{display:flex;align-items:center;gap:10px;margin-bottom:20px}
.logo .dot{width:38px;height:38px;border-radius:10px;background:#101828;color:#fff;display:flex;align-items:center;justify-content:center;font-weight:800;font-size:18px}
.logo b{font-size:19px} .logo small{display:block;color:#667085;font-size:12px}
label{font-size:13px;color:#344054;font-weight:600;display:block;margin:10px 0 4px}
input{width:100%;box-sizing:border-box;padding:10px;margin:0 0 4px;border:1px solid #d0d5dd;border-radius:8px;font-size:14px}
input:focus{outline:2px solid #1a7f37;border-color:#1a7f37}
button{width:100%;padding:11px;background:#1a7f37;color:#fff;border:0;border-radius:8px;font-weight:600;cursor:pointer;margin-top:14px;font-size:15px}
button:hover{background:#166f30}
.e{color:#b42318;font-size:14px;background:#fee4e2;padding:8px 12px;border-radius:8px;margin-bottom:10px}
.hint{color:#667085;font-size:12px;margin-top:14px;text-align:center}</style></head><body><div class="c">
<div class="logo"><div class="dot">P</div><div><b>Paypan Admin</b><small>Payment Gateway Console</small></div></div>
{{if .}}<p class="e">{{.}}</p>{{end}}
<form method="post">
<label>Username</label><input type="text" name="user" placeholder="username" autofocus autocomplete="username">
<label>Password</label><input type="password" name="pass" placeholder="password" autocomplete="current-password">
<button>Login</button>
</form>
<div class="hint">Akses terbatas — dilarang dibagikan</div></div></body></html>` }


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
*{box-sizing:border-box}
body{font-family:system-ui;background:#f2f4f8;margin:0}
.layout{display:flex;min-height:100vh}
.sidebar{width:230px;background:#101828;color:#cbd5e1;padding:18px 14px;display:flex;flex-direction:column;position:fixed;top:0;bottom:0;left:0}
.brand{display:flex;align-items:center;gap:10px;padding:6px 8px 20px;border-bottom:1px solid #1e293b;margin-bottom:14px}
.brand .dot{width:36px;height:36px;border-radius:10px;background:#1a7f37;color:#fff;display:flex;align-items:center;justify-content:center;font-weight:800;font-size:17px;flex-shrink:0}
.brand b{color:#fff;font-size:16px;display:block;line-height:1.1}
.brand small{color:#64748b;font-size:11px}
.menu a{display:flex;align-items:center;gap:10px;padding:11px 12px;border-radius:10px;color:#cbd5e1;text-decoration:none;font-size:14px;font-weight:500;margin-bottom:4px}
.menu a:hover{background:#1e293b;color:#fff}
.menu a.on{background:#1a7f37;color:#fff;font-weight:600}
.menu .ico{width:20px;text-align:center;flex-shrink:0}
.sidebar .foot{margin-top:auto;padding:12px 8px 0;border-top:1px solid #1e293b;font-size:12px;color:#64748b}
.main{margin-left:230px;flex:1;min-width:0}
header{background:#fff;border-bottom:1px solid #e4e7ec;padding:14px 28px;display:flex;justify-content:space-between;align-items:center;position:sticky;top:0;z-index:5}
header h1{font-size:18px;margin:0;color:#101828}
header .right{display:flex;align-items:center;gap:14px}
header .who{font-size:13px;color:#667085}
header a.out{color:#b42318;text-decoration:none;font-size:13px;font-weight:600;padding:7px 14px;border:1px solid #fda29b;border-radius:8px}
header a.out:hover{background:#fee4e2}
.content{max-width:980px;margin:24px auto;padding:0 24px}
.card{background:#fff;border-radius:12px;padding:20px;margin-bottom:20px;box-shadow:0 1px 6px rgba(0,0,0,.06)}
h2{margin:0 0 12px;font-size:16px;color:#101828}
table{width:100%;border-collapse:collapse;font-size:14px}
th,td{padding:8px 10px;border-bottom:1px solid #eee;text-align:left}
th{color:#667085;font-weight:600;background:#f9fafb}
code{background:#f2f4f8;padding:2px 6px;border-radius:6px;font-size:13px}
input,select,textarea{padding:8px;border:1px solid #d0d5dd;border-radius:8px;margin:4px 0;font-size:14px}
input:focus,textarea:focus,select:focus{outline:2px solid #1a7f37;border-color:#1a7f37}
button{padding:8px 14px;background:#1a7f37;color:#fff;border:0;border-radius:8px;cursor:pointer;font-weight:600;font-size:13px}
button:hover{background:#166f30}
button.del{background:#b42318}button.del:hover{background:#912018}
button.sec{background:#fff;color:#344054;border:1px solid #d0d5dd}button.sec:hover{background:#f9fafb}
.badge{padding:3px 10px;border-radius:999px;font-size:12px;font-weight:600}
.paid{background:#d4edda;color:#186a3b}.pending{background:#fff3cd;color:#8a6d00}
.expired{background:#f8d7da;color:#8a1c1c}
.pg{color:#1a7f37;text-decoration:none;font-weight:600;margin:0 6px}
.money{font-variant-numeric:tabular-nums;text-align:right}
.flash{background:#d4edda;color:#186a3b;padding:10px 14px;border-radius:8px;margin-bottom:14px}
small{color:#667085}
form.inline{display:inline}
@media(max-width:800px){.sidebar{display:none}.main{margin-left:0}}
</style></head><body>
<div class="layout">
<div class="sidebar">
<div class="brand"><div class="dot">P</div><div><b>Paypan</b><small>payment gateway</small></div></div>
<div class="menu">
<a href="/admin" class="{{if eq .Tab "dash"}}on{{end}}"><span class="ico">▤</span> Dashboard</a>
<a href="/admin/apps" class="{{if eq .Tab "apps"}}on{{end}}"><span class="ico">⧉</span> Aplikasi &amp; Token</a>
<a href="/admin/log" class="{{if eq .Tab "log"}}on{{end}}"><span class="ico">☰</span> Log</a>
<a href="/admin/config" class="{{if eq .Tab "config"}}on{{end}}"><span class="ico">⚙</span> Konfigurasi</a>
</div>
<div class="foot">v1.1 · jhopanstore</div>
</div>
<div class="main">
<header><h1>{{.Title}}</h1>
<div class="right"><span class="who">👤 {{.User}}</span><a class="out" href="/admin/logout">Logout</a></div></header>
<div class="content">
{{if .Flash}}<div class="flash">{{.Flash}}</div>{{end}}
{{.Body}}
</div></div></div></body></html>`))
type pageData struct {
	Title string
	Tab   string
	Flash string
	Body  func() template.HTML
}

func (s *srv) renderPage(w http.ResponseWriter, tab, title, flash string, body func() template.HTML) {
	adminTmpl.Execute(w, map[string]any{
		"Tab": tab, "Title": title, "Flash": flash, "Body": body(),
		"User": s.adminUser(),
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
		b.WriteString(`<div class="card"><h2>Order terakhir</h2><table><tr><th>ID</th><th>Status</th><th>Price</th><th>Total</th><th></th></tr>`)
		for _, o := range orders {
			b.WriteString(`<tr><td><code>` + o.ID + `</code></td><td><span class="badge ` + o.Status + `">` + o.Status + `</span></td><td class="money">` + itoa64(o.Price) + `</td><td class="money">` + itoa64(o.Total) + `</td><td><a href="/admin/tx/` + o.ID + `" style="font-size:13px">detail →</a></td></tr>`)
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
		actor := s.adminUser()
		switch r.FormValue("act") {
		case "qris":
			q := strings.TrimSpace(r.FormValue("qris"))
			if strings.Contains(q, "010211") && strings.Contains(q, "5802ID") && strings.Contains(q, "6304") {
				s.db.Exec("INSERT INTO settings(key,value) VALUES('qris_base',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", q)
				os.WriteFile("qris_base.txt", []byte(q), 0600) // fallback lokal
				// sinkron: regenerate gambar QR dari payload
				if png, err := qrcode.Encode(q, qrcode.Medium, 512); err == nil {
					img := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
					s.db.Exec("INSERT INTO settings(key,value) VALUES('qris_image',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", img)
				}
				s.audit(actor, "qris.update", "payload "+itoa(len(q))+" char (gambar ikut digenerate)")
				flash = "QRIS tersimpan: payload + gambar sinkron"
			} else {
				flash = "Payload tidak dikenali (butuh 010211, 5802ID, 6304)"
			}
		case "qrisimg":
			// upload gambar QRIS: decode QR dari gambar -> payload teks auto-terisi.
			// gambar + payload disimpan bareng = selalu sinkron.
			img := strings.TrimSpace(r.FormValue("img"))
			if !strings.HasPrefix(img, "data:image/png;base64,") && !strings.HasPrefix(img, "data:image/jpeg;base64,") {
				flash = "Format harus PNG/JPG"
				break
			}
			if len(img) > 700_000 {
				flash = "Gambar terlalu besar (max ~500KB)"
				break
			}
			payload, err := decodeQRFromDataURL(img)
			if err != nil || !strings.Contains(payload, "010211") || !strings.Contains(payload, "5802ID") || !strings.Contains(payload, "6304") {
				flash = "QR pada gambar tidak terbaca / bukan QRIS statis. Paste payload manual di kolom sebelah, lalu Simpan Payload."
				break
			}
			s.db.Exec("INSERT INTO settings(key,value) VALUES('qris_image',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", img)
			s.db.Exec("INSERT INTO settings(key,value) VALUES('qris_base',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", payload)
			os.WriteFile("qris_base.txt", []byte(payload), 0600)
			s.audit(actor, "qris.image", "upload gambar + payload "+itoa(len(payload))+" char (sinkron)")
			flash = "QRIS tersimpan: gambar + payload sinkron"
		case "qrisimg_del":
			s.db.Exec("DELETE FROM settings WHERE key='qris_image'")
			flash = "Gambar QRIS dihapus"
		case "pass":
			np := r.FormValue("newpass")
			nu := strings.TrimSpace(r.FormValue("newuser"))
			if nu != "" && nu != actor {
				s.setAdminUser(nu)
				s.audit(actor, "admin.user", "username -> "+nu)
				actor = nu
			}
			if np != "" {
				if len(np) >= 6 {
					s.setAdminPass(np)
					s.audit(actor, "admin.pass", "password diganti")
					flash = "Login admin diperbarui"
				} else {
					flash = "Password minimal 6 karakter"
				}
			} else if nu != "" && nu != actor {
				flash = "Username diganti"
			}
		case "wh_add":
			u := strings.TrimSpace(r.FormValue("url"))
			if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
				s.db.Exec("INSERT INTO webhooks(app_rowid,url,active,created_at) VALUES((SELECT COALESCE(MAX(rowid),0) FROM apps),?,1,strftime('%s','now'))", u)
				s.audit(actor, "webhook.add", u)
				flash = "Webhook ditambahkan"
			} else {
				flash = "URL tidak valid"
			}
		case "wh_del":
			var id int64
			fmt_Sscan(r.FormValue("id"), &id)
			s.db.Exec("DELETE FROM webhooks WHERE rowid=?", id)
			s.audit(actor, "webhook.del", "id "+r.FormValue("id"))
			flash = "Webhook dihapus"
		case "wh_toggle":
			var id int64
			fmt_Sscan(r.FormValue("id"), &id)
			s.db.Exec("UPDATE webhooks SET active=1-active WHERE rowid=?", id)
			flash = "Status webhook diubah"
		case "tg":
			s.db.Exec("INSERT INTO settings(key,value) VALUES('tg_token',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", r.FormValue("tgtoken"))
			s.db.Exec("INSERT INTO settings(key,value) VALUES('tg_chat',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", r.FormValue("tgchat"))
			s.loadTGFromDB()
			s.audit(actor, "telegram.update", "")
			flash = "Telegram tersimpan"
		}
	}
	cur := s.readQrisBase()
	curImg := s.qrisImageBase64()
	var tgToken, tgChat string
	s.db.QueryRow("SELECT value FROM settings WHERE key='tg_token'").Scan(&tgToken)
	s.db.QueryRow("SELECT value FROM settings WHERE key='tg_chat'").Scan(&tgChat)

	s.renderPage(w, "config", "Konfigurasi", flash, func() template.HTML {
		var b strings.Builder
		// QRIS: gambar + payload dalam SATU kartu, simpan bareng = selalu sinkron
		b.WriteString(`<div class="card"><h2>QRIS Statis</h2>
<div style="display:flex;gap:24px;flex-wrap:wrap;margin-top:12px">`)
		b.WriteString(`<div style="flex:0 0 200px;text-align:center">
<label style="font-size:13px;color:#344054;font-weight:600">Upload Gambar</label>`)
		if curImg != "" {
			b.WriteString(`<img src="` + curImg + `" alt="QRIS" style="width:190px;border:1px solid #eee;border-radius:10px">`)
		} else {
			b.WriteString(`<div style="width:190px;height:190px;border:2px dashed #d0d5dd;border-radius:10px;display:flex;align-items:center;justify-content:center;color:#98a2b3;font-size:13px;margin:0 auto">Belum ada gambar</div>`)
		}
		b.WriteString(`<form method="post"><input type="hidden" name="act" value="qrisimg">
<input type="file" id="qrfile" accept="image/png,image/jpeg" style="display:none" onchange="if(this.files[0])upl()">
<div style="margin-top:8px"><button type="button" class="sec" onclick="document.getElementById('qrfile').click()">Pilih Gambar</button> `)
		if curImg != "" {
			b.WriteString(`<button type="button" class="del" onclick="document.getElementById('delimg').click()">Hapus</button>`)
		}
		b.WriteString(`</div><input type="hidden" name="img" id="imgdata"></form>`)
		if curImg != "" {
			b.WriteString(`<form method="post" id="delimg"></form>`)
		}
		// tombol hapus: form terpisah aktif via JS biar gak nested form
		if curImg != "" {
			b.WriteString(`<script>document.addEventListener('DOMContentLoaded',function(){var d=document.getElementById('delimg');if(d){var f=document.createElement('form');f.method='post';f.style.display='none';f.innerHTML='<input type=hidden name=act value=qrisimg_del>';document.body.appendChild(f);document.getElementById('delimg').type='button';document.getElementById('delimg').onclick=function(){f.submit()}}})</script>`)
		}
		b.WriteString(`</div>`)
		b.WriteString(`<div style="flex:1;min-width:260px">
<form method="post"><input type="hidden" name="act" value="qris">
<label style="font-size:13px;color:#344054;font-weight:600">Payload</label>
<textarea id="qrispayload" name="qris" rows="5" style="width:100%;box-sizing:border-box;font-family:monospace;font-size:12px;padding:8px" placeholder="000201010211...">` + cur + `</textarea>
<button>Simpan Payload</button></form></div>
</div>
<script>
function upl(){var f=document.getElementById('qrfile').files[0];if(!f){alert('pilih file');return}
var r=new FileReader();r.onload=function(){document.getElementById('imgdata').value=r.result;document.getElementById('imgdata').form.submit()};r.readAsDataURL(f)}
</script></div>`)
		b.WriteString(`<div class="card"><h2>Webhook</h2>`)
		hooks := s.listWebhooks()
		if len(hooks) > 0 {
			b.WriteString(`<table><tr><th>URL</th><th>Status</th><th>Aksi</th></tr>`)
			for _, hk := range hooks {
				st := "aktif"
				if !hk.Active {
					st = "off"
				}
				b.WriteString(`<tr><td><code>` + hk.URL + `</code></td><td>` + st + `</td>
<td><form method="post" class="inline"><input type="hidden" name="act" value="wh_toggle"><input type="hidden" name="id" value="` + itoa64(hk.ID) + `"><button class="sec">On/Off</button></form>
<form method="post" class="inline"><input type="hidden" name="act" value="wh_del"><input type="hidden" name="id" value="` + itoa64(hk.ID) + `"><button class="del">Hapus</button></form></td></tr>`)
			}
			b.WriteString(`</table>`)
		}
		b.WriteString(`<form method="post"><input type="hidden" name="act" value="wh_add">
<input name="url" placeholder="https://website-loke/api/webhook" style="width:70%">
<button>Tambah Webhook</button></form>
<small>POST JSON <code>{event:"order.paid", order:{id,price,code,total,paid_at}}</code> + header <code>X-Paypan-Event</code>. Retry 2x jika gagal.</small></div>`)
		b.WriteString(`<div class="card"><h2>Notifikasi Telegram (opsional)</h2>
<form method="post"><input type="hidden" name="act" value="tg">
<input name="tgtoken" placeholder="Bot token" value="` + tgToken + `" style="width:100%">
<input name="tgchat" placeholder="Chat ID tujuan" value="` + tgChat + `" style="width:100%">
<button>Simpan Telegram</button></form></div>`)
		b.WriteString(`<div class="card"><h2>Login admin</h2>
<form method="post"><input type="hidden" name="act" value="pass">
<label style="font-size:13px;color:#344054">Username</label>
<input name="newuser" placeholder="username baru (opsional)" value="` + s.adminUser() + `" style="width:100%">
<label style="font-size:13px;color:#344054">Password baru</label>
<input type="password" name="newpass" placeholder="kosongkan jika tidak diubah" style="width:100%">
<button>Simpan</button></form></div>`)
		return template.HTML(b.String())
	})
}
