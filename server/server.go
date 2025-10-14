// Package server handles HTTP endpoints and request routing.
package server

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"advrider-notifier/pkg/notifier"
)

//go:embed tmpl/*.tmpl
var templateFS embed.FS

var (
	advRiderThreadRegex = regexp.MustCompile(`^https://advrider\.com/f/threads/[^/]+\.(\d+)(/.*)?$`)
	emailRegex          = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

	// Templates.
	templates = template.Must(template.ParseFS(templateFS, "tmpl/*.tmpl"))
)

// Scraper interface for verifying threads.
type Scraper interface {
	LatestPost(ctx context.Context, threadURL string) (*notifier.Post, string, error)
}

// Store interface for subscription management.
type Store interface {
	TokenFromEmail(email string) string
	LoadByEmail(ctx context.Context, email string) (*notifier.Subscription, error)
	LoadByToken(ctx context.Context, token string) (*notifier.Subscription, error)
	Save(ctx context.Context, sub *notifier.Subscription) error
	Delete(ctx context.Context, email string) error
}

// Emailer interface for sending welcome emails.
type Emailer interface {
	SendWelcome(ctx context.Context, sub *notifier.Subscription, thread *notifier.Thread, ip, userAgent string) error
}

// Poller interface for triggering checks.
type Poller interface {
	CheckAll(ctx context.Context) error
}

// IsHTTP403 checks if an error is a 403 Forbidden error.
type IsHTTP403 func(error) bool

// IsNotFound checks if an error is a not found error.
type IsNotFound func(error) bool

// Server handles HTTP requests.
type Server struct {
	scraper    Scraper
	store      Store
	emailer    Emailer
	poller     Poller
	isHTTP403  IsHTTP403
	isNotFound IsNotFound
	baseURL    string
	logger     *slog.Logger
}

// Config holds server configuration.
type Config struct {
	Scraper    Scraper
	Store      Store
	Emailer    Emailer
	Poller     Poller
	IsHTTP403  IsHTTP403
	IsNotFound IsNotFound
	BaseURL    string
	Logger     *slog.Logger
}

// New creates a new HTTP server handler.
func New(cfg *Config) *Server {
	return &Server{
		scraper:    cfg.Scraper,
		store:      cfg.Store,
		emailer:    cfg.Emailer,
		poller:     cfg.Poller,
		isHTTP403:  cfg.IsHTTP403,
		isNotFound: cfg.IsNotFound,
		baseURL:    cfg.BaseURL,
		logger:     cfg.Logger,
	}
}

// ServeHTTP sets up all routes and starts the server.
func (s *Server) ServeHTTP(mediaFS embed.FS, port string) error {
	http.HandleFunc("/", s.handleRoot)
	http.HandleFunc("/health", s.handleHealth)
	http.HandleFunc("/pollz", s.handlePoll)
	http.HandleFunc("/subscribe", s.handleSubscribe)
	http.HandleFunc("/unsubscribe", s.handleUnsubscribe)
	http.HandleFunc("/manage", s.handleManage)

	// Serve static media files
	mediaSubFS, err := fs.Sub(mediaFS, "media")
	if err != nil {
		return fmt.Errorf("create media sub-filesystem: %w", err)
	}
	http.Handle("/media/", http.StripPrefix("/media/", http.FileServer(http.FS(mediaSubFS))))

	// Configure server with timeouts to prevent resource exhaustion
	server := &http.Server{
		Addr:              ":" + port,
		ReadTimeout:       10 * time.Second,  // Time to read request headers and body
		WriteTimeout:      30 * time.Second,  // Time to write response
		IdleTimeout:       120 * time.Second, // Time to keep connection alive between requests
		ReadHeaderTimeout: 5 * time.Second,   // Time to read request headers only
	}

	s.logger.Info("Starting HTTP server", "port", port)
	return server.ListenAndServe()
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")

	// Get saved email from cookie
	savedEmail := emailCookie(r)

	data := map[string]string{
		"SavedEmail": savedEmail,
	}

	if err := templates.ExecuteTemplate(w, "index.tmpl", data); err != nil {
		s.logger.Error("Failed to render template", "template", "index.tmpl", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, `{"status":"healthy"}`); err != nil {
		s.logger.Warn("Failed to write health response", "error", err)
		return
	}
}

func (s *Server) handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.logger.Info("Poll endpoint triggered")

	if err := s.poller.CheckAll(r.Context()); err != nil {
		s.logger.Error("Poll check failed", "error", err)
		http.Error(w, "Check failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, `{"status":"completed"}`); err != nil {
		s.logger.Warn("Failed to write response", "error", err)
	}
}

// Rate limiter for all HTTP requests (max 5 per second per IP).
type rateLimiter struct {
	clients     map[string][]time.Time
	mu          sync.Mutex
	lastCleanup time.Time
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Second)

	// Periodic cleanup of old IP entries (every minute)
	if now.Sub(rl.lastCleanup) > time.Minute {
		for ip, timestamps := range rl.clients {
			var recent []time.Time
			for _, ts := range timestamps {
				if ts.After(cutoff) {
					recent = append(recent, ts)
				}
			}
			if len(recent) == 0 {
				delete(rl.clients, ip) // Remove empty entries to prevent memory leak
			} else {
				rl.clients[ip] = recent
			}
		}
		rl.lastCleanup = now
	}

	// Clean old entries for this specific IP
	timestamps := rl.clients[ip]
	var recent []time.Time
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			recent = append(recent, ts)
		}
	}

	if len(recent) >= 5 {
		return false
	}

	recent = append(recent, now)
	rl.clients[ip] = recent
	return true
}

var globalRateLimiter = &rateLimiter{
	clients: make(map[string][]time.Time),
}

func clientIP(r *http.Request) string {
	// Security: X-Forwarded-For can be spoofed by clients, so we don't trust it.
	// In Cloud Run, the load balancer sets this, but attackers can still manipulate it.
	// Use RemoteAddr which is the actual TCP connection source.
	// Note: This means all traffic from Cloud Run's load balancer will appear as the same IP
	// in local dev, but in production Cloud Run will have consistent IPs per connection.
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

func isValidEmail(email string) bool {
	if len(email) < 3 || len(email) > 254 {
		return false
	}

	// Use mail.ParseAddress for robust validation
	_, err := mail.ParseAddress(email)
	return err == nil && emailRegex.MatchString(email)
}

func normalizeThreadURL(threadURL, threadID string) (string, error) {
	u, err := url.Parse(threadURL)
	if err != nil {
		return "", errors.New("could not extract thread slug")
	}

	parts := regexp.MustCompile(`/threads/([^/]+)\.(\d+)`).FindStringSubmatch(u.Path)
	if len(parts) < 2 {
		return "", errors.New("could not extract thread slug")
	}

	slug := parts[1]
	return fmt.Sprintf("https://advrider.com/f/threads/%s.%s/", slug, threadID), nil
}

func setEmailCookie(w http.ResponseWriter, email string) {
	cookie := &http.Cookie{
		Name:     "advrider_email",
		Value:    email,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)
}

func emailCookie(r *http.Request) string {
	cookie, err := r.Cookie("advrider_email")
	if err != nil {
		return ""
	}
	// Validate the email from cookie before using it
	// This prevents injection attacks via cookie manipulation
	if !isValidEmail(cookie.Value) {
		return ""
	}
	return cookie.Value
}
