package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func TestLoad_valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{
		"fetch": {"retries": 5, "timeout_seconds": 30, "parallelism": 4},
		"routing": {
			"domains": {"url": "https://example.com/domains.lst"},
			"subnets": {
				"urls": ["https://example.com/sub1.lst"],
				"manual_file": "/tmp/manual.lst"
			}
		}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Fetch.Retries != 5 {
		t.Errorf("retries = %d, want 5", cfg.Fetch.Retries)
	}
	if cfg.Fetch.TimeoutSeconds != 30 {
		t.Errorf("timeout = %d, want 30", cfg.Fetch.TimeoutSeconds)
	}
	if cfg.Fetch.Parallelism != 4 {
		t.Errorf("parallelism = %d, want 4", cfg.Fetch.Parallelism)
	}
	if cfg.Routing.Domains.URL != "https://example.com/domains.lst" {
		t.Errorf("domains.url = %q", cfg.Routing.Domains.URL)
	}
	if len(cfg.Routing.Subnets.URLs) != 1 {
		t.Errorf("subnets.urls length = %d, want 1", len(cfg.Routing.Subnets.URLs))
	}
	if cfg.Routing.Subnets.ManualFile != "/tmp/manual.lst" {
		t.Errorf("manual_file = %q", cfg.Routing.Subnets.ManualFile)
	}
}

func TestLoad_defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{
		"routing": {
			"domains": {"url": "https://example.com/d.lst"}
		}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Fetch.Retries != 3 {
		t.Errorf("default retries = %d, want 3", cfg.Fetch.Retries)
	}
	if cfg.Fetch.TimeoutSeconds != 15 {
		t.Errorf("default timeout = %d, want 15", cfg.Fetch.TimeoutSeconds)
	}
	if cfg.Fetch.Parallelism != 2 {
		t.Errorf("default parallelism = %d, want 2", cfg.Fetch.Parallelism)
	}
	if cfg.Routing.Subnets.ManualFile != "/etc/horn-vpn-manager/lists/manual-ip.lst" {
		t.Errorf("default manual_file = %q", cfg.Routing.Subnets.ManualFile)
	}
}

func TestLoad_validation_empty_routing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{"routing": {}}`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty routing")
	}
}

func TestLoad_missing_file(t *testing.T) {
	_, err := Load("/nonexistent/config.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_invalid_json(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{invalid`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
