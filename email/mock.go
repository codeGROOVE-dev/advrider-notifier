package email

import (
	"context"
	"log/slog"
)

// MockProvider is a mock email provider for local development.
type MockProvider struct {
	logger *slog.Logger
}

// NewMockProvider creates a new mock email provider.
func NewMockProvider(logger *slog.Logger) *MockProvider {
	return &MockProvider{
		logger: logger,
	}
}

// Send logs the email instead of sending it.
func (m *MockProvider) Send(ctx context.Context, to, subject, htmlBody string) error {
	m.logger.Info("MOCK EMAIL",
		"to", to,
		"subject", subject,
		"body_length", len(htmlBody))
	return nil
}
