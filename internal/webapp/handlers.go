package webapp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/caijiawei02/telegram-order-bot/internal/model"
	"github.com/caijiawei02/telegram-order-bot/internal/payment"
	"github.com/caijiawei02/telegram-order-bot/internal/storage"
)

// isTestMode reports whether the bot is running with a Stripe test key.
func (s *Server) isTestMode() bool {
	return strings.HasPrefix(s.deps.StripeSecret, "sk_test_")
}

// --- shared helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// requireAuth checks the session cookie and returns the user, or writes 401.
func (s *Server) requireAuth(r *http.Request) *SessionUser {
	user := verifySessionCookie(r, s.deps.SessionSecret)
	if user == nil {
		return nil
	}
	return user
}

// --- menu ---

func (s *Server) handleMenu(w http.ResponseWriter, r *http.Request) {
	user := s.requireAuth(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := storage.LoadMenu(s.deps.DB)
	if err != nil {
		fmt.Printf("load menu: %v\n", err)
		writeError(w, http.StatusInternalServerError, "failed to load menu")
		return
	}
	type menuItemResp struct {
		ID         int64  `json:"id"`
		SKU        string `json:"sku"`
		Name       string `json:"name"`
		Category   string `json:"category"`
		PriceCents int    `json:"price_cents"`
	}
	out := make([]menuItemResp, 0, len(items))
	for _, m := range items {
		out = append(out, menuItemResp{
			ID: m.ID, SKU: m.SKU, Name: m.Name,
			Category: m.Category, PriceCents: m.PriceCents,
		})
	}
	shopOpen, _ := storage.GetShopOpen(s.deps.DB)
	writeJSON(w, http.StatusOK, map[string]any{
		"shop_open": shopOpen,
		"items":     out,
	})
}

// --- create order ---

type createOrderReq struct {
	Items         []createOrderItem `json:"items"`
	PickupMinutes int               `json:"pickup_minutes"`
	Note          string            `json:"note"`
}

type createOrderItem struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type createOrderResp struct {
	OrderID         int64  `json:"order_id"`
	OrderNo         int    `json:"order_no"`
	TotalCents      int    `json:"total_cents"`
	QRImageURL      string `json:"qr_url"`
	PaymentIntentID string `json:"payment_intent_id"`
	PickupTime      string `json:"pickup_time"`
	TestMode        bool   `json:"test_mode"`
}

func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	user := s.requireAuth(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method == http.MethodGet {
		s.listOrders(w, r, user)
		return
	}
	if r.Method == http.MethodPost {
		s.createOrder(w, r, user)
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request, user *SessionUser) {
	// Guard: shop must be open to place orders.
	shopOpen, _ := storage.GetShopOpen(s.deps.DB)
	if !shopOpen {
		writeError(w, http.StatusForbidden, "shop is closed")
		return
	}
	var req createOrderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "cart is empty")
		return
	}

	// Validate items and compute total from the DB menu (never trust client prices).
	var orderItems []model.OrderItem
	totalCents := 0
	for _, ri := range req.Items {
		if ri.Quantity < 1 || ri.Quantity > 20 {
			writeError(w, http.StatusBadRequest, "invalid quantity")
			return
		}
		mi, err := storage.MenuItemBySKU(s.deps.DB, ri.SKU)
		if err != nil {
			fmt.Printf("lookup menu item %s: %v\n", ri.SKU, err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if mi == nil || !mi.Available {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("item %s not available", ri.SKU))
			return
		}
		orderItems = append(orderItems, model.OrderItem{
			SKU:       mi.SKU,
			Name:      mi.Name,
			UnitCents: mi.PriceCents,
			Quantity:  ri.Quantity,
		})
		totalCents += mi.PriceCents * ri.Quantity
	}

	// Validate pickup minutes.
	validPickup := false
	for _, opt := range []int{0, 15, 30, 45} {
		if req.PickupMinutes == opt {
			validPickup = true
			break
		}
	}
	if !validPickup {
		writeError(w, http.StatusBadRequest, "invalid pickup time")
		return
	}

	// Upsert customer to get the DB id.
	custID, err := storage.UpsertCustomer(s.deps.DB, user.UserID, user.Username, user.FirstName)
	if err != nil {
		fmt.Printf("upsert customer: %v\n", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Get next order number.
	orderNo, err := storage.NextOrderNo(s.deps.DB)
	if err != nil {
		fmt.Printf("next order no: %v\n", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Create the order (status=awaiting_payment, no payment intent yet).
	createdAt := time.Now().UTC()
	orderID, err := storage.CreateOrder(s.deps.DB, model.Order{
		OrderNo:       orderNo,
		CustomerID:    custID,
		UserID:        user.UserID,
		ChatID:        user.UserID, // DM chat id == user id in Telegram
		TotalCents:    totalCents,
		PickupMinutes: req.PickupMinutes,
		Note:          req.Note,
		CreatedAt:     createdAt,
	}, orderItems)
	if err != nil {
		fmt.Printf("create order: %v\n", err)
		writeError(w, http.StatusInternalServerError, "failed to create order")
		return
	}

	// Create + confirm the Stripe PayNow PaymentIntent.
	desc := fmt.Sprintf("Order #%d — %d items", orderNo, len(orderItems))
	piID, err := payment.CreatePayNowIntent(s.deps.StripeSecret, totalCents, orderID, orderNo, desc)
	if err != nil {
		fmt.Printf("create payment intent: %v\n", err)
		writeError(w, http.StatusInternalServerError, "payment setup failed")
		return
	}
	_ = storage.SetPaymentIntent(s.deps.DB, orderID, piID)

	result, err := payment.ConfirmPayNow(s.deps.StripeSecret, piID)
	if err != nil {
		fmt.Printf("confirm payment intent: %v\n", err)
		writeError(w, http.StatusInternalServerError, "payment confirmation failed")
		return
	}

	writeJSON(w, http.StatusOK, createOrderResp{
		OrderID:         orderID,
		OrderNo:         orderNo,
		TotalCents:      totalCents,
		QRImageURL:      result.QRImageURL,
		PaymentIntentID: piID,
		PickupTime:      pickupLabel(req.PickupMinutes, createdAt, s.deps.SGT),
		TestMode:        s.isTestMode(),
	})
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request, user *SessionUser) {
	cust, err := storage.CustomerByUserID(s.deps.DB, user.UserID)
	if err != nil {
		fmt.Printf("lookup customer: %v\n", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if cust == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	orders, err := storage.OrdersByCustomer(s.deps.DB, cust.ID, 20)
	if err != nil {
		fmt.Printf("list orders: %v\n", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	type orderResp struct {
		OrderNo      int    `json:"order_no"`
		Status       string `json:"status"`
		TotalCents   int    `json:"total_cents"`
		PickupMinutes int   `json:"pickup_minutes"`
		CreatedAt    string `json:"created_at"`
	}
	out := make([]orderResp, 0, len(orders))
	for _, o := range orders {
		out = append(out, orderResp{
			OrderNo: o.OrderNo,
			Status:  o.Status,
			TotalCents: o.TotalCents,
			PickupMinutes: o.PickupMinutes,
			CreatedAt: o.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- order status (polled by frontend) ---

type orderStatusResp struct {
	OrderNo         int    `json:"order_no"`
	Status          string `json:"status"`
	TotalCents      int    `json:"total_cents"`
	PickupMinutes   int    `json:"pickup_minutes"`
	PickupTime      string `json:"pickup_time"`
	PaymentIntentID string `json:"payment_intent_id"`
}

func (s *Server) handleOrderStatus(w http.ResponseWriter, r *http.Request) {
	user := s.requireAuth(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// Extract order id from path: /api/orders/{id}
	path := strings.TrimPrefix(r.URL.Path, "/api/orders/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "missing order id")
		return
	}
	orderID, err := strconv.ParseInt(path, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order id")
		return
	}

	order, err := storage.OrderByID(s.deps.DB, orderID)
	if err != nil {
		fmt.Printf("lookup order: %v\n", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if order == nil {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}
	// Security: only the owner can see their order.
	if order.UserID != user.UserID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// If still awaiting_payment, poll Stripe to check if paid (fallback if
	// the webhook hasn't fired yet).
	if order.Status == model.StatusAwaitingPayment && order.StripePaymentIntent != "" {
		status, chargeID, err := payment.GetIntentStatus(s.deps.StripeSecret, order.StripePaymentIntent)
		if err == nil && status == "succeeded" {
			_ = storage.MarkPaid(s.deps.DB, order.ID, chargeID)
			order.Status = model.StatusPaid
			// Notify staff (best effort) — handled by the webhook normally;
			// the polling path is a fallback.
		}
	}

	resp := orderStatusResp{
		OrderNo:         order.OrderNo,
		Status:          order.Status,
		TotalCents:      order.TotalCents,
		PickupMinutes:   order.PickupMinutes,
		PickupTime:      pickupLabel(order.PickupMinutes, order.CreatedAt, s.deps.SGT),
		PaymentIntentID: order.StripePaymentIntent,
	}
	writeJSON(w, http.StatusOK, resp)
}

// downloadQR fetches the QR image bytes from a URL (used if the bot needs to
// forward the QR to chat, though in the web app flow the frontend loads it
// directly).
func downloadQR(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// pickupLabel returns "ASAP" for 0 minutes, or the exact pickup clock time
// in the given timezone (24-hour format, e.g. "15:45").
func pickupLabel(mins int, base time.Time, loc *time.Location) string {
	if mins == 0 {
		return "ASAP"
	}
	return base.In(loc).Add(time.Duration(mins) * time.Minute).Format("15:04")
}

// handleTestPay simulates a successful PayNow payment for test mode. Only
// works when STRIPE_SECRET_KEY starts with "sk_test_". Marks the order paid
// and triggers staff + customer notifications, just like the real webhook.
func (s *Server) handleTestPay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.isTestMode() {
		writeError(w, http.StatusForbidden, "test mode only")
		return
	}
	user := s.requireAuth(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// Extract order id from path: /api/orders/test-pay/{id}
	path := strings.TrimPrefix(r.URL.Path, "/api/orders/test-pay/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "missing order id")
		return
	}
	orderID, err := strconv.ParseInt(path, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order id")
		return
	}

	order, err := storage.OrderByID(s.deps.DB, orderID)
	if err != nil || order == nil {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}
	if order.UserID != user.UserID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if order.Status != model.StatusAwaitingPayment {
		writeError(w, http.StatusBadRequest, "order is "+order.Status)
		return
	}

	if err := storage.MarkPaid(s.deps.DB, order.ID, "test_charge"); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Trigger the same notifications as the real Stripe webhook.
	if s.deps.NotifyStaff != nil {
		go s.deps.NotifyStaff(order.ID)
	}
	if s.deps.NotifyCustomer != nil {
		go s.deps.NotifyCustomer(order.ID)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "paid"})
}