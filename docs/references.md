# References

External documentation consulted during the implementation of this project.

## Telegram Bot API — Web Apps

- **URL**: https://core.telegram.org/bots/webapps
- **What was referenced**: Telegram Web App (Mini App) lifecycle, `WebApp.initData`
  authentication scheme, `web_app` button type, theme parameters.
- **How used**: The customer-facing ordering UI is a Telegram Web App. The
  `initData` HMAC verification in `internal/webapp/auth.go` follows the
  official validation scheme: `secret = HMAC_SHA256("WebAppData", bot_token)`,
  then `hash = HMAC_SHA256(secret, data_check_string)`.

## Telegram Bot API — Payments (reference, not used)

- **URL**: https://core.telegram.org/bots/payments
- **What was referenced**: Telegram native invoice flow (Invoice, PreCheckout,
  SuccessfulPayment types).
- **How used**: Initially planned but **not used** — Telegram native invoices
  don't support PayNow. We use Stripe Direct API instead. Documented here
  for context on the architecture decision.

## telebot.v3 (Go Telegram bot framework)

- **URL**: https://pkg.go.dev/gopkg.in/telebot.v3
- **Source**: https://github.com/go-telebot/telebot
- **What was referenced**: `telebot.Webhook` poller, `telebot.WebApp` button
  type, `ReplyMarkup`, `Chat` recipient, handler registration patterns.
- **How used**: Bot framework for the Telegram webhook (`:8080`), `/start`
  handler with a WebApp button, and staff group commands.

## Stripe — PayNow

- **URL**: https://stripe.com/docs/payments/paynow
- **Direct API guide**: https://docs.stripe.com/payments/paynow/accept-a-payment.md?payment-ui=direct-api
- **PaymentIntent object**: https://docs.stripe.com/api/payment_intents/object.md
- **What was referenced**: PayNow is a QR-code payment method for Singapore.
  Create a PaymentIntent with `payment_method_types=["paynow"]`, confirm with
  `payment_method_data.type="paynow"`, retrieve the QR image URL from
  `next_action.paynow_display_qr_code.image_url_png`. Webhook event
  `payment_intent.succeeded` confirms payment.
- **How used**: `internal/payment/stripe.go` implements
  `CreatePayNowIntent`, `ConfirmPayNow` (extracts the QR URL), and
  `GetIntentStatus` (polling fallback). `internal/webapp/stripe.go`
  handles the webhook via `webhook.ConstructEvent` for signature verification.

## Stripe Go SDK

- **URL**: https://github.com/stripe/stripe-go
- **Version**: v76.25.0
- **What was referenced**: `paymentintent.New`, `paymentintent.Confirm`,
  `paymentintent.Get`, `webhook.ConstructEvent`.
- **Note**: The field for PayNow QR data is `PayNowDisplayQRCode` (camelCase
  "Now"), not `PaynowDisplayQRCode`. This was discovered during build.

## SQLite (modernc.org/sqlite)

- **URL**: https://pkg.go.dev/modernc.org/sqlite
- **Version**: v1.34.5 (pinned for Go 1.24 compatibility; later versions
  require Go 1.25+)
- **What was referenced**: Pure-Go SQLite driver (no CGO), WAL mode, foreign
  keys, `ON DELETE CASCADE`.
- **How used**: `internal/storage/db.go` opens the DB with WAL + busy_timeout
  + foreign_keys pragmas. Migrations create 4 tables with indexes.

## godotenv

- **URL**: https://github.com/joho/godotenv
- **What was referenced**: `.env` file loading for local dev.

## robfig/cron/v3

- **URL**: https://github.com/robfig/cron
- **What was referenced**: Cron scheduler with location + seconds-field
  support.
- **How used**: Two cron jobs in `cmd/bot/main.go`: `0 0 0 * * *` (cleanup)
  and `0 59 23 * * *` (daily sales summary), both in Asia/Singapore.

## Reference project: cailorie

- **Path**: `~/cailorie` (local project by the same author)
- **What was referenced**: Project structure (`cmd/bot`, `internal/{bot,
  storage,model}`), storage patterns (SQLite migrations, RFC3339 timestamps,
  `tzOffset`), webhook mode setup, Dockerfile/compose shape, cron scheduling.
- **How used**: This project mirrors cailorie's conventions for consistency:
  same module layout, same storage patterns, same Docker/deploy shape, same
  env-driven config approach.