package main

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
)

func itoa(n int) string       { return strconv.Itoa(n) }
func itoa64(n int64) string   { return strconv.FormatInt(n, 10) }
func fmt_Sscan(s string, v *int64) { fmt.Sscan(s, v) }
func os_WriteFile(p string, d []byte, m os.FileMode) { os.WriteFile(p, d, m) }

type orderLite struct {
	ID     string
	Status string
	Price  int64
	Total  int64
}

type payLite struct {
	Pkg    string
	Title  string
	Amount sql.NullInt64
}

type unmatchLite struct {
	Amount int64
	Reason string
}

func (s *srv) recentOrders(n int) []orderLite {
	rows, err := s.db.Query("SELECT id,status,price,total FROM orders ORDER BY created_at DESC LIMIT ?", n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []orderLite
	for rows.Next() {
		var o orderLite
		if rows.Scan(&o.ID, &o.Status, &o.Price, &o.Total) == nil {
			out = append(out, o)
		}
	}
	return out
}

func (s *srv) recentPayments(n int) []payLite {
	rows, err := s.db.Query("SELECT pkg,title,amount FROM payments ORDER BY received_at DESC LIMIT ?", n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []payLite
	for rows.Next() {
		var p payLite
		if rows.Scan(&p.Pkg, &p.Title, &p.Amount) == nil {
			out = append(out, p)
		}
	}
	return out
}

func (s *srv) recentUnmatched(n int) []unmatchLite {
	rows, err := s.db.Query("SELECT amount,reason FROM unmatched ORDER BY received_at DESC LIMIT ?", n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []unmatchLite
	for rows.Next() {
		var u unmatchLite
		if rows.Scan(&u.Amount, &u.Reason) == nil {
			out = append(out, u)
		}
	}
	return out
}

func (s *srv) readQris() string {
	b, err := os.ReadFile("qris_base.txt")
	if err != nil {
		return ""
	}
	return string(b)
}

// loadTGFromDB: baca pengaturan telegram dari tabel settings (set via web)
func (s *srv) loadTGFromDB() {
	s.db.QueryRow("SELECT value FROM settings WHERE key='tg_token'").Scan(&s.tgToken)
	s.db.QueryRow("SELECT value FROM settings WHERE key='tg_chat'").Scan(&s.tgChat)
}
