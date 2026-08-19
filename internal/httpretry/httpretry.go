package httpretry

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	maxAttempts = 5
	baseDelay   = 500 * time.Millisecond
	maxDelay    = 8 * time.Second
)

// Do executes a request factory with bounded retries for network failures,
// rate limiting, and transient server responses. The factory is called again
// for each attempt so request bodies and short-lived tokens are fresh.
func Do(ctx context.Context, client *http.Client, newRequest func() (*http.Request, error)) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := newRequest()
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req.WithContext(ctx))
		if err != nil {
			lastErr = err
			if attempt == maxAttempts-1 {
				break
			}
			if err := wait(ctx, backoff(attempt)); err != nil {
				return nil, err
			}
			continue
		}

		if !retryable(resp.StatusCode) || attempt == maxAttempts-1 {
			return resp, nil
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if err := wait(ctx, retryAfter(resp.Header.Get("Retry-After"), backoff(attempt))); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func retryable(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func backoff(attempt int) time.Duration {
	delay := baseDelay << attempt
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func retryAfter(value string, fallback time.Duration) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err == nil && seconds >= 0 {
		delay := time.Duration(seconds) * time.Second
		if delay > maxDelay {
			return maxDelay
		}
		return delay
	}
	if timestamp, err := http.ParseTime(strings.TrimSpace(value)); err == nil {
		delay := time.Until(timestamp)
		if delay < 0 {
			return 0
		}
		if delay > maxDelay {
			return maxDelay
		}
		return delay
	}
	return fallback
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
