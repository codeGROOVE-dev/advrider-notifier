package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// subscriptionKey generates a stable filename from an email address.
// Format: sub-{hash}.json where hash is SHA256 of email (first 16 chars)
func subscriptionKey(email string) string {
	h := sha256.Sum256([]byte(email))
	return fmt.Sprintf("sub-%x.json", h[:8])
}

// generateToken creates a secure random token for unsubscribe links.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// saveSubscription saves a subscription to storage (Cloud Storage or local filesystem).
func (m *Monitor) saveSubscription(ctx context.Context, sub *Subscription) error {
	key := subscriptionKey(sub.Email)
	m.logger.Debug("Saving subscription", "key", key, "email", sub.Email)

	data, err := json.MarshalIndent(sub, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal subscription: %w", err)
	}

	// Local filesystem storage
	if m.localStorage != "" {
		filePath := filepath.Join(m.localStorage, key)
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return fmt.Errorf("write to local storage: %w", err)
		}
		m.logger.Info("Subscription saved to local storage", "path", filePath, "email", sub.Email, "thread_count", len(sub.Threads))
		return nil
	}

	// Cloud Storage
	w := m.storageClient.Bucket(m.bucket).Object(key).NewWriter(ctx)
	if _, err := w.Write(data); err != nil {
		w.Close()
		return fmt.Errorf("write to storage: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close storage writer: %w", err)
	}

	m.logger.Info("Subscription saved", "key", key, "email", sub.Email, "thread_count", len(sub.Threads))
	return nil
}

// loadSubscription loads a subscription from storage (Cloud Storage or local filesystem).
func (m *Monitor) loadSubscription(ctx context.Context, key string) (*Subscription, error) {
	var data []byte
	var err error

	// Local filesystem storage
	if m.localStorage != "" {
		filePath := filepath.Join(m.localStorage, key)
		data, err = os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("storage: object doesn't exist")
			}
			return nil, fmt.Errorf("read from local storage: %w", err)
		}
	} else {
		// Cloud Storage
		r, err := m.storageClient.Bucket(m.bucket).Object(key).NewReader(ctx)
		if err != nil {
			return nil, fmt.Errorf("open storage reader: %w", err)
		}
		defer r.Close()

		data, err = io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("read from storage: %w", err)
		}
	}

	var sub Subscription
	if err := json.Unmarshal(data, &sub); err != nil {
		return nil, fmt.Errorf("unmarshal subscription: %w", err)
	}

	return &sub, nil
}

// loadSubscriptionByEmail loads a subscription by email address.
func (m *Monitor) loadSubscriptionByEmail(ctx context.Context, email string) (*Subscription, error) {
	key := subscriptionKey(email)
	return m.loadSubscription(ctx, key)
}

// deleteSubscription removes a subscription from storage (Cloud Storage or local filesystem).
func (m *Monitor) deleteSubscription(ctx context.Context, email string) error {
	key := subscriptionKey(email)
	m.logger.Debug("Deleting subscription", "key", key, "email", email)

	// Local filesystem storage
	if m.localStorage != "" {
		filePath := filepath.Join(m.localStorage, key)
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete from local storage: %w", err)
		}
		m.logger.Info("Subscription deleted from local storage", "path", filePath, "email", email)
		return nil
	}

	// Cloud Storage
	if err := m.storageClient.Bucket(m.bucket).Object(key).Delete(ctx); err != nil {
		return fmt.Errorf("delete from storage: %w", err)
	}

	m.logger.Info("Subscription deleted", "key", key, "email", email)
	return nil
}

// listSubscriptions lists all subscriptions from storage (Cloud Storage or local filesystem).
func (m *Monitor) listSubscriptions(ctx context.Context) ([]*Subscription, error) {
	var subs []*Subscription

	// Local filesystem storage
	if m.localStorage != "" {
		entries, err := os.ReadDir(m.localStorage)
		if err != nil {
			return nil, fmt.Errorf("read local storage directory: %w", err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "sub-") || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}

			sub, err := m.loadSubscription(ctx, entry.Name())
			if err != nil {
				m.logger.Warn("Failed to load subscription", "file", entry.Name(), "error", err)
				continue
			}

			subs = append(subs, sub)
		}

		return subs, nil
	}

	// Cloud Storage
	it := m.storageClient.Bucket(m.bucket).Objects(ctx, &storage.Query{
		Prefix: "sub-",
	})

	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("iterate storage: %w", err)
		}

		sub, err := m.loadSubscription(ctx, attrs.Name)
		if err != nil {
			m.logger.Warn("Failed to load subscription", "key", attrs.Name, "error", err)
			continue
		}

		subs = append(subs, sub)
	}

	return subs, nil
}

// findSubscriptionByToken finds a subscription by its secure token.
func (m *Monitor) findSubscriptionByToken(ctx context.Context, token string) (*Subscription, error) {
	subs, err := m.listSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	for _, sub := range subs {
		if sub.Token == token {
			return sub, nil
		}
	}

	return nil, fmt.Errorf("subscription not found")
}
