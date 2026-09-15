package main

// ---------- Backup Google Sheets (Opsi A) ----------
//
// Konsep:
//   1 Spreadsheet Google per bulan, dinamai "PayPan Backup YYYY-MM".
//   Di dalamnya, sheet per minggu: "Minggu 1 (01-07)", "Minggu 2 (08-14)", dst.
//   Tiap sheet berisi transaksi (orders) minggu tsb + rekap di sheet "Ringkasan".
//   Bulan ganti → spreadsheet baru dibikin otomatis oleh Apps Script.
//
// Alur:
//   paypan (worker harian + tombol manual admin)
//     → POST JSON ke Apps Script Web App URL (dgn secret sederhana)
//     → Apps Script: cari/bikin spreadsheet bulan tsb, cari/bikin sheet minggu tsb,
//       tulis (upsert by invoice id).
//
// Konfigurasi (settings):
//   ss_url    = URL Web App Apps Script
//   ss_secret = secret bersama (dicek Apps Script sebelum menulis)
//   Kosong = fitur off.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ssOrder struct {
	ID        string `json:"id"`
	Tanggal   string `json:"tanggal"`   // created_at formatted
	Jam       string `json:"jam"`
	Harga     int64  `json:"harga"`
	Kode      string `json:"kode"`  // "007"
	Total     int64  `json:"total"`
	Status    string `json:"status"`
	Bayar     string `json:"bayar"` // paid_at formatted (kosong jika belum)
	Source    string `json:"source"`
	ExpiredAt string `json:"expired_at"`
}

type ssPayload struct {
	Secret string    `json:"secret"`
	Month  string    `json:"month"`  // "2026-09"
	Orders []ssOrder `json:"orders"`
}

func (s *srv) ssSettings() (url, secret string) {
	s.db.QueryRow("SELECT value FROM settings WHERE key='ss_url'").Scan(&url)
	s.db.QueryRow("SELECT value FROM settings WHERE key='ss_secret'").Scan(&secret)
	return strings.TrimSpace(url), strings.TrimSpace(secret)
}

// collectOrders: ambil orders created_at dalam rentang [from, to) unix.
func (s *srv) collectOrders(from, to int64) []ssOrder {
	rows, err := s.db.Query(`SELECT o.id, o.price, o.code, o.total, o.status,
		COALESCE(o.paid_at,0), o.created_at, o.expires_at,
		(SELECT p.source FROM payments p WHERE p.amount=o.total ORDER BY p.received_at DESC LIMIT 1)
		FROM orders o WHERE o.created_at >= ? AND o.created_at < ? ORDER BY o.created_at`, from, to)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ssOrder
	for rows.Next() {
		var o ssOrder
		var id string
		var price, code, total int64
		var status string
		var paidAt, createdAt, expiresAt sql.NullInt64
		var source sql.NullString
		if err := rows.Scan(&id, &price, &code, &total, &status, &paidAt, &createdAt, &expiresAt, &source); err != nil {
			continue
		}
		loc := time.Local
		created := time.Unix(createdAt.Int64, 0).In(loc)
		o.ID = id
		o.Tanggal = created.Format("02 Jan 2006")
		o.Jam = created.Format("15:04")
		o.Harga = price
		o.Kode = fmt.Sprintf("%03d", code)
		o.Total = total
		o.Status = status
		if paidAt.Valid && paidAt.Int64 > 0 {
			o.Bayar = time.Unix(paidAt.Int64, 0).In(loc).Format("02 Jan 15:04")
		}
		o.Source = source.String
		if expiresAt.Valid && expiresAt.Int64 > 0 {
			o.ExpiredAt = time.Unix(expiresAt.Int64, 0).In(loc).Format("02 Jan 15:04")
		}
		out = append(out, o)
	}
	return out
}

// weekRange: kembalikan (label, startUnix, endUnix) utk minggu ke-n dalam bulan tsb.
// Minggu 1 = tanggal 1-7, Minggu 2 = 8-14, dst. Bulan bisa punya minggu ke-5 (29-31).
func weekRange(year, month int, week int, loc *time.Location) (label string, from, to time.Time) {
	first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
	startDay := (week-1)*7 + 1
	from = time.Date(year, time.Month(month), startDay, 0, 0, 0, 0, loc)
	to = from.AddDate(0, 0, 7)
	if to.After(first.AddDate(0, 1, 0)) {
		to = first.AddDate(0, 1, 0) // jangan melewati akhir bulan
	}
	return fmt.Sprintf("Minggu %d (%02d-%02d)", week, startDay, to.AddDate(0, 0, -1).Day()), from, to
}

// pushWeeks: kirim semua minggu yang sudah lewat (atau sedang berjalan) bulan tsb.
func (s *srv) pushWeeks(url, secret string, year, month int) (string, error) {
	if url == "" || secret == "" {
		return "", fmt.Errorf("backup sheets belum dikonfigurasi (ss_url / ss_secret kosong)")
	}
	loc := time.Local
	now := time.Now().In(loc)
	// minggu terakhir dalam bulan (1..5)
	lastWeek := 5
	for w := 5; w >= 1; w-- {
		_, from, _ := weekRange(year, month, w, loc)
		first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
		if from.Before(first.AddDate(0, 1, 0)) {
			lastWeek = w
			break
		}
	}
	payload := ssPayload{Secret: secret, Month: fmt.Sprintf("%04d-%02d", year, month)}
	sent := 0
	for w := 1; w <= lastWeek; w++ {
		_, from, to := weekRange(year, month, w, loc)
		// hanya kirim minggu yang sudah mulai (yang belum tiba, skip)
		if from.After(now.AddDate(0, 0, 1)) {
			continue
		}
		payload.Orders = s.collectOrders(from.Unix(), to.Unix())
		if err := s.ssPost(url, payload); err != nil {
			return fmt.Sprintf("minggu %d: %v", w, err), err
		}
		sent++
	}
	_ = lastWeek
	return fmt.Sprintf("%d minggu terkirim bulan %04d-%02d", sent, year, month), nil
}

// ssPost: kirim payload JSON ke Apps Script, return error jika status bukan 200.
func (s *srv) ssPost(url string, payload any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// sheetWorker: backup otomatis harian jam 23:55 WIB-ish (pakai time.Local debian).
// Gagal → dicoba lagi 10 menit berikutnya (loop cek apakah backup hari ini sudah sukses).
func (s *srv) sheetWorker() {
	for {
		now := time.Now()
		// next 23:55
		next := time.Date(now.Year(), now.Month(), now.Day(), 23, 55, 0, 0, now.Location())
		if next.Before(now) {
			next = next.AddDate(0, 0, 1)
		}
		time.Sleep(next.Sub(now))
		url, secret := s.ssSettings()
		if url == "" || secret == "" {
			continue // fitur off — cek lagi besok
		}
		n := now.AddDate(0, 0, -1) // backup data HARI SEBELUMNYA yang sudah lengkap
		if _, err := s.pushWeeks(url, secret, n.Year(), int(n.Month())); err != nil {
			s.audit("system", "sheets.gagal", err.Error())
			// retry 10 menit sampai berhasil atau hari berganti
			for i := 0; i < 6; i++ {
				time.Sleep(10 * time.Minute)
				if _, err := s.pushWeeks(url, secret, n.Year(), int(n.Month())); err == nil {
					break
				}
			}
		} else {
			s.audit("system", "sheets.backup", fmt.Sprintf("backup %04d-%02d terkirim", n.Year(), int(n.Month())))
		}
	}
}

// ssManual: handler tombol "Backup ke Sheets sekarang" di admin (backup bulan berjalan).
func (s *srv) ssManual() (string, error) {
	url, secret := s.ssSettings()
	if url == "" || secret == "" {
		return "", fmt.Errorf("isi ss_url dan ss_secret dulu di Konfigurasi")
	}
	n := time.Now()
	return s.pushWeeks(url, secret, n.Year(), int(n.Month()))
}
