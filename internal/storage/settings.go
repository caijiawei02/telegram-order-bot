package storage

import (
	"database/sql"
	"fmt"
)

// SettingShopOpen is the settings key for the shop open/closed state.
const SettingShopOpen = "shop_open"

// GetSetting returns the value for a settings key, or the default if not found.
func GetSetting(db *sql.DB, key, def string) (string, error) {
	var val string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return def, nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting %s: %w", key, err)
	}
	return val, nil
}

// SetSetting upserts a settings key-value pair.
func SetSetting(db *sql.DB, key, value string) error {
	_, err := db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}

// GetShopOpen returns true if the shop is open (default: open).
func GetShopOpen(db *sql.DB) (bool, error) {
	val, err := GetSetting(db, SettingShopOpen, "1")
	if err != nil {
		return false, err
	}
	return val == "1", nil
}

// SetShopOpen sets the shop open/closed state.
func SetShopOpen(db *sql.DB, open bool) error {
	v := "0"
	if open {
		v = "1"
	}
	return SetSetting(db, SettingShopOpen, v)
}