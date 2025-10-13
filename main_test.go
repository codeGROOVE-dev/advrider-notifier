package main

import (
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
	m := &Monitor{
		baseURL: "https://test.example.com",
	}

	sub := &Subscription{
		Email: "test@example.com",
		Token: "test-token-1234567890abcdef1234567890abcdef1234567890abcdef1234",
	}

	thread := &Thread{
		ThreadID:  "12345",
		ThreadURL: "https://advrider.com/f/threads/test.12345/",
	}

	posts := []*Post{
		{
			ID:        "67890",
			Author:    "TestUser",
			Content:   "This is a test post",
			Timestamp: "2025-10-13T12:00:00Z",
			URL:       "https://advrider.com/f/threads/test.12345/#post-67890",
		},
	}

	body := m.formatEmailBody(sub, thread, posts)

	if !strings.Contains(body, "TestUser") {
		t.Error("Email body missing author")
	}
	if !strings.Contains(body, "This is a test post") {
		t.Error("Email body missing content")
	}
	if !strings.Contains(body, posts[0].URL) {
		t.Error("Email body missing post URL")
	}
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("Email body missing HTML declaration")
	}
	if !strings.Contains(body, "ADVRider") {
		t.Error("Email body missing ADVRider branding")
	}
	if !strings.Contains(body, "Manage Subscriptions") {
		t.Error("Email body missing manage link")
	}
	if !strings.Contains(body, sub.Token) {
		t.Error("Email body missing secure token")
	}
}
