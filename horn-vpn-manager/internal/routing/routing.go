// Package routing implements domain/IP list processing for VPN routing.
//
// It handles downloading, caching, normalizing, deduplicating, and merging
// domain and subnet lists. System side-effects (dnsmasq, firewall) are
// delegated to the system package via the Applier interface.
package routing

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/fetch"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
)

// Paths on the target device.
const (
	DefaultListsDir  = "/etc/horn-vpn-manager/lists"
	DomainsCacheFile = "domains.lst"
	SubnetsCacheFile = "subnets.lst"
	VPNIPListFile    = "vpn-ip-list.lst"
)

// Applier abstracts system side-effects so routing logic stays testable.
type Applier interface {
	ApplyDomains(cacheFile, dnsmasqDir string) error
	ApplyIPs(ipListFile string) error
}

type Runner struct {
	Cfg      *config.Config
	Applier  Applier
	ListsDir string
}

func NewRunner(cfg *config.Config, applier Applier) *Runner {
	return &Runner{
		Cfg:      cfg,
		Applier:  applier,
		ListsDir: DefaultListsDir,
	}
}

func (r *Runner) fetchOpts() fetch.Options {
	return fetch.Options{
		Retries:     r.Cfg.Fetch.Retries,
		Timeout:     time.Duration(r.Cfg.Fetch.TimeoutSeconds) * time.Second,
		Parallelism: r.Cfg.Fetch.Parallelism,
	}
}

// Run downloads fresh lists, caches them, and applies to the system.
func (r *Runner) Run(ctx context.Context) error {
	if err := os.MkdirAll(r.ListsDir, 0o755); err != nil {
		return fmt.Errorf("create lists dir: %w", err)
	}

	logx.Header("routing run")

	opts := r.fetchOpts()
	var domainsUpdated, subnetsUpdated bool

	// Download domains
	if url := r.Cfg.Routing.Domains.URL; url != "" {
		logx.Info("Downloading domain list...")
		logx.Detail("  URL: %s", url)
		data, err := fetch.Download(ctx, url, opts)
		if err != nil {
			logx.Err("Failed to download domain list: %v", err)
		} else {
			if err := atomicWrite(r.domainsCachePath(), data); err != nil {
				return fmt.Errorf("write domains cache: %w", err)
			}
			logx.Info("Domain list cached: %s lines -> %s", logx.Bold(fmt.Sprintf("%d", countLines(data))), r.domainsCachePath())
			domainsUpdated = true
		}
	} else {
		logx.Info("domains.url not configured, skipping")
	}

	// Download subnets
	if urls := r.Cfg.Routing.Subnets.URLs; len(urls) > 0 {
		logx.Info("Downloading %s subnet list(s)...", logx.Bold(fmt.Sprintf("%d", len(urls))))
		results := fetch.DownloadAll(ctx, urls, opts)

		var allLines []string
		anySucceeded := false
		for i, res := range results {
			if res.Err != nil {
				logx.Err("Failed to download subnet list: %s", res.URL)
				continue
			}
			anySucceeded = true
			lines := ParseLines(res.Data)
			logx.Detail("  [%d/%d] %s entries from %s", i+1, len(urls), logx.Bold(fmt.Sprintf("%d", len(lines))), lastPathSegment(res.URL))
			allLines = append(allLines, lines...)
		}

		if anySucceeded {
			deduped := Dedup(allLines)
			data := []byte(strings.Join(deduped, "\n") + "\n")
			if err := atomicWrite(r.subnetsCachePath(), data); err != nil {
				return fmt.Errorf("write subnets cache: %w", err)
			}
			logx.Info("Subnet cache: %s unique entries -> %s", logx.Bold(fmt.Sprintf("%d", len(deduped))), r.subnetsCachePath())
			subnetsUpdated = true
		} else {
			logx.Warn("All subnet downloads failed; keeping existing cache")
		}
	} else {
		logx.Info("No subnet URLs configured, skipping")
	}

	// Apply
	if domainsUpdated {
		if err := r.Applier.ApplyDomains(r.domainsCachePath(), "/tmp/dnsmasq.d"); err != nil {
			logx.Err("Failed to apply domains: %v", err)
		}
	}
	if subnetsUpdated || fileExists(r.Cfg.Routing.Subnets.ManualFile) {
		ipList, err := r.BuildIPList()
		if err != nil {
			return fmt.Errorf("build IP list: %w", err)
		}
		if len(ipList) > 0 {
			data := []byte(strings.Join(ipList, "\n") + "\n")
			path := filepath.Join(r.ListsDir, VPNIPListFile)
			// Only write and reload the firewall when content actually changed.
			existing, _ := os.ReadFile(path)
			if string(existing) != string(data) {
				if err := atomicWrite(path, data); err != nil {
					return fmt.Errorf("write vpn-ip-list: %w", err)
				}
				logx.Info("IP list updated: %s entries -> %s", logx.Bold(fmt.Sprintf("%d", len(ipList))), path)
				if err := r.Applier.ApplyIPs(path); err != nil {
					logx.Err("Failed to apply IPs: %v", err)
				}
			} else {
				logx.Info("IP list unchanged, skipping firewall reload")
			}
		} else {
			logx.Info("No IP entries, skipping firewall reload")
		}
	}

	logx.Header("done")
	return nil
}

// Restore applies cached lists to the system without downloading.
func (r *Runner) Restore() error {
	logx.Header("routing restore")

	var restored bool

	domainsCache := r.domainsCachePath()
	if fileExistsNonEmpty(domainsCache) {
		logx.Info("Restoring domain list from cache...")
		if err := r.Applier.ApplyDomains(domainsCache, "/tmp/dnsmasq.d"); err != nil {
			logx.Err("Failed to apply domains: %v", err)
		} else {
			restored = true
		}
	} else {
		logx.Info("No domain cache to restore")
	}

	if fileExistsNonEmpty(r.subnetsCachePath()) || fileExistsNonEmpty(r.Cfg.Routing.Subnets.ManualFile) {
		logx.Info("Restoring IP list from cache...")
		ipList, err := r.BuildIPList()
		if err != nil {
			return fmt.Errorf("build IP list: %w", err)
		}
		if len(ipList) > 0 {
			path := filepath.Join(r.ListsDir, VPNIPListFile)
			data := []byte(strings.Join(ipList, "\n") + "\n")
			if err := atomicWrite(path, data); err != nil {
				return fmt.Errorf("write vpn-ip-list: %w", err)
			}
			logx.Info("IP list updated: %s entries -> %s", logx.Bold(fmt.Sprintf("%d", len(ipList))), path)
			if err := r.Applier.ApplyIPs(path); err != nil {
				logx.Err("Failed to apply IPs: %v", err)
			} else {
				restored = true
			}
		}
	} else {
		logx.Info("No IP cache to restore")
	}

	if restored {
		logx.Header("restore complete")
	} else {
		logx.Header("nothing to restore")
	}
	return nil
}

// BuildIPList merges subnet cache and manual IPs, returning deduplicated lines.
func (r *Runner) BuildIPList() ([]string, error) {
	var allLines []string

	if data, err := os.ReadFile(r.subnetsCachePath()); err == nil {
		allLines = append(allLines, ParseLines(data)...)
	}

	if data, err := os.ReadFile(r.Cfg.Routing.Subnets.ManualFile); err == nil {
		allLines = append(allLines, ParseLines(data)...)
	}

	return Dedup(allLines), nil
}

// ParseLines extracts non-empty, non-comment lines from raw data.
func ParseLines(data []byte) []string {
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		// Line exceeded scanner buffer; return what was collected so far.
		return lines
	}
	return lines
}

// Dedup returns sorted unique entries.
func Dedup(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(lines))
	var result []string
	for _, l := range lines {
		if _, ok := seen[l]; !ok {
			seen[l] = struct{}{}
			result = append(result, l)
		}
	}
	sort.Strings(result)
	return result
}

func (r *Runner) domainsCachePath() string {
	return filepath.Join(r.ListsDir, DomainsCacheFile)
}

func (r *Runner) subnetsCachePath() string {
	return filepath.Join(r.ListsDir, SubnetsCacheFile)
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup
		return err
	}
	return nil
}

func countLines(data []byte) int {
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	return n
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fileExistsNonEmpty(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

func lastPathSegment(url string) string {
	if i := strings.LastIndex(url, "/"); i >= 0 && i < len(url)-1 {
		return url[i+1:]
	}
	return url
}
