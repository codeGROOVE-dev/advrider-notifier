# ADVRider Thread Notifier

<p align="center">
  <img src="media/logo.png" alt="ADVRider Notifier Logo" width="200">
</p>

A secure, minimal Go service that notifies subscribers about new ADVRider forum posts via email. Designed for Google Cloud Run.

## Features

- **Multi-subscription support** - Users can subscribe to multiple threads with one email
- **Secure token-based authentication** - 64-char random tokens prevent enumeration attacks
- **Efficient thread fetching** - Each thread is fetched only once per check cycle, regardless of subscriber count
- **Smart URL normalization** - Handles page numbers and anchors automatically
- **Rate limiting** - 5 subscriptions per IP per hour
- **Thread verification** - Validates threads exist before creating subscriptions
- **Constant-time token comparison** - Prevents timing attacks
- **Comprehensive security headers** - CSP, X-Frame-Options, X-Content-Type-Options
- **Retry logic** - Exponential backoff with jitter for HTTP and Gmail API calls
- **Structured logging** - JSON logs for Cloud Run with slog
- **Graceful degradation** - Continues monitoring despite individual failures
- **HTML email formatting** - Clean, responsive email templates

## Prerequisites

- Go 1.23 or later
- Google Cloud Project with:
  - Cloud Run API enabled
  - Cloud Storage API enabled
  - Gmail API enabled
- Service account with:
  - Gmail API access (https://www.googleapis.com/auth/gmail.send)
  - Cloud Storage access (Storage Object Admin role)
- [ko](https://ko.build/) for deployment

## Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `STORAGE_BUCKET` | Cloud Storage bucket name for subscription data | `advrider-subscriptions` |
| `BASE_URL` | Public URL of the deployed service | `https://advrider-notifier-xyz.run.app` |
| `GOOGLE_CREDENTIALS_JSON` | Service account credentials JSON | `{"type":"service_account",...}` |
| `PORT` | HTTP server port (optional, defaults to 8080) | `8080` |
| `LOCAL_STORAGE` | Local filesystem path for subscription data (optional, defaults to ./data) | `/var/tmp/advrider-notify` |

## Local Development

### Build

```bash
make build
```

### Run Tests

```bash
make test
```

### Run Locally

The service automatically runs in local development mode with mock email when no `STORAGE_BUCKET` is set:

```bash
# Simplest - just run it (uses ./data for storage, mocks email)
go run .

# Trigger a poll manually (POST only)
curl -X POST http://localhost:8080/poll
```

## Deployment

The service uses [ko](https://ko.build/) for containerless deployment to Cloud Run.

### Deploy

```bash
make deploy
```
