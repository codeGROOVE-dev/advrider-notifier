package scraper

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestParseDurhamThread is an integration test that validates the parser
// against a real ADVRider thread that's known to have stable structure.
func TestParseDurhamThread(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := New(client, logger)

	ctx := context.Background()
	threadURL := "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/"

	// Fetch the first page
	page, err := s.fetchSinglePage(ctx, threadURL)
	if err != nil {
		t.Fatalf("Failed to fetch Durham thread: %v", err)
	}

	// Validate thread title
	expectedTitle := "Durham / RTP - Wednesday ADVLunch"
	if page.Title != expectedTitle {
		t.Errorf("Expected title %q, got %q", expectedTitle, page.Title)
	}
	t.Logf("Thread title: %q", page.Title)

	// Validate last page is at least 326 (as of Oct 2025)
	if page.LastPage < 326 {
		t.Errorf("Expected at least 326 pages, got %d", page.LastPage)
	}
	t.Logf("Thread has %d pages", page.LastPage)

	// Validate current page is 1
	if page.CurrentPage != 1 {
		t.Errorf("Expected current page to be 1, got %d", page.CurrentPage)
	}

	// Validate posts were found
	if len(page.Posts) == 0 {
		t.Fatal("No posts found on first page")
	}
	t.Logf("Found %d posts on first page", len(page.Posts))

	// Validate post structure
	firstPost := page.Posts[0]
	if firstPost.ID == "" {
		t.Error("First post should have an ID")
	}
	if firstPost.Author == "" {
		t.Error("First post should have an author")
	}
	if firstPost.Content == "" {
		t.Error("First post should have content")
	}
	if firstPost.URL == "" {
		t.Error("First post should have a URL")
	}
	if firstPost.Timestamp == "" {
		t.Error("First post should have a timestamp")
	} else {
		// Validate it can be parsed
		if _, err := time.Parse(time.RFC3339, firstPost.Timestamp); err != nil {
			t.Errorf("Failed to parse timestamp %q: %v", firstPost.Timestamp, err)
		}
	}

	t.Logf("First post ID: %s, Author: %s, Timestamp: %s, Content length: %d bytes",
		firstPost.ID, firstPost.Author, firstPost.Timestamp, len(firstPost.Content))

	// Validate URL format
	expectedURLPrefix := threadURL + "#post-"
	if len(firstPost.URL) <= len(expectedURLPrefix) {
		t.Errorf("Post URL %q is too short", firstPost.URL)
	}
}

// TestParseDurhamThreadPage293 validates we can parse a specific page number.
func TestParseDurhamThreadPage293(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := New(client, logger)

	ctx := context.Background()
	baseURL := "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/"

	// Test that we can fetch page 293 (mentioned in the original error)
	page293URL := buildPageURL(baseURL, 293)
	page, err := s.fetchSinglePage(ctx, page293URL)
	if err != nil {
		t.Fatalf("Failed to fetch Durham thread page 293: %v", err)
	}

	// Validate current page number
	if page.CurrentPage != 293 {
		t.Errorf("Expected current page to be 293, got %d", page.CurrentPage)
	}

	// Validate posts were found
	if len(page.Posts) == 0 {
		t.Fatal("No posts found on page 293")
	}
	t.Logf("Found %d posts on page 293", len(page.Posts))

	// Validate last post has timestamp (most important for latest post tracking)
	lastPost := page.Posts[len(page.Posts)-1]
	if lastPost.Timestamp == "" {
		t.Error("Last post on page 293 should have a timestamp")
	} else {
		// Validate it can be parsed
		parsedTime, err := time.Parse(time.RFC3339, lastPost.Timestamp)
		if err != nil {
			t.Errorf("Failed to parse timestamp %q: %v", lastPost.Timestamp, err)
		} else {
			t.Logf("Last post ID: %s, Author: %s, Timestamp: %s",
				lastPost.ID, lastPost.Author, parsedTime.Format(time.RFC3339))
		}
	}
}

// TestParseDurhamThreadLatestPost validates we can fetch the absolute latest post.
func TestParseDurhamThreadLatestPost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := New(client, logger)

	ctx := context.Background()
	threadURL := "https://advrider.com/f/threads/durham-rtp-wednesday-advlunch.365943/"

	// Use LatestPost to fetch the most recent post (same as subscription creation does)
	post, title, err := s.LatestPost(ctx, threadURL)
	if err != nil {
		t.Fatalf("Failed to fetch Durham thread latest post: %v", err)
	}

	t.Logf("Thread title: %q", title)
	if title == "" {
		t.Error("Thread title should not be empty")
	}

	// Validate post ID
	if post.ID == "" {
		t.Fatal("Latest post should have an ID")
	}
	t.Logf("Latest post ID: %s", post.ID)

	// Validate post timestamp - CRITICAL
	if post.Timestamp == "" {
		t.Fatal("Latest post should have a timestamp")
	}
	t.Logf("Latest post timestamp: %s", post.Timestamp)

	// Validate timestamp can be parsed as RFC3339
	parsedTime, err := time.Parse(time.RFC3339, post.Timestamp)
	if err != nil {
		t.Fatalf("Failed to parse timestamp %q as RFC3339: %v", post.Timestamp, err)
	}
	t.Logf("Parsed timestamp: %s", parsedTime.Format(time.RFC3339))

	// Validate timestamp is reasonable (thread started in 2008, so posts should be after that)
	now := time.Now()
	if parsedTime.IsZero() {
		t.Error("Parsed timestamp should not be zero")
	}
	if parsedTime.After(now) {
		t.Errorf("Parsed timestamp %s is in the future (now: %s)", parsedTime, now)
	}
	if parsedTime.Year() < 2008 {
		t.Errorf("Parsed timestamp %s is before thread creation (2008)", parsedTime)
	}

	// Validate other post fields
	if post.Author == "" {
		t.Error("Latest post should have an author")
	}
	if post.Content == "" {
		t.Error("Latest post should have content")
	}
	t.Logf("Latest post by %s, content length: %d bytes", post.Author, len(post.Content))
}

// TestParseElectricMotorcycleThread validates timestamp parsing for the Electric Motorcycle thread.
func TestParseElectricMotorcycleThread(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := New(client, logger)

	ctx := context.Background()
	threadURL := "https://advrider.com/f/threads/electric-motorcycle-scooter-news-updates.1154248/"

	// Fetch the latest post
	post, title, err := s.LatestPost(ctx, threadURL)
	if err != nil {
		t.Fatalf("Failed to fetch Electric Motorcycle thread: %v", err)
	}

	// Validate thread title
	t.Logf("Thread title: %q", title)
	if title == "" {
		t.Error("Thread title should not be empty")
	}

	// Validate post ID
	if post.ID == "" {
		t.Fatal("Latest post should have an ID")
	}
	t.Logf("Latest post ID: %s", post.ID)

	// Validate post timestamp - THIS IS THE CRITICAL CHECK
	if post.Timestamp == "" {
		t.Fatal("Latest post should have a timestamp")
	}
	t.Logf("Latest post timestamp: %s", post.Timestamp)

	// Validate timestamp can be parsed as RFC3339
	parsedTime, err := time.Parse(time.RFC3339, post.Timestamp)
	if err != nil {
		t.Fatalf("Failed to parse timestamp %q as RFC3339: %v", post.Timestamp, err)
	}
	t.Logf("Parsed timestamp: %s", parsedTime.Format(time.RFC3339))

	// Validate timestamp is not zero and is reasonable (not in the future, not too old)
	now := time.Now()
	if parsedTime.IsZero() {
		t.Error("Parsed timestamp should not be zero")
	}
	if parsedTime.After(now) {
		t.Errorf("Parsed timestamp %s is in the future (now: %s)", parsedTime, now)
	}
	// Electric motorcycle thread started in 2013, so posts should be after that
	if parsedTime.Year() < 2013 {
		t.Errorf("Parsed timestamp %s is before thread creation (2013)", parsedTime)
	}

	// Validate other post fields
	if post.Author == "" {
		t.Error("Latest post should have an author")
	}
	if post.Content == "" {
		t.Error("Latest post should have content")
	}
	t.Logf("Latest post by %s, content length: %d bytes", post.Author, len(post.Content))
}
