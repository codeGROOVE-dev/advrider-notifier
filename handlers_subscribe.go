package main

import (
	"net/http"
	"strings"
	"time"
)

func (m *Monitor) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rate limiting by IP
	ip := getClientIP(r)
	if !subscribeRateLimiter.allow(ip) {
		m.logger.Warn("Rate limit exceeded", "ip", ip)
		http.Error(w, "Too many subscription requests. Please try again later.", http.StatusTooManyRequests)
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
	post, err := m.fetchLatestPost(r.Context(), baseThreadURL)
	if err != nil {
		m.logger.Warn("Failed to verify thread", "url", baseThreadURL, "error", err)
		http.Error(w, "Could not verify thread URL - make sure it's a valid ADVRider thread", http.StatusBadRequest)
		return
	}

	// Load or create subscription
	sub, err := m.loadSubscriptionByEmail(r.Context(), email)
	if err != nil {
		// Check if it's a "not found" error
		if isNotFoundError(err) {
			// Create new subscription
			token, err := generateToken()
			if err != nil {
				m.logger.Error("Failed to generate token", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}

			sub = &Subscription{
				Email:   email,
				Token:   token,
				Threads: make(map[string]*Thread),
			}
		} else {
			m.logger.Error("Failed to load subscription", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	// Check if already subscribed to this thread
	if _, exists := sub.Threads[threadID]; exists {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if err := templates.ExecuteTemplate(w, "already_subscribed.tmpl", map[string]string{"Email": email}); err != nil {
			m.logger.Error("Failed to render template", "template", "already_subscribed.tmpl", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
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
	sub.Threads[threadID] = &Thread{
		ThreadURL:    baseThreadURL,
		ThreadID:     threadID,
		LastPostID:   post.ID,
		LastPostTime: lastPostTime,
		LastPolledAt: time.Time{}, // Will be set on first poll
		CreatedAt:    time.Now().UTC(),
	}

	if err := m.saveSubscription(r.Context(), sub); err != nil {
		m.logger.Error("Failed to save subscription", "error", err)
		http.Error(w, "Failed to create subscription", http.StatusInternalServerError)
		return
	}

	m.logger.Info("Subscription created", "email", email, "thread_id", threadID, "ip", ip)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if err := templates.ExecuteTemplate(w, "subscribed.tmpl", map[string]string{"Email": email}); err != nil {
		m.logger.Error("Failed to render template", "template", "subscribed.tmpl", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
