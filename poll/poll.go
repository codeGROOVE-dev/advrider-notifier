// Package poll handles thread monitoring and checking for new posts.
package poll

import (
	"advrider-notifier/pkg/notifier"
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

const maxPostsPerEmail = 10 // Safety limit: max posts to include in a single email

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
	pollMutex   sync.Mutex // Prevents concurrent polling
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
// This function is protected by a mutex to prevent concurrent polling.
func (m *Monitor) CheckAll(ctx context.Context) error {
	// Try to acquire the lock - if already polling, skip this cycle
	if !m.pollMutex.TryLock() {
		m.logger.Warn("Poll cycle already in progress - skipping this invocation")
		return nil
	}
	defer m.pollMutex.Unlock()

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
			} else if thread.LastPolledAt.IsZero() && !uniqueThreads[thread.ThreadURL].thread.LastPolledAt.IsZero() {
				// If we already have this thread but current subscriber needs immediate check (LastPolledAt.IsZero()),
				// use this subscriber's state instead so the thread gets polled immediately
				uniqueThreads[thread.ThreadURL].thread = thread
				uniqueThreads[thread.ThreadURL].threadID = threadID
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

		// New subscriptions (LastPolledAt.IsZero()) should be checked immediately
		var interval time.Duration
		var reason string
		var timeSinceLastPoll time.Duration
		var needsCheck bool

		if thread.LastPolledAt.IsZero() {
			// New subscription - check immediately
			interval = 0
			reason = "new subscription - first check"
			needsCheck = true
		} else {
			interval, reason = CalculateInterval(thread.LastPostTime, thread.LastPolledAt)
			timeSinceLastPoll = time.Since(thread.LastPolledAt)
			needsCheck = timeSinceLastPoll >= interval
		}

		// Format times for logging, handling zero values
		lastPolledStr := "never"
		if !thread.LastPolledAt.IsZero() {
			lastPolledStr = thread.LastPolledAt.Format(time.RFC3339)
		}
		lastPostTimeStr := "none"
		if !thread.LastPostTime.IsZero() {
			lastPostTimeStr = thread.LastPostTime.Format(time.RFC3339)
		}
		timeSinceLastPostStr := "n/a"
		if !thread.LastPostTime.IsZero() {
			timeSinceLastPostStr = time.Since(thread.LastPostTime).Round(time.Second).String()
		}
		timeSincePollStr := "n/a"
		if !thread.LastPolledAt.IsZero() {
			timeSincePollStr = timeSinceLastPoll.Round(time.Second).String()
		}

		m.logger.Info(fmt.Sprintf("Thread %d/%d: Evaluating", threadNum, len(uniqueThreads)),
			"cycle", m.cycleNumber,
			"thread_url", threadURL,
			"thread_title", thread.ThreadTitle,
			"subscriber_count", len(info.subscribers),
			"last_polled", lastPolledStr,
			"last_post_time", lastPostTimeStr,
			"time_since_last_post", timeSinceLastPostStr,
			"time_since_poll", timeSincePollStr,
			"required_interval", interval.String(),
			"interval_reason", reason,
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
func (m *Monitor) checkThreadForSubscribers(
	ctx context.Context,
	info *threadCheckInfo,
	cache map[string][]*notifier.Post,
	now time.Time,
) (bool, map[string]bool, error) {
	threadURL := info.thread.ThreadURL

	// Fetch posts and update thread titles
	posts, latestPostTime, err := m.fetchThreadPosts(ctx, info, cache)
	if err != nil {
		return false, nil, err
	}

	if len(posts) == 0 {
		// Update LastPolledAt for all subscribers and save
		savedEmails := make(map[string]bool)
		for email, sub := range info.subscribers {
			thread := sub.Threads[info.threadID]
			if thread == nil {
				m.logger.Error("CRITICAL: Thread not found when updating poll time - data corruption",
					"cycle", m.cycleNumber,
					"thread_id", info.threadID)
				continue
			}
			thread.LastPolledAt = now

			if err := m.store.Save(ctx, sub); err != nil {
				m.logger.Error("Failed to save state after no posts returned",
					"cycle", m.cycleNumber,
					"email", email,
					"thread_url", threadURL,
					"error", err)
			} else {
				savedEmails[email] = true
			}
		}
		return false, savedEmails, nil
	}

	latestPost := posts[len(posts)-1]
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
		if thread == nil {
			m.logger.Error("CRITICAL: Thread not found in subscriber's thread map - data corruption or logic error",
				"cycle", m.cycleNumber,
				"email", email,
				"thread_id", info.threadID,
				"thread_url", threadURL)
			continue
		}

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
		} else if thread.LastPostTime.IsZero() {
			m.logger.Warn("No post timestamp available after fetching thread - interval calculation will default to immediate polling",
				"cycle", m.cycleNumber,
				"email", email,
				"thread_url", threadURL,
				"thread_title", thread.ThreadTitle)
		}

		// Legacy/recovery case: If LastPostID is empty (shouldn't happen for subscriptions created via
		// the subscribe handler, but could occur from manual storage edits or migrations), just record
		// the current latest post without sending a notification.
		if thread.LastPostID == "" {
			thread.LastPostID = latestPost.ID
			m.logger.Info("Empty LastPostID detected - recording current state without notification (recovery mode)",
				"cycle", m.cycleNumber,
				"email", email,
				"thread_url", threadURL,
				"thread_title", thread.ThreadTitle,
				"initial_post_id", latestPost.ID)
			m.saveStateNoNewPosts(ctx, sub, email, info.threadID, threadURL, savedEmails)
			continue // Move to next subscriber (other subscribers will still be notified)
		}

		// Find new posts for this subscriber
		newPosts := m.findNewPosts(posts, thread, email, threadURL)

		if len(newPosts) > 0 {
			if m.sendNotificationAndSave(ctx, sub, thread, newPosts, latestPost, email, threadURL, savedEmails) {
				hasUpdates = true
			}
		} else {
			m.saveStateNoNewPosts(ctx, sub, email, info.threadID, threadURL, savedEmails)
		}
	}

	return hasUpdates, savedEmails, nil
}

// fetchThreadPosts fetches posts for a thread (using cache if available) and updates thread titles.
func (m *Monitor) fetchThreadPosts(
	ctx context.Context,
	info *threadCheckInfo,
	cache map[string][]*notifier.Post,
) ([]*notifier.Post, time.Time, error) {
	threadURL := info.thread.ThreadURL
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
			return nil, time.Time{}, fmt.Errorf("fetch thread page: %w", err)
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
			if thread == nil {
				m.logger.Error("CRITICAL: Thread not found when updating title - data corruption",
					"cycle", m.cycleNumber,
					"thread_id", info.threadID,
					"thread_url", threadURL)
				continue
			}
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
		return posts, time.Time{}, nil
	}

	// Parse the timestamp of the latest post
	var latestPostTime time.Time
	latestPost := posts[len(posts)-1]
	if latestPost.Timestamp != "" {
		if postTime, err := time.Parse(time.RFC3339, latestPost.Timestamp); err == nil {
			latestPostTime = postTime
		}
	}

	return posts, latestPostTime, nil
}

// findNewPosts identifies new posts for a subscriber since their last seen post.
func (m *Monitor) findNewPosts(posts []*notifier.Post, thread *notifier.Thread, email, threadURL string) []*notifier.Post {
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

	return newPosts
}

// sendNotificationAndSave sends a notification for new posts and saves the updated state.
func (m *Monitor) sendNotificationAndSave(ctx context.Context, sub *notifier.Subscription, thread *notifier.Thread, newPosts []*notifier.Post, latestPost *notifier.Post, email, threadURL string, savedEmails map[string]bool) bool {
	// Apply safety limit
	originalCount := len(newPosts)
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

	m.logger.Info("Sending notification",
		"cycle", m.cycleNumber,
		"email", email,
		"thread_url", threadURL,
		"thread_title", thread.ThreadTitle,
		"new_posts_count", len(newPosts),
		"original_count", originalCount,
		"capped", originalCount > maxPostsPerEmail,
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
		return false
	}

	// Update last post ID after successful notification
	thread.LastPostID = latestPost.ID

	m.logger.Info("Saving state after successful notification",
		"cycle", m.cycleNumber,
		"email", email,
		"thread_url", threadURL,
		"new_last_post_id", latestPost.ID)

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

	return true
}

// saveStateNoNewPosts saves state when there are no new posts for a subscriber.
func (m *Monitor) saveStateNoNewPosts(ctx context.Context, sub *notifier.Subscription, email, threadID, threadURL string, savedEmails map[string]bool) {
	thread := sub.Threads[threadID]
	if thread == nil {
		m.logger.Error("CRITICAL: Thread not found when saving state (no new posts) - data corruption",
			"cycle", m.cycleNumber,
			"email", email,
			"thread_id", threadID,
			"thread_url", threadURL)
		return
	}

	m.logger.Info("No new posts - saving state",
		"cycle", m.cycleNumber,
		"email", email,
		"thread_url", threadURL,
		"thread_title", thread.ThreadTitle)

	if err := m.store.Save(ctx, sub); err != nil {
		m.logger.Error("Failed to save state (no new posts)",
			"cycle", m.cycleNumber,
			"email", email,
			"thread_url", threadURL,
			"thread_title", thread.ThreadTitle,
			"error", err)
	} else {
		savedEmails[email] = true
		m.logger.Info("State saved successfully (no new posts)",
			"cycle", m.cycleNumber,
			"email", email,
			"thread_url", threadURL,
			"thread_title", thread.ThreadTitle)
	}
}

// CalculateInterval determines how often to poll a thread based on activity.
// Uses exponential backoff: the longer since the last post, the less frequently we check.
// Formula: interval = min(minInterval * 2^(hours_since_post / scaleFactor), maxInterval)
//
// This provides smooth scaling:
//   - 0h since post → 5 minutes
//   - 3h since post → 10 minutes
//   - 6h since post → 20 minutes
//   - 12h since post → 80 minutes
//   - 24h+ since post → 4 hours (capped)
//
// NEVER returns 0s - always returns a minimum interval to prevent polling loops.
func CalculateInterval(lastPostTime, lastPolledAt time.Time) (time.Duration, string) {
	const minInterval = 5 * time.Minute // Minimum safe interval
	const maxInterval = 4 * time.Hour   // Maximum interval for inactive threads
	const scaleFactor = 3.0             // Hours before interval doubles (smaller = more aggressive backoff)

	// CRITICAL: These should NEVER be zero after subscription creation.
	// If they are, it indicates a serious bug in subscription or polling logic.
	if lastPolledAt.IsZero() {
		return maxInterval, "CRITICAL ERROR: LastPolledAt is zero (this should never happen - bug in subscription creation)"
	}

	if lastPostTime.IsZero() {
		return maxInterval, "CRITICAL ERROR: LastPostTime is zero (this should never happen - timestamp validation failed)"
	}

	// Calculate time since last post
	hoursSincePost := time.Since(lastPostTime).Hours()

	// Exponential backoff: interval doubles every scaleFactor hours
	// Example with scaleFactor=3: 0h→5m, 3h→10m, 6h→20m, 9h→40m, 12h→80m
	multiplier := math.Pow(2.0, hoursSincePost/scaleFactor)
	interval := time.Duration(float64(minInterval) * multiplier)

	// Clamp to min/max bounds
	if interval > maxInterval {
		interval = maxInterval
	}
	if interval < minInterval {
		interval = minInterval
	}

	// Format reason with readable time units
	var reason string
	switch {
	case hoursSincePost < 1:
		reason = fmt.Sprintf("%.0f minutes since last post", hoursSincePost*60)
	case hoursSincePost < 48:
		reason = fmt.Sprintf("%.1f hours since last post", hoursSincePost)
	default:
		reason = fmt.Sprintf("%.0f days since last post", hoursSincePost/24)
	}

	return interval, reason
}
