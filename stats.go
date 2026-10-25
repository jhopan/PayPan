package main

import "time"

// appStat: ringkasan per aplikasi (hari ini).
type appStat struct {
	Name  string
	Paid  int64
	Sum   int64
	Pend  int64
	Total int64 // semua order hari ini
}

// appStatsToday: rekap per aplikasi untuk hari ini.
func (s *srv) appStatsToday() []appStat {
	start := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.Local).Unix()
	rows, err := s.db.Query(`
		SELECT COALESCE(a.name,'(kasir)') AS nm,
		       SUM(o.status='paid') AS paid,
		       COALESCE(SUM(CASE WHEN o.status='paid' THEN o.total ELSE 0 END),0) AS sum,
		       SUM(o.status='pending') AS pend,
		       COUNT(*) AS tot
		FROM orders o LEFT JOIN apps a ON a.rowid=o.created_by
		WHERE o.created_at >= ?
		GROUP BY nm ORDER BY sum DESC, tot DESC LIMIT 10`, start)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []appStat
	for rows.Next() {
		var a appStat
		if rows.Scan(&a.Name, &a.Paid, &a.Sum, &a.Pend, &a.Total) == nil {
			out = append(out, a)
		}
	}
	return out
}

// dailyStat: satu titik data untuk grafik.
type dailyStat struct {
	Day   string
	Count int64
	Sum   int64
}

// dailyStats: omzet harian N hari terakhir (paid).
func (s *srv) dailyStats(days int) []dailyStat {
	rows, err := s.db.Query(`
		SELECT date(paid_at,'unixepoch','localtime') d, COUNT(*), SUM(total)
		FROM orders WHERE status='paid' AND paid_at > strftime('%s','now') - ?
		GROUP BY d ORDER BY d`, int64(days)*86400)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []dailyStat
	for rows.Next() {
		var d dailyStat
		if rows.Scan(&d.Day, &d.Count, &d.Sum) == nil {
			out = append(out, d)
		}
	}
	return out
}
