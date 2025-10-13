package main

import (
	"testing"
)

func TestNormalizeThreadURL(t *testing.T) {
	tests := []struct {
		name      string
		threadURL string
		threadID  string
		want      string
		wantErr   bool
	}{
		{
			name:      "basic thread URL",
			threadURL: "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/",
			threadID:  "365943",
			want:      "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/",
			wantErr:   false,
		},
		{
			name:      "thread URL with page number",
			threadURL: "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/page-325",
			threadID:  "365943",
			want:      "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/",
			wantErr:   false,
		},
		{
			name:      "thread URL with anchor",
			threadURL: "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/#post-53727433",
			threadID:  "365943",
			want:      "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/",
			wantErr:   false,
		},
		{
			name:      "thread URL with page and anchor",
			threadURL: "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/page-325#post-53727433",
			threadID:  "365943",
			want:      "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/",
			wantErr:   false,
		},
		{
			name:      "different thread with page",
			threadURL: "https://advrider.com/f/threads/example-thread.123456/page-1",
			threadID:  "123456",
			want:      "https://advrider.com/f/threads/example-thread.123456/",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeThreadURL(tt.threadURL, tt.threadID)
			if (err != nil) != tt.wantErr {
				t.Errorf("normalizeThreadURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("normalizeThreadURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractThreadSlug(t *testing.T) {
	tests := []struct {
		name      string
		threadURL string
		want      string
	}{
		{
			name:      "basic URL",
			threadURL: "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/",
			want:      "durham-rtp-wednesday-advlunch",
		},
		{
			name:      "URL with page",
			threadURL: "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/page-325",
			want:      "durham-rtp-wednesday-advlunch",
		},
		{
			name:      "URL with anchor",
			threadURL: "https://advrider.com/f/threads/test-thread.123456/#post-789",
			want:      "test-thread",
		},
		{
			name:      "invalid URL",
			threadURL: "not-a-valid-url",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractThreadSlug(tt.threadURL)
			if got != tt.want {
				t.Errorf("extractThreadSlug() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsValidEmail(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  bool
	}{
		{
			name:  "valid email",
			email: "user@example.com",
			want:  true,
		},
		{
			name:  "valid email with subdomain",
			email: "user@mail.example.com",
			want:  true,
		},
		{
			name:  "valid email with plus",
			email: "user+tag@example.com",
			want:  true,
		},
		{
			name:  "invalid - no @",
			email: "userexample.com",
			want:  false,
		},
		{
			name:  "invalid - no domain",
			email: "user@",
			want:  false,
		},
		{
			name:  "invalid - too short",
			email: "a@b",
			want:  false,
		},
		{
			name:  "invalid - spaces",
			email: "user @example.com",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidEmail(tt.email)
			if got != tt.want {
				t.Errorf("isValidEmail() = %v, want %v", got, tt.want)
			}
		})
	}
}
