package main

import (
	"os"
	"strconv"
	"sync"
)

// TuningCfg: angka operasional yang bisa di-tuning via env tanpa build ulang.
// Env dibaca SEKALI saat startup. Ubah env → restart service.
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
