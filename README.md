# ADVRider Thread Notifier

<p align="center">
  <img src="media/logo.png" alt="ADVRider Notifier Logo" width="200">
</p>

Email notifications for ADVRider threads. Built for Cloud Run, written in Go.

ADVRider's built-in notifications only email you once after your last visit. This keeps the conversation going by emailing every new post until you unsubscribe.

## How it works

Subscribe to any ADVRider thread and get emails when new posts appear.

**Respectful polling:** Adaptive intervals from 5 minutes (active threads) to 4 hours (quiet threads) using exponential backoff: `5min × 2^(hours_since_post / 3)`. Polling cycles are triggered every 10 minutes via Cloud Scheduler, making the real-world minimum check interval ~10 minutes for active threads. Each thread is scraped once per cycle regardless of subscriber count, minimizing server load.

**User limits:** Maximum 20 threads per email address. Notifications batch up to 10 posts to prevent spam.

**Security:** Rate limited to 5 requests/second per IP. Token-based subscription management. Thread verification before subscription. Email content sanitized to prevent XSS and phishing.

**Email quality:** Dark mode support, WCAG AA compliant, clickable post anchors linking directly to specific posts.

## Running locally

```bash
go run .
```

Visit http://localhost:8080 and subscribe to a thread.

## Architecture

```
/                    Subscribe form
/subscribe           Create subscription (verifies thread exists)
/manage?token=...    View/delete subscriptions
/pollz               Trigger poll (rate limited)
```

Storage: Local filesystem (`./data`) or GCS. Email via Brevo API (auto-mocks when `BREVO_API_KEY` not set).

## License

Apache 2.0 - see LICENSE file

Built by [codeGROOVE llc](https://codegroove.dev)
