package main

import (
	"database/sql"
	"time"

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
	created_by INTEGER NOT NULL DEFAULT 0,
	paid_at INTEGER,
	label TEXT NOT NULL DEFAULT '',
	buyer TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_orders_status_total ON orders(status, total);
CREATE INDEX IF NOT EXISTS idx_orders_expires ON orders(status, expires_at);

-- KLAIM TOTAL EKSKLUSIF: satu total hanya boleh dimiliki SATU order pending.
-- SQLite enforce ini di level storage — dua INSERT bareng, salah satu pasti gagal.
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_pending_total
	ON orders(total) WHERE status='pending';

-- Riwayat perubahan status per order (audit transaksi, bukan audit admin).
CREATE TABLE IF NOT EXISTS order_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	order_id TEXT NOT NULL,
	from_status TEXT NOT NULL DEFAULT '',
	to_status TEXT NOT NULL,
	actor TEXT NOT NULL DEFAULT 'system',
	note TEXT NOT NULL DEFAULT '',
	at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_order_events_order ON order_events(order_id, at);

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
-- Antrean webhook persisten: retry tidak hilang saat server restart.
CREATE TABLE IF NOT EXISTS webhook_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	order_id TEXT NOT NULL,
	hook_id INTEGER NOT NULL,
	url TEXT NOT NULL,
	secret TEXT,
	payload TEXT NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	next_at INTEGER NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_whq_next ON webhook_queue(next_at);
`

func initDB(path string) (*sql.DB, error) {
	return initDBFromDSN(path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
}

// initDBFromDSN dipakai test (DSN memory sudah lengkap, jangan ditambah pragma).
func initDBFromDSN(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: hindari SQLITE_BUSY
	// koneksi tidak boleh hidup selamanya: kalau masuk state buruk,
	// semua query macet sampai restart. Daur ulang tiap 30 menit.
	db.SetConnMaxLifetime(30 * time.Minute)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// migrasi ringan: tambah kolom yang belum ada (CREATE TABLE gak update tabel lama)
	var colCount int
	db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('orders') WHERE name='created_by'").Scan(&colCount)
	if colCount == 0 {
		if _, err := db.Exec("ALTER TABLE orders ADD COLUMN created_by INTEGER NOT NULL DEFAULT 0"); err != nil {
			db.Close()
			return nil, err
		}
	}
	var whFired int
	db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('orders') WHERE name='webhook_fired'").Scan(&whFired)
	if whFired == 0 {
		if _, err := db.Exec("ALTER TABLE orders ADD COLUMN webhook_fired INTEGER NOT NULL DEFAULT 0"); err != nil {
			db.Close()
			return nil, err
		}
	}
	for _, m := range []struct{ name, ddl string }{
		{"label", "ALTER TABLE orders ADD COLUMN label TEXT NOT NULL DEFAULT ''"},
		{"buyer", "ALTER TABLE orders ADD COLUMN buyer TEXT NOT NULL DEFAULT ''"},
	} {
		var n int
		db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('orders') WHERE name=?", m.name).Scan(&n)
		if n == 0 {
			if _, err := db.Exec(m.ddl); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	var whSec int
	db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('webhooks') WHERE name='secret'").Scan(&whSec)
	if whSec == 0 {
		db.Exec("ALTER TABLE webhooks ADD COLUMN secret TEXT")
	}
	// backfill: webhook lama tanpa secret (dibuat sebelum kolom ada) diisi otomatis
	db.Exec("UPDATE webhooks SET secret=? WHERE secret IS NULL OR secret=''", genToken())
	return db, nil
}
