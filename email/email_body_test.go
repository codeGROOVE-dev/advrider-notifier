package email

import (
	"advrider-notifier/pkg/notifier"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNotificationBodySinglePost(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	provider := NewMockProvider(logger)
	sender := New(provider, logger, "http://localhost:8080")

	sub := &notifier.Subscription{
		Email: "test@example.com",
		Token: "test123",
	}

	thread := &notifier.Thread{
		ThreadURL:   "https://advrider.com/f/threads/test.123/",
		ThreadTitle: "Test Thread",
	}

	posts := []*notifier.Post{
		{
			ID:        "12345",
			Author:    "TestUser",
			Content:   "Test content",
			Timestamp: time.Now().Format(time.RFC3339),
			URL:       "https://advrider.com/f/threads/test.123/#post-12345",
		},
	}

	body := sender.formatNotificationBody(sub, thread, posts)

	// Single post should have inline styles to remove borders and padding
	if !strings.Contains(body, `style="padding-top: 0; border-bottom: none; padding-bottom: 0;"`) {
		t.Errorf("Single post missing inline styles for Gmail compatibility.\nGot:\n%s", body)
	}

	// Should still have the .post class for email clients that support CSS
	if !strings.Contains(body, `class="post"`) {
		t.Error("Missing .post class")
	}
}

func TestNotificationBodyMultiplePosts(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	provider := NewMockProvider(logger)
	sender := New(provider, logger, "http://localhost:8080")

	sub := &notifier.Subscription{
		Email: "test@example.com",
		Token: "test123",
	}

	thread := &notifier.Thread{
		ThreadURL:   "https://advrider.com/f/threads/test.123/",
		ThreadTitle: "Test Thread",
	}

	posts := []*notifier.Post{
		{
			ID:        "12345",
			Author:    "User1",
			Content:   "First post",
			Timestamp: time.Now().Add(-time.Hour).Format(time.RFC3339),
			URL:       "https://advrider.com/f/threads/test.123/#post-12345",
		},
		{
			ID:        "12346",
			Author:    "User2",
			Content:   "Second post",
			Timestamp: time.Now().Format(time.RFC3339),
			URL:       "https://advrider.com/f/threads/test.123/#post-12346",
		},
	}

	body := sender.formatNotificationBody(sub, thread, posts)

	// First post should have inline style to remove top padding
	if !strings.Contains(body, `style="padding-top: 0;"`) {
		t.Errorf("First post of multiple missing inline style.\nGot:\n%s", body)
	}

	// Last post should have inline style to remove bottom border
	if !strings.Contains(body, `style="border-bottom: none; padding-bottom: 0;"`) {
		t.Errorf("Last post of multiple missing inline style.\nGot:\n%s", body)
	}

	// Footer should have grey border
	if !strings.Contains(body, `class="footer with-border"`) {
		t.Error("Footer missing with-border class")
	}
}
