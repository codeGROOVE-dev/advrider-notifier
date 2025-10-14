package poll

import (
	"testing"
	"time"
)

// TestCalculateInterval verifies the exponential backoff algorithm produces reasonable intervals.
func TestCalculateInterval(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name         string
		lastPostTime time.Time
		wantMin      time.Duration
		wantMax      time.Duration
	}{
		{
			name:         "very recent post (5 minutes ago)",
			lastPostTime: now.Add(-5 * time.Minute),
			wantMin:      5 * time.Minute,
			wantMax:      6 * time.Minute,
		},
		{
			name:         "recent post (1 hour ago)",
			lastPostTime: now.Add(-1 * time.Hour),
			wantMin:      6 * time.Minute,
			wantMax:      8 * time.Minute,
		},
		{
			name:         "active thread (3 hours ago)",
			lastPostTime: now.Add(-3 * time.Hour),
			wantMin:      9 * time.Minute,
			wantMax:      11 * time.Minute,
		},
		{
			name:         "moderately active (6 hours ago)",
			lastPostTime: now.Add(-6 * time.Hour),
			wantMin:      18 * time.Minute,
			wantMax:      22 * time.Minute,
		},
		{
			name:         "daily active (12 hours ago)",
			lastPostTime: now.Add(-12 * time.Hour),
			wantMin:      75 * time.Minute,
			wantMax:      85 * time.Minute,
		},
		{
			name:         "daily thread (24 hours ago)",
			lastPostTime: now.Add(-24 * time.Hour),
			wantMin:      3*time.Hour + 50*time.Minute,
			wantMax:      4 * time.Hour,
		},
		{
			name:         "inactive thread (7 days ago)",
			lastPostTime: now.Add(-7 * 24 * time.Hour),
			wantMin:      4 * time.Hour,
			wantMax:      4 * time.Hour,
		},
		{
			name:         "zero last polled (error case)",
			lastPostTime: now.Add(-1 * time.Hour),
			wantMin:      4 * time.Hour,
			wantMax:      4 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastPolledAt := now
			if tt.name == "zero last polled (error case)" {
				lastPolledAt = time.Time{}
			}

			interval, reason := CalculateInterval(tt.lastPostTime, lastPolledAt)

			if interval < tt.wantMin || interval > tt.wantMax {
				t.Errorf("CalculateInterval() interval = %v, want between %v and %v", interval, tt.wantMin, tt.wantMax)
			}

			if reason == "" {
				t.Error("CalculateInterval() reason should not be empty")
			}

			t.Logf("Post age: %v → Interval: %v (reason: %s)", time.Since(tt.lastPostTime).Round(time.Minute), interval.Round(time.Minute), reason)
		})
	}
}

// TestCalculateIntervalNeverReturnsZero ensures we never return a zero interval.
func TestCalculateIntervalNeverReturnsZero(t *testing.T) {
	now := time.Now()

	// Test various edge cases
	testCases := []struct {
		name         string
		lastPostTime time.Time
		lastPolledAt time.Time
	}{
		{"current time", now, now},
		{"1 second ago", now.Add(-1 * time.Second), now},
		{"zero post time", time.Time{}, now},
		{"zero polled time", now, time.Time{}},
		{"both zero", time.Time{}, time.Time{}},
		{"far future (should never happen)", now.Add(24 * time.Hour), now},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			interval, _ := CalculateInterval(tc.lastPostTime, tc.lastPolledAt)
			if interval == 0 {
				t.Errorf("CalculateInterval() returned 0 for %s", tc.name)
			}
			if interval < 5*time.Minute {
				t.Errorf("CalculateInterval() returned %v, which is less than minimum (5min) for %s", interval, tc.name)
			}
		})
	}
}

// TestCalculateIntervalExponentialBehavior verifies the interval grows exponentially.
func TestCalculateIntervalExponentialBehavior(t *testing.T) {
	now := time.Now()

	// Test that interval approximately doubles every 3 hours
	interval3h, _ := CalculateInterval(now.Add(-3*time.Hour), now)
	interval6h, _ := CalculateInterval(now.Add(-6*time.Hour), now)

	ratio := float64(interval6h) / float64(interval3h)

	// Should be approximately 2x (allow 10% tolerance for rounding)
	if ratio < 1.8 || ratio > 2.2 {
		t.Errorf("Expected interval to double every 3 hours, got ratio %.2f (3h=%v, 6h=%v)", ratio, interval3h, interval6h)
	}

	t.Logf("3h interval: %v, 6h interval: %v, ratio: %.2fx", interval3h, interval6h, ratio)
}
