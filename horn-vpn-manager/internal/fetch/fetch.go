// Package fetch provides HTTP downloading with retries and bounded parallelism.
package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
)

type Options struct {
	Retries     int
	Timeout     time.Duration
	Parallelism int
}

type Result struct {
	URL  string
	Data []byte
	Err  error
}

// Download fetches a single URL with retries.
func Download(ctx context.Context, url string, opts Options) ([]byte, error) {
	client := &http.Client{Timeout: opts.Timeout}

	var lastErr error
	for attempt := 1; attempt <= opts.Retries; attempt++ {
		logx.Trace("fetch %s (attempt %d/%d)", url, attempt, opts.Retries)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("attempt %d: %w", attempt, err)
			if attempt > 1 || opts.Retries > 1 {
				logx.Warn("  connection failed (attempt %d/%d)", attempt, opts.Retries)
			}
			if attempt < opts.Retries {
				sleep(ctx, backoff(attempt))
			}
			continue
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if err != nil {
			lastErr = fmt.Errorf("attempt %d: read body: %w", attempt, err)
			if attempt < opts.Retries {
				sleep(ctx, backoff(attempt))
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("attempt %d: HTTP %d", attempt, resp.StatusCode)
			logx.Warn("  HTTP %d (attempt %d/%d)", resp.StatusCode, attempt, opts.Retries)
			if attempt < opts.Retries {
				sleep(ctx, backoff(attempt))
			}
			continue
		}

		if len(body) == 0 {
			lastErr = fmt.Errorf("attempt %d: empty response", attempt)
			if attempt < opts.Retries {
				sleep(ctx, backoff(attempt))
			}
			continue
		}

		return body, nil
	}
	return nil, fmt.Errorf("download %s: all %d attempts failed: %w", url, opts.Retries, lastErr)
}

// DownloadAll fetches multiple URLs with bounded parallelism.
func DownloadAll(ctx context.Context, urls []string, opts Options) []Result {
	results := make([]Result, len(urls))

	sem := make(chan struct{}, opts.Parallelism)
	var wg sync.WaitGroup

	for i, u := range urls {
		wg.Add(1)
		go func(idx int, url string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			data, err := Download(ctx, url, opts)
			results[idx] = Result{URL: url, Data: data, Err: err}
		}(i, u)
	}

	wg.Wait()
	return results
}

func backoff(attempt int) time.Duration {
	return time.Duration(attempt) * 2 * time.Second
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
