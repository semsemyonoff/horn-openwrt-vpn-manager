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

// Validators carries the cache validators of a previously fetched copy. When
// either field is set the request is conditional and the server may answer 304.
type Validators struct {
	ETag         string
	LastModified string
}

// Request is one conditional download job for DownloadAll.
type Request struct {
	Validators

	URL string
}

type Result struct {
	// Validators of the returned body, to be stored alongside it. Empty on a
	// 304, where the previously stored validators remain in force.
	Validators

	URL  string
	Data []byte
	Err  error

	// NotModified reports a 304 answer to a conditional request: Data is empty
	// and the caller's existing copy is still current.
	NotModified bool
}

// Download fetches a single URL with retries.
func Download(ctx context.Context, url string, opts Options) ([]byte, error) {
	res := DownloadConditional(ctx, Request{URL: url}, opts)
	if res.Err == nil && res.NotModified {
		// Unreachable: an unconditional request cannot be answered 304, and
		// DownloadConditional rejects one that is. Guarded anyway because the
		// callers of Download write whatever comes back straight into a cache,
		// and an empty body there empties the routed set.
		return nil, fmt.Errorf("download %s: server reported not-modified for an unconditional request", url)
	}
	return res.Data, res.Err
}

// DownloadConditional fetches req.URL with retries, sending If-None-Match /
// If-Modified-Since when req carries validators. A 304 answer is a success with
// NotModified set and no body.
//
// Every request is sent with no-cache so an intermediary cannot answer with a
// revision older than the origin's: a stale route list silently changes which
// outbound a domain uses, and that is invisible in the generated config.
func DownloadConditional(ctx context.Context, req Request, opts Options) Result {
	client := &http.Client{Timeout: opts.Timeout}

	var lastErr error
	for attempt := 1; attempt <= opts.Retries; attempt++ {
		logx.Trace("fetch %s (attempt %d/%d)", req.URL, attempt, opts.Retries)

		hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, http.NoBody)
		if err != nil {
			return Result{URL: req.URL, Err: fmt.Errorf("create request: %w", err)}
		}
		hreq.Header.Set("Cache-Control", "no-cache")
		hreq.Header.Set("Pragma", "no-cache")
		if req.ETag != "" {
			hreq.Header.Set("If-None-Match", req.ETag)
		}
		if req.LastModified != "" {
			hreq.Header.Set("If-Modified-Since", req.LastModified)
		}

		resp, err := client.Do(hreq)
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

		// A 304 is only meaningful as the answer to validators we sent. A server
		// or intermediary that returns one to an unconditional request would
		// otherwise hand the caller a successful empty body, and the callers
		// write that straight into a cache — an empty domain list silently
		// empties the routed set. Treat it as any other unexpected status.
		conditional := req.ETag != "" || req.LastModified != ""
		if resp.StatusCode == http.StatusNotModified && conditional {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
			_ = resp.Body.Close()
			return Result{URL: req.URL, NotModified: true}
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
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

		return Result{
			URL:  req.URL,
			Data: body,
			Validators: Validators{
				ETag:         resp.Header.Get("ETag"),
				LastModified: resp.Header.Get("Last-Modified"),
			},
		}
	}
	return Result{
		URL: req.URL,
		Err: fmt.Errorf("download %s: all %d attempts failed: %w", req.URL, opts.Retries, lastErr),
	}
}

// DownloadAll fetches multiple URLs with bounded parallelism.
func DownloadAll(ctx context.Context, urls []string, opts Options) []Result {
	reqs := make([]Request, len(urls))
	for i, u := range urls {
		reqs[i] = Request{URL: u}
	}
	return DownloadAllConditional(ctx, reqs, opts)
}

// DownloadAllConditional fetches multiple conditional requests with bounded
// parallelism. Results are index-aligned with reqs.
func DownloadAllConditional(ctx context.Context, reqs []Request, opts Options) []Result {
	results := make([]Result, len(reqs))

	parallelism := opts.Parallelism
	if parallelism <= 0 {
		parallelism = 1
	}
	work := make(chan int, len(reqs))
	for i := range reqs {
		work <- i
	}
	close(work)

	var wg sync.WaitGroup
	for range parallelism {
		wg.Go(func() {
			for i := range work {
				results[i] = DownloadConditional(ctx, reqs[i], opts)
			}
		})
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
