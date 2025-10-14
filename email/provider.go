// Package email handles sending notification emails via Brevo or mock.
package email

import (
	"context"
	"log/slog"

	"advrider-notifier/pkg/notifier"
)

// Provider defines the interface for email sending implementations.
type Provider interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}

// Sender sends notification emails.
type Sender struct {
	provider Provider
	logger   *slog.Logger
	baseURL  string // For links in emails
}

// New creates a new email sender.
func New(provider Provider, logger *slog.Logger, baseURL string) *Sender {
	return &Sender{
		provider: provider,
		logger:   logger,
		baseURL:  baseURL,
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
