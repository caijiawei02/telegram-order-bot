package bot

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/caijiawei02/telegram-order-bot/internal/model"
	"github.com/caijiawei02/telegram-order-bot/internal/storage"
	telebot "gopkg.in/telebot.v3"
)

// Handler bundles the bot with its dependencies.
type Handler struct {
	bot           *telebot.Bot
	db            *sql.DB
	sgt           *time.Location
	staffChatID   int64
	webAppURL     string
	pickupOptions []int
}

// NewHandler constructs a Handler.
func NewHandler(b *telebot.Bot, db *sql.DB, sgt *time.Location, staffChatID int64, webAppURL string, pickupOptions []int) *Handler {
	return &Handler{
		bot: b, db: db, sgt: sgt,
		staffChatID:   staffChatID,
		webAppURL:     webAppURL,
		pickupOptions: pickupOptions,
	}
}

// Register attaches all handlers to the bot.
func (h *Handler) Register() {
	h.bot.Handle("/start", h.onStart)
	h.bot.Handle("/help", h.onHelp)
	h.bot.Handle("/chatid", h.onChatID)
	// Staff commands (scoped to staff group via isStaffChat guard inside).
	h.bot.Handle("/ready", h.onReady)
	h.bot.Handle("/done", h.onDone)
	h.bot.Handle("/cancel", h.onCancel)
	h.bot.Handle("/orders", h.onOrders)
}

func (h *Handler) isStaffChat(c telebot.Context) bool {
	m := c.Message()
	if m == nil || m.Chat == nil {
		return false
	}
	return m.Chat.ID == h.staffChatID
}

// onStart sends a welcome message with a Web App button to open the menu.
func (h *Handler) onStart(c telebot.Context) error {
	m := c.Message()
	if m == nil {
		return nil
	}
	// Upsert the customer.
	_ = h.trackUser(c)

	markup := &telebot.ReplyMarkup{}
	btn := markup.WebApp("Order Coffee \u25B6", &telebot.WebApp{
		URL: h.webAppURL,
	})
	markup.Inline(markup.Row(btn))

	return c.Reply(
		"\u2615 Welcome! Tap below to browse the menu and order.\n\n"+
			"Pay with PayNow and pick up at the counter.",
		markup,
	)
}

func (h *Handler) onHelp(c telebot.Context) error {
	return c.Reply(
		"\u2615 Coffee Order Bot\n\n" +
			"Tap the Order Coffee button to browse the menu, add items to your cart, and pay with PayNow.\n\n" +
			"Commands:\n" +
			"/start \u2014 open the order menu\n" +
			"/chatid \u2014 show this chat's id\n" +
			"/help \u2014 show this help",
	)
}

// onChatID replies with the current chat id. Works in any chat (DM or group).
func (h *Handler) onChatID(c telebot.Context) error {
	m := c.Message()
	if m == nil || m.Chat == nil {
		return nil
	}
	return c.Reply(fmt.Sprintf("chat_id: %d", m.Chat.ID))
}

// trackUser upserts the sender into the customers table.
func (h *Handler) trackUser(c telebot.Context) error {
	m := c.Message()
	if m == nil || m.Sender == nil {
		return nil
	}
	u := m.Sender
	_, err := storage.UpsertCustomer(h.db, u.ID, u.Username, u.FirstName)
	if err != nil {
		log.Printf("upsert user %d: %v", u.ID, err)
	}
	return err
}

// --- Staff commands ---

func (h *Handler) onReady(c telebot.Context) error {
	if !h.isStaffChat(c) {
		return nil
	}
	orderNo, err := parseOrderNo(c)
	if err != nil {
		return c.Reply("Usage: /ready <order_no>")
	}
	order, err := storage.OrderByOrderNo(h.db, orderNo)
	if err != nil || order == nil {
		return c.Reply(fmt.Sprintf("Order #%d not found", orderNo))
	}
	if !order.HasGoneThrough() {
		return c.Reply(fmt.Sprintf("Order #%d is not paid (status: %s)", orderNo, order.Status))
	}
	if err := storage.SetStatus(h.db, order.ID, model.StatusReady); err != nil {
		return c.Reply("Internal error, please try again.")
	}
	// DM the customer.
	h.bot.Send(&telebot.Chat{ID: order.ChatID},
		fmt.Sprintf("\u2615 Order #%d is ready for pickup! Show #%d at the counter.", order.OrderNo, order.OrderNo))
	return c.Reply(fmt.Sprintf("\u2705 #%d marked ready.", orderNo))
}

func (h *Handler) onDone(c telebot.Context) error {
	if !h.isStaffChat(c) {
		return nil
	}
	orderNo, err := parseOrderNo(c)
	if err != nil {
		return c.Reply("Usage: /done <order_no>")
	}
	order, err := storage.OrderByOrderNo(h.db, orderNo)
	if err != nil || order == nil {
		return c.Reply(fmt.Sprintf("Order #%d not found", orderNo))
	}
	if err := storage.SetStatus(h.db, order.ID, model.StatusCompleted); err != nil {
		return c.Reply("Internal error, please try again.")
	}
	return c.Reply(fmt.Sprintf("\u2705 #%d completed.", orderNo))
}

func (h *Handler) onCancel(c telebot.Context) error {
	if !h.isStaffChat(c) {
		return nil
	}
	orderNo, err := parseOrderNo(c)
	if err != nil {
		return c.Reply("Usage: /cancel <order_no>")
	}
	order, err := storage.OrderByOrderNo(h.db, orderNo)
	if err != nil || order == nil {
		return c.Reply(fmt.Sprintf("Order #%d not found", orderNo))
	}
	if err := storage.SetStatus(h.db, order.ID, model.StatusCancelled); err != nil {
		return c.Reply("Internal error, please try again.")
	}
	// DM the customer.
	h.bot.Send(&telebot.Chat{ID: order.ChatID},
		fmt.Sprintf("Order #%d has been cancelled. Please expect a refund if you have paid.", order.OrderNo))
	return c.Reply(fmt.Sprintf("\u274C #%d cancelled.", orderNo))
}

func (h *Handler) onOrders(c telebot.Context) error {
	if !h.isStaffChat(c) {
		return nil
	}
	orders, err := storage.PendingStaffOrders(h.db)
	if err != nil {
		return c.Reply("Internal error, please try again.")
	}
	if len(orders) == 0 {
		return c.Reply("No pending orders.")
	}
	var sb strings.Builder
	sb.WriteString("Pending Orders:\n\n")
	for _, o := range orders {
		items, _ := storage.OrderItems(h.db, o.ID)
		statusEmoji := "\u23F3"
		if o.Status == model.StatusReady {
			statusEmoji = "\u2705"
		}
		base := o.PaidAt
		if base.IsZero() {
			base = o.CreatedAt
		}
		customer, _ := storage.CustomerByUserID(h.db, o.UserID)
		who := displayName(customer)
		sb.WriteString(fmt.Sprintf("%s #%d \u2014 %s \u2014 $%.2f \u2014 %s\n", statusEmoji, o.OrderNo, who, float64(o.TotalCents)/100, pickupLabel(o.PickupMinutes, base, h.sgt)))
		for _, it := range items {
			sb.WriteString(fmt.Sprintf("   %d\u00D7 %s\n", it.Quantity, it.Name))
		}
	}
	return c.Reply(sb.String())
}

func parseOrderNo(c telebot.Context) (int, error) {
	args := c.Args()
	if len(args) < 1 {
		return 0, fmt.Errorf("missing order number")
	}
	return strconv.Atoi(strings.TrimSpace(args[0]))
}

func pickupLabel(mins int, base time.Time, loc *time.Location) string {
	if mins == 0 {
		return "ASAP"
	}
	return base.In(loc).Add(time.Duration(mins) * time.Minute).Format("15:04")
}

// NotifyStaffNewOrder sends a new paid order notification to the staff group.
// Called after a successful payment (from the Stripe webhook or the polling
// fallback).
func (h *Handler) NotifyStaffNewOrder(orderID int64) {
	order, err := storage.OrderByID(h.db, orderID)
	if err != nil || order == nil {
		return
	}
	items, _ := storage.OrderItems(h.db, orderID)
	customer, _ := storage.CustomerByUserID(h.db, order.UserID)
	who := displayName(customer)
	base := order.PaidAt
	if base.IsZero() {
		base = order.CreatedAt
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\U0001F195 #%d \u2014 %s, pickup: %s\n", order.OrderNo, who, pickupLabel(order.PickupMinutes, base, h.sgt)))
	for _, it := range items {
		sb.WriteString(fmt.Sprintf("   %d\u00D7 %s\n", it.Quantity, it.Name))
	}
	sb.WriteString(fmt.Sprintf("   Total: $%.2f", float64(order.TotalCents)/100))
	h.bot.Send(&telebot.Chat{ID: h.staffChatID}, sb.String())
}

// displayName returns @username, or first name, or "User <id>" as fallback.
func displayName(c *model.Customer) string {
	if c == nil {
		return "Unknown"
	}
	if c.Username != "" {
		return "@" + c.Username
	}
	if c.FirstName != "" {
		return c.FirstName
	}
	return fmt.Sprintf("User %d", c.UserID)
}

func orderStatusEmoji(s string) string {
	switch s {
	case model.StatusPaid:
		return "\u2705 Paid"
	case model.StatusPreparing:
		return "\U0001F525 Preparing"
	case model.StatusReady:
		return "\u2615 Ready"
	default:
		return s
	}
}