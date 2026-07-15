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

// Callback prefixes for inline buttons on staff order messages.
const (
	cbReady   = "stf_ready"
	cbCancel  = "stf_cancel"
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
	// Staff text commands (fallback for manual use).
	h.bot.Handle("/ready", h.onReadyCmd)
	h.bot.Handle("/cancel", h.onCancelCmd)
	h.bot.Handle("/orders", h.onOrders)
	h.bot.Handle("/openshop", h.onOpenShop)
	h.bot.Handle("/closeshop", h.onCloseShop)
	// Inline button callbacks (primary staff path).
	h.bot.Handle("\f"+cbReady, h.onReadyBtn)
	h.bot.Handle("\f"+cbCancel, h.onCancelBtn)
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
	_, err := storage.UpsertCustomer(h.db, u.ID, u.Username)
	if err != nil {
		log.Printf("upsert user %d: %v", u.ID, err)
	}
	return err
}

// --- Staff: inline button callbacks ---

// onReadyBtn handles the "Mark Ready" inline button on staff order messages.
func (h *Handler) onReadyBtn(c telebot.Context) error {
	if !h.isStaffCallback(c) {
		return c.Respond()
	}
	orderNo, err := strconv.Atoi(c.Callback().Data)
	if err != nil {
		return c.Respond(&telebot.CallbackResponse{Text: "Invalid order"})
	}
	order, err := storage.OrderByOrderNo(h.db, orderNo)
	if err != nil || order == nil {
		return c.Respond(&telebot.CallbackResponse{Text: "Order not found"})
	}
	if order.Status != model.StatusPaid {
		return c.Respond(&telebot.CallbackResponse{Text: "Order is " + order.Status})
	}
	if err := storage.SetStatus(h.db, order.ID, model.StatusReady); err != nil {
		return c.Respond(&telebot.CallbackResponse{Text: "Internal error"})
	}
	// Edit the staff message: remove buttons, append status.
	h.editStaffOrderMessage(c, order, "Ready")
	// DM the customer.
	h.notifyCustomerReady(order)
	return c.Respond(&telebot.CallbackResponse{Text: "Marked ready"})
}

// onCancelBtn handles the "Cancel" inline button on staff order messages.
func (h *Handler) onCancelBtn(c telebot.Context) error {
	if !h.isStaffCallback(c) {
		return c.Respond()
	}
	orderNo, err := strconv.Atoi(c.Callback().Data)
	if err != nil {
		return c.Respond(&telebot.CallbackResponse{Text: "Invalid order"})
	}
	order, err := storage.OrderByOrderNo(h.db, orderNo)
	if err != nil || order == nil {
		return c.Respond(&telebot.CallbackResponse{Text: "Order not found"})
	}
	if order.Status != model.StatusPaid {
		return c.Respond(&telebot.CallbackResponse{Text: "Order is " + order.Status})
	}
	if err := storage.SetStatus(h.db, order.ID, model.StatusCancelled); err != nil {
		return c.Respond(&telebot.CallbackResponse{Text: "Internal error"})
	}
	// Edit the staff message: remove buttons, append status.
	h.editStaffOrderMessage(c, order, "Cancelled")
	// DM the customer.
	h.bot.Send(&telebot.Chat{ID: order.ChatID},
		fmt.Sprintf("Order #%d has been cancelled. Please expect a refund if you have paid.", order.OrderNo))
	return c.Respond(&telebot.CallbackResponse{Text: "Cancelled"})
}

// isStaffCallback checks if the callback came from the staff group.
func (h *Handler) isStaffCallback(c telebot.Context) bool {
	cb := c.Callback()
	if cb == nil || cb.Message == nil || cb.Message.Chat == nil {
		return false
	}
	return cb.Message.Chat.ID == h.staffChatID
}

// editStaffOrderMessage edits the order notification message to remove the
// buttons and append the new status.
func (h *Handler) editStaffOrderMessage(c telebot.Context, order *model.Order, status string) {
	items, _ := storage.OrderItems(h.db, order.ID)
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
	sb.WriteString(fmt.Sprintf("   Total: $%.2f\n", float64(order.TotalCents)/100))
	sb.WriteString(fmt.Sprintf("   \u2014 %s", status))
	_, err := h.bot.Edit(c.Message(), sb.String())
	if err != nil {
		log.Printf("edit staff order message: %v", err)
	}
}

// --- Staff: text commands (fallback) ---

func (h *Handler) onReadyCmd(c telebot.Context) error {
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
	if order.Status != model.StatusPaid {
		return c.Reply(fmt.Sprintf("Order #%d is %s (must be paid)", orderNo, order.Status))
	}
	if err := storage.SetStatus(h.db, order.ID, model.StatusReady); err != nil {
		return c.Reply("Internal error, please try again.")
	}
	h.notifyCustomerReady(order)
	return c.Reply(fmt.Sprintf("\u2705 #%d marked ready.", orderNo))
}

func (h *Handler) onCancelCmd(c telebot.Context) error {
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
		base := o.PaidAt
		if base.IsZero() {
			base = o.CreatedAt
		}
		customer, _ := storage.CustomerByUserID(h.db, o.UserID)
		who := displayName(customer)
		sb.WriteString(fmt.Sprintf("\u23F3 #%d \u2014 %s \u2014 $%.2f \u2014 %s\n", o.OrderNo, who, float64(o.TotalCents)/100, pickupLabel(o.PickupMinutes, base, h.sgt)))
		for _, it := range items {
			sb.WriteString(fmt.Sprintf("   %d\u00D7 %s\n", it.Quantity, it.Name))
		}
	}
	return c.Reply(sb.String())
}

func (h *Handler) onOpenShop(c telebot.Context) error {
	if !h.isStaffChat(c) {
		return nil
	}
	if err := storage.SetShopOpen(h.db, true); err != nil {
		return c.Reply("Internal error, please try again.")
	}
	return c.Reply("\u2705 Shop is now OPEN. Customers can place orders.")
}

func (h *Handler) onCloseShop(c telebot.Context) error {
	if !h.isStaffChat(c) {
		return nil
	}
	if err := storage.SetShopOpen(h.db, false); err != nil {
		return c.Reply("Internal error, please try again.")
	}
	return c.Reply("\U0001F6D1 Shop is now CLOSED. New orders are disabled.")
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

// orderMessageText builds the text for a staff order notification (without buttons).
func (h *Handler) orderMessageText(order *model.Order) string {
	items, _ := storage.OrderItems(h.db, order.ID)
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
	return sb.String()
}

// NotifyStaffNewOrder sends a new paid order notification to the staff group
// with inline buttons for "Mark Ready" and "Cancel".
func (h *Handler) NotifyStaffNewOrder(orderID int64) {
	order, err := storage.OrderByID(h.db, orderID)
	if err != nil || order == nil {
		return
	}
	text := h.orderMessageText(order)
	markup := &telebot.ReplyMarkup{}
	btnReady := markup.Data("Mark Ready", cbReady, fmt.Sprintf("%d", order.OrderNo))
	btnCancel := markup.Data("Cancel", cbCancel, fmt.Sprintf("%d", order.OrderNo))
	markup.Inline(markup.Row(btnReady, btnCancel))
	h.bot.Send(&telebot.Chat{ID: h.staffChatID}, text, markup)
}

// NotifyCustomerPaid sends a DM to the customer confirming their payment was received.
func (h *Handler) NotifyCustomerPaid(orderID int64) {
	order, err := storage.OrderByID(h.db, orderID)
	if err != nil || order == nil {
		return
	}
	base := order.PaidAt
	if base.IsZero() {
		base = order.CreatedAt
	}
	msg := fmt.Sprintf("Payment received! Order #%d \u2014 pickup at %s. We'll notify you when it's ready.",
		order.OrderNo, pickupLabel(order.PickupMinutes, base, h.sgt))
	h.bot.Send(&telebot.Chat{ID: order.ChatID}, msg)
}

// displayName returns the customer's @username for staff-facing messages.
func displayName(c *model.Customer) string {
	if c == nil {
		return "Unknown"
	}
	return "@" + c.Username
}

// notifyCustomerReady DMs the customer that their order is ready, with
// itemized order details.
func (h *Handler) notifyCustomerReady(order *model.Order) {
	items, _ := storage.OrderItems(h.db, order.ID)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\u2615 Order #%d is ready for pickup!\n\n", order.OrderNo))
	for _, it := range items {
		sb.WriteString(fmt.Sprintf("%d\u00D7 %s\n", it.Quantity, it.Name))
	}
	sb.WriteString(fmt.Sprintf("\nShow #%d at the counter.", order.OrderNo))
	h.bot.Send(&telebot.Chat{ID: order.ChatID}, sb.String())
}