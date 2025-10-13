// Package email handles sending notification emails via Gmail API.
package email

import (
	"advrider-notifier/pkg/notifier"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"google.golang.org/api/gmail/v1"
)

// Sender sends notification emails.
type Sender struct {
	service  *gmail.Service
	logger   *slog.Logger
	baseURL  string // For links in emails
	mockMode bool   // For development/testing
}

// New creates a new email sender.
func New(service *gmail.Service, logger *slog.Logger, baseURL string, mockMode bool) *Sender {
	return &Sender{
		service:  service,
		logger:   logger,
		baseURL:  baseURL,
		mockMode: mockMode,
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

	return s.send(ctx, sub.Email, subject, body, len(posts))
}

// SendWelcome sends a welcome email when a user first subscribes.
func (s *Sender) SendWelcome(ctx context.Context, sub *notifier.Subscription, thread *notifier.Thread, ip, userAgent string) error {
	// Use thread title for email subject to enable proper threading
	subject := thread.ThreadTitle
	if subject == "" {
		subject = "ADVRider Thread Update"
	}

	body := s.formatWelcomeBody(sub, thread, ip, userAgent)

	return s.send(ctx, sub.Email, subject, body, 0)
}

func (s *Sender) send(ctx context.Context, to, subject, body string, postCount int) error {
	// Mock email mode for local development
	if s.mockMode {
		logType := "MOCK EMAIL"
		if postCount == 0 {
			logType = "MOCK WELCOME EMAIL"
		}
		s.logger.Info(logType,
			"to", to,
			"subject", subject,
			"post_count", postCount)
		return nil
	}

	// Create MIME message
	var msg strings.Builder
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	msg.WriteString(body)
	encoded := base64.URLEncoding.EncodeToString([]byte(msg.String()))

	err := retry.Do(
		func() error {
			s.logger.Info("Gmail API request starting",
				"method", "POST",
				"endpoint", "users.messages.send",
				"to", to,
				"post_count", postCount,
				"subject", subject)

			startTime := time.Now()
			_, err := s.service.Users.Messages.Send("me", &gmail.Message{
				Raw: encoded,
			}).Context(ctx).Do()
			duration := time.Since(startTime)

			if err != nil {
				s.logger.Warn("Gmail API send failed, will retry",
					"to", to,
					"duration_ms", duration.Milliseconds(),
					"error", err)
				return err
			}

			s.logger.Info("Gmail API request completed",
				"endpoint", "users.messages.send",
				"to", to,
				"duration_ms", duration.Milliseconds(),
				"status", "success")

			return nil
		},
		retry.Attempts(10),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.MaxJitter(10*time.Second),
		retry.Context(ctx),
		retry.OnRetry(func(n uint, err error) {
			s.logger.Info("Retrying email send after error", "attempt", n, "error", err)
		}),
	)

	if err != nil {
		return fmt.Errorf("after retries: %w", err)
	}

	s.logger.Info("Email successfully sent", "to", to, "post_count", postCount)
	return nil
}

func (s *Sender) formatNotificationBody(sub *notifier.Subscription, thread *notifier.Thread, posts []*notifier.Post) string {
	var b strings.Builder

	b.WriteString("<!DOCTYPE html>\n<html>\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n")
	b.WriteString("<style>\n")
	b.WriteString("body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; line-height: 1.6; color: #333; max-width: 800px; margin: 0 auto; padding: 20px; }\n")
	b.WriteString(".header { border-bottom: 2px solid #e67e22; padding-bottom: 10px; margin-bottom: 20px; }\n")
	b.WriteString(".post { margin-bottom: 30px; padding-bottom: 20px; border-bottom: 1px solid #ecf0f1; }\n")
	b.WriteString(".post:last-of-type { border-bottom: none; }\n")
	b.WriteString(".author { color: #e67e22; font-weight: 600; }\n")
	b.WriteString(".timestamp { color: #7f8c8d; font-size: 0.9em; }\n")
	b.WriteString(".content { background: #f8f9fa; padding: 20px; border-radius: 8px; margin: 15px 0; white-space: pre-wrap; }\n")
	b.WriteString(".footer { margin-top: 20px; padding-top: 10px; border-top: 2px solid #ecf0f1; color: #7f8c8d; font-size: 0.9em; }\n")
	b.WriteString("a { color: #e67e22; text-decoration: none; }\n")
	b.WriteString("a:hover { text-decoration: underline; }\n")
	b.WriteString(".view-post { display: inline-block; margin-top: 10px; font-size: 0.9em; }\n")
	b.WriteString("</style>\n</head>\n<body>\n")

	b.WriteString("<div class=\"header\">\n")
	if len(posts) == 1 {
		b.WriteString("<h2>New ADVRider Post</h2>\n")
	} else {
		b.WriteString(fmt.Sprintf("<h2>%d New ADVRider Posts</h2>\n", len(posts)))
	}
	b.WriteString("</div>\n")

	// Render each post
	for _, post := range posts {
		b.WriteString("<div class=\"post\">\n")
		b.WriteString("<div class=\"meta\">\n")
		b.WriteString(fmt.Sprintf("<span class=\"author\">%s</span>\n", escapeHTML(post.Author)))
		if post.Timestamp != "" {
			t, err := time.Parse(time.RFC3339, post.Timestamp)
			if err == nil {
				b.WriteString(fmt.Sprintf("<span class=\"timestamp\"> &bull; %s</span>\n", t.Format("Jan 2, 2006 at 3:04 PM")))
			}
		}
		b.WriteString("</div>\n")

		b.WriteString("<div class=\"content\">\n")
		b.WriteString(escapeHTML(post.Content))
		b.WriteString("</div>\n")

		b.WriteString(fmt.Sprintf("<a href=\"%s\" class=\"view-post\">View this post on ADVRider</a>\n", escapeHTML(post.URL)))
		b.WriteString("</div>\n")
	}

	b.WriteString("<div class=\"footer\">\n")
	b.WriteString(fmt.Sprintf("<a href=\"%s\">View full thread on ADVRider</a>\n", escapeHTML(thread.ThreadURL)))
	b.WriteString(" &bull; \n")
	// Use secure token in manage link
	manageURL := fmt.Sprintf("%s/manage?token=%s", s.baseURL, url.QueryEscape(sub.Token))
	b.WriteString(fmt.Sprintf("<a href=\"%s\">Manage Subscriptions</a>\n", escapeHTML(manageURL)))
	b.WriteString("</div>\n")

	b.WriteString("</body>\n</html>")

	return b.String()
}

func (s *Sender) formatWelcomeBody(sub *notifier.Subscription, thread *notifier.Thread, ip, userAgent string) string {
	manageURL := fmt.Sprintf("%s/manage?token=%s", s.baseURL, url.QueryEscape(sub.Token))

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html>\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n")
	b.WriteString("<style>\n")
	b.WriteString("body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; line-height: 1.6; color: #333; max-width: 800px; margin: 0 auto; padding: 20px; }\n")
	b.WriteString(".header { border-bottom: 2px solid #e67e22; padding-bottom: 10px; margin-bottom: 20px; }\n")
	b.WriteString(".content { background: #f8f9fa; padding: 20px; border-radius: 8px; margin: 15px 0; }\n")
	b.WriteString(".info { color: #7f8c8d; font-size: 0.9em; margin: 15px 0; }\n")
	b.WriteString(".footer { margin-top: 20px; padding-top: 10px; border-top: 2px solid #ecf0f1; color: #7f8c8d; font-size: 0.9em; }\n")
	b.WriteString("a { color: #e67e22; text-decoration: none; }\n")
	b.WriteString("a:hover { text-decoration: underline; }\n")
	b.WriteString("</style>\n</head>\n<body>\n")

	b.WriteString("<div class=\"header\">\n")
	b.WriteString("<h2>ADVRider Thread Subscription Confirmed</h2>\n")
	b.WriteString("</div>\n")

	b.WriteString("<div class=\"content\">\n")
	b.WriteString(fmt.Sprintf("<p>You've successfully subscribed to notifications for the thread: <strong>%s</strong></p>\n", escapeHTML(thread.ThreadTitle)))
	b.WriteString("<p>You'll receive an email whenever new posts are added to this thread.</p>\n")
	b.WriteString("</div>\n")

	b.WriteString("<div class=\"info\">\n")
	b.WriteString("<p><strong>Subscription Details:</strong></p>\n")
	b.WriteString("<ul>\n")
	b.WriteString(fmt.Sprintf("<li>IP Address: %s</li>\n", escapeHTML(ip)))
	b.WriteString(fmt.Sprintf("<li>Browser: %s</li>\n", escapeHTML(userAgent)))
	b.WriteString("</ul>\n")
	b.WriteString("</div>\n")

	b.WriteString("<div class=\"footer\">\n")
	b.WriteString(fmt.Sprintf("<a href=\"%s\">View thread on ADVRider</a>\n", escapeHTML(thread.ThreadURL)))
	b.WriteString(" &bull; \n")
	b.WriteString(fmt.Sprintf("<a href=\"%s\">Manage Subscriptions</a>\n", escapeHTML(manageURL)))
	b.WriteString("</div>\n")

	b.WriteString("</body>\n</html>")

	return b.String()
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}
