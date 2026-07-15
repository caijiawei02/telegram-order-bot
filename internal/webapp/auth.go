package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/caijiawei02/telegram-order-bot/internal/storage"
)

// sessionLifetime is how long a session cookie stays valid.
const sessionLifetime = 7 * 24 * time.Hour

// SessionUser holds the authenticated user's Telegram identity.
type SessionUser struct {
	UserID    int64
	Username  string
	FirstName string
}

// verifyInitData validates the Telegram WebApp initData string and extracts
// the user. Returns nil if the data is invalid.
func verifyInitData(initData, botToken string) *SessionUser {
	parsed, err := url.ParseQuery(initData)
	if err != nil {
		return nil
	}
	hash := parsed.Get("hash")
	if hash == "" {
		return nil
	}
	parsed.Del("hash")

	// Build the data-check string: sorted "key=value" joined by newline.
	var keys []string
	for k := range parsed {
		keys = append(keys, k)
	}
	// Sort keys.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(parsed.Get(k))
	}
	dataCheckString := sb.String()

	// secret = HMAC_SHA256("WebAppData", botToken)
	secretKey := hmac.New(sha256.New, []byte("WebAppData"))
	secretKey.Write([]byte(botToken))
	secret := secretKey.Sum(nil)

	// hash = HMAC_SHA256(secret, dataCheckString)
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(dataCheckString))
	computedHash := hex.EncodeToString(h.Sum(nil))

	if !hmac.Equal([]byte(hash), []byte(computedHash)) {
		return nil
	}

	// Extract user JSON.
	userJSON := parsed.Get("user")
	if userJSON == "" {
		return nil
	}
	var u struct {
		ID        int64  `json:"id"`
		Username  string `json:"username"`
		FirstName string `json:"first_name"`
	}
	if err := json.Unmarshal([]byte(userJSON), &u); err != nil {
		return nil
	}
	return &SessionUser{
		UserID:    u.ID,
		Username:  u.Username,
		FirstName: u.FirstName,
	}
}

// makeSessionCookie creates a signed session cookie for the given user.
func makeSessionCookie(u SessionUser, sessionSecret string) *http.Cookie {
	expires := time.Now().Add(sessionLifetime)
	value := signSession(u, sessionSecret, expires)
	return &http.Cookie{
		Name:     "session",
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	}
}

// signSession creates "userID|expires|hmac(userID|expires)".
func signSession(u SessionUser, sessionSecret string, expires time.Time) string {
	expStr := strconv.FormatInt(expires.Unix(), 10)
	payload := fmt.Sprintf("%d|%s", u.UserID, expStr)
	h := hmac.New(sha256.New, []byte(sessionSecret))
	h.Write([]byte(payload))
	sig := hex.EncodeToString(h.Sum(nil))
	return fmt.Sprintf("%d|%s|%s", u.UserID, expStr, sig)
}

// verifySessionCookie validates the session cookie and returns the user.
// Returns nil if the cookie is missing, tampered, or expired.
func verifySessionCookie(r *http.Request, sessionSecret string) *SessionUser {
	cookie, err := r.Cookie("session")
	if err != nil {
		return nil
	}
	parts := strings.SplitN(cookie.Value, "|", 3)
	if len(parts) != 3 {
		return nil
	}
	userID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil
	}
	expUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil
	}
	if time.Now().Unix() > expUnix {
		return nil
	}
	// Recompute signature.
	payload := fmt.Sprintf("%d|%s", userID, parts[1])
	h := hmac.New(sha256.New, []byte(sessionSecret))
	h.Write([]byte(payload))
	expectedSig := hex.EncodeToString(h.Sum(nil))
	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return nil
	}
	return &SessionUser{UserID: userID}
}

// handleAuth verifies the Telegram initData, upserts the customer, and sets
// a session cookie. Responds with the user's info.
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		InitData string `json:"init_data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.InitData == "" {
		http.Error(w, "missing init_data", http.StatusBadRequest)
		return
	}
	user := verifyInitData(body.InitData, s.deps.BotToken)
	if user == nil {
		http.Error(w, "invalid init_data", http.StatusUnauthorized)
		return
	}
	_, err := storage.UpsertCustomer(s.deps.DB, user.UserID, user.Username, user.FirstName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "upsert customer: %v\n", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, makeSessionCookie(*user, s.deps.SessionSecret))
	json.NewEncoder(w).Encode(map[string]any{
		"user_id":    user.UserID,
		"username":   user.Username,
		"first_name": user.FirstName,
	})
}