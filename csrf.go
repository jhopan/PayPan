package main

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- CSRF: double-submit cookie ----------
// Token disimpan di cookie (HttpOnly=false, dibaca JS) + hidden field di form.
// Server membandingkan keduanya dengan constant-time compare.
// Kenapa aman: situs lain tidak bisa membaca cookie kita (same-origin policy),
// jadi tidak bisa menebak nilai yang harus dimasukkan ke field.

const csrfCookie = "paypan_csrf"
const csrfField = "csrf_token"
const csrfTTL = 12 * time.Hour

type csrfStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

var csrfTokens = &csrfStore{seen: map[string]time.Time{}}

func newCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// fallback: tidak boleh terjadi; token tetap unik per waktu
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// ensureCSRF: pastikan cookie ada, kembalikan nilainya (untuk dirender di form).
func (s *srv) ensureCSRF(w http.ResponseWriter, r *http.Request) string {
	if r != nil {
		if c, err := r.Cookie(csrfCookie); err == nil && len(c.Value) >= 20 {
			return c.Value
		}
	}
	tok := newCSRFToken()
	// Jangan set cookie baru pada response POST: browser sudah mengirim form
	// dengan token lama, dan Set-Cookie di POST akan menimpa token itu
	// sehingga submit berikutnya gagal (403 acak).
	if r == nil || r.Method != http.MethodPost {
		http.SetCookie(w, &http.Cookie{
			Name:     csrfCookie,
			Value:    tok,
			Path:     "/",
			HttpOnly: false, // harus bisa dibaca JS untuk tidak perlu render ulang
			SameSite: http.SameSiteStrictMode,
			MaxAge:   int(csrfTTL.Seconds()),
		})
	}
	csrfTokens.mu.Lock()
	csrfTokens.seen[tok] = time.Now().Add(csrfTTL)
	csrfTokens.mu.Unlock()
	return tok
}

// checkCSRF: validasi token form == token cookie.
func (s *srv) checkCSRF(r *http.Request) bool {
	c, err := r.Cookie(csrfCookie)
	if err != nil || len(c.Value) < 20 {
		return false
	}
	form := r.FormValue(csrfField)
	if form == "" {
		form = r.Header.Get("X-CSRF-Token") // untuk fetch/JSON
	}
	if form == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(form), []byte(c.Value)) == 1
}

// csrfJanitor: buang token kedaluwarsa (dipanggil apiJanitorWorker).
func csrfJanitor() {
	now := time.Now()
	csrfTokens.mu.Lock()
	for t, exp := range csrfTokens.seen {
		if now.After(exp) {
			delete(csrfTokens.seen, t)
		}
	}
	csrfTokens.mu.Unlock()
}

// ---------- password hashing (PBKDF2-HMAC-SHA256) ----------

const pbkdf2Iter = 210000 // OWASP 2023 untuk SHA-256
const pbkdf2KeyLen = 32
const pbkdf2SaltLen = 16

// hashPassword: hasil "pbkdf2$<iter>$<salt-b64>$<hash-b64>".
func hashPassword(pw string) string {
	salt := make([]byte, pbkdf2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return ""
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iter, pbkdf2KeyLen)
	if err != nil {
		return ""
	}
	return "pbkdf2$" + itoa(pbkdf2Iter) + "$" +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(key)
}

// verifyPassword: cocokkan password dengan hash tersimpan (constant-time).
func verifyPassword(pw, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}

// isHashedPassword: cek apakah nilai tersimpan sudah hash (bukan plaintext lama).
func isHashedPassword(v string) bool {
	return strings.HasPrefix(v, "pbkdf2$") && strings.Count(v, "$") == 3
}

// ---------- body size limit ----------

// maxBodyBytes: batas aman untuk API JSON (notif/invoice).
const maxBodyBytes = 64 << 10 // 64 KB

// limitBody: potong body request supaya tidak bisa menghabiskan RAM.
func limitBody(w http.ResponseWriter, r *http.Request, n int64) {
	r.Body = http.MaxBytesReader(w, r.Body, n)
}
