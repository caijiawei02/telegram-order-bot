// Package webapp serves the Telegram Web App frontend and the JSON API.
package webapp

import (
	"database/sql"
	"embed"
	"io/fs"
	"net/http"
	"time"
)

//go:embed static/*
var staticFS embed.FS

// Deps holds the dependencies injected from main.
type Deps struct {
	DB                  *sql.DB
	BotToken            string
	SessionSecret       string
	StripeSecret        string
	StripeWebhookSecret string
	StaffChatID         int64
	Currency            string
	SGT                 *time.Location
	NotifyStaff         func(orderID int64)
	NotifyCustomer      func(orderID int64)
}

// Server is the HTTP server for the web app, API, Stripe webhook, and health.
type Server struct {
	mux       *http.ServeMux
	staticDir fs.FS
	deps      Deps
}

// NewServer constructs a Server and registers all routes.
func NewServer(deps Deps) *Server {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	s := &Server{
		mux:       http.NewServeMux(),
		staticDir: sub,
		deps:      deps,
	}
	s.registerRoutes()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// registerRoutes wires up all HTTP routes.
func (s *Server) registerRoutes() {
	// API routes.
	s.mux.HandleFunc("/api/auth", s.handleAuth)
	s.mux.HandleFunc("/api/menu", s.handleMenu)
	s.mux.HandleFunc("/api/orders", s.handleOrders)
	s.mux.HandleFunc("/api/orders/pending", s.handlePendingOrder) // before /api/orders/
	s.mux.HandleFunc("/api/orders/test-pay/", s.handleTestPay)   // POST, before /api/orders/
	s.mux.HandleFunc("/api/orders/", s.handleOrderStatus)        // /api/orders/{id}

	// Health.
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Static files (catch-all, must be last).
	s.mux.Handle("/", http.FileServer(http.FS(s.staticDir)))
}