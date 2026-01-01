package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	distDir   = "dist/yaml"
	configDir = "config"
	cacheDir  = ".cache/external"
)

// === Caching Logic ===

func getCachePath(url string) string {
	hash := sha256.Sum256([]byte(url))
	return filepath.Join(cacheDir, hex.EncodeToString(hash[:]))
}

type CacheMeta struct {
	ETag         string `json:"etag"`
	LastModified string `json:"last_modified"`
}

func fetchURL(url string) ([]string, error) {
	if url == "" {
		return nil, nil
	}

	cacheFile := getCachePath(url)
	metaFile := cacheFile + ".meta"

	// Ensure cache directory exists
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, err
	}

	// Prepare request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "iplist-bot/1.0")

	// Load cache metadata
	var meta CacheMeta
	if data, err := os.ReadFile(metaFile); err == nil {
		json.Unmarshal(data, &meta)
		if meta.ETag != "" {
			req.Header.Set("If-None-Match", meta.ETag)
		}
		if meta.LastModified != "" {
			req.Header.Set("If-Modified-Since", meta.LastModified)
		}
	}

	// Do request
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// On error, if we have cache, fallback to it
		if _, err := os.Stat(cacheFile); err == nil {
			log.Printf("Network error fetching %s, using outdated cache: %v", url, err)
			return readLines(cacheFile)
		}
		return nil, err
	}
	defer resp.Body.Close()

	// Handle Response
	if resp.StatusCode == http.StatusNotModified {
		log.Printf("Cached (304): %s", url)
		return readLines(cacheFile)
	}

	if resp.StatusCode != http.StatusOK {
		// Logic: if fail, fallback to cache if valid
		if _, err := os.Stat(cacheFile); err == nil {
			log.Printf("HTTP %d fetching %s, using outdated cache", resp.StatusCode, url)
			return readLines(cacheFile)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Read body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Save to cache
	if err := os.WriteFile(cacheFile, body, 0644); err != nil {
		log.Printf("Failed to write cache: %v", err)
	}

	// Save metadata
	newMeta := CacheMeta{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}
	if metaBytes, err := json.Marshal(newMeta); err == nil {
		os.WriteFile(metaFile, metaBytes, 0644)
	}

	log.Printf("Fetched (200): %s", url)
	return strings.Split(string(body), "\n"), nil
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(data), "\n"), nil
}

// === Normalization ===

func normalizeDomain(line string) string {
	line = strings.TrimSpace(line)
	line = strings.ReplaceAll(line, "\"", "")
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	// If no wildcard (*), no +., start with dot -> assume domain -> add +.
	// Logic from python script:
	// if not (line.startswith('*') or line.startswith('+.') or line.startswith('.')) and '/' not in line
	if !strings.HasPrefix(line, "*") && !strings.HasPrefix(line, "+.") && !strings.HasPrefix(line, ".") && !strings.Contains(line, "/") {
		return "+." + line
	}
	return line
}

func normalizeIP(line string) string {
	line = strings.TrimSpace(line)
	line = strings.ReplaceAll(line, "\"", "")
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	// If already has CIDR
	if strings.Contains(line, "/") {
		return line
	}
	// Add CIDR
	if strings.Contains(line, ":") {
		return line + "/128"
	}
	return line + "/32"
}

// === Config Processing ===

type Config struct {
	Domains []string          `json:"domains"`
	IP4     []string          `json:"ip4"`
	IP6     []string          `json:"ip6"`
	CIDR4   []string          `json:"cidr4"`
	CIDR6   []string          `json:"cidr6"`
	External map[string][]string `json:"external"`
}

type Result struct {
	Group   string
	Site    string
	Domains []string
	IPs     []string
}

func processConfig(path string) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	relPath, _ := filepath.Rel(configDir, path)
	group := filepath.Dir(relPath)
	site := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(path))

	if group == "." || site == "" {
		return nil, nil // skip root files
	}

	var domains []string
	var ips []string

	// Local data
	domains = append(domains, cfg.Domains...)
	ips = append(ips, cfg.IP4...)
	ips = append(ips, cfg.IP6...)
	ips = append(ips, cfg.CIDR4...)
	ips = append(ips, cfg.CIDR6...)

	// External
	for _, url := range cfg.External["domains"] {
		lines, _ := fetchURL(url)
		domains = append(domains, lines...)
	}
	for _, field := range []string{"ip4", "ip6", "cidr4", "cidr6"} {
		for _, url := range cfg.External[field] {
			lines, _ := fetchURL(url)
			ips = append(ips, lines...)
		}
	}

	// Normalize
	domSet := make(map[string]bool)
	for _, d := range domains {
		n := normalizeDomain(d)
		if n != "" {
			domSet[n] = true
		}
	}

	ipSet := make(map[string]bool)
	for _, i := range ips {
		n := normalizeIP(i)
		if n != "" {
			ipSet[n] = true
		}
	}

	res := &Result{
		Group:   group,
		Site:    site,
		Domains: make([]string, 0, len(domSet)),
		IPs:     make([]string, 0, len(ipSet)),
	}

	for k := range domSet {
		res.Domains = append(res.Domains, k)
	}
	for k := range ipSet {
		res.IPs = append(res.IPs, k)
	}
	sort.Strings(res.Domains)
	sort.Strings(res.IPs)

	return res, nil
}

// === Writing ===

func writeYAML(path string, items []string) {
	if len(items) == 0 {
		return
	}
	// Sort again just in case
	sort.Strings(items)

	// Build content
	var sb strings.Builder
	sb.WriteString("payload:\n")
	for _, item := range items {
		sb.WriteString("  - \"")
		sb.WriteString(item)
		sb.WriteString("\"\n")
	}
	newContent := sb.String()

	// Compare with existing
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == newContent {
			return // skip
		}
	}

	// Write
	os.WriteFile(path, []byte(newContent), 0644)
}

func main() {
	os.MkdirAll(filepath.Join(distDir, "domain"), 0755)
	os.MkdirAll(filepath.Join(distDir, "ipcidr"), 0755)

	files, err := filepath.Glob(filepath.Join(configDir, "*", "*.json"))
	if err != nil {
		log.Fatal(err)
	}

	results := make([]*Result, len(files))
	var wg sync.WaitGroup

	// Semaphore/worker pool logic (8 workers)
	sem := make(chan struct{}, 8)

	for i, f := range files {
		wg.Add(1)
		go func(i int, f string) {
			defer wg.Done()
			sem <- struct{}{} // acquire
			res, err := processConfig(f)
			<-sem // release
			
			if err != nil {
				log.Printf("Error processing %s: %v", f, err)
			}
			results[i] = res
		}(i, f)
	}
	wg.Wait()

	// Aggregation
	groupsDomains := make(map[string][]string)
	groupsIPs := make(map[string][]string)
	var allDomains []string
	var allIPs []string

	for _, res := range results {
		if res == nil {
			continue
		}

		// Write site files
		writeYAML(filepath.Join(distDir, "domain", fmt.Sprintf("%s--%s.yaml", res.Group, res.Site)), res.Domains)
		writeYAML(filepath.Join(distDir, "ipcidr", fmt.Sprintf("%s--%s.yaml", res.Group, res.Site)), res.IPs)

		// Aggregate
		groupsDomains[res.Group] = append(groupsDomains[res.Group], res.Domains...)
		groupsIPs[res.Group] = append(groupsIPs[res.Group], res.IPs...)
		allDomains = append(allDomains, res.Domains...)
		allIPs = append(allIPs, res.IPs...)
	}

	// Write Groups (deduplicated)
	for g, items := range groupsDomains {
		writeYAML(filepath.Join(distDir, "domain", g+".yaml"), unique(items))
	}
	for g, items := range groupsIPs {
		writeYAML(filepath.Join(distDir, "ipcidr", g+".yaml"), unique(items))
	}

	// Write All (deduplicated)
	writeYAML(filepath.Join(distDir, "domain", "all.yaml"), unique(allDomains))
	writeYAML(filepath.Join(distDir, "ipcidr", "all.yaml"), unique(allIPs))

	fmt.Println("Go YAML generation complete.")
}

func unique(items []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range items {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
