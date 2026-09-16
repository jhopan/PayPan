package main

import (
	"database/sql"
	"os"
	"strconv"
	"sync"
)

// TuningCfg: angka operasional. Urutan prioritas:
// web admin (settings DB) > env > default bawaan.
// DB hanya diisi kalau admin menyimpan dari web — sampai saat itu env berlaku.
type TuningCfg struct {
	mu             sync.RWMutex
	InvoiceExpiry  int64 // detik window bayar invoice (default 300 = 5 menit)
	CodeCooldown   int64 // detik cooldown kode expired/refunded (default 7200 = 2 jam)
	PendingLimit   int64 // maks invoice pending per token (default 50)
	APIMaxPerMin   int64 // rate limit API req/menit/token (default 60)
	LoginMaxPerWin int64 // rate limit login per window IP (default 5)
}

var tuning = &TuningCfg{
	InvoiceExpiry:  300,
	CodeCooldown:   7200,
	PendingLimit:   50,
	APIMaxPerMin:   60,
	LoginMaxPerWin: 5,
}

// LoadTuningFromEnv: baca semua PAYPAN_* numerik sekali di main().
func LoadTuningFromEnv() {
	tuning.mu.Lock()
	defer tuning.mu.Unlock()
	tuning.InvoiceExpiry = envInt("PAYPAN_INVOICE_EXPIRY", tuning.InvoiceExpiry)
	tuning.CodeCooldown = envInt("PAYPAN_CODE_COOLDOWN", tuning.CodeCooldown)
	tuning.PendingLimit = envInt("PAYPAN_PENDING_LIMIT", tuning.PendingLimit)
	tuning.APIMaxPerMin = envInt("PAYPAN_API_MAX_PER_MIN", tuning.APIMaxPerMin)
	tuning.LoginMaxPerWin = envInt("PAYPAN_LOGIN_MAX", tuning.LoginMaxPerWin)
}

// LoadTuningFromDB: timpa nilai dari settings DB kalau ada (dipanggil setelah initDB).
func LoadTuningFromDB(db *sql.DB) {
	for key, setter := range map[string]func(int64){
		"tuning_invoice_expiry": func(v int64) { tuning.InvoiceExpiry = v },
		"tuning_code_cooldown":  func(v int64) { tuning.CodeCooldown = v },
		"tuning_pending_limit":  func(v int64) { tuning.PendingLimit = v },
		"tuning_api_max":        func(v int64) { tuning.APIMaxPerMin = v },
		"tuning_login_max":      func(v int64) { tuning.LoginMaxPerWin = v },
	} {
		var sv string
		if err := db.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&sv); err == nil {
			if n, e := strconv.ParseInt(sv, 10, 64); e == nil && n > 0 {
				setter(n)
			}
		}
	}
}

// SetTuning: simpan satu nilai ke DB + memori (dari web admin).
func SetTuning(db *sql.DB, key string, val int64) {
	tuning.mu.Lock()
	defer tuning.mu.Unlock()
	switch key {
	case "tuning_invoice_expiry":
		tuning.InvoiceExpiry = val
	case "tuning_code_cooldown":
		tuning.CodeCooldown = val
	case "tuning_pending_limit":
		tuning.PendingLimit = val
	case "tuning_api_max":
		tuning.APIMaxPerMin = val
	case "tuning_login_max":
		tuning.LoginMaxPerWin = val
	}
	db.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, strconv.FormatInt(val, 10))
}

// Getters (thread-safe).
func (t *TuningCfg) InvoiceExpirySec() int64 { t.mu.RLock(); defer t.mu.RUnlock(); return t.InvoiceExpiry }
func (t *TuningCfg) CodeCooldownSec() int64  { t.mu.RLock(); defer t.mu.RUnlock(); return t.CodeCooldown }
func (t *TuningCfg) PendingMax() int64       { t.mu.RLock(); defer t.mu.RUnlock(); return t.PendingLimit }
func (t *TuningCfg) APIMax() int64           { t.mu.RLock(); defer t.mu.RUnlock(); return t.APIMaxPerMin }
func (t *TuningCfg) LoginMax() int64         { t.mu.RLock(); defer t.mu.RUnlock(); return t.LoginMaxPerWin }

// envInt: parse int64 positif, fallback default kalau kosong/salah.
func envInt(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
