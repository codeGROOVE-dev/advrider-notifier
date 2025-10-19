package server

import (
	"advrider-notifier/pkg/notifier"
	"fmt"
	"net/http"
	"strings"
	"time"
)

//nolint:funlen // HTTP handler with comprehensive validation - complexity justified for security
func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
		//nolint:revive // Error message - line length unavoidable for clarity
		http.Error(w, "Invalid ADVRider thread URL - must contain '/f/threads/' (e.g., https://advrider.com/f/threads/example.123456/ or https://www.advrider.com/f/threads/example.123456/)", http.StatusBadRequest)
		return
	}

	threadID := matches[2]

	// Normalize URL (remove page numbers, anchors)
	baseThreadURL, err := normalizeThreadURL(threadURL, threadID)
	if err != nil {
		http.Error(w, "Invalid thread URL", http.StatusBadRequest)
		return
	}

	// Verify thread exists by fetching it
	post, threadTitle, err := s.scraper.LatestPost(r.Context(), baseThreadURL)
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
				//nolint:revive // Error message - line length unavoidable for clarity
				http.Error(w, "This thread is in a login-required forum (like Jo Momma) and cannot be monitored. We apologize for the inconvenience.", http.StatusForbidden)
			}
			return
		}

		http.Error(w, "Could not verify thread URL - make sure it's a valid ADVRider thread", http.StatusBadRequest)
		return
	}

	// Validate thread title was successfully parsed
	if threadTitle == "" {
		s.logger.Warn("Thread title is empty", "url", baseThreadURL)
		http.Error(w, "Could not parse thread title - the page structure may have changed or the thread may not exist", http.StatusBadRequest)
		return
	}

	// Load or create subscription
	sub, err := s.store.LoadByEmail(r.Context(), email)
	if err != nil {
		// If not a "not found" error, it's a real error
		if !s.isNotFound(err) {
			s.logger.Error("Failed to load subscription", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Create new subscription with deterministic token from email
		token := s.store.TokenFromEmail(email)
		sub = &notifier.Subscription{
			Email:   email,
			Token:   token,
			Threads: make(map[string]*notifier.Thread),
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
	const maxThreadsPerUser = 20
	if len(sub.Threads) >= maxThreadsPerUser {
		s.logger.Warn("Thread limit exceeded", "email", email, "current_count", len(sub.Threads))
		http.Error(w, fmt.Sprintf("Maximum thread limit reached (%d threads per user)", maxThreadsPerUser), http.StatusBadRequest)
		return
	}

	// Validate that we have a valid post ID before creating subscription
	if post.ID == "" {
		s.logger.Error("Latest post has empty ID", "url", baseThreadURL, "title", threadTitle)
		http.Error(w, "Could not determine latest post ID - please try again", http.StatusInternalServerError)
		return
	}

	// Validate and parse post timestamp to initialize LastPostTime
	if post.Timestamp == "" {
		s.logger.Error("Latest post has empty timestamp", "url", baseThreadURL, "title", threadTitle, "post_id", post.ID)
		http.Error(w, "Could not determine post timestamp - the page structure may have changed", http.StatusInternalServerError)
		return
	}

	lastPostTime, err := time.Parse(time.RFC3339, post.Timestamp)
	if err != nil {
		//nolint:revive // Log message with multiple fields - line length unavoidable
		s.logger.Error("Failed to parse post timestamp", "url", baseThreadURL, "title", threadTitle, "post_id", post.ID, "timestamp", post.Timestamp, "error", err)
		http.Error(w, "Could not parse post timestamp - the page structure may have changed", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC()

	s.logger.Info("Creating subscription with latest post ID",
		"email", email,
		"thread_id", threadID,
		"thread_title", threadTitle,
		"last_post_id", post.ID,
		"last_post_time", lastPostTime.Format(time.RFC3339))

	// Add thread to subscription
	// Leave LastPolledAt as zero time - this signals to the poller that this is a new subscription
	// The poller will check it immediately on the next poll cycle
	sub.Threads[threadID] = &notifier.Thread{
		ThreadURL:    baseThreadURL,
		ThreadID:     threadID,
		ThreadTitle:  threadTitle,
		LastPostID:   post.ID,
		LastPostTime: lastPostTime,
		LastPolledAt: time.Time{}, // Zero time signals new subscription needing immediate check
		CreatedAt:    now,
	}

	if err := s.store.Save(r.Context(), sub); err != nil {
		s.logger.Error("Failed to save subscription", "error", err)
		http.Error(w, "Failed to create subscription", http.StatusInternalServerError)
		return
	}

	s.logger.Info("Subscription created", "email", email, "thread_id", threadID)

	// Send welcome email
	userAgent := r.Header.Get("User-Agent")
	if err := s.emailer.SendWelcome(r.Context(), sub, sub.Threads[threadID], "", userAgent); err != nil {
		// Log error but don't fail the subscription
		s.logger.Warn("Failed to send welcome email", "email", email, "error", err)
	}

	// For new subscriptions, the thread will be checked on the next poll cycle (within 5 minutes)
	// We can't use CalculateInterval here because LastPolledAt is zero (not yet polled)
	crawlTimeStr := "5 minutes"
	nextCrawlTime := now.Add(5 * time.Minute)

	s.logger.Info("Subscription completed",
		"email", email,
		"thread_id", threadID,
		"next_crawl_in", crawlTimeStr)

	// Set cookie to remember email address
	setEmailCookie(w, email)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if err := templates.ExecuteTemplate(w, "subscribed.tmpl", map[string]any{
		"Email":       email,
		"CrawlTime":   crawlTimeStr,
		"NextCrawlAt": nextCrawlTime.Format("3:04 PM MST"),
	}); err != nil {
		s.logger.Error("Failed to render template", "template", "subscribed.tmpl", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
