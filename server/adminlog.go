package main

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func timeFmt(ts int64) string {
	if ts <= 0 {
		return "—"
	}
	return time.Unix(ts, 0).Format("02 Jan 2006 15:04")
}

// GET /admin/log — audit log penuh, tabel terpisah (dipindah dari Konfigurasi).
func (s *srv) handleAdminLog(w http.ResponseWriter, r *http.Request) {
	if !s.requireSession(w, r) {
		return
	}
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("p")); err == nil && p > 0 {
		page = p
	}
	const perPage = 25
	total := 0
	s.db.QueryRow("SELECT COUNT(*) FROM audit").Scan(&total)
	maxPage := (total + perPage - 1) / perPage
	if maxPage < 1 {
		maxPage = 1
	}
	if page > maxPage {
		page = maxPage
	}
	rows, err := s.db.Query(
		"SELECT at,actor,action,IFNULL(detail,'') FROM audit ORDER BY id DESC LIMIT ? OFFSET ?",
		perPage, (page-1)*perPage)
	logs := []map[string]string{}
	if err == nil {
		for rows.Next() {
			var at int64
			var actor, action, detail string
			rows.Scan(&at, &actor, &action, &detail)
			logs = append(logs, map[string]string{
				"at":     timeFmt(at),
				"actor":  actor,
				"action": action,
				"detail": detail,
			})
		}
		rows.Close()
	}

	s.renderPage(w, "log", "Log", r.URL.Query().Get("m"), func() template.HTML {
		var sb strings.Builder
		sb.WriteString(`<div class="card"><h2>Audit Log</h2>
<table><tr><th>Waktu</th><th>Oleh</th><th>Aksi</th><th>Detail</th></tr>`)
		if len(logs) == 0 {
			sb.WriteString(`<tr><td colspan="4" class="empty">Belum ada aktivitas</td></tr>`)
		}
		for _, l := range logs {
			act := l["action"]
			cls := "badge scope"
			switch {
			case strings.Contains(act, "gagal"):
				cls = "badge expired"
			case strings.Contains(act, "login"):
				cls = "badge pending"
			}
			sb.WriteString(`<tr><td><small>` + l["at"] + `</small></td><td>` + l["actor"] + `</td><td><span class="` + cls + `">` + act + `</span></td><td>` + l["detail"] + `</td></tr>`)
		}
		sb.WriteString(`</table><div style="margin-top:10px">`)
		if page > 1 {
			sb.WriteString(`<a class="pg" href="/admin/log?p=` + itoa(page-1) + `">← Sebelumnya</a> `)
		}
		sb.WriteString(`<small>Halaman ` + itoa(page) + ` / ` + itoa(maxPage) + ` · total ` + itoa(total) + `</small>`)
		if page < maxPage {
			sb.WriteString(` <a class="pg" href="/admin/log?p=` + itoa(page+1) + `">Berikutnya →</a>`)
		}
		sb.WriteString(`</div></div>`)
		return template.HTML(sb.String())
	})
}
