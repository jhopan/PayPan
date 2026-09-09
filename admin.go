package main

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---------- apps (token per aplikasi) ----------

// appsTable: name, token, scopes ('notif','order','both'), created_at, active
func ensureAdminDefaults(db *sql.DB) {
	// token master awal: "paypan-secret-2026" — ganti via web setelah login pertama
	db.Exec(`INSERT OR IGNORE INTO apps(name, token, scopes, active, created_at)
	         SELECT 'master', 'paypan-secret-2026', 'both', 1, strftime('%s','now')
	         WHERE NOT EXISTS(SELECT 1 FROM apps)`)
}

type appRow struct {
	ID      int64
	Name    string
	Token   string
	Scopes  string
	Active  bool
	Created int64
}

func listApps(db *sql.DB) []appRow {
	rows, err := db.Query("SELECT rowid,name,token,scopes,active,created_at FROM apps ORDER BY rowid")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []appRow
	for rows.Next() {
		var a appRow
		if rows.Scan(&a.ID, &a.Name, &a.Token, &a.Scopes, &a.Active, &a.Created) == nil {
			out = append(out, a)
		}
	}
	return out
}

func genToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	const hx = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = hx[int(v)%len(hx)]
	}
	return "pp_" + string(out)
}

// authScope: token valid + scope cocok + rate limit per token
func (s *srv) authScope(r *http.Request, scope string) (string, bool) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok == "" {
		return "", false
	}
	if !apiLimiter.allow(tok) {
		return "", false
	}
	var scopes string
	var active bool
	err := s.db.QueryRow("SELECT scopes,active FROM apps WHERE token=?", tok).Scan(&scopes, &active)
	if err != nil || !active {
		return "", false
	}
	if scopes != "both" && scopes != scope {
		return "", false
	}
	var name string
	s.db.QueryRow("SELECT name FROM apps WHERE token=?", tok).Scan(&name)
	return name, true
}

// ---------- admin session ----------

type sessionStore struct {
	mu sync.Mutex
	m  map[string]time.Time // token -> expiry
}

var sessions = &sessionStore{m: map[string]time.Time{}}

const sessionTTL = 12 * time.Hour

func (ss *sessionStore) newSession() string {
	tok := genToken() + genToken()
	ss.mu.Lock()
	defer ss.mu.Unlock()
	// buang expired
	now := time.Now()
	for k, v := range ss.m {
		if now.After(v) {
			delete(ss.m, k)
		}
	}
	ss.m[tok] = now.Add(sessionTTL)
	return tok
}

func (ss *sessionStore) valid(tok string) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	exp, ok := ss.m[tok]
	return ok && time.Now().Before(exp)
}

func (ss *sessionStore) drop(tok string) {
	ss.mu.Lock()
	delete(ss.m, tok)
	ss.mu.Unlock()
}

// adminPass baca password admin dari settings (default dibuat saat init)
func (s *srv) adminPassCheck(input string) bool {
	// constant-time compare: cegah timing side-channel
	return subtle.ConstantTimeCompare([]byte(input), []byte(s.adminPass())) == 1
}
func (s *srv) adminUser() string {
	var p string
	s.db.QueryRow("SELECT value FROM settings WHERE key='admin_user'").Scan(&p)
	if p == "" {
		p = "admin"
		s.db.Exec("INSERT OR IGNORE INTO settings(key,value) VALUES('admin_user','admin')")
	}
	return p
}

func (s *srv) setAdminUser(u string) {
	s.db.Exec("INSERT INTO settings(key,value) VALUES('admin_user',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", u)
}

func (s *srv) adminPass() string {
	var p string
	s.db.QueryRow("SELECT value FROM settings WHERE key='admin_pass'").Scan(&p)
	if p == "" {
		p = "admin123" // default; WAJIB diganti via web
		s.db.Exec("INSERT OR IGNORE INTO settings(key,value) VALUES('admin_pass','admin123')")
	}
	return p
}

func (s *srv) setAdminPass(p string) {
	s.db.Exec("INSERT INTO settings(key,value) VALUES('admin_pass',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", p)
}

// middleware: cookie paypan_session wajib valid
func (s *srv) requireSession(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie("paypan_session")
	if err != nil || !sessions.valid(c.Value) {
		http.Redirect(w, r, "/admin/login", 302)
		return false
	}
	return true
}

