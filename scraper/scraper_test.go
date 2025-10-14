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

	t.Logf("First post ID: %s, Author: %s, Content length: %d bytes",
		firstPost.ID, firstPost.Author, len(firstPost.Content))

	// Validate URL format
	expectedURLPrefix := threadURL + "#post-"
	if len(firstPost.URL) <= len(expectedURLPrefix) {
		t.Errorf("Post URL %q is too short", firstPost.URL)
	}
}

// TestParseDurhamThreadLastPage validates we can parse a specific page number.
func TestParseDurhamThreadLastPage(t *testing.T) {
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
}
