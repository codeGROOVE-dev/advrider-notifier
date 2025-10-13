// Package main implements a Cloud Run service that monitors ADVRider threads
// and sends email notifications via Gmail API when new posts are detected.
package main

import (
	"context"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/codeGROOVE-dev/retry"
	"golang.org/x/net/html"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

//go:embed media/*
var mediaFS embed.FS

const (
	checkInterval    = 10 * time.Minute
	maxPostsPerEmail = 5 // Safety limit: max posts to include in a single email
)

// HTTP403Error indicates a 403 Forbidden response (login required).
type HTTP403Error struct {
	URL string
}

func (e *HTTP403Error) Error() string {
	return fmt.Sprintf("HTTP 403 Forbidden: %s", e.URL)
}

func isHTTP403Error(err error) bool {
	var forbidden *HTTP403Error
	return errors.As(err, &forbidden)
}

type Post struct {
	ID        string
	Author    string
	Content   string
	Timestamp string
	URL       string
}

type ThreadPage struct {
	Posts       []*Post
	Title       string
	LastPage    int // Last page number (0 if single page)
	CurrentPage int // Current page number
}

type Thread struct {
	LastPostTime time.Time `json:"last_post_time"` // When the last post was seen
	LastPolledAt time.Time `json:"last_polled_at"` // When we last checked this thread
	CreatedAt    time.Time `json:"created_at"`     // Subscription timestamp
	ThreadURL    string    `json:"thread_url"`     // Full thread URL
	ThreadID     string    `json:"thread_id"`      // Extracted thread ID
	ThreadTitle  string    `json:"thread_title"`   // Thread title for email threading
	LastPostID   string    `json:"last_post_id"`   // Track last seen post
}

type Subscription struct {
	Threads map[string]*Thread `json:"threads"` // Map of threadID -> Thread
	Email   string             `json:"email"`   // Subscriber email
	Token   string             `json:"token"`   // Secure token for unsubscribe
}

type Monitor struct {
	gmailService  *gmail.Service
	storageClient *storage.Client
	bucket        string
	logger        *slog.Logger
	httpClient    *http.Client
	baseURL       string // For unsubscribe links
	localStorage  string // Local storage path for development (optional)
	mockEmail     bool   // Mock email sending for development
}

func main() {
	ctx := context.Background()

	// Initialize structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Check for local development mode
	localStorage := os.Getenv("LOCAL_STORAGE")
	bucket := os.Getenv("STORAGE_BUCKET")
	baseURL := os.Getenv("BASE_URL")

	// Default to local development mode if no bucket specified
	if bucket == "" && localStorage == "" {
		localStorage = "./data"
		logger.Info("No STORAGE_BUCKET set, defaulting to local development mode", "storage_path", localStorage)
	}

	// Local development mode
	if localStorage != "" {
		logger.Info("Running in local development mode", "storage_path", localStorage)
		if baseURL == "" {
			baseURL = "http://localhost:8080"
		}

		// Create local storage directory
		if err := os.MkdirAll(localStorage, 0755); err != nil {
			logger.Error("Failed to create local storage directory", "error", err)
			os.Exit(1)
		}

		// Mock email unless credentials are provided
		mockEmail := os.Getenv("GOOGLE_CREDENTIALS_JSON") == ""
		if mockEmail {
			logger.Info("Mock email mode enabled (no GOOGLE_CREDENTIALS_JSON)")
		}

		var gmailService *gmail.Service
		if !mockEmail {
			var err error
			gmailService, err = initGmailService(ctx)
			if err != nil {
				logger.Warn("Failed to initialize Gmail service, using mock email", "error", err)
				mockEmail = true
			}
		}

		monitor := &Monitor{
			gmailService: gmailService,
			logger:       logger,
			httpClient:   &http.Client{Timeout: 30 * time.Second},
			baseURL:      baseURL,
			localStorage: localStorage,
			mockEmail:    mockEmail,
		}

		startServer(monitor, logger)
		return
	}

	// Production mode (Cloud Run)
	if bucket == "" {
		logger.Error("STORAGE_BUCKET environment variable required")
		os.Exit(1)
	}

	if baseURL == "" {
		logger.Error("BASE_URL environment variable required (e.g., https://your-service.run.app)")
		os.Exit(1)
	}

	// Initialize Gmail service
	gmailService, err := initGmailService(ctx)
	if err != nil {
		logger.Error("Failed to initialize Gmail service", "error", err)
		os.Exit(1)
	}

	// Initialize Storage client
	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		logger.Error("Failed to initialize Storage client", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := storageClient.Close(); err != nil {
			logger.Warn("Failed to close storage client", "error", err)
		}
	}()

	monitor := &Monitor{
		gmailService:  gmailService,
		storageClient: storageClient,
		bucket:        bucket,
		logger:        logger,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		baseURL:       baseURL,
	}

	startServer(monitor, logger)
}

func startServer(monitor *Monitor, logger *slog.Logger) {
	// HTTP server for Cloud Run
	http.HandleFunc("/", monitor.handleRoot)
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/pollz", monitor.handlePoll)
	http.HandleFunc("/subscribe", monitor.handleSubscribe)
	http.HandleFunc("/unsubscribe", monitor.handleUnsubscribe)
	http.HandleFunc("/manage", monitor.handleManage)

	// Serve static media files from embedded filesystem
	mediaSubFS, err := fs.Sub(mediaFS, "media")
	if err != nil {
		logger.Error("Failed to create media sub-filesystem", "error", err)
		os.Exit(1)
	}
	http.Handle("/media/", http.StripPrefix("/media/", http.FileServer(http.FS(mediaSubFS))))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	logger.Info("Starting HTTP server", "port", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		logger.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

// isCloudRun checks if we're running in a GCP environment by querying the metadata server.
func isCloudRun(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://metadata.google.internal/computeMetadata/v1/project/project-id", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Metadata-Flavor", "Google")

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	return resp.StatusCode == http.StatusOK
}

func initGmailService(ctx context.Context) (*gmail.Service, error) {
	// Try explicit credentials first (for local development or specific use cases)
	credsJSON := os.Getenv("GOOGLE_CREDENTIALS_JSON")
	if credsJSON != "" {
		return gmail.NewService(ctx, option.WithCredentialsJSON([]byte(credsJSON)))
	}

	// If running in Cloud Run, use Application Default Credentials (ADC)
	// This automatically uses the service account
	// The service account needs Gmail API access (gmail.send scope)
	if isCloudRun(ctx) {
		return gmail.NewService(ctx)
	}

	// Not in Cloud Run and no explicit credentials
	return nil, errors.New("GOOGLE_CREDENTIALS_JSON required when not running in Cloud Run")
}

func (m *Monitor) handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	m.logger.Info("Poll endpoint triggered")

	if err := m.checkAllSubscriptions(r.Context()); err != nil {
		m.logger.Error("Poll check failed", "error", err)
		http.Error(w, "Check failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(w, `{"status":"completed"}`); err != nil {
		m.logger.Warn("Failed to write response", "error", err)
	}
}

// calculatePollInterval determines how often to poll a thread based on activity.
func calculatePollInterval(lastPostTime, lastPolledAt time.Time) time.Duration {
	// If never polled or never seen a post, poll now
	if lastPolledAt.IsZero() || lastPostTime.IsZero() {
		return 0
	}

	// Calculate time since last post
	timeSinceLastPost := time.Since(lastPostTime)

	var interval time.Duration
	switch {
	case timeSinceLastPost < 30*time.Minute:
		interval = 5 * time.Minute
	case timeSinceLastPost < 2*time.Hour:
		interval = 10 * time.Minute
	case timeSinceLastPost < 6*time.Hour:
		interval = 20 * time.Minute
	case timeSinceLastPost < 24*time.Hour:
		interval = 1 * time.Hour
	default:
		interval = 6 * time.Hour
	}

	return interval
}

func (m *Monitor) checkAllSubscriptions(ctx context.Context) error {
	subs, err := m.listSubscriptions(ctx)
	if err != nil {
		return fmt.Errorf("list subscriptions: %w", err)
	}

	now := time.Now()
	m.logger.Info("Checking subscriptions", "count", len(subs), "timestamp", now.Format(time.RFC3339))

	// Group threads by URL to fetch each thread only once
	threadCache := make(map[string][]*Post)
	var totalThreads, skippedThreads int

	for _, sub := range subs {
		for threadID, thread := range sub.Threads {
			totalThreads++

			// Check if thread needs polling based on activity
			interval := calculatePollInterval(thread.LastPostTime, thread.LastPolledAt)
			timeSinceLastPoll := now.Sub(thread.LastPolledAt)
			if timeSinceLastPoll < interval {
				nextPoll := thread.LastPolledAt.Add(interval)
				m.logger.Debug("Skipping thread (not due for polling)",
					"email", sub.Email,
					"thread_id", threadID,
					"last_polled", thread.LastPolledAt.Format(time.RFC3339),
					"next_poll", nextPoll.Format(time.RFC3339),
					"interval", interval.String())
				skippedThreads++
				continue
			}

			if err := m.checkThread(ctx, sub, threadID, thread, threadCache); err != nil {
				m.logger.Warn("Thread check failed", "email", sub.Email, "thread_id", threadID, "error", err)
				// Continue with other threads despite errors
			}
		}
	}

	m.logger.Info("Subscription check completed",
		"total_threads", totalThreads,
		"checked", totalThreads-skippedThreads,
		"skipped", skippedThreads)

	return nil
}

func (m *Monitor) checkThread(ctx context.Context, sub *Subscription, threadID string, thread *Thread, cache map[string][]*Post) error {
	now := time.Now()

	m.logger.Info("Starting thread check",
		"email", sub.Email,
		"thread_id", threadID,
		"thread_url", thread.ThreadURL,
		"last_post_id", thread.LastPostID)

	// Check cache first to avoid redundant fetches
	posts, ok := cache[thread.ThreadURL]
	if !ok {
		// Use smart fetch to get posts efficiently
		page, err := m.fetchSmartThreadPosts(ctx, thread.ThreadURL, thread.LastPostID)
		if err != nil {
			return fmt.Errorf("fetch thread page: %w", err)
		}
		posts = page.Posts
		cache[thread.ThreadURL] = posts

		// Update thread title if not set
		if thread.ThreadTitle == "" {
			thread.ThreadTitle = page.Title
			m.logger.Info("Thread title captured", "title", page.Title)
		}
	}

	// Update last polled time
	thread.LastPolledAt = now

	if len(posts) == 0 {
		return errors.New("no posts found in thread")
	}

	latestPost := posts[len(posts)-1]

	// Parse the timestamp of the latest post
	if latestPost.Timestamp != "" {
		if postTime, err := time.Parse(time.RFC3339, latestPost.Timestamp); err == nil {
			thread.LastPostTime = postTime
		}
	}

	m.logger.Info("Posts fetched for comparison",
		"total_posts", len(posts),
		"first_post_id", posts[0].ID,
		"latest_post_id", latestPost.ID,
		"last_seen_post_id", thread.LastPostID,
		"last_post_time", thread.LastPostTime.Format(time.RFC3339))

	if thread.LastPostID == "" {
		// First check - just record the latest post ID and times
		thread.LastPostID = latestPost.ID
		if err := m.saveSubscription(ctx, sub); err != nil {
			return fmt.Errorf("save subscription: %w", err)
		}
		m.logger.Info("Initial post ID recorded", "email", sub.Email, "thread_id", threadID, "post_id", latestPost.ID, "title", thread.ThreadTitle)
		return nil
	}

	// Find all new posts since LastPostID
	var newPosts []*Post
	foundLast := false
	for i, post := range posts {
		if foundLast {
			newPosts = append(newPosts, post)
			m.logger.Debug("Found new post", "index", i, "post_id", post.ID, "author", post.Author)
		}
		if post.ID == thread.LastPostID {
			foundLast = true
			m.logger.Info("Found last seen post", "index", i, "post_id", post.ID)
		}
	}

	if !foundLast && thread.LastPostID != "" {
		m.logger.Warn("Last seen post ID not found in fetched posts - possible gap or old post",
			"last_seen_post_id", thread.LastPostID,
			"posts_fetched", len(posts),
			"first_fetched_id", posts[0].ID,
			"last_fetched_id", latestPost.ID)
		// Treat all fetched posts as new (safer than missing posts)
		newPosts = posts
	}

	if len(newPosts) > 0 {
		// Apply safety limit - only send the most recent maxPostsPerEmail posts
		if len(newPosts) > maxPostsPerEmail {
			m.logger.Warn("Too many new posts, limiting to most recent",
				"email", sub.Email,
				"thread_id", threadID,
				"total_new", len(newPosts),
				"sending", maxPostsPerEmail)
			newPosts = newPosts[len(newPosts)-maxPostsPerEmail:]
		}

		m.logger.Info("New posts detected",
			"email", sub.Email,
			"thread_id", threadID,
			"count", len(newPosts),
			"latest_post_id", latestPost.ID,
			"previous", thread.LastPostID)

		if err := m.sendEmail(ctx, sub, thread, newPosts); err != nil {
			return fmt.Errorf("send email: %w", err)
		}

		thread.LastPostID = latestPost.ID
		if err := m.saveSubscription(ctx, sub); err != nil {
			return fmt.Errorf("save subscription: %w", err)
		}
	} else {
		// No new posts, but still save to update LastPolledAt and LastPostTime
		if err := m.saveSubscription(ctx, sub); err != nil {
			return fmt.Errorf("save subscription: %w", err)
		}
	}

	return nil
}

// buildPageURL constructs a URL for a specific page number.
func buildPageURL(baseURL string, pageNum int) string {
	if pageNum <= 1 {
		return baseURL
	}
	// Remove trailing slash if present
	baseURL = strings.TrimSuffix(baseURL, "/")
	return fmt.Sprintf("%s/page-%d", baseURL, pageNum)
}

// fetchSmartThreadPosts fetches posts efficiently using multi-page strategy.
func (m *Monitor) fetchSmartThreadPosts(ctx context.Context, threadURL string, lastSeenPostID string) (*ThreadPage, error) {
	m.logger.Info("Starting smart thread fetch", "url", threadURL, "last_seen_post", lastSeenPostID)

	// Step 1: Fetch first page to get title and last page number
	firstPage, err := m.fetchSinglePage(ctx, threadURL)
	if err != nil {
		return nil, fmt.Errorf("fetch first page: %w", err)
	}

	m.logger.Info("First page fetched",
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
	lastPage, err := m.fetchSinglePage(ctx, lastPageURL)
	if err != nil {
		return nil, fmt.Errorf("fetch last page: %w", err)
	}

	m.logger.Info("Last page fetched",
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
		m.logger.Info("Last seen post not found on last page, fetching second-to-last page",
			"last_seen_post", lastSeenPostID,
			"fetching_page", firstPage.LastPage-1)

		secondToLastURL := buildPageURL(threadURL, firstPage.LastPage-1)
		secondToLastPage, err := m.fetchSinglePage(ctx, secondToLastURL)
		if err != nil {
			m.logger.Warn("Failed to fetch second-to-last page, continuing with last page only", "error", err)
		} else {
			m.logger.Info("Second-to-last page fetched", "posts_on_page", len(secondToLastPage.Posts))
			// Prepend second-to-last page posts (they're older)
			allPosts = append(secondToLastPage.Posts, lastPage.Posts...)
		}
	}

	return &ThreadPage{
		Posts:       allPosts,
		Title:       firstPage.Title,
		LastPage:    firstPage.LastPage,
		CurrentPage: lastPage.CurrentPage,
	}, nil
}

// fetchSinglePage fetches a single thread page.
func (m *Monitor) fetchSinglePage(ctx context.Context, pageURL string) (*ThreadPage, error) {
	var page *ThreadPage

	err := retry.Do(
		func() error {
			m.logger.Info("HTTP request starting",
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
			req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
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
			resp, err := m.httpClient.Do(req)
			duration := time.Since(startTime)

			if err != nil {
				m.logger.Warn("HTTP request failed, will retry",
					"url", pageURL,
					"duration_ms", duration.Milliseconds(),
					"error", err)
				return err
			}
			defer func() {
				if closeErr := resp.Body.Close(); closeErr != nil {
					m.logger.Warn("Failed to close response body", "error", closeErr)
				}
			}()

			m.logger.Info("HTTP request completed",
				"url", pageURL,
				"status_code", resp.StatusCode,
				"duration_ms", duration.Milliseconds(),
				"content_length", resp.ContentLength)

			if resp.StatusCode == http.StatusForbidden {
				m.logger.Warn("HTTP 403 Forbidden - thread requires login", "url", pageURL)
				return &HTTP403Error{URL: pageURL}
			}

			if resp.StatusCode != http.StatusOK {
				m.logger.Warn("HTTP request returned non-OK status, will retry", "status_code", resp.StatusCode)
				return fmt.Errorf("HTTP %d", resp.StatusCode)
			}

			page, err = parseThreadPage(resp.Body, pageURL)
			if err != nil {
				m.logger.Warn("Failed to parse HTML, will retry", "error", err)
				return err
			}

			m.logger.Info("Thread page parsed successfully",
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
			m.logger.Info("Retrying fetch after error", "attempt", n, "error", err)
		}),
		retry.RetryIf(func(err error) bool {
			// Don't retry on 403 Forbidden errors (login required)
			return !isHTTP403Error(err)
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("after retries: %w", err)
	}

	return page, nil
}

func (m *Monitor) fetchLatestPost(ctx context.Context, threadURL string) (*Post, error) {
	page, err := m.fetchSmartThreadPosts(ctx, threadURL, "")
	if err != nil {
		return nil, err
	}
	posts := page.Posts
	if len(posts) == 0 {
		return nil, errors.New("no posts found")
	}
	return posts[len(posts)-1], nil
}

// parseThreadPage extracts title, posts, and pagination info from a thread page.
func parseThreadPage(body interface{ Read([]byte) (int, error) }, threadURL string) (*ThreadPage, error) {
	doc, err := html.Parse(body)
	if err != nil {
		return nil, err
	}

	var posts []*Post
	var title string
	var lastPage, currentPage int

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		// Extract thread title from <title> tag or h1.p-title-value
		if n.Type == html.ElementNode {
			if n.Data == "h1" && hasClass(n, "p-title-value") {
				title = strings.TrimSpace(text(n))
			} else if n.Data == "title" && title == "" {
				// Fallback: extract from <title> tag and clean it up
				rawTitle := strings.TrimSpace(text(n))
				// Remove " | ADVrider" suffix if present
				if idx := strings.Index(rawTitle, " | "); idx > 0 {
					title = rawTitle[:idx]
				} else {
					title = rawTitle
				}
			}

			// Extract pagination info from pageNav elements
			if n.Data == "a" && hasClass(n, "pageNav-page") {
				pageText := strings.TrimSpace(text(n))
				if pageNum, err := strconv.Atoi(pageText); err == nil {
					if pageNum > lastPage {
						lastPage = pageNum
					}
				}
			}

			// Extract current page from pageNav-page--current
			if n.Data == "li" && hasClass(n, "pageNav-page--current") {
				pageText := strings.TrimSpace(text(n))
				if pageNum, err := strconv.Atoi(pageText); err == nil {
					currentPage = pageNum
				}
			}

			// Extract posts from li elements with id="post-XXX" and class="message"
			if n.Data == "li" && hasClass(n, "message") {
				var id, author, content, ts string

				for _, a := range n.Attr {
					if a.Key == "id" {
						// Extract post ID from id like "post-12345"
						if strings.HasPrefix(a.Val, "post-") {
							id = strings.TrimPrefix(a.Val, "post-")
						}
					}
				}

				// Extract author, content, and timestamp from child nodes
				extractData(n, &author, &content, &ts)

				if id != "" && content != "" {
					posts = append(posts, &Post{
						ID:        id,
						Author:    author,
						Content:   content,
						Timestamp: ts,
						URL:       threadURL + "#post-" + id,
					})
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(doc)

	if len(posts) == 0 {
		return nil, errors.New("no posts found")
	}

	if title == "" {
		title = "ADVRider Thread"
	}

	if currentPage == 0 {
		currentPage = 1
	}

	return &ThreadPage{
		Posts:       posts,
		Title:       title,
		LastPage:    lastPage,
		CurrentPage: currentPage,
	}, nil
}

func extractData(n *html.Node, author, content, timestamp *string) {
	if n.Type == html.ElementNode {
		// Extract author from username link
		if n.Data == "a" && hasClass(n, "username") {
			*author = text(n)
		}

		// Extract timestamp
		if n.Data == "time" {
			for _, a := range n.Attr {
				if a.Key == "datetime" {
					*timestamp = a.Val
				}
			}
		}

		// Extract post content from blockquote with class messageText
		if n.Data == "blockquote" && hasClass(n, "messageText") {
			*content = textContent(n)
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractData(c, author, content, timestamp)
	}
}

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" && strings.Contains(a.Val, class) {
			return true
		}
	}
	return false
}

func text(n *html.Node) string {
	if n.Type == html.TextNode {
		return strings.TrimSpace(n.Data)
	}
	var s string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		s += text(c)
	}
	return s
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var extract func(*html.Node)
	extract = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extract(c)
		}
	}
	extract(n)
	return strings.TrimSpace(b.String())
}

func (m *Monitor) sendEmail(ctx context.Context, sub *Subscription, thread *Thread, posts []*Post) error {
	if len(posts) == 0 {
		return nil
	}

	// Use thread title for email subject to enable proper threading in email clients
	subject := thread.ThreadTitle
	if subject == "" {
		subject = "ADVRider Thread Update"
	}

	body := m.formatEmailBody(sub, thread, posts)

	// Mock email mode for local development
	if m.mockEmail {
		m.logger.Info("MOCK EMAIL",
			"to", sub.Email,
			"subject", subject,
			"post_count", len(posts),
			"thread_id", thread.ThreadID)
		return nil
	}

	// Create MIME message
	var msg strings.Builder
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString(fmt.Sprintf("To: %s\r\n", sub.Email))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	msg.WriteString(body)
	encoded := base64.URLEncoding.EncodeToString([]byte(msg.String()))

	err := retry.Do(
		func() error {
			m.logger.Info("Gmail API request starting",
				"method", "POST",
				"endpoint", "users.messages.send",
				"to", sub.Email,
				"post_count", len(posts),
				"subject", subject)

			startTime := time.Now()
			_, err := m.gmailService.Users.Messages.Send("me", &gmail.Message{
				Raw: encoded,
			}).Context(ctx).Do()
			duration := time.Since(startTime)

			if err != nil {
				m.logger.Warn("Gmail API send failed, will retry",
					"to", sub.Email,
					"duration_ms", duration.Milliseconds(),
					"error", err)
				return err
			}

			m.logger.Info("Gmail API request completed",
				"endpoint", "users.messages.send",
				"to", sub.Email,
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
			m.logger.Info("Retrying email send after error", "attempt", n, "error", err)
		}),
	)

	if err != nil {
		return fmt.Errorf("after retries: %w", err)
	}

	m.logger.Info("Email successfully sent", "to", sub.Email, "post_count", len(posts))
	return nil
}

func (m *Monitor) formatEmailBody(sub *Subscription, thread *Thread, posts []*Post) string {
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
	manageURL := fmt.Sprintf("%s/manage?token=%s", m.baseURL, url.QueryEscape(sub.Token))
	b.WriteString(fmt.Sprintf("<a href=\"%s\">Manage Subscriptions</a>\n", escapeHTML(manageURL)))
	b.WriteString("</div>\n")

	b.WriteString("</body>\n</html>")

	return b.String()
}

// sendWelcomeEmail sends a welcome email when a user first subscribes to a thread.
func (m *Monitor) sendWelcomeEmail(ctx context.Context, sub *Subscription, thread *Thread, ip, userAgent string) error {
	// Use thread title for email subject to enable proper threading
	subject := thread.ThreadTitle
	if subject == "" {
		subject = "ADVRider Thread Update"
	}

	manageURL := fmt.Sprintf("%s/manage?token=%s", m.baseURL, url.QueryEscape(sub.Token))

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

	body := b.String()

	// Mock email mode for local development
	if m.mockEmail {
		m.logger.Info("MOCK WELCOME EMAIL",
			"to", sub.Email,
			"subject", subject,
			"thread_id", thread.ThreadID,
			"ip", ip)
		return nil
	}

	// Create MIME message
	var msg strings.Builder
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString(fmt.Sprintf("To: %s\r\n", sub.Email))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	msg.WriteString(body)
	encoded := base64.URLEncoding.EncodeToString([]byte(msg.String()))

	err := retry.Do(
		func() error {
			m.logger.Info("Gmail API request starting",
				"method", "POST",
				"endpoint", "users.messages.send",
				"to", sub.Email,
				"type", "welcome",
				"subject", subject)

			startTime := time.Now()
			_, err := m.gmailService.Users.Messages.Send("me", &gmail.Message{
				Raw: encoded,
			}).Context(ctx).Do()
			duration := time.Since(startTime)

			if err != nil {
				m.logger.Warn("Gmail API send failed, will retry",
					"to", sub.Email,
					"duration_ms", duration.Milliseconds(),
					"error", err)
				return err
			}

			m.logger.Info("Gmail API request completed",
				"endpoint", "users.messages.send",
				"to", sub.Email,
				"type", "welcome",
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
			m.logger.Info("Retrying welcome email send after error", "attempt", n, "error", err)
		}),
	)

	if err != nil {
		return fmt.Errorf("after retries: %w", err)
	}

	m.logger.Info("Welcome email successfully sent", "to", sub.Email)
	return nil
}

// escapeHTML escapes HTML special characters for security.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}
