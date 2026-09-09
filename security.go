package main

import (
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------- rate limit login (per IP) ----------

type loginLimit struct {
	mu      sync.Mutex
	attempt map[string][]time.Time
}

var limiter = &loginLimit{attempt: map[string][]time.Time{}}

const (
	llWindow = 10 * time.Minute
	llMax    = 5
)

// allow: true kalau IP belum melewati batas
func (l *loginLimit) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	keep := l.attempt[ip][:0]
	for _, t := range l.attempt[ip] {
		if now.Sub(t) < llWindow {
			keep = append(keep, t)
		}
	}
	l.attempt[ip] = keep
	return len(keep) < llMax
}

func (l *loginLimit) hit(ip string) {
	l.mu.Lock()
	l.attempt[ip] = append(l.attempt[ip], time.Now())
	l.mu.Unlock()
}

func (l *loginLimit) reset(ip string) {
	l.mu.Lock()
	delete(l.attempt, ip)
	l.mu.Unlock()
}

func clientIP(r *http.Request) string {
	// CF tunnel: X-Forwarded-For ; direct: RemoteAddr
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}

// ---------- rate limit API (per token, brute-force guard) ----------

type apiLimit struct {
	mu    sync.Mutex
	hits  map[string]*tokenHits
}

type tokenHits struct {
	times []time.Time
	last  time.Time // untuk janitor eviction
}

var apiLimiter = &apiLimit{hits: map[string]*tokenHits{}}

const (
	apiWindow = time.Minute
	apiMax    = 60 // 60 req/menit per token — jauh di atas kebutuhan normal
	// janitor: buang token yang tidak aktif, cegah map tumbuh tak terkendali
	apiJanitorEvery = 10 * time.Minute
	apiIdleEvict    = 30 * time.Minute
)

func (l *apiLimit) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	th, exists := l.hits[key]
	if !exists {
		th = &tokenHits{}
		l.hits[key] = th
	}
	keep := th.times[:0]
	for _, t := range th.times {
		if now.Sub(t) < apiWindow {
			keep = append(keep, t)
		}
	}
	th.times = keep
	th.last = now
	if len(th.times) >= apiMax {
		return false
	}
	th.times = append(th.times, now)
	return true
}

// apiJanitorWorker: buang entri rate limit yang idle > apiIdleEvict.
// Jalankan sebagai goroutine saat startup.
func apiJanitorWorker() {
	for range time.Tick(apiJanitorEvery) {
		now := time.Now()
		apiLimiter.mu.Lock()
		for k, th := range apiLimiter.hits {
			if now.Sub(th.last) > apiIdleEvict {
				delete(apiLimiter.hits, k)
			}
		}
		apiLimiter.mu.Unlock()
	}
}

// ---------- backup DB otomatis ----------

// backupWorker: copy paypan.db tiap 6 jam ke folder backup/ (maks 28 file).
func (s *srv) backupWorker(dbPath string) {
	for range time.Tick(6 * time.Hour) {
		_ = os.MkdirAll("backup", 0755)
		name := "backup/paypan-" + time.Now().Format("2006-01-02-1504") + ".db"
		// WAL checkpoint dulu biar file konsisten
		s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		src, err := os.ReadFile(dbPath)
		if err != nil {
			continue
		}
		os.WriteFile(name, src, 0600)
		// buang backup lama, sisakan 28
		files, _ := os.ReadDir("backup")
		var names []string
		for _, f := range files {
			if strings.HasPrefix(f.Name(), "paypan-") {
				names = append(names, f.Name())
			}
		}
		if len(names) > 28 {
			sort.Strings(names)
			for _, f := range names[:len(names)-28] {
				os.Remove("backup/" + f)
			}
		}
	}
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; script-src 'self' 'unsafe-inline'")
		next.ServeHTTP(w, r)
	})
}

// ---------- audit log ----------

func (s *srv) audit(actor, action, detail string) {
	s.db.Exec("INSERT INTO audit(at,actor,action,detail) VALUES(strftime('%s','now'),?,?,?)",
		actor, action, detail)
}

func (s *srv) recentAudit(n int) []map[string]string {
	rows, err := s.db.Query("SELECT at,actor,action,IFNULL(detail,'') FROM audit ORDER BY id DESC LIMIT ?", n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []map[string]string
	for rows.Next() {
		var at int64
		var actor, action, detail string
		rows.Scan(&at, &actor, &action, &detail)
		out = append(out, map[string]string{
			"at":     time.Unix(at, 0).Format("02 Jan 15:04"),
			"actor":  actor,
			"action": action,
			"detail": detail,
		})
	}
	return out
}
