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
	b.WriteString(".post:last-of-type { border-bottom: none; padding-bottom: 0; margin-bottom: 0; }\n")
	b.WriteString(".post:first-of-type { padding-top: 0; }\n")
	b.WriteString(".meta { margin-bottom: 12px; }\n")
	b.WriteString(".post-number { color: #7f8c8d; font-weight: 500; font-size: 1.1em; text-decoration: none; }\n")
	b.WriteString(".post-number:hover { text-decoration: underline; }\n")
	b.WriteString(".author { color: #e67e22; font-weight: 600; font-size: 1.2em; }\n")
	b.WriteString(".timestamp { color: #7f8c8d; font-size: 0.9em; }\n")
	b.WriteString(".content { margin: 15px 0; }\n")
	b.WriteString(".content img { max-width: 100%; height: auto; margin: 10px 0; display: block; }\n")
	b.WriteString(".content blockquote { border-left: 3px solid #ddd; padding-left: 15px; margin: 10px 0; color: #666; font-size: 0.95em; }\n")
	b.WriteString(".footer { margin-top: 30px; padding-top: 15px; font-size: 0.9em; color: #7f8c8d; }\n")
	b.WriteString(".footer.with-border { border-top: 1px solid #ddd; }\n")
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
	b.WriteString(".footer { color: #a0a0a0; }\n")
	b.WriteString(".footer.with-border { border-top-color: #444; }\n")
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
		// SECURITY: HTML content from forum posts is untrusted user input.
		// We sanitize it to allow only safe tags (img, blockquote, p, br, b, i, em, strong)
		// and safe attributes (src, alt for images) to prevent XSS and phishing.
		if post.HTMLContent != "" {
			b.WriteString(sanitizeHTML(post.HTMLContent))
		} else {
			b.WriteString(escapeHTML(post.Content))
		}
		b.WriteString("</div>\n")

		b.WriteString("</div>\n")
	}

	// Footer with thread link and manage link
	// Only add border if there are multiple posts (orange lines provide separation)
	footerClass := "footer"
	if len(posts) > 1 {
		footerClass = "footer with-border"
	}
	b.WriteString(fmt.Sprintf("<div class=\"%s\">\n", footerClass))

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
	b.WriteString(fmt.Sprintf("<a href=\"%s\">View thread</a>\n", escapeHTML(thread.ThreadURL)))
	b.WriteString(" &bull; \n")
	b.WriteString(fmt.Sprintf("<a href=\"%s\">Manage</a>\n", escapeHTML(manageURL)))
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

// sanitizeHTML sanitizes untrusted HTML content using a strict whitelist approach.
// Only allows safe tags and attributes to prevent XSS, phishing, and tracking.
// This is designed for email contexts where security is critical.
func sanitizeHTML(html string) string {
	// Whitelist of allowed tags (no scripts, forms, iframes, etc.)
	allowedTags := map[string]bool{
		"p":          true,
		"br":         true,
		"b":          true,
		"strong":     true,
		"i":          true,
		"em":         true,
		"u":          true,
		"blockquote": true,
		"img":        true,
		"a":          true,
		"ul":         true,
		"ol":         true,
		"li":         true,
		"div":        true,
		"span":       true,
	}

	var result strings.Builder
	inTag := false
	tagStart := 0

	for i := 0; i < len(html); i++ {
		if html[i] == '<' {
			if inTag {
				// Malformed HTML - escape the previous <
				result.WriteString("&lt;")
			}
			inTag = true
			tagStart = i
		} else if html[i] == '>' && inTag {
			tagContent := html[tagStart+1 : i]

			// Check if it's a closing tag
			isClosing := strings.HasPrefix(tagContent, "/")
			if isClosing {
				tagContent = tagContent[1:]
			}

			// Extract tag name (before space or end)
			tagName := tagContent
			if idx := strings.IndexAny(tagContent, " \t\n"); idx != -1 {
				tagName = tagContent[:idx]
			}
			tagName = strings.ToLower(tagName)

			if allowedTags[tagName] {
				// For allowed tags, sanitize attributes
				if isClosing {
					result.WriteString("</")
					result.WriteString(tagName)
					result.WriteString(">")
				} else {
					result.WriteString("<")
					result.WriteString(tagName)

					// Only allow safe attributes for specific tags
					if tagName == "img" {
						// Extract and validate src and alt attributes
						if src := extractAttribute(tagContent, "src"); src != "" && isSafeURL(src) {
							result.WriteString(` src="`)
							result.WriteString(escapeHTML(src))
							result.WriteString(`"`)
						}
						if alt := extractAttribute(tagContent, "alt"); alt != "" {
							result.WriteString(` alt="`)
							result.WriteString(escapeHTML(alt))
							result.WriteString(`"`)
						}
					} else if tagName == "a" {
						// Extract and validate href attribute
						if href := extractAttribute(tagContent, "href"); href != "" && isSafeURL(href) {
							result.WriteString(` href="`)
							result.WriteString(escapeHTML(href))
							result.WriteString(`"`)
						}
					}
					// No attributes allowed for other tags

					result.WriteString(">")
				}
			} else {
				// Disallowed tag - show placeholder for certain dangerous tags
				// This helps users understand that content was removed for security
				if !isClosing {
					// Only show placeholder for opening tags, not closing tags
					if tagName == "iframe" {
						// For iframes, extract the src URL and show it as a link
						if src := extractAttribute(tagContent, "src"); src != "" && isSafeURL(src) {
							result.WriteString("[iframe: <a href=\"")
							result.WriteString(escapeHTML(src))
							result.WriteString("\">")
							result.WriteString(escapeHTML(src))
							result.WriteString("</a>]")
						} else {
							result.WriteString("[replaced iframe]")
						}
					} else if tagName == "video" || tagName == "embed" || tagName == "object" {
						result.WriteString("[replaced ")
						result.WriteString(tagName)
						result.WriteString("]")
					} else {
						// For other disallowed tags, escape them
						result.WriteString("&lt;")
						result.WriteString(escapeHTML(tagContent))
						result.WriteString("&gt;")
					}
				}
				// Closing tags are silently removed
			}

			inTag = false
		} else if !inTag {
			// Regular content - keep as-is (already HTML entities in the original)
			result.WriteByte(html[i])
		}
	}

	// Handle unclosed tag at end
	if inTag {
		result.WriteString("&lt;")
		result.WriteString(escapeHTML(html[tagStart+1:]))
	}

	return result.String()
}

// extractAttribute extracts an attribute value from an HTML tag string.
func extractAttribute(tag, attrName string) string {
	// Look for attrName="value" or attrName='value'
	patterns := []string{
		attrName + `="`,
		attrName + `='`,
	}

	for _, pattern := range patterns {
		idx := strings.Index(strings.ToLower(tag), pattern)
		if idx == -1 {
			continue
		}

		start := idx + len(pattern)
		quote := pattern[len(pattern)-1]
		end := strings.IndexByte(tag[start:], quote)
		if end == -1 {
			continue
		}

		return tag[start : start+end]
	}

	return ""
}

// isSafeURL validates that a URL is safe for use in emails.
// Only allows http, https, and relative URLs. Blocks javascript:, data:, etc.
func isSafeURL(urlStr string) bool {
	urlStr = strings.TrimSpace(strings.ToLower(urlStr))

	// Block dangerous protocols
	dangerousProtocols := []string{
		"javascript:",
		"data:",
		"vbscript:",
		"file:",
		"about:",
	}

	for _, protocol := range dangerousProtocols {
		if strings.HasPrefix(urlStr, protocol) {
			return false
		}
	}

	// Allow http, https, and relative URLs
	return strings.HasPrefix(urlStr, "http://") ||
		strings.HasPrefix(urlStr, "https://") ||
		strings.HasPrefix(urlStr, "/") ||
		strings.HasPrefix(urlStr, "./") ||
		strings.HasPrefix(urlStr, "../") ||
		(!strings.Contains(urlStr, ":") && len(urlStr) > 0) // relative path without protocol
}
