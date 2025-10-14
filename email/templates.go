package email

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"advrider-notifier/pkg/notifier"
)

func (s *Sender) formatNotificationBody(sub *notifier.Subscription, thread *notifier.Thread, posts []*notifier.Post) string {
	var b strings.Builder

	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<style>\n")
	b.WriteString("body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; line-height: 1.6; color: #333; max-width: 800px; margin: 0 auto; padding: 20px; background: #fff; }\n")
	b.WriteString(".post { margin-bottom: 30px; padding-bottom: 30px; border-bottom: 2px solid #e67e22; }\n")
	b.WriteString(".post:last-of-type { border-bottom: none; padding-bottom: 0; }\n")
	b.WriteString(".post:first-of-type { padding-top: 0; }\n")
	b.WriteString(".meta { margin-bottom: 12px; }\n")
	b.WriteString(".post-number { color: #7f8c8d; font-weight: 500; font-size: 1.1em; text-decoration: none; }\n")
	b.WriteString(".post-number:hover { text-decoration: underline; }\n")
	b.WriteString(".author { color: #e67e22; font-weight: 600; font-size: 1.2em; }\n")
	b.WriteString(".timestamp { color: #7f8c8d; font-size: 0.9em; }\n")
	b.WriteString(".content { margin: 15px 0; }\n")
	b.WriteString(".content img { max-width: 100%; height: auto; margin: 10px 0; display: block; }\n")
	b.WriteString(".content blockquote { border-left: 3px solid #ddd; padding-left: 15px; margin: 10px 0; color: #666; font-size: 0.95em; }\n")
	b.WriteString(".footer { margin-top: 30px; padding-top: 15px; border-top: 1px solid #ddd; font-size: 0.9em; color: #7f8c8d; }\n")
	b.WriteString(".footer a { color: #7f8c8d; text-decoration: underline; margin: 0 8px; }\n")
	b.WriteString(".footer a:first-child { margin-left: 0; }\n")
	b.WriteString("a { color: #e67e22; text-decoration: none; }\n")
	b.WriteString("a:hover { text-decoration: underline; }\n")
	b.WriteString("@media (prefers-color-scheme: dark) {\n")
	b.WriteString("body { background: #1a1a1a; color: #e0e0e0; }\n")
	b.WriteString(".post-number { color: #a0a0a0; }\n")
	b.WriteString(".author { color: #ff8c42; }\n")
	b.WriteString(".timestamp { color: #a0a0a0; }\n")
	b.WriteString(".content blockquote { border-left-color: #444; color: #b0b0b0; }\n")
	b.WriteString(".content img { opacity: 0.9; }\n")
	b.WriteString(".footer { border-top-color: #444; color: #a0a0a0; }\n")
	b.WriteString(".footer a { color: #a0a0a0; }\n")
	b.WriteString("a { color: #ff8c42; }\n")
	b.WriteString("}\n")
	b.WriteString("</style>\n</head>\n<body>\n")

	// Render each post - no redundant header
	for _, post := range posts {
		b.WriteString("<div class=\"post\">\n")
		b.WriteString("<div class=\"meta\">\n")
		b.WriteString(fmt.Sprintf("<a href=\"%s\" class=\"post-number\">#%s</a>\n", escapeHTML(post.URL), escapeHTML(post.ID)))
		b.WriteString(fmt.Sprintf("<span class=\"author\"> &bull; %s</span>\n", escapeHTML(post.Author)))
		if post.Timestamp != "" {
			t, err := time.Parse(time.RFC3339, post.Timestamp)
			if err == nil {
				b.WriteString(fmt.Sprintf("<span class=\"timestamp\"> &bull; %s UTC</span>\n", t.Format("Jan 2, 2006 at 3:04 PM")))
			}
		}
		b.WriteString("</div>\n")

		b.WriteString("<div class=\"content\">\n")
		// Use HTML content if available (includes images), otherwise fall back to plain text
		if post.HTMLContent != "" {
			b.WriteString(post.HTMLContent)
		} else {
			b.WriteString(escapeHTML(post.Content))
		}
		b.WriteString("</div>\n")

		b.WriteString("</div>\n")
	}

	// Footer with thread link and manage link
	b.WriteString("<div class=\"footer\">\n")

	// Link to the last page with anchor to latest post (e.g., .../page-12#post-12345)
	// This loads the full page context but scrolls to the most recent post
	threadLink := thread.ThreadURL
	if len(posts) > 0 && posts[len(posts)-1].URL != "" {
		threadLink = posts[len(posts)-1].URL
	}
	b.WriteString(fmt.Sprintf("<a href=\"%s\">View thread</a>\n", escapeHTML(threadLink)))

	manageURL := fmt.Sprintf("%s/manage?token=%s", s.baseURL, url.QueryEscape(sub.Token))
	b.WriteString(fmt.Sprintf("<a href=\"%s\">Manage</a>\n", escapeHTML(manageURL)))
	b.WriteString("</div>\n")

	b.WriteString("</body>\n</html>")

	return b.String()
}

func (s *Sender) formatWelcomeBody(sub *notifier.Subscription, thread *notifier.Thread, ip, userAgent string) string {
	manageURL := fmt.Sprintf("%s/manage?token=%s", s.baseURL, url.QueryEscape(sub.Token))

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<style>\n")
	b.WriteString("body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; line-height: 1.6; color: #333; max-width: 800px; margin: 0 auto; padding: 20px; background: #fff; }\n")
	b.WriteString(".header { border-bottom: 2px solid #e67e22; padding-bottom: 10px; margin-bottom: 20px; }\n")
	b.WriteString(".content { background: #f8f9fa; padding: 20px; border-radius: 8px; margin: 15px 0; }\n")
	b.WriteString(".info { color: #7f8c8d; font-size: 0.9em; margin: 15px 0; }\n")
	b.WriteString(".footer { margin-top: 20px; padding-top: 10px; border-top: 2px solid #ecf0f1; color: #7f8c8d; font-size: 0.9em; }\n")
	b.WriteString("a { color: #e67e22; text-decoration: none; }\n")
	b.WriteString("a:hover { text-decoration: underline; }\n")
	b.WriteString("@media (prefers-color-scheme: dark) {\n")
	b.WriteString("body { background: #1a1a1a; color: #e0e0e0; }\n")
	b.WriteString(".header { border-bottom-color: #ff8c42; }\n")
	b.WriteString(".content { background: #2a2a2a; }\n")
	b.WriteString(".info { color: #a0a0a0; }\n")
	b.WriteString(".footer { border-top-color: #444; color: #a0a0a0; }\n")
	b.WriteString("a { color: #ff8c42; }\n")
	b.WriteString("}\n")
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
