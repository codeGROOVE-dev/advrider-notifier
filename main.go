// Package main implements a Cloud Run service that monitors ADVRider threads
// and sends email notifications when new posts are detected.
package main

import (
	"context"
	"embed"
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

	// Load SALT from GSM or environment variable
	salt := secret(ctx, "SALT", logger)
	if salt == "" {
		logger.Error("SALT is not set in environment or GSM - subscription unsubscribe URLs will be guessable, allowing anyone to unsubscribe any email address. This is a CRITICAL SECURITY ISSUE.")
		os.Exit(1)
	}

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
		if err := os.MkdirAll(localStorage, 0o750); err != nil {
			logger.Error("Failed to create local storage directory", "error", err)
			os.Exit(1)
		}

		// Initialize email: auto-detect Brevo vs Mock
		var emailSender *email.Sender
		if apiKey := secret(ctx, "BREVO_API_KEY", logger); apiKey != "" {
			fromAddr := os.Getenv("MAIL_FROM")
			fromName := os.Getenv("MAIL_NAME")
			if fromName == "" {
				fromName = "ADVRider Notifier"
			}
			if fromAddr == "" {
				fromAddr = "postmaster@" + domainFromURL(baseURL)
			}
			logger.Info("Using Brevo email provider", "from", fromAddr, "name", fromName)
			provider := email.NewBrevoProvider(apiKey, fromAddr, fromName, logger)
			emailSender = email.New(provider, logger, baseURL)
		} else {
			logger.Info("Using mock email provider (no emails will be sent)")
			provider := email.NewMockProvider(logger)
			emailSender = email.New(provider, logger, baseURL)
		}

		// Initialize components
		httpClient := &http.Client{Timeout: 30 * time.Second}
		scraperSvc := scraper.New(httpClient, logger)
		storageSvc := storage.New(nil, "", localStorage, []byte(salt), logger)
		pollSvc := poll.New(scraperSvc, storageSvc, emailSender, logger)

		// Run initial polling cycle on startup
		logger.Info("Running initial polling cycle on startup")
		if err := pollSvc.CheckAll(ctx); err != nil {
			logger.Warn("Initial polling cycle failed", "error", err)
		} else {
			logger.Info("Initial polling cycle completed successfully")
		}

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

	logger.Info("Running in production mode", "bucket", bucket)

	// Initialize email: Brevo required in production
	apiKey := secret(ctx, "BREVO_API_KEY", logger)
	if apiKey == "" {
		logger.Error("BREVO_API_KEY required for production (set in environment or GSM)")
		os.Exit(1)
	}
	fromAddr := os.Getenv("MAIL_FROM")
	fromName := os.Getenv("MAIL_NAME")
	if fromName == "" {
		fromName = "ADVRider Notifier"
	}
	if fromAddr == "" {
		if domain := domainFromURL(baseURL); domain != "" {
			fromAddr = "postmaster@" + domain
		}
	}
	if fromAddr == "" {
		logger.Error("MAIL_FROM could not be determined (set BASE_URL or MAIL_FROM)")
		os.Exit(1)
	}
	logger.Info("Using Brevo email provider", "from", fromAddr, "name", fromName)
	provider := email.NewBrevoProvider(apiKey, fromAddr, fromName, logger)
	emailSender := email.New(provider, logger, baseURL)

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

	// Run initial polling cycle on startup
	logger.Info("Running initial polling cycle on startup")
	if err := pollSvc.CheckAll(ctx); err != nil {
		logger.Warn("Initial polling cycle failed", "error", err)
	} else {
		logger.Info("Initial polling cycle completed successfully")
	}

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

// domainFromURL extracts the domain from a URL for use in email addresses.
func domainFromURL(baseURL string) string {
	domain := strings.TrimPrefix(baseURL, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	if idx := strings.Index(domain, "/"); idx != -1 {
		domain = domain[:idx]
	}
	if idx := strings.Index(domain, ":"); idx != -1 {
		domain = domain[:idx]
	}
	return domain
}
