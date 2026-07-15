# Backend — Coffee Order Telegram Bot

A Telegram Web App bot for ordering coffee with PayNow (Stripe) payment and
in-store pickup. Customers browse a menu in an embedded Telegram Web App,
add items to a cart, pay via PayNow QR, and pick up at the counter. Staff
manage orders via inline buttons in a Telegram group.

## Stack

| Concern        | Library |
|----------------|---------|
| Language        | Go 1.24 |
| Telegram Bot    | `gopkg.in/telebot.v3` v3.3.8 (webhook mode, nginx terminates TLS) |
| Telegram Web App | Vanilla HTML/CSS/JS, embedded via `go:embed` (no build step, no Node.js) |
| Payment         | Stripe PayNow via `github.com/stripe/stripe-go/v76` (Direct API, server-side) |
| Storage         | SQLite via `modernc.org/sqlite` v1.34.5 (pure-Go, no CGO) |
| Scheduler       | `github.com/robfig/cron/v3` (Asia/Singapore, seconds-field enabled) |
| Config          | `github.com/joho/godotenv` + OS env |

## Module layout

```
cmd/bot/main.go              Entrypoint: load env, open DB, seed menu, start
                             telebot (webhook), web app server, Stripe webhook,
                             cron (00:00 cleanup, 23:59 daily summary), /health.
internal/
  bot/
    handler.go               /start (Web App button), /help, /chatid, staff commands
                             (/ready, /cancel, /orders, /openshop, /closeshop),
                             inline button callbacks (Mark Ready, Cancel),
                             NotifyStaffNewOrder, NotifyCustomerPaid,
                             notifyCustomerReady.
    summary.go               SendDailySalesSummary (cron 23:59 SGT),
                             DeleteStalePendingOrders (cron 00:00 SGT).
  webapp/
    server.go                HTTP mux, route registration, go:embed static files.
    auth.go                  Telegram initData HMAC verification, signed session cookies.
    handlers.go             /api/auth, /api/menu, /api/orders (create + list),
                             /api/orders/pending (resume after reopen),
                             /api/orders/{id} (status polling),
                             /api/orders/test-pay/{id} (test mode only).
    stripe.go                Stripe webhook handler (payment_intent.succeeded/failed).
    static/
      index.html             Single-page app (menu, cart, payment, success, error screens).
      styles.css             Telegram-themed (uses WebApp themeParams).
      app.js                 State management, API calls, cart persistence (localStorage),
                             pending order resume, payment polling, menu card steppers.
  payment/
    stripe.go                CreatePayNowIntent, ConfirmPayNow, GetIntentStatus, GetPayNowQR.
  storage/
    db.go                    Open + migrations (customers, menu_items, orders, order_items, settings).
    customers.go             UpsertCustomer, CustomerByUserID.
    orders.go                CreateOrder, OrderByID, OrderByPaymentIntent, OrderByOrderNo,
                             PendingOrderByUser, MarkPaid, SetStatus, SetPaymentIntent,
                             OrdersByCustomer, DeleteStalePending, DaySalesSummary,
                             DayOrderCount, PendingStaffOrders.
    menu.go                  SeedMenuIfEmpty, LoadMenu, MenuItemBySKU.
    settings.go              GetShopOpen, SetShopOpen, GetSetting, SetSetting.
  model/
    order.go                 Order, OrderItem structs + status constants.
    customer.go              Customer struct.
    menu.go                  MenuItem struct.
docs/BACKEND.md, docs/references.md
.env.example
Dockerfile, docker-compose.yml, docker-compose.prod.yml
.github/workflows/deploy.yml
```

## Data model

All timestamps stored as RFC3339 strings in UTC. Prices stored in cents
(integer). The Web App displays `$X.XX` (cents/100). Stripe API receives
cents. No float arithmetic in the backend.

### `customers`
| column | type | notes |
|---|---|---|
| id | INTEGER PK AUTOINCREMENT | |
| user_id | INTEGER NOT NULL UNIQUE | Telegram user id |
| username | TEXT NOT NULL DEFAULT '' | |
| first_name | TEXT NOT NULL DEFAULT '' | |
| last_seen_at | TEXT (RFC3339 UTC) | updated on every auth |

### `menu_items`
| column | type | notes |
|---|---|---|
| id | INTEGER PK AUTOINCREMENT | |
| sku | TEXT NOT NULL UNIQUE | e.g. "latte_hot" |
| name | TEXT NOT NULL | "Latte (Hot)" |
| category | TEXT NOT NULL | "Latte", "Americano" |
| price_cents | INTEGER NOT NULL | 300 = $3.00 (SGD cents) |
| available | INTEGER NOT NULL DEFAULT 1 | 0 to hide |
| sort_order | INTEGER NOT NULL DEFAULT 0 | display order |

Seeded on first run if empty: Latte (Hot) $3.00, Latte (Iced) $3.50,
Americano (Hot) $2.00, Americano (Iced) $2.50.

### `orders`
| column | type | notes |
|---|---|---|
| id | INTEGER PK AUTOINCREMENT | |
| order_no | INTEGER NOT NULL UNIQUE | human-readable (max+1) |
| customer_id | INTEGER NOT NULL FK customers | |
| user_id | INTEGER NOT NULL | Telegram user id (denormalized) |
| chat_id | INTEGER NOT NULL | DM chat id (= user_id) for sending updates |
| status | TEXT NOT NULL DEFAULT 'awaiting_payment' | see status lifecycle below |
| total_cents | INTEGER NOT NULL | |
| pickup_minutes | INTEGER NOT NULL DEFAULT 0 | 0=ASAP, 15/30/45 |
| note | TEXT NOT NULL DEFAULT '' | |
| stripe_payment_intent | TEXT NOT NULL DEFAULT '' | pi_... |
| stripe_charge_id | TEXT NOT NULL DEFAULT '' | ch_... (set on webhook) |
| created_at | TEXT (RFC3339 UTC) | |
| paid_at | TEXT (RFC3339 UTC, nullable) | set when webhook marks paid |

Indexes: `idx_orders_customer(customer_id, created_at)`, `idx_orders_status`,
`idx_orders_payment_intent(stripe_payment_intent)`.

### `order_items`
| column | type | notes |
|---|---|---|
| id | INTEGER PK AUTOINCREMENT | |
| order_id | INTEGER NOT NULL FK orders (ON DELETE CASCADE) | |
| sku | TEXT NOT NULL | |
| name | TEXT NOT NULL | snapshot of menu item name at order time |
| unit_cents | INTEGER NOT NULL | snapshot price at order time |
| quantity | INTEGER NOT NULL | |

Index: `idx_order_items_order(order_id)`.

### `settings`
| column | type | notes |
|---|---|---|
| key | TEXT PRIMARY KEY | e.g. "shop_open" |
| value | TEXT NOT NULL | "1" (open) or "0" (closed) |

Single key-value store. `shop_open` defaults to "1" (open) if absent.

## Order status lifecycle

```
awaiting_payment  →  paid  →  ready
     ↓                ↓
  failed         cancelled
```

- **awaiting_payment**: order created at checkout, QR sent. Invisible to
  customer and staff. If never paid, stays here (cleaned up by 00:00 cron).
- **paid**: Stripe webhook `payment_intent.succeeded` fires → `MarkPaid()`.
  Staff group notified with inline buttons. Customer DMs payment confirmation.
- **ready**: staff tap "Mark Ready" button (or `/ready`) → customer DMs
  itemized "ready for pickup" message.
- **cancelled**: staff tap "Cancel" button (or `/cancel`) → customer DMs
  cancellation.
- **failed**: Stripe webhook `payment_intent.payment_failed`, or PaymentIntent
  expired (detected on pending order resume).

No "preparing" or "completed" status — the lifecycle is intentionally minimal.

## Shop open/close

Staff run `/openshop` or `/closeshop` in the staff group. The bot stores the
state in the `settings` table (`shop_open` key).

- When **closed**: `GET /api/menu` returns `shop_open: false`, `POST /api/orders`
  returns `403 "shop is closed"`. The Web App shows a "Shop is currently closed"
  screen.
- When **open**: normal operation.
- Default: open (if no `shop_open` row exists).

## Payment flow (PayNow via Stripe Direct API)

```
1. Web App checkout → POST /api/orders
   (backend checks shop is open)
2. Backend creates order (status=awaiting_payment) in DB
3. Backend calls Stripe: POST /v1/payment_intents
   (amount, currency=sgd, payment_method_types=["paynow"], metadata.order_id)
4. Backend calls Stripe: POST /v1/payment_intents/{id}/confirm
   (payment_method_data.type=paynow)
5. Stripe returns PaymentIntent with status=requires_action,
   next_action.paynow_display_qr_code.image_url_png
6. Backend returns QR URL + test_mode flag to the Web App
7. Web App displays QR (non-clickable), polls GET /api/orders/{id} every 2s
8. Customer saves QR, scans with bank app (DBS/OCBC/UOB), pays
9. Stripe fires payment_intent.succeeded webhook → POST /stripe/webhook
10. Backend verifies signature, MarkPaid(), notifies staff + customer
11. Web App polling sees status=paid → shows success screen, clears cart
```

### Test mode

When `STRIPE_SECRET_KEY` starts with `sk_test_`, the Web App shows a
"[Test Mode] Simulate Payment Success" button on the QR screen. Tapping it
calls `POST /api/orders/test-pay/{id}` which marks the order paid and
triggers the same notifications as the real webhook. The endpoint returns
`403` in production (live key).

### Pending order resume

If the user closes the Web App and reopens it while an order is
`awaiting_payment`:
1. Frontend calls `GET /api/orders/pending` on load.
2. Backend finds the user's most recent `awaiting_payment` order.
3. Backend re-fetches the QR URL from Stripe via `GetPayNowQR`.
4. If the PaymentIntent is still `requires_action` → returns the order + QR.
   Frontend jumps to the payment screen.
5. If the PaymentIntent has expired → marks the order `failed`, returns null.
   Frontend loads the menu fresh.

### Cart persistence

The cart is saved to `localStorage` on every change. On app load (when no
pending order), the cart is restored. On successful payment, the cart is
cleared.

## Customer notifications (bot DMs)

| Event | DM message |
|---|---|
| Payment confirmed | "Payment received! Order #X — pickup at HH:MM. We'll notify you when it's ready." |
| Staff: Mark Ready | "Order #X is ready for pickup!" + itemized list + "Show #X at the counter." |
| Staff: Cancel | "Order #X has been cancelled. Please expect a refund if you have paid." |

## Staff order notifications (staff group)

Each paid order sends a separate message with inline buttons:

```
🆕 #1 — @john, pickup: 15:45
   2× Latte (Hot)
   Total: $6.00
[ Mark Ready ] [ Cancel ]
```

On tap, the message is edited to remove buttons and append the status
("Ready" or "Cancelled"), and the customer is DM'd.

## HTTP listeners

| Port | Path | Purpose |
|------|------|---------|
| :8080 | /tg/{secret}/ | Telegram webhook (telebot) |
| :8083 | / | Web App static files + API |
| :8083 | /api/auth | Telegram initData verification |
| :8083 | /api/menu | Menu items + shop_open status |
| :8083 | /api/orders | Create order (POST), list orders (GET) |
| :8083 | /api/orders/pending | Resume pending payment (GET) |
| :8083 | /api/orders/test-pay/{id} | Simulate payment (test mode only) |
| :8083 | /api/orders/{id} | Order status (polled by frontend) |
| :8083 | /stripe/webhook | Stripe webhook |
| :8081 | /health | Healthcheck |

nginx routes `/tg/` → :8080, `/stripe/` → :8083, everything else → :8083.

## Authentication (Telegram Web App initData)

1. Web App loads, reads `Telegram.WebApp.initData`.
2. POST /api/auth with `{init_data: "..."}`.
3. Backend verifies HMAC: `secret = HMAC_SHA256("WebAppData", bot_token)`,
   then `hash == HMAC_SHA256(secret, sorted_data_check_string)`.
4. Backend upserts customer, sets signed HttpOnly session cookie
   (`userID|expires|HMAC(userID|expires, SESSION_SECRET)`).
5. Subsequent API calls use the cookie. No session state in DB.

## Cron jobs

| Schedule (SGT) | Job |
|---|---|
| `0 0 0 * * *` (00:00) | DeleteStalePendingOrders — removes orders stuck in `awaiting_payment` from before today. |
| `0 59 23 * * *` (23:59) | SendDailySalesSummary — posts order count, items sold, revenue, and per-item breakdown to the staff group. |

## Configuration (env)

| Var | Required | Default | Meaning |
|---|---|---|---|
| `TELEGRAM_TOKEN` | yes | — | BotFather token |
| `STRIPE_SECRET_KEY` | yes | — | Stripe API key (sk_test_... or sk_live_...) |
| `STRIPE_WEBHOOK_SECRET` | yes | — | Stripe webhook signing secret (whsec_...) |
| `STRIPE_WEBHOOK_PATH` | no | /stripe/webhook | Path for the Stripe webhook |
| `SESSION_SECRET` | yes | — | Random string for signing session cookies |
| `STAFF_CHAT_ID` | yes | — | Barista group chat id (int64) |
| `TZ` | no | Asia/Singapore | IANA timezone |
| `DB_PATH` | no | coffee.db | SQLite file path |
| `PICKUP_OPTIONS` | no | 0,15,30,45 | Comma-separated pickup minutes |
| `WEBAPP_PUBLIC_URL` | yes | — | HTTPS URL for the Web App |
| `WEBAPP_LISTEN` | no | :8083 | Web app HTTP listen addr |
| `WEBHOOK_PUBLIC_URL` | yes | — | HTTPS URL Telegram POSTs updates to |
| `WEBHOOK_LISTEN` | yes | — | Telegram webhook listen addr |
| `WEBHOOK_SECRET_TOKEN` | yes | — | Telegram secret token header |
| `HEALTH_LISTEN` | no | :8081 | /health endpoint addr |

## Staff commands (staff group only)

| Command | Action |
|---|---|
| `/ready <no>` | Mark order ready, DM customer with itemized details |
| `/cancel <no>` | Mark order cancelled, DM customer |
| `/orders` | List pending (paid) orders |
| `/openshop` | Open the shop — customers can order |
| `/closeshop` | Close the shop — new orders disabled |
| `/chatid` | Show the current chat's id (works in any chat) |

Staff commands are ignored outside `STAFF_CHAT_ID` (guarded by `isStaffChat`).
Inline buttons (`Mark Ready`, `Cancel`) are the primary staff interface; text
commands are a fallback.

## Deployment

Deployed to `your-domain.example.com` on the same Oracle Cloud Free Tier VM
as cailorie, using the same pattern:

- **Docker Compose** (`docker-compose.prod.yml`): builds the Go binary, runs
  it as a container named `coffee-bot`, joins the external `shared` Docker
  network so fyp's nginx can reach it.
- **nginx** (`~/fyp/backend/nginx.conf` on the VM): terminates TLS (Let's
  Encrypt), routes by Host header:
  - `/tg/` → `coffee-bot:8080` (Telegram webhook)
  - `/stripe/` → `coffee-bot:8083` (Stripe webhook)
  - `/` → `coffee-bot:8083` (Web App + API)
  - `/health` → `coffee-bot:8081` (healthcheck)
- **GitHub Actions** (`.github/workflows/deploy.yml`): on push to main, SSH
  to the VM, pull latest, `docker compose up --build`, reload fyp's nginx.
  Requires GitHub secrets `OCI_VM_IP` and `OCI_SSH_PRIVATE_KEY`.

### Setup steps (one-time on the VM)

1. Add DNS A record for `your-domain` → VM IP.
2. Get Let's Encrypt cert (stop nginx first, use standalone mode):
   ```
   docker compose -f ~/fyp/backend/docker-compose.prod.yml stop nginx
   sudo certbot certonly --standalone -d your-domain.example.com
   docker compose -f ~/fyp/backend/docker-compose.prod.yml start nginx
   ```
3. `docker network connect shared caregiver-nginx` (if not already connected).
4. Reload nginx: `docker compose -f ~/fyp/backend/docker-compose.prod.yml exec -T nginx nginx -s reload`
5. `cp .env.example .env` and fill in secrets.
6. In Stripe dashboard: add webhook endpoint `https://your-domain.example.com/stripe/webhook`.
7. In BotFather: set webhook `https://your-domain.example.com/tg/<secret>/`.

### Accessing the SQLite DB

```bash
docker exec -it coffee-bot sqlite3 /var/lib/coffee/data.db
```

(`sqlite3` is included in the Docker image via `apk add sqlite`.)