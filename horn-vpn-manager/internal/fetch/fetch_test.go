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

// TestDownloadConditional_not_modified pins that a 304 is a success carrying no
// body, so the caller keeps the copy it already has.
func TestDownloadConditional_not_modified(t *testing.T) {
	var gotINM, gotIMS, gotCC string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotINM = r.Header.Get("If-None-Match")
		gotIMS = r.Header.Get("If-Modified-Since")
		gotCC = r.Header.Get("Cache-Control")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	res := DownloadConditional(context.Background(), Request{
		URL:        srv.URL,
		Validators: Validators{ETag: `"v1"`, LastModified: "Mon, 17 Aug 2026 12:00:00 GMT"},
	}, Options{Retries: 2, Timeout: 5 * time.Second})

	if res.Err != nil {
		t.Fatalf("DownloadConditional: %v", res.Err)
	}
	if !res.NotModified {
		t.Error("NotModified = false, want true")
	}
	if len(res.Data) != 0 {
		t.Errorf("Data = %q, want empty", res.Data)
	}
	if gotINM != `"v1"` {
		t.Errorf("If-None-Match = %q, want %q", gotINM, `"v1"`)
	}
	if gotIMS != "Mon, 17 Aug 2026 12:00:00 GMT" {
		t.Errorf("If-Modified-Since = %q", gotIMS)
	}
	// An intermediary answering from its own cache would hide a changed list.
	if gotCC != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", gotCC)
	}
}

// TestDownloadConditional_returns_validators pins that the validators of a 200
// come back so they can be stored with the body.
func TestDownloadConditional_returns_validators(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			t.Errorf("unexpected If-None-Match on an unconditional request")
		}
		w.Header().Set("ETag", `"v2"`)
		w.Header().Set("Last-Modified", "Mon, 17 Aug 2026 13:00:00 GMT")
		_, _ = w.Write([]byte("body\n"))
	}))
	defer srv.Close()

	res := DownloadConditional(context.Background(), Request{URL: srv.URL}, Options{Retries: 1, Timeout: 5 * time.Second})
	if res.Err != nil {
		t.Fatalf("DownloadConditional: %v", res.Err)
	}
	if res.NotModified {
		t.Error("NotModified = true, want false")
	}
	if res.ETag != `"v2"` || res.LastModified != "Mon, 17 Aug 2026 13:00:00 GMT" {
		t.Errorf("validators = %q / %q", res.ETag, res.LastModified)
	}
}

// TestDownload_unconditional_304_is_an_error pins that a 304 answered to a
// request that carried no validators is rejected. Accepting it hands the caller
// a successful empty body, and routing.Run writes that straight into the domain
// cache — emptying the routed set and reloading dnsmasq on top of it.
func TestDownload_unconditional_304_is_an_error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" {
			t.Error("test server was sent validators it did not expect")
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	data, err := Download(context.Background(), srv.URL, Options{Retries: 1, Timeout: 5 * time.Second})
	if err == nil {
		t.Fatalf("Download returned %q with no error, want an error", data)
	}
	if len(data) != 0 {
		t.Errorf("Data = %q, want empty", data)
	}

	res := DownloadConditional(context.Background(), Request{URL: srv.URL}, Options{Retries: 1, Timeout: 5 * time.Second})
	if res.NotModified {
		t.Error("NotModified = true for an unconditional request")
	}
	if res.Err == nil {
		t.Error("DownloadConditional: expected an error for an unconditional 304")
	}
}
