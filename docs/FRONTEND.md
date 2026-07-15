# Frontend — Coffee Order Web App

Single-page vanilla HTML/CSS/JS app embedded in Telegram, served via
`go:embed` from `internal/webapp/static/`. No build step, no Node.js.

## Screens

| Screen | Id | Purpose |
|---|---|---|
| Menu | `screen-menu` | Category tabs, menu cards with +/- steppers, sticky cart bar. |
| Cart | `screen-cart` | Itemized cart, pickup-time options, "Place Order & Pay" button. |
| Payment | `screen-payment` | PayNow QR image, payment-status spinner, test-pay button (test mode only). |
| Success | `screen-success` | "Paid!" confirmation with order number and pickup time. |
| Error | `screen-error` | Generic error with message and "Back to Menu" button. |
| Shop Closed | `screen-closed` | Shown when `/api/menu` returns `shop_open: false`. Static screen — "Sorry, shop is closed :(" with no buttons. |

## Shop-closed flow

On init, after auth, the app calls `/api/menu`. If the response has
`shop_open: false`, `loadMenu()` calls `showShopClosed()` → `showClosed()`,
which reveals `screen-closed`. This is a dedicated screen (not the error
screen) because a closed shop is not an error condition, and the generic
error screen's "Back to Menu" button would dead-end on a blank menu (menu
items are never loaded when the shop is closed).

## Files

- `index.html` — screen markup.
- `styles.css` — Telegram-themed styles (uses `WebApp.themeParams`).
- `app.js` — state, API calls, cart persistence (`localStorage`), payment polling.