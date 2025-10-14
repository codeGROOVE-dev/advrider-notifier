# ADVRider Thread Notifier

<p align="center">
  <img src="media/logo.png" alt="ADVRider Notifier Logo" width="200">
</p>

Email notifications for ADVRider threads. Built for Cloud Run, written in Go.

ADVRider's built-in notifications only email you once after your last visit to that thread. This keeps the party going by emailing every new post until you unsubscribe.

## What it does

Subscribe to ADVRider forum threads and get emails when new posts appear. That's it.

- Adaptive polling (5min to 4hr based on thread activity)
- Multiple threads per email address
- Handles login-required forums gracefully (reports 403, doesn't crash)
- Exponential backoff with retry on transient failures
- Local dev mode with mocked email

## Running it

**Local (filesystem + mock email):**
```bash
go run .
```

Visit http://localhost:8080 and subscribe to a thread.

**Cloud Run (GCS + Brevo):**
```bash
export STORAGE_BUCKET=your-bucket-name
export BASE_URL=https://your-service.run.app
export BREVO_API_KEY=your-api-key
ko apply -f service.yaml
```

Requires a service account with Cloud Storage access and a Brevo API key.

## Architecture

```
/                    Homepage (subscribe form)
/subscribe           POST: Create subscription (verifies thread exists first)
/manage?token=...    View/delete subscriptions
/pollz               POST: Trigger immediate poll (no auth, rate limited by IP)
```

Storage is either local filesystem (`./data`) or GCS (`STORAGE_BUCKET`).

Email via Brevo API. Falls back to mock in dev.

Each thread is scraped once per poll cycle regardless of subscriber count. Polling interval calculated per-thread using exponential backoff: `5min × 2^(hours_since_post / 3)`, capped at 4 hours.

## Security

- Rate limiting: 5 subscriptions/hour per IP
- Token-based subscription management (64-char random, constant-time comparison)
- Thread limit: 20 per user (prevents resource exhaustion)
- Email limit: 10 posts per notification (prevents abuse)
- CSP, X-Frame-Options, X-Content-Type-Options headers
- Thread verification before subscription (validates URL is actually an ADVRider thread)

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `STORAGE_BUCKET` | Cloud only | GCS bucket for subscription data |
| `BASE_URL` | Cloud only | Public URL (for manage links in emails) |
| `BREVO_API_KEY` | Cloud only | Brevo API key for sending emails |
| `SALT` | Required | Secret salt for token generation (set in GSM or env) |
| `PORT` | Optional | HTTP port (default: 8080) |
| `LOCAL_STORAGE` | Optional | Local storage path (default: ./data) |
| `MAIL_FROM` | Optional | From email address (defaults to postmaster@domain) |
| `MAIL_NAME` | Optional | From name (default: ADVRider Notifier) |

## Development

```bash
make build      # Build binary
make test       # Run tests
make lint       # golangci-lint
make deploy     # Deploy to Cloud Run via ko
```

Tests use real ADVRider HTML parsing (no mocks for scraper). Exponential backoff algorithm is tested for correctness across all activity levels.

## Email Templates

Dark mode support via `prefers-color-scheme`. WCAG AA compliant. Post numbers are clickable anchors to specific posts on specific pages.

Footer links: "View thread" (goes to last page + last post anchor) and "Manage" (token-authenticated subscription management).

## Why Go?

Fast cold starts on Cloud Run, trivial deploys with ko, stdlib has everything we need. No npm, no containers, no Dockerfile.

## License

Apache 2.0 - see LICENSE file

Built by [codeGROOVE llc](https://codegroove.dev)
