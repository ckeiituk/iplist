package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	cacheDir           = ".cache/external"
	defaultExternalURL = "https://raw.githubusercontent.com/legiz-ru/mihomo-rule-sets/main/ru-bundle/rule.yaml"
)

// === Caching Logic (same as generate_yaml.go) ===

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

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "iplist-bot/1.0")

	var meta CacheMeta
	if data, err := os.ReadFile(metaFile); err == nil {
		_ = json.Unmarshal(data, &meta)
		if meta.ETag != "" {
			req.Header.Set("If-None-Match", meta.ETag)
		}
		if meta.LastModified != "" {
			req.Header.Set("If-Modified-Since", meta.LastModified)
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if _, err := os.Stat(cacheFile); err == nil {
			log.Printf("Network error fetching %s, using outdated cache: %v", url, err)
			return readLines(cacheFile)
		}
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		log.Printf("Cached (304): %s", url)
		return readLines(cacheFile)
	}

	if resp.StatusCode != http.StatusOK {
		if _, err := os.Stat(cacheFile); err == nil {
			log.Printf("HTTP %d fetching %s, using outdated cache", resp.StatusCode, url)
			return readLines(cacheFile)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(cacheFile, body, 0644); err != nil {
		log.Printf("Failed to write cache: %v", err)
	}

	newMeta := CacheMeta{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}
	if metaBytes, err := json.Marshal(newMeta); err == nil {
		_ = os.WriteFile(metaFile, metaBytes, 0644)
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
	line = strings.ReplaceAll(line, "'", "")
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	if !strings.HasPrefix(line, "*") && !strings.HasPrefix(line, "+.") && !strings.HasPrefix(line, ".") && !strings.Contains(line, "/") {
		return "+." + line
	}
	return line
}

func suffixHost(pattern string) string {
	if strings.HasPrefix(pattern, "+.") {
		return pattern[2:]
	}
	if strings.HasPrefix(pattern, ".") {
		return pattern[1:]
	}
	return ""
}

func coveredByExternal(host string, externalSuffixHosts map[string]bool) bool {
	if externalSuffixHosts == nil || host == "" {
		return false
	}
	if externalSuffixHosts[host] {
		return true
	}
	parts := strings.Split(host, ".")
	for i := 1; i < len(parts); i++ {
		parent := strings.Join(parts[i:], ".")
		if externalSuffixHosts[parent] {
			return true
		}
	}
	return false
}

func normalizeMaybeIP(line string) (string, bool) {
	line = strings.TrimSpace(line)
	line = strings.ReplaceAll(line, "\"", "")
	line = strings.ReplaceAll(line, "'", "")
	if line == "" || strings.HasPrefix(line, "#") {
		return "", false
	}

	if strings.Contains(line, "/") {
		if _, _, err := net.ParseCIDR(line); err == nil {
			return line, true
		}
		return "", false
	}

	if ip := net.ParseIP(line); ip != nil {
		if strings.Contains(line, ":") {
			return line + "/128", true
		}
		return line + "/32", true
	}
	return "", false
}

// === YAML parsing/writing ===

func parsePayloadLines(lines []string) []string {
	items := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || line == "payload:" {
			continue
		}
		if !strings.HasPrefix(line, "-") {
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(line, "-"))
		item = strings.Trim(item, "\"'")
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func readYAMLItems(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePayloadLines(strings.Split(string(data), "\n")), nil
}

func writeYAML(path string, items []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("payload:\n")
	for _, item := range items {
		sb.WriteString("  - \"")
		sb.WriteString(item)
		sb.WriteString("\"\n")
	}
	newContent := sb.String()

	if existing, err := os.ReadFile(path); err == nil && string(existing) == newContent {
		return nil
	}
	return os.WriteFile(path, []byte(newContent), 0644)
}

// === Filtering ===

func buildExternalSets(lines []string) (map[string]bool, map[string]bool, map[string]bool) {
	domains := make(map[string]bool)
	suffixHosts := make(map[string]bool)
	ips := make(map[string]bool)

	for _, item := range parsePayloadLines(lines) {
		if ip, ok := normalizeMaybeIP(item); ok {
			ips[ip] = true
			continue
		}
		if dom := normalizeDomain(item); dom != "" {
			domains[dom] = true
			if host := suffixHost(dom); host != "" {
				suffixHosts[host] = true
			}
		}
	}
	return domains, suffixHosts, ips
}

func filterDomains(items []string, extDomains, extSuffixHosts map[string]bool) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]bool)

	for _, item := range items {
		n := normalizeDomain(item)
		if n == "" {
			continue
		}
		if extDomains[n] {
			continue
		}
		if host := suffixHost(n); host != "" && coveredByExternal(host, extSuffixHosts) {
			continue
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

func filterIPs(items []string, extIPs map[string]bool) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]bool)

	for _, item := range items {
		ip, ok := normalizeMaybeIP(item)
		if !ok {
			continue
		}
		if extIPs[ip] {
			continue
		}
		if seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, ip)
	}
	return out
}

func processKind(kind, srcRoot, dstRoot string, filter func([]string) []string) error {
	srcDir := filepath.Join(srcRoot, kind)
	dstDir := filepath.Join(dstRoot, kind)

	if _, err := os.Stat(srcDir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") {
			return nil
		}

		items, err := readYAMLItems(path)
		if err != nil {
			return err
		}

		filtered := filter(items)

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstDir, rel)
		return writeYAML(dstPath, filtered)
	})
}

func main() {
	var srcDir string
	var dstDir string
	var externalURL string

	flag.StringVar(&srcDir, "src", "dist/yaml", "Source YAML dir")
	flag.StringVar(&dstDir, "dst", "dist/yaml-dedup", "Output YAML dir")
	flag.StringVar(&externalURL, "external-url", defaultExternalURL, "External YAML URL")
	flag.Parse()

	if filepath.Clean(srcDir) == filepath.Clean(dstDir) {
		log.Fatal("src and dst directories must be different")
	}
	if dstDir == "" || dstDir == "/" {
		log.Fatal("invalid dst directory")
	}
	if err := os.RemoveAll(dstDir); err != nil {
		log.Fatalf("failed to clear dst dir: %v", err)
	}

	lines, err := fetchURL(externalURL)
	if err != nil {
		log.Printf("Failed to fetch external list: %v", err)
		lines = nil
	}

	extDomains, extSuffixHosts, extIPs := buildExternalSets(lines)

	if err := processKind("domain", srcDir, dstDir, func(items []string) []string {
		return filterDomains(items, extDomains, extSuffixHosts)
	}); err != nil {
		log.Fatal(err)
	}
	if err := processKind("ipcidr", srcDir, dstDir, func(items []string) []string {
		return filterIPs(items, extIPs)
	}); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Dedup YAML generation complete.")
}
