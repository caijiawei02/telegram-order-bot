package model

import "time"

// Customer represents a Telegram user who has interacted with the bot.
type Customer struct {
	ID         int64
	UserID     int64 // Telegram user id
	Username   string
	LastSeenAt time.Time // UTC
}