package main

import (
	"net/http"
)

func (m *Monitor) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")

	if err := templates.ExecuteTemplate(w, "index.tmpl", nil); err != nil {
		m.logger.Error("Failed to render template", "template", "index.tmpl", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
