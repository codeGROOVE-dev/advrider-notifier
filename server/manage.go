package server

import (
	"crypto/subtle"
	"net/http"
	"net/url"
)

func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	// Rate limiting by IP to prevent token enumeration
	ip := clientIP(r)
	if !globalRateLimiter.allow(ip) {
		s.logger.Warn("Rate limit exceeded", "ip", ip)
		http.Error(w, "Too many requests. Please try again later.", http.StatusTooManyRequests)
		return
	}

	// Redirect to manage page with token
	token := r.URL.Query().Get("token")
	if token == "" || len(token) != 64 {
		http.Error(w, "Invalid or missing token", http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/manage?token="+url.QueryEscape(token), http.StatusSeeOther)
}

func (s *Server) handleManage(w http.ResponseWriter, r *http.Request) {
	// Rate limiting by IP to prevent token enumeration
	ip := clientIP(r)
	if !globalRateLimiter.allow(ip) {
		s.logger.Warn("Rate limit exceeded", "ip", ip)
		http.Error(w, "Too many requests. Please try again later.", http.StatusTooManyRequests)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" || len(token) != 64 {
		http.Error(w, "Invalid or missing token", http.StatusBadRequest)
		return
	}

	// Find subscription by token
	sub, err := s.store.LoadByToken(r.Context(), token)
	if err != nil {
		s.logger.Warn("Subscription not found for token", "error", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		if err := templates.ExecuteTemplate(w, "not_found.tmpl", nil); err != nil {
			s.logger.Error("Failed to render template", "template", "not_found.tmpl", "error", err)
			http.Error(w, "Subscription not found", http.StatusNotFound)
		}
		return
	}

	// Handle unsubscribe from specific thread
	if r.Method == http.MethodPost {
		action := r.FormValue("action")
		threadID := r.FormValue("thread_id")

		// Verify token matches (constant-time comparison for security)
		formToken := r.FormValue("token")
		if subtle.ConstantTimeCompare([]byte(formToken), []byte(token)) != 1 {
			http.Error(w, "Invalid token", http.StatusForbidden)
			return
		}

		if action == "unsubscribe" && threadID != "" {
			delete(sub.Threads, threadID)

			if len(sub.Threads) == 0 {
				// No threads left, delete subscription entirely
				if err := s.store.Delete(r.Context(), sub.Email); err != nil {
					s.logger.Error("Failed to delete subscription", "error", err)
					http.Error(w, "Failed to unsubscribe", http.StatusInternalServerError)
					return
				}
				s.logger.Info("All subscriptions removed", "email", sub.Email)

				// Show unsubscribed page instead of redirecting (token no longer valid)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				if err := templates.ExecuteTemplate(w, "unsubscribed.tmpl", nil); err != nil {
					s.logger.Error("Failed to render template", "template", "unsubscribed.tmpl", "error", err)
					http.Error(w, "Internal server error", http.StatusInternalServerError)
				}
				return
			}

			// Save updated subscription
			if err := s.store.Save(r.Context(), sub); err != nil {
				s.logger.Error("Failed to save subscription", "error", err)
				http.Error(w, "Failed to unsubscribe", http.StatusInternalServerError)
				return
			}
			s.logger.Info("Thread unsubscribed", "email", sub.Email, "thread_id", threadID)

			// Redirect back to manage page (subscription still has other threads)
			http.Redirect(w, r, "/manage?token="+url.QueryEscape(token), http.StatusSeeOther)
			return
		}

		if action == "unsubscribe_all" {
			if err := s.store.Delete(r.Context(), sub.Email); err != nil {
				s.logger.Error("Failed to delete subscription", "error", err)
				http.Error(w, "Failed to unsubscribe", http.StatusInternalServerError)
				return
			}

			s.logger.Info("All subscriptions removed", "email", sub.Email)

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			if err := templates.ExecuteTemplate(w, "unsubscribed.tmpl", nil); err != nil {
				s.logger.Error("Failed to render template", "template", "unsubscribed.tmpl", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
			return
		}
	}

	// Display manage page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(http.StatusOK)

	// Prepare threads for template
	type ThreadData struct {
		ThreadID  string
		ThreadURL string
		CreatedAt string
	}
	threads := make([]ThreadData, 0, len(sub.Threads))
	for threadID, thread := range sub.Threads {
		threads = append(threads, ThreadData{
			ThreadID:  threadID,
			ThreadURL: thread.ThreadURL,
			CreatedAt: thread.CreatedAt.Format("Jan 2, 2006"),
		})
	}

	data := map[string]any{
		"Email":   sub.Email,
		"Token":   token,
		"Threads": threads,
	}

	if err := templates.ExecuteTemplate(w, "manage.tmpl", data); err != nil {
		s.logger.Error("Failed to render template", "template", "manage.tmpl", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
