// Package poll handles thread monitoring and checking for new posts.
package poll

import (
	"advrider-notifier/pkg/notifier"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
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
	scraper Scraper
	store   Store
	emailer Emailer
	logger  *slog.Logger
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
	subs, err := m.store.List(ctx)
	if err != nil {
		return fmt.Errorf("list subscriptions: %w", err)
	}

	now := time.Now()
	m.logger.Info("Checking subscriptions", "count", len(subs), "timestamp", now.Format(time.RFC3339))

	// Group threads by URL to fetch each thread only once
	cache := make(map[string][]*notifier.Post)
	var totalThreads, skippedThreads int

	for _, sub := range subs {
		for threadID, thread := range sub.Threads {
			// Check for context cancellation
			select {
			case <-ctx.Done():
				m.logger.Info("Context cancelled, stopping poll check", "error", ctx.Err())
				return ctx.Err()
			default:
				// Continue processing
			}

			totalThreads++

			// Check if thread needs polling based on activity
			interval := calculateInterval(thread.LastPostTime, thread.LastPolledAt)
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

			if err := m.checkThread(ctx, sub, threadID, thread, cache, now); err != nil {
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
