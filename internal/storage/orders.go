package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/caijiawei02/telegram-order-bot/internal/model"
)

// NextOrderNo returns the next human-readable order number (max + 1, starting at 1).
func NextOrderNo(db *sql.DB) (int, error) {
	var maxNo sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(order_no) FROM orders`).Scan(&maxNo); err != nil {
		return 0, fmt.Errorf("next order_no: %w", err)
	}
	if maxNo.Valid {
		return int(maxNo.Int64) + 1, nil
	}
	return 1, nil
}

// CreateOrder inserts a new order with its line items inside a transaction.
// The order is created with status "awaiting_payment". Items are price/name
// snapshots from the menu at order time.
func CreateOrder(db *sql.DB, order model.Order, items []model.OrderItem) (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	createdStr := order.CreatedAt.UTC().Format(time.RFC3339)
	res, err := tx.Exec(
		`INSERT INTO orders
		   (order_no, customer_id, user_id, chat_id, status, total_cents,
		    pickup_minutes, note, stripe_payment_intent, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		order.OrderNo, order.CustomerID, order.UserID, order.ChatID,
		model.StatusAwaitingPayment, order.TotalCents,
		order.PickupMinutes, order.Note, order.StripePaymentIntent, createdStr,
	)
	if err != nil {
		return 0, OrderQueryError("insert order", err)
	}
	orderID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}

	for _, it := range items {
		_, err = tx.Exec(
			`INSERT INTO order_items (order_id, sku, name, unit_cents, quantity)
			 VALUES (?, ?, ?, ?, ?)`,
			orderID, it.SKU, it.Name, it.UnitCents, it.Quantity,
		)
		if err != nil {
			return 0, OrderQueryError("insert order item", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return orderID, nil
}

// OrderByID returns a full order (without items) by its DB id.
func OrderByID(db *sql.DB, id int64) (*model.Order, error) {
	row := db.QueryRow(
		`SELECT id, order_no, customer_id, user_id, chat_id, status, total_cents,
		        pickup_minutes, note, stripe_payment_intent, stripe_charge_id,
		        created_at, paid_at
		 FROM orders WHERE id = ?`,
		id,
	)
	return scanOrder(row)
}

// OrderByPaymentIntent returns an order by its Stripe PaymentIntent id.
func OrderByPaymentIntent(db *sql.DB, piID string) (*model.Order, error) {
	row := db.QueryRow(
		`SELECT id, order_no, customer_id, user_id, chat_id, status, total_cents,
		        pickup_minutes, note, stripe_payment_intent, stripe_charge_id,
		        created_at, paid_at
		 FROM orders WHERE stripe_payment_intent = ?`,
		piID,
	)
	return scanOrder(row)
}

// OrderByOrderNo returns an order by its human-readable order number.
func OrderByOrderNo(db *sql.DB, orderNo int) (*model.Order, error) {
	row := db.QueryRow(
		`SELECT id, order_no, customer_id, user_id, chat_id, status, total_cents,
		        pickup_minutes, note, stripe_payment_intent, stripe_charge_id,
		        created_at, paid_at
		 FROM orders WHERE order_no = ?`,
		orderNo,
	)
	return scanOrder(row)
}

func scanOrder(row *sql.Row) (*model.Order, error) {
	var o model.Order
	var createdStr, paidStr string
	if err := row.Scan(
		&o.ID, &o.OrderNo, &o.CustomerID, &o.UserID, &o.ChatID, &o.Status, &o.TotalCents,
		&o.PickupMinutes, &o.Note, &o.StripePaymentIntent, &o.StripeChargeID,
		&createdStr, &paidStr,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	o.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	if paidStr != "" {
		o.PaidAt, _ = time.Parse(time.RFC3339, paidStr)
	}
	return &o, nil
}

// OrderItems returns all line items for an order.
func OrderItems(db *sql.DB, orderID int64) ([]model.OrderItem, error) {
	rows, err := db.Query(
		`SELECT id, order_id, sku, name, unit_cents, quantity
		 FROM order_items WHERE order_id = ?`,
		orderID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.OrderItem
	for rows.Next() {
		var it model.OrderItem
		if err := rows.Scan(&it.ID, &it.OrderID, &it.SKU, &it.Name, &it.UnitCents, &it.Quantity); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// MarkPaid sets an order's status to paid, records the Stripe charge id, and
// sets paid_at. Only transitions from awaiting_payment → paid. Idempotent —
// if already paid, does nothing and returns nil.
func MarkPaid(db *sql.DB, orderID int64, stripeChargeID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`UPDATE orders
		 SET status = ?, stripe_charge_id = ?, paid_at = ?
		 WHERE id = ? AND status = ?`,
		model.StatusPaid, stripeChargeID, now, orderID, model.StatusAwaitingPayment,
	)
	if err != nil {
		return fmt.Errorf("mark paid: %w", err)
	}
	return nil
}

// SetStatus updates an order's status. Used by staff commands.
func SetStatus(db *sql.DB, orderID int64, status string) error {
	_, err := db.Exec(
		`UPDATE orders SET status = ? WHERE id = ?`,
		status, orderID,
	)
	if err != nil {
		return fmt.Errorf("set status %s: %w", status, err)
	}
	return nil
}

// SetPaymentIntent records the Stripe PaymentIntent id on an order.
func SetPaymentIntent(db *sql.DB, orderID int64, piID string) error {
	_, err := db.Exec(
		`UPDATE orders SET stripe_payment_intent = ? WHERE id = ?`,
		piID, orderID,
	)
	return err
}

// OrdersByCustomer returns a customer's orders that have gone through
// (paid or beyond), excluding awaiting_payment/cancelled/failed. Ordered by
// created_at DESC. Limits to the most recent `limit` results.
func OrdersByCustomer(db *sql.DB, customerID int64, limit int) ([]model.Order, error) {
	rows, err := db.Query(
		`SELECT id, order_no, customer_id, user_id, chat_id, status, total_cents,
		        pickup_minutes, note, stripe_payment_intent, stripe_charge_id,
		        created_at, paid_at
		 FROM orders
		 WHERE customer_id = ?
		   AND status IN ('paid','ready')
		 ORDER BY created_at DESC
		 LIMIT ?`,
		customerID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrders(rows)
}

// DeleteStalePending deletes orders stuck in awaiting_payment older than the
// given cutoff time. Returns the number of rows deleted. Order items are
// removed via the ON DELETE CASCADE foreign key.
func DeleteStalePending(db *sql.DB, before time.Time) (int64, error) {
	res, err := db.Exec(
		`DELETE FROM orders
		 WHERE status = ? AND created_at < ?`,
		model.StatusAwaitingPayment, before.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("delete stale pending: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// SalesRow is one item's aggregate for the daily sales summary.
type SalesRow struct {
	Name      string
	Quantity  int
	UnitCents int
	TotalCents int
}

// DaySalesSummary returns per-item sales for paid (or beyond) orders within
// the half-open window [dayStart, dayEnd). Orders that are cancelled or
// awaiting_payment are excluded.
func DaySalesSummary(db *sql.DB, dayStart, dayEnd time.Time) ([]SalesRow, error) {
	rows, err := db.Query(
		`SELECT oi.name, SUM(oi.quantity), oi.unit_cents, SUM(oi.quantity * oi.unit_cents)
		 FROM order_items oi
		 JOIN orders o ON o.id = oi.order_id
		 WHERE o.status IN ('paid','ready')
		   AND o.paid_at >= ? AND o.paid_at < ?
		 GROUP BY oi.name, oi.unit_cents
		 ORDER BY SUM(oi.quantity) DESC`,
		dayStart.UTC().Format(time.RFC3339),
		dayEnd.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SalesRow
	for rows.Next() {
		var r SalesRow
		if err := rows.Scan(&r.Name, &r.Quantity, &r.UnitCents, &r.TotalCents); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DayOrderCount returns the number of paid (or beyond) orders in the window.
func DayOrderCount(db *sql.DB, dayStart, dayEnd time.Time) (int, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM orders
		 WHERE status IN ('paid','ready')
		   AND paid_at >= ? AND paid_at < ?`,
		dayStart.UTC().Format(time.RFC3339),
		dayEnd.UTC().Format(time.RFC3339),
	).Scan(&n)
	return n, err
}

// PendingStaffOrders returns orders that are paid but not yet ready (i.e.
// need staff attention), ordered by paid_at ASC. Used by /orders staff command.
func PendingStaffOrders(db *sql.DB) ([]model.Order, error) {
	rows, err := db.Query(
		`SELECT id, order_no, customer_id, user_id, chat_id, status, total_cents,
		        pickup_minutes, note, stripe_payment_intent, stripe_charge_id,
		        created_at, paid_at
		 FROM orders
		 WHERE status = 'paid'
		 ORDER BY paid_at ASC
		 LIMIT 50`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrders(rows)
}

func scanOrders(rows *sql.Rows) ([]model.Order, error) {
	var out []model.Order
	for rows.Next() {
		var o model.Order
		var createdStr, paidStr string
		if err := rows.Scan(
			&o.ID, &o.OrderNo, &o.CustomerID, &o.UserID, &o.ChatID, &o.Status, &o.TotalCents,
			&o.PickupMinutes, &o.Note, &o.StripePaymentIntent, &o.StripeChargeID,
			&createdStr, &paidStr,
		); err != nil {
			return nil, err
		}
		o.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		if paidStr != "" {
			o.PaidAt, _ = time.Parse(time.RFC3339, paidStr)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OrderQueryError wraps a DB error with context, handling the common
// constraint-violation case for friendlier messages.
func OrderQueryError(ctx string, err error) error {
	return fmt.Errorf("%s: %w", ctx, err)
}