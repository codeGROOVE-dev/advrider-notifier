package server

import (
	"advrider-notifier/pkg/notifier"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rate limiting by IP
	ip := clientIP(r)
	if !globalRateLimiter.allow(ip) {
		s.logger.Warn("Rate limit exceeded", "ip", ip)
		http.Error(w, "Too many requests. Please try again later.", http.StatusTooManyRequests)
		return
	}

	// Parse and validate inputs
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	threadURL := strings.TrimSpace(r.FormValue("thread_url"))
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))

	// Validate email format
	if !isValidEmail(email) {
		http.Error(w, "Invalid email address", http.StatusBadRequest)
		return
	}

	// Validate ADVRider thread URL
	matches := advRiderThreadRegex.FindStringSubmatch(threadURL)
	if matches == nil {
		http.Error(w, "Invalid ADVRider thread URL - must contain '/f/threads/' (e.g., https://advrider.com/f/threads/example.123456/)", http.StatusBadRequest)
		return
	}

	threadID := matches[1]

	// Normalize URL (remove page numbers, anchors)
	baseThreadURL, err := normalizeThreadURL(threadURL, threadID)
	if err != nil {
		http.Error(w, "Invalid thread URL", http.StatusBadRequest)
		return
	}

	// Verify thread exists by fetching it
	post, err := s.scraper.LatestPost(r.Context(), baseThreadURL)
	if err != nil {
		s.logger.Warn("Failed to verify thread", "url", baseThreadURL, "error", err)

		// Check if it's a 403 Forbidden error (login-required forum)
		if s.isHTTP403(err) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			if err := templates.ExecuteTemplate(w, "forbidden.tmpl", map[string]string{
				"Email":     email,
				"ThreadURL": threadURL,
			}); err != nil {
				s.logger.Error("Failed to render template", "template", "forbidden.tmpl", "error", err)
				http.Error(w, "This thread is in a login-required forum (like Jo Momma) and cannot be monitored. We apologize for the inconvenience.", http.StatusForbidden)
			}
			return
		}

		http.Error(w, "Could not verify thread URL - make sure it's a valid ADVRider thread", http.StatusBadRequest)
		return
	}

	// Load or create subscription
	sub, err := s.store.LoadByEmail(r.Context(), email)
	if err != nil {
		// Check if it's a "not found" error
		if s.isNotFound(err) {
			// Create new subscription with deterministic token from email
			token := s.store.TokenFromEmail(email)

			sub = &notifier.Subscription{
				Email:   email,
				Token:   token,
				Threads: make(map[string]*notifier.Thread),
			}
		} else {
			s.logger.Error("Failed to load subscription", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	// Check if already subscribed to this thread
	if _, exists := sub.Threads[threadID]; exists {
		// Set cookie to remember email address
		setEmailCookie(w, email)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if err := templates.ExecuteTemplate(w, "already_subscribed.tmpl", map[string]string{"Email": email}); err != nil {
			s.logger.Error("Failed to render template", "template", "already_subscribed.tmpl", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	// Enforce thread limit per user (prevent resource exhaustion)
	const maxThreadsPerUser = 100
	if len(sub.Threads) >= maxThreadsPerUser {
		s.logger.Warn("Thread limit exceeded", "email", email, "current_count", len(sub.Threads))
		http.Error(w, fmt.Sprintf("Maximum thread limit reached (%d threads per user)", maxThreadsPerUser), http.StatusBadRequest)
		return
	}

	// Parse post timestamp to initialize LastPostTime
	var lastPostTime time.Time
	if post.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, post.Timestamp); err == nil {
			lastPostTime = t
		}
	}

	// Add thread to subscription
	sub.Threads[threadID] = &notifier.Thread{
		ThreadURL:    baseThreadURL,
		ThreadID:     threadID,
		LastPostID:   post.ID,
		LastPostTime: lastPostTime,
		LastPolledAt: time.Time{}, // Will be set on first poll
		CreatedAt:    time.Now().UTC(),
	}

	if err := s.store.Save(r.Context(), sub); err != nil {
		s.logger.Error("Failed to save subscription", "error", err)
		http.Error(w, "Failed to create subscription", http.StatusInternalServerError)
		return
	}

	// Send welcome email
	userAgent := r.Header.Get("User-Agent")
	if err := s.emailer.SendWelcome(r.Context(), sub, sub.Threads[threadID], ip, userAgent); err != nil {
		// Log error but don't fail the subscription
		s.logger.Warn("Failed to send welcome email", "email", email, "error", err)
	}

	s.logger.Info("Subscription created", "email", email, "thread_id", threadID, "ip", ip)

	// Set cookie to remember email address
	setEmailCookie(w, email)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if err := templates.ExecuteTemplate(w, "subscribed.tmpl", map[string]string{"Email": email}); err != nil {
		s.logger.Error("Failed to render template", "template", "subscribed.tmpl", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
