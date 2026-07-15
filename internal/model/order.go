package model

import "time"

// Order status constants.
const (
	StatusAwaitingPayment = "awaiting_payment"
	StatusPaid             = "paid"
	StatusPreparing        = "preparing"
	StatusReady            = "ready"
	StatusCompleted        = "completed"
	StatusCancelled        = "cancelled"
	StatusFailed           = "failed"
)

// Order represents a customer's coffee order.
type Order struct {
	ID                  int64
	OrderNo             int    // human-readable, e.g. 1042
	CustomerID          int64  // FK customers.id
	UserID              int64  // Telegram user id (denormalized for convenience)
	ChatID              int64  // the DM chat — for sending updates to the customer
	Status              string
	TotalCents          int
	PickupMinutes       int    // ASAP=0, or 15/30/45
	Note                string
	StripePaymentIntent string // pi_...
	StripeChargeID      string // ch_... (set on webhook success)
	CreatedAt           time.Time // UTC
	PaidAt              time.Time // UTC, zero if not paid
}

// OrderItem is one line item in an order (price snapshot at order time).
type OrderItem struct {
	ID        int64
	OrderID   int64
	SKU       string
	Name      string // snapshot of menu item name
	UnitCents int    // snapshot price
	Quantity  int
}

// HasGoneThrough reports whether the order successfully transitioned to paid
// or beyond (excluding cancelled/failed/awaiting_payment).
func (o Order) HasGoneThrough() bool {
	switch o.Status {
	case StatusPaid, StatusPreparing, StatusReady, StatusCompleted:
		return true
	default:
		return false
	}
}