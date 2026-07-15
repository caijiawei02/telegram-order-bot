// Package payment wraps Stripe PayNow payment intents.
package payment

import (
	"fmt"

	stripe "github.com/stripe/stripe-go/v76"
	pintent "github.com/stripe/stripe-go/v76/paymentintent"
)

// PayNowResult holds the QR code data returned after confirming a PayNow
// PaymentIntent.
type PayNowResult struct {
	PaymentIntentID string
	QRImageURL      string // image_url_png from next_action.paynow_display_qr_code
}

// CreatePayNowIntent creates a PaymentIntent for PayNow with the given amount
// (in cents, SGD) and order metadata. Returns the PaymentIntent id.
func CreatePayNowIntent(secretKey string, amountCents int, orderID int64, orderNo int, description string) (string, error) {
	stripe.Key = secretKey
	params := &stripe.PaymentIntentParams{
		Amount:        stripe.Int64(int64(amountCents)),
		Currency:      stripe.String(string(stripe.CurrencySGD)),
		Description:   stripe.String(description),
		PaymentMethodTypes: stripe.StringSlice([]string{
			"paynow",
		}),
		AutomaticPaymentMethods: &stripe.PaymentIntentAutomaticPaymentMethodsParams{
			Enabled: stripe.Bool(false),
		},
	}
	params.Metadata = map[string]string{
		"order_id":   fmt.Sprintf("%d", orderID),
		"order_no":   fmt.Sprintf("%d", orderNo),
	}
	pi, err := pintent.New(params)
	if err != nil {
		return "", fmt.Errorf("create payment intent: %w", err)
	}
	return pi.ID, nil
}

// ConfirmPayNow confirms a PaymentIntent using the paynow payment method.
// After confirmation, the PaymentIntent transitions to requires_action and
// the next_action contains the PayNow QR code info.
func ConfirmPayNow(secretKey string, paymentIntentID string) (*PayNowResult, error) {
	stripe.Key = secretKey
	params := &stripe.PaymentIntentConfirmParams{
		PaymentMethodData: &stripe.PaymentIntentPaymentMethodDataParams{
			Type: stripe.String("paynow"),
		},
	}
	pi, err := pintent.Confirm(paymentIntentID, params)
	if err != nil {
		return nil, fmt.Errorf("confirm payment intent: %w", err)
	}

	// Extract QR code URL from next_action.paynow_display_qr_code.
	if pi.NextAction == nil || pi.NextAction.PayNowDisplayQRCode == nil {
		return nil, fmt.Errorf("no paynow qr code in payment intent response (status=%s)", pi.Status)
	}
	qr := pi.NextAction.PayNowDisplayQRCode
	return &PayNowResult{
		PaymentIntentID: pi.ID,
		QRImageURL:      qr.ImageURLPNG,
	}, nil
}

// GetIntentStatus retrieves a PaymentIntent's current status and (if
// succeeded) the associated charge id.
func GetIntentStatus(secretKey string, paymentIntentID string) (status string, chargeID string, err error) {
	stripe.Key = secretKey
	pi, err := pintent.Get(paymentIntentID, nil)
	if err != nil {
		return "", "", fmt.Errorf("get payment intent: %w", err)
	}
	charge := ""
	if pi.LatestCharge != nil {
		charge = pi.LatestCharge.ID
	}
	return string(pi.Status), charge, nil
}