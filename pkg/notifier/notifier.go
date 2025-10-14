// Package notifier contains the core domain types for the ADVRider notification service.
package notifier

import "time"

// Post represents a single post in a thread.
type Post struct {
	ID          string
	Author      string
	Content     string // Plain text content for fallback
	HTMLContent string // HTML content with images and formatting
	Timestamp   string
	URL         string
}

// Thread represents a monitored thread with its state.
type Thread struct {
	LastPostTime time.Time `json:"last_post_time"` // When the last post was seen
	LastPolledAt time.Time `json:"last_polled_at"` // When we last checked this thread
	CreatedAt    time.Time `json:"created_at"`     // Subscription timestamp
	ThreadURL    string    `json:"thread_url"`     // Full thread URL
	ThreadID     string    `json:"thread_id"`      // Extracted thread ID
	ThreadTitle  string    `json:"thread_title"`   // Thread title for email threading
	LastPostID   string    `json:"last_post_id"`   // Track last seen post
}

// Subscription represents a user's subscription to one or more threads.
type Subscription struct {
	Threads map[string]*Thread `json:"threads"` // Map of threadID -> Thread
	Email   string             `json:"email"`   // Subscriber email
	Token   string             `json:"token"`   // Secure token for unsubscribe
}
