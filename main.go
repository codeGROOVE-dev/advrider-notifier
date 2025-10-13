// Package main implements a Cloud Run service that monitors ADVRider threads
// and sends email notifications via Gmail API when new posts are detected.
package main

import (
	"advrider-notifier/email"
	"advrider-notifier/poll"
	"advrider-notifier/scraper"
	"advrider-notifier/server"
	"advrider-notifier/storage"
	"context"
	"embed"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	gcs "cloud.google.com/go/storage"
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
	salt := os.Getenv("SALT")

	if salt == "" {
		logger.Error("SALT environment variable is not set - subscription unsubscribe URLs will be guessable, allowing anyone to unsubscribe any email address. This is a CRITICAL SECURITY ISSUE.")
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

		runLocal(ctx, gmailService, localStorage, baseURL, salt, mockEmail, logger)
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

	runProduction(ctx, gmailService, storageClient, bucket, baseURL, salt, logger)
}

func runLocal(ctx context.Context, gmailService *gmail.Service, localStorage, baseURL, salt string, mockEmail bool, logger *slog.Logger) {
	// Initialize components
	httpClient := &http.Client{Timeout: 30 * time.Second}
	scraperSvc := scraper.New(httpClient, logger)
	storageSvc := storage.New(nil, "", localStorage, []byte(salt), logger)
	emailSvc := email.New(gmailService, logger, baseURL, mockEmail)
	pollSvc := poll.New(scraperSvc, storageSvc, emailSvc, logger)

	// Create server
	srv := server.New(&server.Config{
		Scraper:   scraperSvc,
		Store:     storageSvc,
		Emailer:   emailSvc,
		Poller:    pollSvc,
		IsHTTP403: scraper.IsHTTP403Error,
		IsNotFound:      storage.IsNotFound,
		BaseURL:   baseURL,
		Logger:    logger,
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	if err := srv.ServeHTTP(mediaFS, port); err != nil {
		logger.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

func runProduction(ctx context.Context, gmailService *gmail.Service, storageClient *gcs.Client, bucket, baseURL, salt string, logger *slog.Logger) {
	// Initialize components
	httpClient := &http.Client{Timeout: 30 * time.Second}
	scraperSvc := scraper.New(httpClient, logger)
	storageSvc := storage.New(storageClient, bucket, "", []byte(salt), logger)
	emailSvc := email.New(gmailService, logger, baseURL, false)
	pollSvc := poll.New(scraperSvc, storageSvc, emailSvc, logger)

	// Create server
	srv := server.New(&server.Config{
		Scraper:   scraperSvc,
		Store:     storageSvc,
		Emailer:   emailSvc,
		Poller:    pollSvc,
		IsHTTP403: scraper.IsHTTP403Error,
		IsNotFound:      storage.IsNotFound,
		BaseURL:   baseURL,
		Logger:    logger,
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	if err := srv.ServeHTTP(mediaFS, port); err != nil {
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
