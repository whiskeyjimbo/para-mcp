package embedder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// retryableStatus reports whether an HTTP status warrants a retry.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code < 600)
}

// doWithRetry executes an HTTP POST, retrying on 429 / 5xx up to maxRetries times.
// Non-retryable errors (4xx except 429) surface immediately.
func doWithRetry(ctx context.Context, client *http.Client, method, url, auth string, payload []byte, maxRetries int) ([]byte, error) {
	var lastErr error
	backoff := 200 * time.Millisecond

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
			}
		}

		var bodyReader io.Reader
		if len(payload) > 0 {
			bodyReader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbedBody))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusOK {
			return body, nil
		}
		if !retryableStatus(resp.StatusCode) {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
		}
		lastErr = fmt.Errorf("HTTP %d (retryable): %s", resp.StatusCode, body)
	}
	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}
