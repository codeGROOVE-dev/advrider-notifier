// Package poll handles thread monitoring and checking for new posts.
package poll

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"advrider-notifier/pkg/notifier"
)

const maxPostsPerEmail = 5 // Safety limit: max posts to include in a single email

// Scraper interface for fetching thread data.
type Scraper interface {
	SmartFetch(ctx context.Context, threadURL string, lastSeenPostID string) (posts []*notifier.Post, title string, err error)
}

// Store interface for subscription persistence.
type Store interface {
	Save(ctx context.Context, sub *notifier.Subscription) error
	List(ctx context.Context) ([]*notifier.Subscription, error)
}

// Emailer interface for sending notifications.
type Emailer interface {
	SendNotification(ctx context.Context, sub *notifier.Subscription, thread *notifier.Thread, posts []*notifier.Post) error
}

// Monitor handles thread polling logic.
type Monitor struct {
	scraper     Scraper
	store       Store
	emailer     Emailer
	logger      *slog.Logger
	cycleNumber int
}

// New creates a new poll monitor.
func New(scraper Scraper, store Store, emailer Emailer, logger *slog.Logger) *Monitor {
	return &Monitor{
		scraper: scraper,
		store:   store,
		emailer: emailer,
		logger:  logger,
	}
}

// CheckAll checks all subscriptions for new posts.
func (m *Monitor) CheckAll(ctx context.Context) error {
	m.cycleNumber++
	cycleStart := time.Now()

	m.logger.Info(fmt.Sprintf("========== POLL CYCLE #%d BEGAN ==========", m.cycleNumber),
		"cycle", m.cycleNumber,
		"timestamp", cycleStart.Format(time.RFC3339))

	subs, err := m.store.List(ctx)
	if err != nil {
		m.logger.Error("Failed to list subscriptions", "cycle", m.cycleNumber, "error", err)
		return fmt.Errorf("list subscriptions: %w", err)
	}

	m.logger.Info("Retrieved subscriptions", "cycle", m.cycleNumber, "subscription_count", len(subs))

	// Group threads by URL to fetch each thread only once
	cache := make(map[string][]*notifier.Post)
	subsToSave := make(map[string]bool) // Track which subscriptions need saving
	var totalThreads, skippedThreads, checkedThreads, threadsWithUpdates int

	// Build a unique set of threads to check
	uniqueThreads := make(map[string]*threadCheckInfo)
	for _, sub := range subs {
		for threadID, thread := range sub.Threads {
			totalThreads++

			if _, exists := uniqueThreads[thread.ThreadURL]; !exists {
				uniqueThreads[thread.ThreadURL] = &threadCheckInfo{
					threadID:    threadID,
					thread:      thread,
					needsCheck:  false,
					subscribers: make(map[string]*notifier.Subscription),
				}
			}
			uniqueThreads[thread.ThreadURL].subscribers[sub.Email] = sub
		}
	}

	m.logger.Info("Grouped threads by URL",
		"cycle", m.cycleNumber,
		"total_thread_subscriptions", totalThreads,
		"unique_threads", len(uniqueThreads))

	// Check each unique thread
	threadNum := 0
	for threadURL, info := range uniqueThreads {
		threadNum++

		// Check for context cancellation
		select {
		case <-ctx.Done():
			m.logger.Info("Context cancelled, stopping poll check",
				"cycle", m.cycleNumber,
				"error", ctx.Err())
			return ctx.Err()
		default:
			// Continue processing
		}

		// Use any subscriber's thread info to check intervals (they should all be the same)
		thread := info.thread
		interval := calculateInterval(thread.LastPostTime, thread.LastPolledAt)
		timeSinceLastPoll := time.Now().Sub(thread.LastPolledAt)
		needsCheck := timeSinceLastPoll >= interval

		m.logger.Info(fmt.Sprintf("Thread %d/%d: Evaluating", threadNum, len(uniqueThreads)),
			"cycle", m.cycleNumber,
			"thread_url", threadURL,
			"thread_title", thread.ThreadTitle,
			"subscriber_count", len(info.subscribers),
			"last_polled", thread.LastPolledAt.Format(time.RFC3339),
			"time_since_poll", timeSinceLastPoll.Round(time.Second).String(),
			"required_interval", interval.String(),
			"needs_check", needsCheck)

		if !needsCheck {
			nextPoll := thread.LastPolledAt.Add(interval)
			m.logger.Info(fmt.Sprintf("Thread %d/%d: SKIPPED (not due yet)", threadNum, len(uniqueThreads)),
				"cycle", m.cycleNumber,
				"thread_url", threadURL,
				"thread_title", thread.ThreadTitle,
				"next_poll_in", time.Until(nextPoll).Round(time.Second).String(),
				"next_poll_at", nextPoll.Format(time.RFC3339))
			skippedThreads += len(info.subscribers)
			continue
		}

		m.logger.Info(fmt.Sprintf("Thread %d/%d: CHECKING", threadNum, len(uniqueThreads)),
			"cycle", m.cycleNumber,
			"thread_url", threadURL,
			"thread_title", thread.ThreadTitle,
			"subscriber_count", len(info.subscribers))

		checkedThreads++

		// Check the thread and update all subscribers
		hasUpdates, savedEmails, err := m.checkThreadForSubscribers(ctx, info, cache, cycleStart)
		if err != nil {
			m.logger.Warn(fmt.Sprintf("Thread %d/%d: CHECK FAILED", threadNum, len(uniqueThreads)),
				"cycle", m.cycleNumber,
				"thread_url", threadURL,
				"thread_title", thread.ThreadTitle,
				"error", err)
			continue
		}

		if hasUpdates {
			threadsWithUpdates++
		}

		// Track all saved subscriptions for statistics
		for email := range savedEmails {
			subsToSave[email] = true
		}
	}

	savedCount := len(subsToSave)

	cycleEnd := time.Now()
	cycleDuration := cycleEnd.Sub(cycleStart)

	m.logger.Info(fmt.Sprintf("========== POLL CYCLE #%d COMPLETED ==========", m.cycleNumber),
		"cycle", m.cycleNumber,
		"duration", cycleDuration.Round(time.Millisecond).String(),
		"unique_threads", len(uniqueThreads),
		"total_subscriptions", totalThreads,
		"checked_threads", checkedThreads,
		"skipped_subscriptions", skippedThreads,
		"threads_with_updates", threadsWithUpdates,
		"subscriptions_saved", savedCount)

	return nil
}

type threadCheckInfo struct {
	threadID    string
	thread      *notifier.Thread
	needsCheck  bool
	subscribers map[string]*notifier.Subscription
}

// checkThreadForSubscribers checks a thread and notifies all subscribers if there are updates.
// Returns true if updates were found, and a map of emails that were successfully notified and saved.
func (m *Monitor) checkThreadForSubscribers(ctx context.Context, info *threadCheckInfo, cache map[string][]*notifier.Post, now time.Time) (bool, map[string]bool, error) {
	threadURL := info.thread.ThreadURL

	// Fetch posts (use cache if available)
	posts, ok := cache[threadURL]
	if !ok {
		m.logger.Info("Fetching thread from ADVRider",
			"cycle", m.cycleNumber,
			"thread_url", threadURL,
			"thread_title", info.thread.ThreadTitle,
			"last_post_id", info.thread.LastPostID)

		var title string
		var err error
		posts, title, err = m.scraper.SmartFetch(ctx, threadURL, info.thread.LastPostID)
		if err != nil {
			return false, nil, fmt.Errorf("fetch thread page: %w", err)
		}
		cache[threadURL] = posts

		m.logger.Info("Thread fetched successfully",
			"cycle", m.cycleNumber,
			"thread_url", threadURL,
			"posts_fetched", len(posts),
			"title", title)

		// Update thread title for all subscribers if not set
		for _, sub := range info.subscribers {
			thread := sub.Threads[info.threadID]
			if thread.ThreadTitle == "" {
				thread.ThreadTitle = title
			}
		}
	}

	if len(posts) == 0 {
		m.logger.Warn("No posts found in thread",
			"cycle", m.cycleNumber,
			"thread_url", threadURL,
			"thread_title", info.thread.ThreadTitle)

		// Update LastPolledAt for all subscribers even if no posts
		for _, sub := range info.subscribers {
			thread := sub.Threads[info.threadID]
			thread.LastPolledAt = now
		}
		return false, nil, nil
	}

	latestPost := posts[len(posts)-1]

	// Parse the timestamp of the latest post
	var latestPostTime time.Time
	if latestPost.Timestamp != "" {
		if postTime, err := time.Parse(time.RFC3339, latestPost.Timestamp); err == nil {
			latestPostTime = postTime
		}
	}

	m.logger.Info("Posts analyzed",
		"cycle", m.cycleNumber,
		"thread_url", threadURL,
		"thread_title", info.thread.ThreadTitle,
		"total_posts", len(posts),
		"latest_post_id", latestPost.ID,
		"latest_post_time", latestPostTime.Format(time.RFC3339))

	// Process each subscriber: check for new posts, send notification if needed, save state
	// This ensures crash safety - each subscriber is fully processed before moving to the next
	hasUpdates := false
	savedEmails := make(map[string]bool)

	for email, sub := range info.subscribers {
		thread := sub.Threads[info.threadID]

		m.logger.Info("Processing subscriber",
			"cycle", m.cycleNumber,
			"email", email,
			"thread_url", threadURL,
			"thread_title", thread.ThreadTitle,
			"last_post_id", thread.LastPostID)

		// Update poll time and latest post time for this subscriber
		thread.LastPolledAt = now
		if !latestPostTime.IsZero() {
			thread.LastPostTime = latestPostTime
		}

		// First check for this subscriber - just record the latest post ID
		if thread.LastPostID == "" {
			thread.LastPostID = latestPost.ID
			if err := m.store.Save(ctx, sub); err != nil {
				m.logger.Error("Failed to save initial state for subscriber",
					"cycle", m.cycleNumber,
					"email", email,
					"thread_url", threadURL,
					"thread_title", thread.ThreadTitle,
					"error", err)
			} else {
				savedEmails[email] = true
				m.logger.Info("Initial post ID recorded and saved",
					"cycle", m.cycleNumber,
					"email", email,
					"thread_url", threadURL,
					"thread_title", thread.ThreadTitle,
					"post_id", latestPost.ID)
			}
			hasUpdates = true
			continue
		}

		// Find new posts for this subscriber
		var newPosts []*notifier.Post
		foundLast := false
		for _, post := range posts {
			if foundLast {
				newPosts = append(newPosts, post)
			}
			if post.ID == thread.LastPostID {
				foundLast = true
			}
		}

		if !foundLast && thread.LastPostID != "" {
			m.logger.Warn("Last seen post ID not found - treating all posts as new",
				"cycle", m.cycleNumber,
				"email", email,
				"thread_url", threadURL,
				"thread_title", thread.ThreadTitle,
				"last_seen_post_id", thread.LastPostID,
				"posts_fetched", len(posts))
			newPosts = posts
		}

		if len(newPosts) > 0 {
			// Apply safety limit
			if len(newPosts) > maxPostsPerEmail {
				m.logger.Warn("Too many new posts, limiting to most recent",
					"cycle", m.cycleNumber,
					"email", email,
					"thread_url", threadURL,
					"thread_title", thread.ThreadTitle,
					"total_new", len(newPosts),
					"sending", maxPostsPerEmail)
				newPosts = newPosts[len(newPosts)-maxPostsPerEmail:]
			}

			// Send notification
			m.logger.Info("Sending notification",
				"cycle", m.cycleNumber,
				"email", email,
				"thread_url", threadURL,
				"thread_title", thread.ThreadTitle,
				"new_posts", len(newPosts),
				"previous_last_post", thread.LastPostID,
				"new_last_post", latestPost.ID)

			if err := m.emailer.SendNotification(ctx, sub, thread, newPosts); err != nil {
				m.logger.Error("Failed to send notification - will retry next cycle",
					"cycle", m.cycleNumber,
					"email", email,
					"thread_url", threadURL,
					"thread_title", thread.ThreadTitle,
					"error", err)
				// Don't update LastPostID - subscriber will get notification next cycle
				// Still save to update LastPolledAt
				if err := m.store.Save(ctx, sub); err != nil {
					m.logger.Error("Failed to save state after notification failure",
						"cycle", m.cycleNumber,
						"email", email,
						"error", err)
				} else {
					savedEmails[email] = true
				}
				continue
			}

			// Update last post ID after successful notification
			thread.LastPostID = latestPost.ID

			// CRITICAL: Save immediately to prevent duplicate notifications if server crashes
			if err := m.store.Save(ctx, sub); err != nil {
				m.logger.Error("CRITICAL: Notification sent but failed to save state - subscriber may get duplicate notification next cycle",
					"cycle", m.cycleNumber,
					"email", email,
					"thread_url", threadURL,
					"thread_title", thread.ThreadTitle,
					"sent_post_id", latestPost.ID,
					"error", err)
			} else {
				savedEmails[email] = true
				m.logger.Info("Notification sent and state saved",
					"cycle", m.cycleNumber,
					"email", email,
					"thread_url", threadURL,
					"thread_title", thread.ThreadTitle,
					"new_last_post_id", latestPost.ID)
			}

			hasUpdates = true
		} else {
			// No new posts for this subscriber - just save state to update LastPolledAt
			if err := m.store.Save(ctx, sub); err != nil {
				m.logger.Warn("Failed to save state (no new posts)",
					"cycle", m.cycleNumber,
					"email", email,
					"thread_url", threadURL,
					"thread_title", thread.ThreadTitle,
					"error", err)
			} else {
				savedEmails[email] = true
			}
		}
	}

	return hasUpdates, savedEmails, nil
}

// getFirstKey returns the first key from a map (for logging purposes).
func getFirstKey(m map[string][]*notifier.Post) string {
	for k := range m {
		return k
	}
	return ""
}

func (m *Monitor) checkThread(ctx context.Context, sub *notifier.Subscription, threadID string, thread *notifier.Thread, cache map[string][]*notifier.Post, now time.Time) error {
	m.logger.Info("Starting thread check",
		"email", sub.Email,
		"thread_id", threadID,
		"thread_url", thread.ThreadURL,
		"last_post_id", thread.LastPostID)

	// Check cache first to avoid redundant fetches
	posts, ok := cache[thread.ThreadURL]
	if !ok {
		// Use smart fetch to get posts efficiently
		var title string
		var err error
		posts, title, err = m.scraper.SmartFetch(ctx, thread.ThreadURL, thread.LastPostID)
		if err != nil {
			return fmt.Errorf("fetch thread page: %w", err)
		}
		cache[thread.ThreadURL] = posts

		// Update thread title if not set
		if thread.ThreadTitle == "" {
			thread.ThreadTitle = title
			m.logger.Info("Thread title captured", "title", title)
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
		if err := m.store.Save(ctx, sub); err != nil {
			return fmt.Errorf("save subscription: %w", err)
		}
		m.logger.Info("Initial post ID recorded", "email", sub.Email, "thread_id", threadID, "post_id", latestPost.ID, "title", thread.ThreadTitle)
		return nil
	}

	// Find all new posts since LastPostID
	var newPosts []*notifier.Post
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

		if err := m.emailer.SendNotification(ctx, sub, thread, newPosts); err != nil {
			return fmt.Errorf("send email: %w", err)
		}

		thread.LastPostID = latestPost.ID
		if err := m.store.Save(ctx, sub); err != nil {
			return fmt.Errorf("save subscription: %w", err)
		}
	} else {
		// No new posts, but still save to update LastPolledAt and LastPostTime
		if err := m.store.Save(ctx, sub); err != nil {
			return fmt.Errorf("save subscription: %w", err)
		}
	}

	return nil
}

// calculateInterval determines how often to poll a thread based on activity.
func calculateInterval(lastPostTime, lastPolledAt time.Time) time.Duration {
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
