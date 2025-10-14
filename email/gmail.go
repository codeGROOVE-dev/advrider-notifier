package email

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"google.golang.org/api/gmail/v1"
)

// GmailProvider sends emails via Gmail API.
type GmailProvider struct {
	service *gmail.Service
	logger  *slog.Logger
}

// NewGmailProvider creates a new Gmail email provider.
func NewGmailProvider(service *gmail.Service, logger *slog.Logger) *GmailProvider {
	return &GmailProvider{
		service: service,
		logger:  logger,
	}
}

// sanitizeEmailHeader removes newlines and control characters to prevent header injection.
// This is critical security: RFC 5322 headers are newline-delimited, so any newline in
// a header value allows an attacker to inject arbitrary headers or body content.
func sanitizeEmailHeader(s string) string {
	// Remove all CR, LF, and other control characters (ASCII 0-31 and 127)
	// These could be used to inject headers or manipulate email structure
	var result strings.Builder
	for _, r := range s {
		// Allow only printable characters (space through ~) and valid UTF-8
		if r >= 32 && r != 127 {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// Send sends an email via Gmail API.
func (g *GmailProvider) Send(ctx context.Context, to, subject, htmlBody string) error {
	// Sanitize headers to prevent email header injection attacks
	// Remove all newlines and control characters that could inject headers
	to = sanitizeEmailHeader(to)
	subject = sanitizeEmailHeader(subject)

	// Create MIME message
	// Note: From address is set by Gmail API based on the authenticated account
	var msg strings.Builder
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	msg.WriteString(htmlBody)
	encoded := base64.URLEncoding.EncodeToString([]byte(msg.String()))

	return retry.Do(
		func() error {
			g.logger.Info("Gmail API request starting",
				"method", "POST",
				"endpoint", "users.messages.send",
				"to", to,
				"subject", subject)

			startTime := time.Now()
			_, err := g.service.Users.Messages.Send("me", &gmail.Message{
				Raw: encoded,
			}).Context(ctx).Do()
			duration := time.Since(startTime)

			if err != nil {
				g.logger.Warn("Gmail API send failed, will retry",
					"to", to,
					"duration_ms", duration.Milliseconds(),
					"error", err)
				return err
			}

			g.logger.Info("Gmail API request completed",
				"endpoint", "users.messages.send",
				"to", to,
				"duration_ms", duration.Milliseconds(),
				"status", "success")

			return nil
		},
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.MaxJitter(10*time.Second),
		retry.Context(ctx),
		retry.OnRetry(func(n uint, err error) {
			g.logger.Info("Retrying Gmail email send after error", "attempt", n, "error", err)
		}),
	)
}
