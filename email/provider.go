// Package email handles sending notification emails via multiple providers.
package email

import (
	"context"
	"log/slog"

	"advrider-notifier/pkg/notifier"
)

// Provider defines the interface for email sending implementations.
type Provider interface {
	// Send sends an email with the given parameters.
	Send(ctx context.Context, to, subject, htmlBody string) error
}

// Sender sends notification emails using a pluggable provider.
type Sender struct {
	provider Provider
	logger   *slog.Logger
	baseURL  string // For links in emails
	fromAddr string // From address for emails
}

// New creates a new email sender with the given provider.
func New(provider Provider, logger *slog.Logger, baseURL, fromAddr string) *Sender {
	return &Sender{
		provider: provider,
		logger:   logger,
		baseURL:  baseURL,
		fromAddr: fromAddr,
	}
}

// SendNotification sends an email notification about new posts.
func (s *Sender) SendNotification(ctx context.Context, sub *notifier.Subscription, thread *notifier.Thread, posts []*notifier.Post) error {
	if len(posts) == 0 {
		return nil
	}

	// Use thread title for email subject to enable proper threading in email clients
	subject := thread.ThreadTitle
	if subject == "" {
		subject = "ADVRider Thread Update"
	}

	body := s.formatNotificationBody(sub, thread, posts)

	s.logger.Info("Sending notification email",
		"to", sub.Email,
		"subject", subject,
		"post_count", len(posts))

	return s.provider.Send(ctx, sub.Email, subject, body)
}

// SendWelcome sends a welcome email when a user first subscribes.
func (s *Sender) SendWelcome(ctx context.Context, sub *notifier.Subscription, thread *notifier.Thread, ip, userAgent string) error {
	// Use thread title for email subject to enable proper threading
	subject := thread.ThreadTitle
	if subject == "" {
		subject = "ADVRider Thread Update"
	}

	body := s.formatWelcomeBody(sub, thread, ip, userAgent)

	s.logger.Info("Sending welcome email",
		"to", sub.Email,
		"subject", subject)

	return s.provider.Send(ctx, sub.Email, subject, body)
}
