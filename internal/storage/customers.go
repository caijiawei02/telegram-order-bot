package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/caijiawei02/telegram-order-bot/internal/model"
)

// UpsertCustomer inserts or updates a customer row keyed by Telegram user_id.
// Returns the customer's internal DB id. Always SELECTs the id after the
// upsert (LastInsertId is unreliable with ON CONFLICT in modernc.org/sqlite).
func UpsertCustomer(db *sql.DB, userID int64, username, firstName string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO customers (user_id, username, first_name, last_seen_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   username = excluded.username,
		   first_name = excluded.first_name,
		   last_seen_at = excluded.last_seen_at`,
		userID, username, firstName, now,
	)
	if err != nil {
		return 0, fmt.Errorf("upsert customer: %w", err)
	}
	var id int64
	err = db.QueryRow(`SELECT id FROM customers WHERE user_id = ?`, userID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("fetch customer id: %w", err)
	}
	return id, nil
}

// CustomerByUserID returns the customer row for a Telegram user id, or nil.
func CustomerByUserID(db *sql.DB, userID int64) (*model.Customer, error) {
	row := db.QueryRow(
		`SELECT id, user_id, username, first_name, last_seen_at
		 FROM customers WHERE user_id = ?`,
		userID,
	)
	var c model.Customer
	var lastSeen string
	if err := row.Scan(&c.ID, &c.UserID, &c.Username, &c.FirstName, &lastSeen); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	c.LastSeenAt, _ = time.Parse(time.RFC3339, lastSeen)
	return &c, nil
}