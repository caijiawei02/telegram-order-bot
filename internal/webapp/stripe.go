package webapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/caijiawei02/telegram-order-bot/internal/model"
	"github.com/caijiawei02/telegram-order-bot/internal/storage"
	stripe "github.com/stripe/stripe-go/v76"
	webhook "github.com/stripe/stripe-go/v76/webhook"
)

// StripeWebhookHandler returns an http.HandlerFunc that processes Stripe
// webhook events. It verifies the signature, handles
// payment_intent.succeeded and payment_intent.payment_failed.
func (s *Server) StripeWebhookHandler(notifyStaff func(orderID int64), notifyCustomer func(orderID int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const maxBody = 1 << 20 // 1 MiB
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read body")
			return
		}

		sig := r.Header.Get("Stripe-Signature")
		if sig == "" {
			writeError(w, http.StatusBadRequest, "missing signature")
			return
		}

		event, err := webhook.ConstructEvent(body, sig, s.deps.StripeWebhookSecret)
		if err != nil {
			fmt.Printf("stripe webhook signature verification failed: %v\n", err)
			writeError(w, http.StatusBadRequest, "signature verification failed")
			return
		}

	switch event.Type {
	case "payment_intent.succeeded":
		s.handlePaymentSucceeded(w, r, event, notifyStaff, notifyCustomer)
		case "payment_intent.payment_failed":
			s.handlePaymentFailed(w, r, event)
		default:
			// Acknowledge unhandled events quickly.
			w.WriteHeader(http.StatusOK)
		}
	}
}

func (s *Server) handlePaymentSucceeded(w http.ResponseWriter, r *http.Request, event stripe.Event, notifyStaff func(int64), notifyCustomer func(int64)) {
	var pi stripe.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
		fmt.Printf("unmarshal payment_intent.succeeded: %v\n", err)
		writeError(w, http.StatusBadRequest, "failed to parse event")
		return
	}

	order, err := storage.OrderByPaymentIntent(s.deps.DB, pi.ID)
	if err != nil {
		fmt.Printf("lookup order by payment intent %s: %v\n", pi.ID, err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if order == nil {
		fmt.Printf("no order found for payment intent %s\n", pi.ID)
		w.WriteHeader(http.StatusOK)
		return
	}

	chargeID := ""
	if pi.LatestCharge != nil {
		chargeID = pi.LatestCharge.ID
	}
	if err := storage.MarkPaid(s.deps.DB, order.ID, chargeID); err != nil {
		fmt.Printf("mark paid (order %d): %v\n", order.ID, err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Notify staff + customer in the background (best effort).
	if notifyStaff != nil {
		go notifyStaff(order.ID)
	}
	if notifyCustomer != nil {
		go notifyCustomer(order.ID)
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePaymentFailed(w http.ResponseWriter, r *http.Request, event stripe.Event) {
	var pi stripe.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
		fmt.Printf("unmarshal payment_intent.payment_failed: %v\n", err)
		writeError(w, http.StatusBadRequest, "failed to parse event")
		return
	}

	order, err := storage.OrderByPaymentIntent(s.deps.DB, pi.ID)
	if err != nil || order == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Mark as failed so it doesn't linger in awaiting_payment forever.
	_ = storage.SetStatus(s.deps.DB, order.ID, model.StatusFailed)
	w.WriteHeader(http.StatusOK)
}

// verifyStripeRequest is a small helper used internally for testing.
func verifyStripeRequest(body []byte, sig, secret string) error {
	_, err := webhook.ConstructEvent(body, sig, secret)
	return err
}

// bufferBody reads an http.Request body into a buffer and restores it so
// downstream handlers can re-read it. (Not currently used but kept for
// potential future middleware needs.)
func bufferBody(r *http.Request) {
	if r.Body == nil {
		return
	}
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
}