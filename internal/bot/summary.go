package bot

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/caijiawei02/telegram-order-bot/internal/storage"
	telebot "gopkg.in/telebot.v3"
)

// SendDailySalesSummary queries all paid (or beyond) orders within today's
// SGT day window, aggregates by item, and posts a summary to the staff group.
func SendDailySalesSummary(b *telebot.Bot, db *sql.DB, loc *time.Location, staffChatID int64, fireTime time.Time) {
	dayStart, dayEnd := sgtDayBounds(fireTime, loc)

	count, err := storage.DayOrderCount(db, dayStart, dayEnd)
	if err != nil {
		fmt.Printf("daily summary order count: %v\n", err)
		return
	}

	rows, err := storage.DaySalesSummary(db, dayStart, dayEnd)
	if err != nil {
		fmt.Printf("daily summary sales query: %v\n", err)
		return
	}

	totalCents := 0
	totalItems := 0
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\U0001F4CA Daily Sales \u2014 %s\n\n", fireTime.In(loc).Format("02 January 2006")))
	sb.WriteString(fmt.Sprintf("Orders: %d\n", count))

	for _, r := range rows {
		totalCents += r.TotalCents
		totalItems += r.Quantity
	}
	sb.WriteString(fmt.Sprintf("Items sold: %d\n", totalItems))
	sb.WriteString(fmt.Sprintf("Revenue: $%.2f\n\n", float64(totalCents)/100))

	if len(rows) > 0 {
		sb.WriteString("Top items:\n")
		for _, r := range rows {
			sb.WriteString(fmt.Sprintf("  %s  %d\u00D7 $%.2f = $%.2f\n",
				r.Name, r.Quantity, float64(r.UnitCents)/100, float64(r.TotalCents)/100))
		}
	} else {
		sb.WriteString("No sales today.\n")
	}

	_, err = b.Send(&telebot.Chat{ID: staffChatID}, sb.String())
	if err != nil {
		fmt.Printf("send daily summary: %v\n", err)
	}
}

// sgtDayBounds returns the half-open [00:00, next 00:00) window for the SGT
// day containing now. Mirrors the cailorie pattern.
func sgtDayBounds(now time.Time, loc *time.Location) (time.Time, time.Time) {
	nowLocal := now.In(loc)
	start := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	return start.UTC(), end.UTC()
}

// DeleteStalePendingOrders runs at 00:00 SGT to remove orders stuck in
// awaiting_payment from before today.
func DeleteStalePendingOrders(db *sql.DB, loc *time.Location) {
	nowLocal := time.Now().In(loc)
	dayStart := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)
	n, err := storage.DeleteStalePending(db, dayStart)
	if err != nil {
		fmt.Printf("cleanup stale pending: %v\n", err)
		return
	}
	if n > 0 {
		fmt.Printf("cleaned up %d awaiting_payment orders\n", n)
	}
}