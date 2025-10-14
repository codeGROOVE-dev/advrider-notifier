package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/codeGROOVE-dev/retry"
)

// BrevoProvider sends emails via Brevo (formerly Sendinblue) API.
type BrevoProvider struct {
	apiKey   string
	fromAddr string
	fromName string
	client   *http.Client
	logger   *slog.Logger
}

// NewBrevoProvider creates a new Brevo email provider.
func NewBrevoProvider(apiKey, fromAddr, fromName string, logger *slog.Logger) *BrevoProvider {
	return &BrevoProvider{
		apiKey:   apiKey,
		fromAddr: fromAddr,
		fromName: fromName,
		client:   &http.Client{Timeout: 30 * time.Second},
		logger:   logger,
	}
}

// brevoSendRequest represents the Brevo API send email request.
type brevoSendRequest struct {
	Sender  brevoContact   `json:"sender"`
	To      []brevoContact `json:"to"`
	Subject string         `json:"subject"`
	HTML    string         `json:"htmlContent"`
}

type brevoContact struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// Send sends an email via Brevo API.
func (b *BrevoProvider) Send(ctx context.Context, to, subject, htmlBody string) error {
	reqBody := brevoSendRequest{
		Sender: brevoContact{
			Email: b.fromAddr,
			Name:  b.fromName,
		},
		To: []brevoContact{
			{Email: to},
		},
		Subject: subject,
		HTML:    htmlBody,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	return retry.Do(
		func() error {
			b.logger.Info("Brevo API request starting",
				"method", "POST",
				"endpoint", "smtp/email",
				"to", to,
				"subject", subject)

			startTime := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				"https://api.brevo.com/v3/smtp/email", bytes.NewReader(jsonData))
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}

			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("api-key", b.apiKey)

			resp, err := b.client.Do(req)
			duration := time.Since(startTime)

			if err != nil {
				b.logger.Warn("Brevo API request failed, will retry",
					"to", to,
					"duration_ms", duration.Milliseconds(),
					"error", err)
				return err
			}
			defer func() {
				if closeErr := resp.Body.Close(); closeErr != nil {
					b.logger.Warn("Failed to close response body", "error", closeErr)
				}
			}()

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				b.logger.Warn("Brevo API returned non-2xx status, will retry",
					"status_code", resp.StatusCode,
					"to", to)
				return fmt.Errorf("HTTP %d", resp.StatusCode)
			}

			b.logger.Info("Brevo API request completed",
				"endpoint", "smtp/email",
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
			b.logger.Info("Retrying Brevo email send after error", "attempt", n, "error", err)
		}),
	)
}
