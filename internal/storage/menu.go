package storage

import (
	"database/sql"
	"fmt"

	"github.com/caijiawei02/telegram-order-bot/internal/model"
)

// defaultMenu is the seed menu written to menu_items on first run if the
// table is empty.
var defaultMenu = []model.MenuItem{
	{SKU: "latte_hot", Name: "Latte (Hot)", Category: "Latte", PriceCents: 300, Available: true, SortOrder: 1},
	{SKU: "latte_iced", Name: "Latte (Iced)", Category: "Latte", PriceCents: 350, Available: true, SortOrder: 2},
	{SKU: "americano_hot", Name: "Americano (Hot)", Category: "Americano", PriceCents: 200, Available: true, SortOrder: 3},
	{SKU: "americano_iced", Name: "Americano (Iced)", Category: "Americano", PriceCents: 250, Available: true, SortOrder: 4},
}

// SeedMenuIfEmpty inserts the default menu items if the menu_items table is
// empty. Safe to call on every startup.
func SeedMenuIfEmpty(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM menu_items`).Scan(&n); err != nil {
		return fmt.Errorf("count menu: %w", err)
	}
	if n > 0 {
		return nil
	}
	for _, item := range defaultMenu {
		avail := 0
		if item.Available {
			avail = 1
		}
		_, err := db.Exec(
			`INSERT INTO menu_items (sku, name, category, price_cents, available, sort_order)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			item.SKU, item.Name, item.Category, item.PriceCents, avail, item.SortOrder,
		)
		if err != nil {
			return fmt.Errorf("seed menu item %s: %w", item.SKU, err)
		}
	}
	return nil
}

// LoadMenu returns all available menu items ordered by sort_order.
func LoadMenu(db *sql.DB) ([]model.MenuItem, error) {
	rows, err := db.Query(
		`SELECT id, sku, name, category, price_cents, available, sort_order
		 FROM menu_items
		 WHERE available = 1
		 ORDER BY sort_order ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("load menu: %w", err)
	}
	defer rows.Close()
	var out []model.MenuItem
	for rows.Next() {
		var m model.MenuItem
		var avail int
		if err := rows.Scan(&m.ID, &m.SKU, &m.Name, &m.Category, &m.PriceCents, &avail, &m.SortOrder); err != nil {
			return nil, err
		}
		m.Available = avail == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// MenuItemBySKU returns a menu item by SKU, or nil if not found.
func MenuItemBySKU(db *sql.DB, sku string) (*model.MenuItem, error) {
	row := db.QueryRow(
		`SELECT id, sku, name, category, price_cents, available, sort_order
		 FROM menu_items WHERE sku = ?`,
		sku,
	)
	var m model.MenuItem
	var avail int
	if err := row.Scan(&m.ID, &m.SKU, &m.Name, &m.Category, &m.PriceCents, &avail, &m.SortOrder); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	m.Available = avail == 1
	return &m, nil
}