package main

import (
	"crypto/sha256"
)

// ---------- QRIS hash pinning (anti-tamper) ----------
//
// Ancaman: penyerang dengan akses file system VPS mengedit qris_base.txt
// dan mengganti payload ke rekening dia. Semua QR baru mengarah ke dia,
// sistem tetap "normal", dana mengalir ke penyerang sampai customer komplain.
//
// Mitigasi: hash payload QRIS disimpan di settings 'qris_hash' (terpisah dari
// file). Setiap build QR memverifikasi hash. Kalau file diubah manual ->
// hash tidak cocok -> server MENOLAK membuat invoice + kirim alarm Telegram.
//
// Perubahan via web admin tetap sah karena menulis hash baru sekaligus.

// qrisHash: simpan hash saat QRIS disimpan via web (sumber kebenaran).
func (s *srv) pinQrisHash(payload string) {
	h := sha256.Sum256([]byte(payload))
	s.db.Exec("INSERT INTO settings(key,value) VALUES('qris_hash',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		hashHex(h))
}

func hashHex(h [32]byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 64)
	for i, b := range h {
		out[i*2] = hexdigits[b>>4]
		out[i*2+1] = hexdigits[b&0x0f]
	}
	return string(out)
}

// verifyQrisIntegrity: true = payload cocok dengan hash terpin.
// Juga mengembalikan payload saat ini (dari DB, fallback file).
func (s *srv) verifyQrisIntegrity() (payload string, ok bool) {
	payload = s.readQrisBase()
	if payload == "" {
		return "", false
	}
	var stored string
	s.db.QueryRow("SELECT value FROM settings WHERE key='qris_hash'").Scan(&stored)
	if stored == "" {
		// belum pernah di-pin (QRIS diset sebelum fitur ini) — pin sekarang
		s.pinQrisHash(payload)
		return payload, true
	}
	h := sha256.Sum256([]byte(payload))
	return payload, hashHex(h) == stored
}

// alertQrisTamper: kirim alarm Telegram sekali per deteksi (jangan spam).
func (s *srv) alertQrisTamper() {
	var alerted int
	s.db.QueryRow("SELECT COUNT(*) FROM settings WHERE key='qris_tamper_alerted' AND value='1'").Scan(&alerted)
	if alerted > 0 {
		return
	}
	s.notifyTGResult("PERINGATAN: qris_base.txt berubah TANPA melalui admin! "+
		"Pembuatan invoice DINONAKTIFKAN. Cek server segera. "+
		"Buka admin -> Konfigurasi -> Simpan QRIS untuk mengaktifkan kembali.", "")
	s.db.Exec("INSERT INTO settings(key,value) VALUES('qris_tamper_alerted','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value")
}

func (s *srv) clearQrisTamperFlag() {
	s.db.Exec("DELETE FROM settings WHERE key='qris_tamper_alerted'")
}
