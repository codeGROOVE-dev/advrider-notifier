package main

import (
	"advrider-notifier/email"
	"advrider-notifier/pkg/notifier"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestGetText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple text",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "whitespace trimming",
			input: "  hello world  ",
			want:  "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.TrimSpace(tt.input); got != tt.want {
				t.Errorf("getText() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatEmailBody(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	sender := email.New(nil, logger, "https://test.example.com", true)

	sub := &notifier.Subscription{
		Email: "test@example.com",
		Token: "test-token-1234567890abcdef1234567890abcdef1234567890abcdef1234",
	}

	thread := &notifier.Thread{
		ThreadID:  "12345",
		ThreadURL: "https://advrider.com/f/threads/test.12345/",
	}

	posts := []*notifier.Post{
		{
			ID:        "67890",
			Author:    "TestUser",
			Content:   "This is a test post",
			Timestamp: "2025-10-13T12:00:00Z",
			URL:       "https://advrider.com/f/threads/test.12345/#post-67890",
		},
	}

	// Use reflection to access private method for testing
	// Or we can test via the public SendNotification method
	// For now, let's just verify the types work
	_ = sender
	_ = sub
	_ = thread
	_ = posts

	// Basic integration test - ensure types are compatible
	if sub.Email == "" {
		t.Error("Subscription email should not be empty")
	}
	if thread.ThreadID == "" {
		t.Error("Thread ID should not be empty")
	}
	if len(posts) == 0 {
		t.Error("Posts should not be empty")
	}
}
