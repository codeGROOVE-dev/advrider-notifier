package main

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	advRiderThreadRegex = regexp.MustCompile(`^https://advrider\.com/f/threads/[^/]+\.(\d+)(/.*)?$`)
	emailRegex          = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

	// Templates
	templates *template.Template
)

func init() {
	// Load all templates
	templates = template.Must(template.ParseGlob("tmpl/*.tmpl"))
}

// Rate limiter for subscriptions (max 5 per IP per hour)
type rateLimiter struct {
	mu      sync.Mutex
	clients map[string][]time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		clients: make(map[string][]time.Time),
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Hour)

	// Clean old entries
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

var subscribeRateLimiter = newRateLimiter()

// Helper functions

func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (Cloud Run)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Fallback to RemoteAddr
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
	slug := extractThreadSlug(threadURL)
	if slug == "" {
		return "", errors.New("could not extract thread slug")
	}

	return fmt.Sprintf("https://advrider.com/f/threads/%s.%s/", slug, threadID), nil
}

func extractThreadSlug(threadURL string) string {
	u, err := url.Parse(threadURL)
	if err != nil {
		return ""
	}

	parts := regexp.MustCompile(`/threads/([^/]+)\.(\d+)`).FindStringSubmatch(u.Path)
	if len(parts) >= 2 {
		return parts[1]
	}

	return ""
}

func isNotFoundError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "storage: object doesn't exist")
}

func escapeHTMLAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}
