package fetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownload_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "line1\nline2\n")
	}))
	defer srv.Close()

	data, err := Download(context.Background(), srv.URL, Options{
		Retries: 3, Timeout: 5 * time.Second, Parallelism: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "line1\nline2\n" {
		t.Errorf("data = %q", string(data))
	}
}

func TestDownload_retries_on_failure(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, "ok\n")
	}))
	defer srv.Close()

	data, err := Download(context.Background(), srv.URL, Options{
		Retries: 3, Timeout: 5 * time.Second, Parallelism: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "ok\n" {
		t.Errorf("data = %q", string(data))
	}
	if attempts.Load() != 3 {
		t.Errorf("attempts = %d, want 3", attempts.Load())
	}
}

func TestDownload_all_retries_fail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := Download(context.Background(), srv.URL, Options{
		Retries: 2, Timeout: 5 * time.Second, Parallelism: 1,
	})
	if err == nil {
		t.Fatal("expected error after all retries fail")
	}
}

func TestDownload_empty_response(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		// empty 200
	}))
	defer srv.Close()

	_, err := Download(context.Background(), srv.URL, Options{
		Retries: 1, Timeout: 5 * time.Second, Parallelism: 1,
	})
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestDownload_context_cancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
		_, _ = fmt.Fprint(w, "late")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := Download(ctx, srv.URL, Options{
		Retries: 1, Timeout: 10 * time.Second, Parallelism: 1,
	})
	if err == nil {
		t.Fatal("expected error on context cancel")
	}
}

func TestDownloadAll_parallel(t *testing.T) {
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := concurrent.Add(1)
		for {
			old := maxConcurrent.Load()
			if n <= old || maxConcurrent.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		concurrent.Add(-1)
		_, _ = fmt.Fprint(w, "data\n")
	}))
	defer srv.Close()

	urls := []string{srv.URL + "/1", srv.URL + "/2", srv.URL + "/3", srv.URL + "/4"}
	results := DownloadAll(context.Background(), urls, Options{
		Retries: 1, Timeout: 5 * time.Second, Parallelism: 2,
	})

	for i, r := range results {
		if r.Err != nil {
			t.Errorf("result[%d] error: %v", i, r.Err)
		}
	}
	if maxConcurrent.Load() > 2 {
		t.Errorf("max concurrent = %d, want <= 2", maxConcurrent.Load())
	}
}
