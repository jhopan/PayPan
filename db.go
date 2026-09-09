package main

import (
	"database/sql"
	_ "modernc.org/sqlite"
)// Schema: orders (order customer), payments (semua notif dari device, audit),
// unmatched (notif yang tidak cocok order pending). id device di payments unique.
var schema = `
CREATE TABLE IF NOT EXISTS orders (
	id TEXT PRIMARY KEY,
	price INTEGER NOT NULL,
	code INTEGER NOT NULL,
	total INTEGER NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending',
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	paid_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_orders_status_total ON orders(status, total);
CREATE INDEX IF NOT EXISTS idx_orders_expires ON orders(status, expires_at);

-- KLAIM TOTAL EKSKLUSIF: satu total hanya boleh dimiliki SATU order pending.
-- SQLite enforce ini di level storage — dua INSERT bareng, salah satu pasti gagal.
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_pending_total
	ON orders(total) WHERE status='pending';

CREATE TABLE IF NOT EXISTS payments (
	id TEXT PRIMARY KEY,
	pkg TEXT,
	title TEXT,
	text TEXT,
	amount INTEGER,
	source TEXT,
	created_at INTEGER,
	received_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS unmatched (
	payment_id TEXT PRIMARY KEY,
	amount INTEGER,
	reason TEXT,
	received_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT
);

CREATE TABLE IF NOT EXISTS apps (
	name TEXT NOT NULL,
	token TEXT UNIQUE NOT NULL,
	scopes TEXT NOT NULL DEFAULT 'both',
	active INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS audit (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	at INTEGER NOT NULL,
	actor TEXT NOT NULL,
	action TEXT NOT NULL,
	detail TEXT
);

CREATE TABLE IF NOT EXISTS webhooks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	app_rowid INTEGER NOT NULL,
	url TEXT NOT NULL,
	secret TEXT,
	active INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS webhook_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	order_id TEXT,
	url TEXT,
	code INTEGER,
	at INTEGER NOT NULL
);
`

func initDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: hindari SQLITE_BUSY
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
