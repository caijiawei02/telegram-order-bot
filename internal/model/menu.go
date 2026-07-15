package model

// MenuItem represents one item on the coffee menu.
type MenuItem struct {
	ID         int64  `json:"id"`
	SKU        string `json:"sku"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	PriceCents int    `json:"price_cents"`
	Available  bool   `json:"available"`
	SortOrder  int    `json:"sort_order"`
}