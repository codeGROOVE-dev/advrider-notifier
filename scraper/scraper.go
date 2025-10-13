// Package scraper handles fetching and parsing ADVRider thread pages.
package scraper

import (
	"advrider-notifier/pkg/notifier"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/codeGROOVE-dev/retry"
)

// Page represents a parsed thread page with posts and metadata.
type Page struct {
	Posts       []*notifier.Post
	Title       string
	LastPage    int // Last page number (0 if single page)
	CurrentPage int // Current page number
}

// HTTP403Error indicates a 403 Forbidden response (login required).
type HTTP403Error struct {
	URL string
}

func (e *HTTP403Error) Error() string {
	return fmt.Sprintf("HTTP 403 Forbidden: %s", e.URL)
}

// IsHTTP403Error checks if an error is an HTTP 403 error.
func IsHTTP403Error(err error) bool {
	var forbidden *HTTP403Error
	return errors.As(err, &forbidden)
}

// Scraper fetches and parses ADVRider threads.
type Scraper struct {
	client *http.Client
	logger *slog.Logger
}

// New creates a new scraper.
func New(client *http.Client, logger *slog.Logger) *Scraper {
	return &Scraper{
		client: client,
		logger: logger,
	}
}

// LatestPost fetches just the latest post from a thread.
func (s *Scraper) LatestPost(ctx context.Context, threadURL string) (*notifier.Post, error) {
	posts, _, err := s.SmartFetch(ctx, threadURL, "")
	if err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return nil, errors.New("no posts found")
	}
	return posts[len(posts)-1], nil
}

// SmartFetch fetches posts efficiently using multi-page strategy.
// Returns posts, title, and error.
func (s *Scraper) SmartFetch(ctx context.Context, threadURL string, lastSeenPostID string) ([]*notifier.Post, string, error) {
	page, err := s.smartFetch(ctx, threadURL, lastSeenPostID)
	if err != nil {
		return nil, "", err
	}
	return page.Posts, page.Title, nil
}

func (s *Scraper) smartFetch(ctx context.Context, threadURL string, lastSeenPostID string) (*Page, error) {
	s.logger.Info("Starting smart thread fetch", "url", threadURL, "last_seen_post", lastSeenPostID)

	// Step 1: Fetch first page to get title and last page number
	firstPage, err := s.fetchSinglePage(ctx, threadURL)
	if err != nil {
		return nil, fmt.Errorf("fetch first page: %w", err)
	}

	s.logger.Info("First page fetched",
		"title", firstPage.Title,
		"current_page", firstPage.CurrentPage,
		"last_page", firstPage.LastPage,
		"posts_on_page", len(firstPage.Posts))

	// If single page thread or we're on the last page already, we're done
	if firstPage.LastPage <= 1 || firstPage.CurrentPage == firstPage.LastPage {
		return firstPage, nil
	}

	// Step 2: Fetch last page to get most recent posts
	lastPageURL := buildPageURL(threadURL, firstPage.LastPage)
	lastPage, err := s.fetchSinglePage(ctx, lastPageURL)
	if err != nil {
		return nil, fmt.Errorf("fetch last page: %w", err)
	}

	s.logger.Info("Last page fetched",
		"page_number", lastPage.CurrentPage,
		"posts_on_page", len(lastPage.Posts))

	// Step 3: Check if we need to fetch second-to-last page
	// This happens when lastSeenPostID is not found on the last page
	needsPreviousPage := false
	if lastSeenPostID != "" {
		found := false
		for _, post := range lastPage.Posts {
			if post.ID == lastSeenPostID {
				found = true
				break
			}
		}
		needsPreviousPage = !found
	}

	allPosts := lastPage.Posts

	if needsPreviousPage && firstPage.LastPage > 1 {
		s.logger.Info("Last seen post not found on last page, fetching second-to-last page",
			"last_seen_post", lastSeenPostID,
			"fetching_page", firstPage.LastPage-1)

		secondToLastURL := buildPageURL(threadURL, firstPage.LastPage-1)
		secondToLastPage, err := s.fetchSinglePage(ctx, secondToLastURL)
		if err != nil {
			s.logger.Warn("Failed to fetch second-to-last page, continuing with last page only", "error", err)
		} else {
			s.logger.Info("Second-to-last page fetched", "posts_on_page", len(secondToLastPage.Posts))
			// Prepend second-to-last page posts (they're older)
			allPosts = append(secondToLastPage.Posts, lastPage.Posts...)
		}
	}

	return &Page{
		Posts:       allPosts,
		Title:       firstPage.Title,
		LastPage:    firstPage.LastPage,
		CurrentPage: lastPage.CurrentPage,
	}, nil
}

func (s *Scraper) fetchSinglePage(ctx context.Context, pageURL string) (*Page, error) {
	var page *Page

	err := retry.Do(
		func() error {
			s.logger.Info("HTTP request starting",
				"method", "GET",
				"url", pageURL,
				"purpose", "fetch_thread_page")

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, http.NoBody)
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}

			// Set essential Chrome-like headers to avoid getting blocked
			req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
			req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
			req.Header.Set("Accept-Language", "en-US,en;q=0.9")
			// Note: Don't set Accept-Encoding - let Go's http.Client handle compression automatically
			req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`)
			req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
			req.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
			req.Header.Set("Sec-Fetch-Dest", "document")
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			req.Header.Set("Sec-Fetch-Site", "none")
			req.Header.Set("Sec-Fetch-User", "?1")
			req.Header.Set("Upgrade-Insecure-Requests", "1")
			req.Header.Set("Cache-Control", "max-age=0")

			startTime := time.Now()
			resp, err := s.client.Do(req)
			duration := time.Since(startTime)

			if err != nil {
				s.logger.Warn("HTTP request failed, will retry",
					"url", pageURL,
					"duration_ms", duration.Milliseconds(),
					"error", err)
				return err
			}
			defer func() {
				if closeErr := resp.Body.Close(); closeErr != nil {
					s.logger.Warn("Failed to close response body", "error", closeErr)
				}
			}()

			s.logger.Info("HTTP request completed",
				"url", pageURL,
				"status_code", resp.StatusCode,
				"duration_ms", duration.Milliseconds(),
				"content_length", resp.ContentLength)

			if resp.StatusCode == http.StatusForbidden {
				s.logger.Warn("HTTP 403 Forbidden - thread requires login", "url", pageURL)
				return &HTTP403Error{URL: pageURL}
			}

			if resp.StatusCode != http.StatusOK {
				s.logger.Warn("HTTP request returned non-OK status, will retry", "status_code", resp.StatusCode)
				return fmt.Errorf("HTTP %d", resp.StatusCode)
			}

			page, err = parsePage(resp.Body, pageURL)
			if err != nil {
				s.logger.Error("Failed to parse HTML", "error", err)
				return retry.Unrecoverable(err)
			}

			s.logger.Info("Thread page parsed successfully",
				"url", pageURL,
				"title", page.Title,
				"current_page", page.CurrentPage,
				"last_page", page.LastPage,
				"posts_found", len(page.Posts),
				"first_post_id", page.Posts[0].ID,
				"last_post_id", page.Posts[len(page.Posts)-1].ID)

			return nil
		},
		retry.Attempts(10),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.MaxJitter(10*time.Second),
		retry.Context(ctx),
		retry.OnRetry(func(n uint, err error) {
			s.logger.Info("Retrying fetch after error", "attempt", n, "error", err)
		}),
		retry.RetryIf(func(err error) bool {
			// Don't retry on 403 Forbidden errors (login required)
			return !IsHTTP403Error(err)
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("after retries: %w", err)
	}

	return page, nil
}

func buildPageURL(baseURL string, pageNum int) string {
	if pageNum <= 1 {
		return baseURL
	}
	// Remove trailing slash if present
	baseURL = strings.TrimSuffix(baseURL, "/")
	return fmt.Sprintf("%s/page-%d", baseURL, pageNum)
}

func parsePage(body interface{ Read([]byte) (int, error) }, threadURL string) (*Page, error) {
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return nil, err
	}

	// Extract thread title
	title := strings.TrimSpace(doc.Find("h1.p-title-value").First().Text())
	if title == "" {
		// Fallback: extract from <title> tag
		rawTitle := strings.TrimSpace(doc.Find("title").First().Text())
		if idx := strings.Index(rawTitle, " | "); idx > 0 {
			title = rawTitle[:idx]
		} else {
			title = rawTitle
		}
	}
	if title == "" {
		title = "ADVRider Thread"
	}

	// Extract pagination info from "Page X of Y" header
	var lastPage, currentPage int
	pageNavHeader := strings.TrimSpace(doc.Find("span.pageNavHeader").First().Text())
	if pageNavHeader != "" {
		// Parse "Page 1 of 326" format
		var curr, last int
		if _, err := fmt.Sscanf(pageNavHeader, "Page %d of %d", &curr, &last); err == nil {
			currentPage = curr
			lastPage = last
		}
	}
	if currentPage == 0 {
		currentPage = 1
	}

	// Extract posts
	var posts []*notifier.Post
	doc.Find("li.message").Each(func(i int, s *goquery.Selection) {
		// Extract post ID from id attribute
		postIDAttr, exists := s.Attr("id")
		if !exists || !strings.HasPrefix(postIDAttr, "post-") {
			return
		}
		id := strings.TrimPrefix(postIDAttr, "post-")

		// Extract author
		author := strings.TrimSpace(s.Find("a.username").First().Text())

		// Extract timestamp
		timestamp, _ := s.Find("time").First().Attr("datetime")

		// Extract content from blockquote
		content := strings.TrimSpace(s.Find("blockquote.messageText").First().Text())
		if content == "" {
			content = "(empty post)"
		}

		posts = append(posts, &notifier.Post{
			ID:        id,
			Author:    author,
			Content:   content,
			Timestamp: timestamp,
			URL:       threadURL + "#post-" + id,
		})
	})

	if len(posts) == 0 {
		return nil, fmt.Errorf("no posts found (title=%q, lastPage=%d, currentPage=%d)", title, lastPage, currentPage)
	}

	return &Page{
		Posts:       posts,
		Title:       title,
		LastPage:    lastPage,
		CurrentPage: currentPage,
	}, nil
}
