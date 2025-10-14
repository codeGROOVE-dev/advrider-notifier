# ADVRider Thread Notifier

<p align="center">
  <img src="media/logo.png" alt="ADVRider Notifier Logo" width="200">
</p>

Email notifications for ADVRider threads. Built for Cloud Run, written in Go.

ADVRider's built-in notifications only email you once after your last visit. This keeps the conversation going by emailing every new post until you unsubscribe.

## How it works

Subscribe to any ADVRider thread and get emails when new posts appear.

**Respectful polling:** Adaptive intervals from ~10 minutes (active threads) to 4 hours using exponential backoff. Minimum poll time is defined as `5min × 2^(hours_since_post / 3)`, with a 10-minute polling loop; shared fetch for all subscribers to minimize load.
**User limits:** Maximum 20 threads per email address. Notifications batch up to 10 posts to prevent spam.
**Security:** Token-based subscription management. mail content sanitized to prevent XSS and phishing.
**Email quality:** Dark mode support, WCAG AA compliant, clickable post anchors linking directly to specific posts.

## Running locally

```bash
go run .
```

Server will be available at http://localhost:8080

## License

Apache 2.0 - see LICENSE file

Built by [codeGROOVE llc](https://codegroove.dev)
