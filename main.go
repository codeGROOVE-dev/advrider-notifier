// Package main implements a Cloud Run service that monitors ADVRider threads
// and sends email notifications when new posts are detected.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"advrider-notifier/email"
	"advrider-notifier/poll"
	"advrider-notifier/scraper"
	"advrider-notifier/server"
	"advrider-notifier/storage"

	gcs "cloud.google.com/go/storage"
	"github.com/codeGROOVE-dev/gsm"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

//go:embed media/*
var mediaFS embed.FS

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
	emailProvider := strings.ToLower(os.Getenv("EMAIL_PROVIDER"))

	// Load SALT from GSM or environment variable
	salt := secret(ctx, "SALT", logger)
	if salt == "" {
		logger.Error("SALT is not set in environment or GSM - subscription unsubscribe URLs will be guessable, allowing anyone to unsubscribe any email address. This is a CRITICAL SECURITY ISSUE.")
		os.Exit(1)
	}

	// Default to brevo provider if not specified
	if emailProvider == "" {
		emailProvider = "brevo"
	}

	// Default to local development mode if no bucket specified
	if bucket == "" && localStorage == "" {
		localStorage = "./data"
		logger.Info("No STORAGE_BUCKET set, defaulting to local development mode", "storage_path", localStorage)
	}

	// Local development mode
	if localStorage != "" {
		logger.Info("Running in local development mode", "storage_path", localStorage, "email_provider", emailProvider)
		if baseURL == "" {
			baseURL = "http://localhost:8080"
		}

		// Create local storage directory
		if err := os.MkdirAll(localStorage, 0o750); err != nil {
			logger.Error("Failed to create local storage directory", "error", err)
			os.Exit(1)
		}

		// Initialize email provider
		emailSender, err := initEmailProvider(ctx, emailProvider, logger, baseURL)
		if err != nil {
			logger.Error("Failed to initialize email provider", "provider", emailProvider, "error", err)
			os.Exit(1)
		}

		// Initialize components
		httpClient := &http.Client{Timeout: 30 * time.Second}
		scraperSvc := scraper.New(httpClient, logger)
		storageSvc := storage.New(nil, "", localStorage, []byte(salt), logger)
		pollSvc := poll.New(scraperSvc, storageSvc, emailSender, logger)

		// Create and run server
		srv := server.New(&server.Config{
			Scraper:    scraperSvc,
			Store:      storageSvc,
			Emailer:    emailSender,
			Poller:     pollSvc,
			IsHTTP403:  scraper.IsHTTP403Error,
			IsNotFound: storage.IsNotFound,
			BaseURL:    baseURL,
			Logger:     logger,
		})

		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}

		if err := srv.ServeHTTP(mediaFS, port); err != nil {
			logger.Error("Server failed", "error", err)
			os.Exit(1)
		}
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

	logger.Info("Running in production mode", "bucket", bucket, "email_provider", emailProvider)

	// Initialize email provider
	emailSender, err := initEmailProvider(ctx, emailProvider, logger, baseURL)
	if err != nil {
		logger.Error("Failed to initialize email provider", "provider", emailProvider, "error", err)
		os.Exit(1)
	}

	// Initialize Storage client
	storageClient, err := gcs.NewClient(ctx)
	if err != nil {
		logger.Error("Failed to initialize Storage client", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := storageClient.Close(); err != nil {
			logger.Warn("Failed to close storage client", "error", err)
		}
	}()

	// Initialize components
	httpClient := &http.Client{Timeout: 30 * time.Second}
	scraperSvc := scraper.New(httpClient, logger)
	storageSvc := storage.New(storageClient, bucket, "", []byte(salt), logger)
	pollSvc := poll.New(scraperSvc, storageSvc, emailSender, logger)

	// Create server
	srv := server.New(&server.Config{
		Scraper:    scraperSvc,
		Store:      storageSvc,
		Emailer:    emailSender,
		Poller:     pollSvc,
		IsHTTP403:  scraper.IsHTTP403Error,
		IsNotFound: storage.IsNotFound,
		BaseURL:    baseURL,
		Logger:     logger,
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	err = srv.ServeHTTP(mediaFS, port)
	if err != nil {
		logger.Error("Server failed", "error", err)
	}
}

// secret retrieves a value from either Google Secret Manager or environment variable.
// It first checks for an environment variable. If not found, it attempts to load
// from Secret Manager using the same name (defaults to current GCP project).
// Returns empty string if not found in either location.
func secret(ctx context.Context, name string, logger *slog.Logger) string {
	// First check environment variable
	if val := os.Getenv(name); val != "" {
		logger.Debug("Using secret from environment variable", "name", name)
		return val
	}

	// Use gsm package to load secret from current project
	val, err := gsm.Fetch(ctx, name)
	if err != nil {
		logger.Warn("Failed to load secret from GSM, continuing without it", "secret", name, "error", err)
		return ""
	}

	logger.Debug("Loaded secret from Google Secret Manager", "secret", name)
	return val
}

// initEmailProvider initializes the appropriate email provider based on configuration.
func initEmailProvider(ctx context.Context, providerName string, logger *slog.Logger, baseURL string) (*email.Sender, error) {
	var provider email.Provider
	fromAddr := os.Getenv("MAIL_FROM")
	fromName := os.Getenv("MAIL_NAME")

	// Default from address to postmaster@<domain> based on BASE_URL
	if fromAddr == "" {
		// Extract domain from URL
		domain := strings.TrimPrefix(baseURL, "https://")
		domain = strings.TrimPrefix(domain, "http://")
		if idx := strings.Index(domain, "/"); idx != -1 {
			domain = domain[:idx]
		}
		if idx := strings.Index(domain, ":"); idx != -1 {
			domain = domain[:idx]
		}
		if domain != "" {
			fromAddr = "postmaster@" + domain
		}
	}

	if fromName == "" {
		fromName = "ADVRider Notifier"
	}

	switch providerName {
	case "brevo":
		apiKey := secret(ctx, "BREVO_API_KEY", logger)
		if apiKey == "" {
			return nil, errors.New("BREVO_API_KEY required for Brevo provider (set in environment or GSM)")
		}
		if fromAddr == "" {
			return nil, errors.New("MAIL_FROM could not be determined (set BASE_URL or MAIL_FROM)")
		}
		logger.Info("Initializing Brevo email provider", "from", fromAddr, "name", fromName)
		provider = email.NewBrevoProvider(apiKey, fromAddr, fromName, logger)

	case "gmail":
		gmailService, err := initGmailService(ctx, logger)
		if err != nil {
			return nil, fmt.Errorf("initialize Gmail service: %w", err)
		}
		logger.Info("Initializing Gmail email provider", "from", fromAddr)
		provider = email.NewGmailProvider(gmailService, logger)

	case "mock":
		logger.Info("Initializing mock email provider (no emails will be sent)", "from", fromAddr)
		provider = email.NewMockProvider(logger)

	default:
		return nil, fmt.Errorf("unknown email provider: %s (valid options: brevo, gmail, mock)", providerName)
	}

	return email.New(provider, logger, baseURL, fromAddr), nil
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
		if err := resp.Body.Close(); err != nil {
			// Silently ignore close errors for metadata check
		}
	}()

	return resp.StatusCode == http.StatusOK
}

func initGmailService(ctx context.Context, logger *slog.Logger) (*gmail.Service, error) {
	// Use gmail.GmailSendScope for send-only access (principle of least privilege)
	// This is more secure than using full Gmail access
	scope := option.WithScopes(gmail.GmailSendScope)

	// Try explicit credentials first (for local development or specific use cases)
	credsJSON := secret(ctx, "GOOGLE_CREDENTIALS_JSON", logger)
	if credsJSON != "" {
		return gmail.NewService(ctx, option.WithCredentialsJSON([]byte(credsJSON)), scope)
	}

	// If running in Cloud Run, use Application Default Credentials (ADC)
	// This automatically uses the service account
	// The service account needs Gmail API access (gmail.send scope)
	if isCloudRun(ctx) {
		return gmail.NewService(ctx, scope)
	}

	// Not in Cloud Run and no explicit credentials
	return nil, errors.New("GOOGLE_CREDENTIALS_JSON required when not running in Cloud Run (set in environment or GSM)")
}
