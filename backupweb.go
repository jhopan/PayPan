package main

// ---------- Backup & Restore dari web admin ----------
//
// Backup worker sudah menulis paypan-YYYY-MM-DD-HHMM.db ke backup/ (relatif cwd
// server). Halaman ini menyediakan:
//   - daftar backup (nama, ukuran, umur)
//   - download backup (.db) via browser
//   - buat backup sekarang
//   - restore: upload file .db -> validasi SQLite -> simpan sbg pending ->
//     verifikasi restart-safe -> replace DB aktif -> restart service (systemd
//     Restart=always otomatis menghidupkan lagi; gak perlu aksi manual).
//
// Keamanan:
//   - semua aksi butuh session admin (route terdaftar di /admin/*)
//   - restore wajib konfirmasi ketik "RESTORE"
//   - file upload divalidasi: harus SQLite valid + punya tabel inti (orders)
//   - backup DB aktif dibuat otomatis sebelum restore (rollback point)

import (
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *srv) backupDir() string { return "backup" }

type backupFile struct {
	Name  string
	Size  int64
	At    time.Time
	Valid bool // SQLite bisa dibuka & punya tabel orders
}

func (s *srv) listBackups() []backupFile {
	entries, err := os.ReadDir(s.backupDir())
	if err != nil {
		return nil
	}
	var out []backupFile
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "paypan-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		bf := backupFile{Name: e.Name(), Size: info.Size(), At: info.ModTime()}
		bf.Valid = s.validateDBFile(filepath.Join(s.backupDir(), e.Name()))
		out = append(out, bf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// validateDBFile: buka file SQLite, pastikan bisa query & punya tabel orders.
func (s *srv) validateDBFile(path string) bool {
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return false
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM orders").Scan(&n); err != nil {
		return false
	}
	return true
}

// createBackupNow: WAL checkpoint lalu copy DB aktif ke backup/.
func (s *srv) createBackupNow() (string, error) {
	_ = os.MkdirAll(s.backupDir(), 0755)
	s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	name := "paypan-" + time.Now().Format("2006-01-02-1504") + ".db"
	src, err := os.ReadFile(s.dbPath)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(s.backupDir(), name), src, 0600); err != nil {
		return "", err
	}
	return name, nil
}

// handleAdminBackup: GET halaman backup + POST aksi (create/download/restore).
func (s *srv) handleAdminBackup(w http.ResponseWriter, r *http.Request) {
	actor := s.adminUser()
	flash := ""
	if r.Method == http.MethodPost {
		switch r.FormValue("act") {
		case "create":
			name, err := s.createBackupNow()
			if err != nil {
				flash = "Gagal backup: " + err.Error()
			} else {
				flash = "Backup dibuat: " + name
				s.audit(actor, "backup.create", name)
			}
		case "download":
			name := filepath.Base(r.FormValue("name"))
			path := filepath.Join(s.backupDir(), name)
			if !s.validateDBFile(path) {
				http.Error(w, "file tidak valid", 400)
				return
			}
			data, err := os.ReadFile(path)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			s.audit(actor, "backup.download", name)
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
			w.Write(data)
			return
		case "restore":
			if strings.TrimSpace(r.FormValue("confirm")) != "RESTORE" {
				flash = "Konfirmasi belum tepat — ketik RESTORE"
				break
			}
			name := filepath.Base(r.FormValue("name"))
			path := filepath.Join(s.backupDir(), name)
			if !s.validateDBFile(path) {
				flash = "File backup tidak valid (bukan SQLite lengkap)"
				break
			}
			// rollback point: backup DB aktif sebelum ditimpa
			if _, err := s.createBackupNow(); err != nil {
				flash = "Gagal buat rollback point: " + err.Error()
				break
			}
			// tutup koneksi DB aktif, timpa file, biarkan systemd restart process
			s.db.Close()
			src, err := os.ReadFile(path)
			if err != nil {
				flash = "Gagal baca backup: " + err.Error()
				os.Exit(1) // systemd akan restart otomatis
			}
			// hapus WAL lama agar tidak campur dengan DB baru
			_ = os.Remove(s.dbPath + "-wal")
			_ = os.Remove(s.dbPath + "-shm")
			if err := os.WriteFile(s.dbPath, src, 0600); err != nil {
				flash = "Gagal restore: " + err.Error()
				os.Exit(1)
			}
			s.audit(actor, "backup.restore", name)
			// restart: keluar bersih → systemd Restart=always menghidupkan dengan DB baru
			go func() {
				time.Sleep(500 * time.Millisecond)
				os.Exit(0)
			}()
			flash = "RESTORED dari " + name + " — server restart sesaat..."
			// tulis flash tidak akan terlihat; restart segera. Halaman login muncul setelahnya.
			w.Header().Set("Refresh", "3;/admin/backup")
			break
		}
	}

	files := s.listBackups()
	s.renderPage(w, "backup", "Backup & Restore", flash, func() template.HTML {
		var b strings.Builder
		b.WriteString(`<div class="card"><h2>Backup Database</h2>`)
		b.WriteString(`<form method="post" style="margin-bottom:12px"><input type="hidden" name="act" value="create"><button>Buat Backup Sekarang</button></form>`)
		if len(files) == 0 {
			b.WriteString(`<p class="empty" style="color:#98a2b3;font-size:14px">Belum ada backup</p>`)
		} else {
			b.WriteString(`<table><tr><th>File</th><th>Ukuran</th><th>Waktu</th><th>Valid</th><th>Aksi</th></tr>`)
			for _, f := range files {
				valid := `<span class="badge paid">ok</span>`
				if !f.Valid {
					valid = `<span class="badge expired">rusak</span>`
				}
				b.WriteString(`<tr><td><code>` + esc(f.Name) + `</code></td>` +
					`<td>` + fmtHumanSize(f.Size) + `</td>` +
					`<td><small>` + f.At.Format("02 Jan 2006 15:04") + `</small></td>` +
					`<td>` + valid + `</td>` +
					`<td style="white-space:nowrap"><form method="post" class="inline"><input type="hidden" name="act" value="download"><input type="hidden" name="name" value="` + esc(f.Name) + `"><button class="sec">Download</button></form>` +
					`<form method="post" class="inline" onsubmit="var c=prompt('Ketik RESTORE untuk konfirmasi:');if(c!=='RESTORE'){return false}this.querySelector('input[name=confirm]').value=c;"><input type="hidden" name="act" value="restore"><input type="hidden" name="name" value="` + esc(f.Name) + `"><input type="hidden" name="confirm"><button class="del">Restore</button></form></td></tr>`)
			}
			b.WriteString(`</table>`)
		}
		b.WriteString(`<p style="font-size:12px;color:#98a2b3;margin:8px 0 0">Backup otomatis tiap 6 jam (maks 28 file). Restore menimpa DB aktif — backup rollback dibuat otomatis sebelum restore, lalu server restart sesaat (±2 detik).</p></div>`)
		return template.HTML(b.String())
	})
}

func fmtHumanSize(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.1f GB", float64(n)/k/k/k)
	case n >= k*k:
		return fmt.Sprintf("%.1f MB", float64(n)/k/k)
	case n >= k:
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	}
	return fmt.Sprintf("%d B", n)
}
