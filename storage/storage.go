// Package storage handles persistence of subscriptions.
package storage

import (
	"advrider-notifier/pkg/notifier"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/codeGROOVE-dev/retry"
	"google.golang.org/api/iterator"
)

// Store handles subscription persistence.
type Store struct {
	client    *storage.Client
	logger    *slog.Logger
	localPath string
	bucket    string
	salt      []byte
}

// New creates a new storage handler.
func New(client *storage.Client, bucket string, localPath string, salt []byte, logger *slog.Logger) *Store {
	return &Store{
		client:    client,
		logger:    logger,
		salt:      salt,
		localPath: localPath,
		bucket:    bucket,
	}
}

// TokenFromEmail derives a deterministic, unguessable token from an email address.
// Uses HMAC-SHA256 with a secret salt to ensure tokens cannot be guessed without the salt.
func (s *Store) TokenFromEmail(email string) string {
	h := hmac.New(sha256.New, s.salt)
	h.Write([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(h.Sum(nil))
}

// SubscriptionKey generates a stable filename from a token.
// Validates that the token is a safe hex string to prevent path traversal.
// Uses constant-time validation to prevent timing attacks.
func SubscriptionKey(token string) string {
	// Validate token is exactly 64 hex characters (SHA256 output)
	if len(token) != 64 {
		return ""
	}

	// Constant-time validation: check all characters, don't exit early
	valid := 1
	for _, c := range token {
		isHexDigit := ((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'))
		if !isHexDigit {
			valid = 0
		}
	}

	if valid == 0 {
		return ""
	}

	return fmt.Sprintf("sub-%s.json", token)
}

// Save saves a subscription.
func (s *Store) Save(ctx context.Context, sub *notifier.Subscription) error {
	key := SubscriptionKey(sub.Token)
	if key == "" {
		return errors.New("invalid token format")
	}
	s.logger.Debug("Saving subscription", "key", key, "email", sub.Email)

	data, err := json.MarshalIndent(sub, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal subscription: %w", err)
	}

	// Local filesystem storage
	if s.localPath != "" {
		filePath := filepath.Join(s.localPath, key)
		if err := os.WriteFile(filePath, data, 0o600); err != nil {
			return fmt.Errorf("write to local storage: %w", err)
		}

		s.logger.Info("Subscription saved to local storage", "path", filePath, "email", sub.Email, "thread_count", len(sub.Threads))
		return nil
	}

	// Cloud Storage with retry logic for reliability
	err = retry.Do(
		func() error {
			w := s.client.Bucket(s.bucket).Object(key).NewWriter(ctx)
			if _, writeErr := w.Write(data); writeErr != nil {
				if closeErr := w.Close(); closeErr != nil {
					s.logger.Warn("Failed to close writer after error", "error", closeErr)
				}
				return fmt.Errorf("write to storage: %w", writeErr)
			}
			if closeErr := w.Close(); closeErr != nil {
				return fmt.Errorf("close storage writer: %w", closeErr)
			}
			return nil
		},
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.MaxJitter(10*time.Second),
		retry.Context(ctx),
		retry.OnRetry(func(n uint, retryErr error) {
			s.logger.Info("Retrying save operation after error", "attempt", n, "key", key, "error", retryErr)
		}),
	)
	if err != nil {
		return fmt.Errorf("save after retries: %w", err)
	}

	s.logger.Info("Subscription saved", "key", key, "email", sub.Email, "thread_count", len(sub.Threads))
	return nil
}

// LoadByEmail loads a subscription by email address.
// Uses HMAC to derive the token from the email, allowing O(1) lookup.
func (s *Store) LoadByEmail(ctx context.Context, email string) (*notifier.Subscription, error) {
	token := s.TokenFromEmail(email)
	return s.Load(ctx, SubscriptionKey(token))
}

// Load loads a subscription by key.
func (s *Store) Load(ctx context.Context, key string) (*notifier.Subscription, error) {
	if key == "" {
		return nil, errors.New("invalid key format")
	}

	var data []byte

	// Local filesystem storage
	if s.localPath != "" {
		var err error
		filePath := filepath.Join(s.localPath, key)
		data, err = os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, errors.New("storage: object doesn't exist")
			}
			return nil, fmt.Errorf("read from local storage: %w", err)
		}
	} else {
		// Cloud Storage with retry logic for reliability
		var readData []byte
		err := retry.Do(
			func() error {
				r, openErr := s.client.Bucket(s.bucket).Object(key).NewReader(ctx)
				if openErr != nil {
					// Don't retry on "not found" errors
					if errors.Is(openErr, storage.ErrObjectNotExist) {
						return retry.Unrecoverable(fmt.Errorf("open storage reader: %w", openErr))
					}
					return fmt.Errorf("open storage reader: %w", openErr)
				}
				defer func() {
					if closeErr := r.Close(); closeErr != nil {
						s.logger.Warn("Failed to close storage reader", "error", closeErr)
					}
				}()

				var readErr error
				readData, readErr = io.ReadAll(r)
				if readErr != nil {
					return fmt.Errorf("read from storage: %w", readErr)
				}
				return nil
			},
			retry.Attempts(3),
			retry.Delay(time.Second),
			retry.MaxDelay(2*time.Minute),
			retry.MaxJitter(10*time.Second),
			retry.Context(ctx),
			retry.OnRetry(func(n uint, retryErr error) {
				s.logger.Info("Retrying load operation after error", "attempt", n, "key", key, "error", retryErr)
			}),
		)
		if err != nil {
			return nil, fmt.Errorf("load after retries: %w", err)
		}
		data = readData
	}

	var sub notifier.Subscription
	if err := json.Unmarshal(data, &sub); err != nil {
		return nil, fmt.Errorf("unmarshal subscription: %w", err)
	}

	return &sub, nil
}

// Delete removes a subscription by email.
func (s *Store) Delete(ctx context.Context, email string) error {
	token := s.TokenFromEmail(email)
	key := SubscriptionKey(token)
	if key == "" {
		return errors.New("invalid token format")
	}
	s.logger.Debug("Deleting subscription", "key", key, "email", email)

	// Local filesystem storage
	if s.localPath != "" {
		filePath := filepath.Join(s.localPath, key)
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete from local storage: %w", err)
		}
		s.logger.Info("Subscription deleted from local storage", "path", filePath, "email", email)
		return nil
	}

	// Cloud Storage with retry logic for reliability
	err := retry.Do(
		func() error {
			if deleteErr := s.client.Bucket(s.bucket).Object(key).Delete(ctx); deleteErr != nil {
				// Don't retry on "not found" errors - deletion is idempotent
				if errors.Is(deleteErr, storage.ErrObjectNotExist) {
					return retry.Unrecoverable(fmt.Errorf("delete from storage: %w", deleteErr))
				}
				return fmt.Errorf("delete from storage: %w", deleteErr)
			}
			return nil
		},
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.MaxJitter(10*time.Second),
		retry.Context(ctx),
		retry.OnRetry(func(n uint, retryErr error) {
			s.logger.Info("Retrying delete operation after error", "attempt", n, "key", key, "error", retryErr)
		}),
	)
	if err != nil {
		return fmt.Errorf("delete after retries: %w", err)
	}

	s.logger.Info("Subscription deleted", "key", key, "email", email)
	return nil
}

// List lists all subscriptions.
func (s *Store) List(ctx context.Context) ([]*notifier.Subscription, error) {
	var subs []*notifier.Subscription

	// Local filesystem storage
	if s.localPath != "" {
		entries, err := os.ReadDir(s.localPath)
		if err != nil {
			return nil, fmt.Errorf("read local storage directory: %w", err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "sub-") || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}

			sub, err := s.Load(ctx, entry.Name())
			if err != nil {
				s.logger.Warn("Failed to load subscription", "file", entry.Name(), "error", err)
				continue
			}

			subs = append(subs, sub)
		}

		return subs, nil
	}

	// Cloud Storage
	it := s.client.Bucket(s.bucket).Objects(ctx, &storage.Query{
		Prefix: "sub-",
	})

	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("iterate storage: %w", err)
		}

		sub, err := s.Load(ctx, attrs.Name)
		if err != nil {
			s.logger.Warn("Failed to load subscription", "key", attrs.Name, "error", err)
			continue
		}

		subs = append(subs, sub)
	}

	return subs, nil
}

// LoadByToken loads a subscription by its token.
// This is O(1) since the token IS the filename.
// Validates token format before attempting load to prevent timing attacks.
func (s *Store) LoadByToken(ctx context.Context, token string) (*notifier.Subscription, error) {
	key := SubscriptionKey(token)
	if key == "" {
		// Return same error as "not found" to prevent timing attacks
		return nil, errors.New("storage: object doesn't exist")
	}
	return s.Load(ctx, key)
}

// IsNotFound checks if an error indicates a subscription was not found.
func IsNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "storage: object doesn't exist")
}
