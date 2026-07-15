package storage

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens the SQLite database at dbPath and runs migrations.
func Open(dbPath string) (*sql.DB, error) {
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS customers (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id      INTEGER NOT NULL UNIQUE,
	username     TEXT NOT NULL DEFAULT '',
	first_name   TEXT NOT NULL DEFAULT '',
	last_seen_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS menu_items (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	sku         TEXT NOT NULL UNIQUE,
	name        TEXT NOT NULL,
	category    TEXT NOT NULL,
	price_cents INTEGER NOT NULL,
	available   INTEGER NOT NULL DEFAULT 1,
	sort_order  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS orders (
	id                    INTEGER PRIMARY KEY AUTOINCREMENT,
	order_no              INTEGER NOT NULL UNIQUE,
	customer_id           INTEGER NOT NULL REFERENCES customers(id),
	user_id               INTEGER NOT NULL,
	chat_id               INTEGER NOT NULL,
	status                TEXT NOT NULL DEFAULT 'awaiting_payment',
	total_cents           INTEGER NOT NULL,
	pickup_minutes        INTEGER NOT NULL DEFAULT 0,
	note                  TEXT NOT NULL DEFAULT '',
	stripe_payment_intent TEXT NOT NULL DEFAULT '',
	stripe_charge_id      TEXT NOT NULL DEFAULT '',
	created_at            TEXT NOT NULL,
	paid_at               TEXT
);
CREATE INDEX IF NOT EXISTS idx_orders_customer ON orders(customer_id, created_at);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_payment_intent ON orders(stripe_payment_intent);

CREATE TABLE IF NOT EXISTS order_items (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	order_id   INTEGER NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
	sku        TEXT NOT NULL,
	name       TEXT NOT NULL,
	unit_cents INTEGER NOT NULL,
	quantity   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_order_items_order ON order_items(order_id);

CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`)
	if err != nil {
		return fmt.Errorf("create tables: %w", err)
	}
	return nil
}