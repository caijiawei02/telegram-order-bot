package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/caijiawei02/telegram-order-bot/internal/bot"
	"github.com/caijiawei02/telegram-order-bot/internal/storage"
	"github.com/caijiawei02/telegram-order-bot/internal/webapp"
	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
	telebot "gopkg.in/telebot.v3"
)

func main() {
	_ = godotenv.Load()

	tgToken := os.Getenv("TELEGRAM_TOKEN")
	tzName := os.Getenv("TZ")
	dbPath := os.Getenv("DB_PATH")
	staffChatIDStr := os.Getenv("STAFF_CHAT_ID")
	stripeSecret := os.Getenv("STRIPE_SECRET_KEY")
	stripeWebhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	sessionSecret := os.Getenv("SESSION_SECRET")
	webAppPublicURL := os.Getenv("WEBAPP_PUBLIC_URL")
	webAppListen := os.Getenv("WEBAPP_LISTEN")
	stripeWebhookPath := os.Getenv("STRIPE_WEBHOOK_PATH")

	// Telegram webhook config.
	webhookPublicURL := os.Getenv("WEBHOOK_PUBLIC_URL")
	webhookListen := os.Getenv("WEBHOOK_LISTEN")
	webhookSecret := os.Getenv("WEBHOOK_SECRET_TOKEN")
	healthListen := os.Getenv("HEALTH_LISTEN")

	if tgToken == "" {
		log.Fatal("TELEGRAM_TOKEN is required")
	}
	if stripeSecret == "" {
		log.Fatal("STRIPE_SECRET_KEY is required")
	}
	if stripeWebhookSecret == "" {
		log.Fatal("STRIPE_WEBHOOK_SECRET is required")
	}
	if sessionSecret == "" {
		log.Fatal("SESSION_SECRET is required")
	}
	if webhookPublicURL == "" || webhookListen == "" {
		log.Fatal("WEBHOOK_PUBLIC_URL and WEBHOOK_LISTEN are required (webhook mode)")
	}
	if webhookSecret == "" {
		log.Fatal("WEBHOOK_SECRET_TOKEN is required")
	}
	if webAppPublicURL == "" {
		log.Fatal("WEBAPP_PUBLIC_URL is required (HTTPS URL for the Telegram Web App)")
	}
	if webAppListen == "" {
		webAppListen = ":8083"
	}
	if stripeWebhookPath == "" {
		stripeWebhookPath = "/stripe/webhook"
	}
	if tzName == "" {
		tzName = "Asia/Singapore"
	}
	if dbPath == "" {
		dbPath = "coffee.db"
	}
	if healthListen == "" {
		healthListen = ":8081"
	}
	staffChatID, err := strconv.ParseInt(staffChatIDStr, 10, 64)
	if err != nil {
		log.Fatal("STAFF_CHAT_ID is required (must be a valid int64 chat id)")
	}

	loc, err := time.LoadLocation(tzName)
	if err != nil {
		log.Fatalf("load timezone %q: %v", tzName, err)
	}
	if err := os.Setenv("TZ", tzName); err == nil {
		if l, e := time.LoadLocation(tzName); e == nil {
			time.Local = l
		}
	}

	// Open SQLite.
	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer db.Close()

	// Seed the menu if empty.
	if err := storage.SeedMenuIfEmpty(db); err != nil {
		log.Fatalf("seed menu: %v", err)
	}

	// Build telebot (webhook).
	pref := telebot.Settings{
		Token:     tgToken,
		ParseMode: telebot.ModeHTML,
		Poller: &telebot.Webhook{
			Listen:         webhookListen,
			SecretToken:    webhookSecret,
			Endpoint:       &telebot.WebhookEndpoint{PublicURL: webhookPublicURL},
			MaxConnections: 40,
			DropUpdates:     true,
		},
	}
	tgBot, err := telebot.NewBot(pref)
	if err != nil {
		log.Fatalf("telegram bot: %v", err)
	}

	pickupOptions := parsePickupOptions(os.Getenv("PICKUP_OPTIONS"))

	// Register bot handlers.
	h := bot.NewHandler(tgBot, db, loc, staffChatID, webAppPublicURL, pickupOptions)
	h.Register()

	// Build the web app server.
	webAppServer := webapp.NewServer(webapp.Deps{
		DB:                  db,
		BotToken:            tgToken,
		SessionSecret:       sessionSecret,
		StripeSecret:        stripeSecret,
		StripeWebhookSecret: stripeWebhookSecret,
		StaffChatID:         staffChatID,
		Currency:            "SGD",
	})

	// Register the Stripe webhook handler on the web app mux.
	webAppMux := http.NewServeMux()
	webAppMux.Handle(stripeWebhookPath, webAppServer.StripeWebhookHandler(func(orderID int64) {
		h.NotifyStaffNewOrder(orderID)
	}))
	// Everything else goes to the web app server.
	webAppMux.Handle("/", webAppServer)

	// Cron: 00:00 SGT cleanup + 23:59 SGT daily sales summary.
	c := cron.New(cron.WithLocation(loc), cron.WithSeconds())
	_, err = c.AddFunc("0 0 0 * * *", func() {
		bot.DeleteStalePendingOrders(db, loc)
	})
	if err != nil {
		log.Fatalf("schedule cleanup: %v", err)
	}
	_, err = c.AddFunc("0 59 23 * * *", func() {
		fireTime := time.Now().In(loc)
		log.Printf("firing daily sales summary at %s", fireTime.Format(time.RFC3339))
		bot.SendDailySalesSummary(tgBot, db, loc, staffChatID, fireTime)
	})
	if err != nil {
		log.Fatalf("schedule daily summary: %v", err)
	}
	c.Start()
	defer c.Stop()

	log.Printf("coffee order bot starting (TZ=%s)", tzName)
	log.Printf("telegram webhook: listen=%s, public=%s", webhookListen, webhookPublicURL)
	log.Printf("web app: listen=%s, public=%s", webAppListen, webAppPublicURL)
	log.Printf("staff chat id: %d", staffChatID)

	// Health endpoint.
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, "ok")
		})
		if err := http.ListenAndServe(healthListen, mux); err != nil {
			log.Printf("health server on %s: %v", healthListen, err)
		}
	}()

	// Web app server (includes static files, API, and Stripe webhook).
	go func() {
		if err := http.ListenAndServe(webAppListen, webAppMux); err != nil {
			log.Fatalf("web app server on %s: %v", webAppListen, err)
		}
	}()

	// Run telebot until SIGINT/SIGTERM.
	go func() {
		tgBot.Start()
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Printf("shutdown signal received, stopping...")
	tgBot.Stop()
}

func parsePickupOptions(env string) []int {
	if env == "" {
		return []int{0, 15, 30, 45}
	}
	var out []int
	for _, part := range strings.Split(env, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return []int{0, 15, 30, 45}
	}
	return out
}