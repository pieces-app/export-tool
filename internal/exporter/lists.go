package exporter

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FetchLists is an explicit public-data download, never part of exporting user data.
// Only domain members are copied; no tar path is extracted to the filesystem.
func FetchLists(ctx context.Context, out string, categories []string) error {
	if len(categories) == 0 {
		return errConfig("select adult and/or bank categories")
	}
	seen := map[string]bool{}
	for _, category := range categories {
		if category != "adult" && category != "bank" {
			return errConfig("supported UT1 categories: adult,bank")
		}
		if seen[category] {
			return errConfig("duplicate category")
		}
		seen[category] = true
	}
	if _, err := os.Lstat(out); !os.IsNotExist(err) {
		return errConfig("list output already exists or cannot be inspected")
	}
	stage := out + ".partial"
	if err := os.MkdirAll(filepath.Dir(stage), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(stage, 0700); err != nil {
		return errConfig("list staging directory already exists or cannot be created")
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	manifest := map[string]any{"provider": "UT1 Université Toulouse Capitole", "license": "https://creativecommons.org/licenses/by-sa/4.0/", "source": "https://dsi.ut-capitole.fr/blacklists/", "retrieved_at": time.Now().UTC(), "scope": "domains only; URL/path-specific entries are not imported"}
	hashes := map[string]string{}
	originalHashes := map[string]string{}
	importCounts := map[string]any{}
	for _, category := range categories {
		req, err := http.NewRequestWithContext(ctx, "GET", "https://dsi.ut-capitole.fr/blacklists/download/"+category+".tar.gz", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return errConfig("category download failed")
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("category provider returned HTTP %d", resp.StatusCode)
		}
		hash, err := extractDomains(resp.Body, filepath.Join(stage, category+".domains"), category)
		resp.Body.Close()
		if err != nil {
			return err
		}
		originalHashes[category] = hash
		cleanHash, entries, skipped, err := normalizeDownloadedList(filepath.Join(stage, category+".domains"))
		if err != nil {
			return err
		}
		hashes[category] = cleanHash
		importCounts[category] = map[string]int{"accepted_entries": entries, "skipped_invalid_entries": skipped}
	}
	manifest["sha256"] = hashes
	manifest["original_domain_member_sha256"] = originalHashes
	manifest["import_counts"] = importCounts
	manifest["normalization"] = "Lowercase/IDNA hostnames; blank/comment lines ignored and invalid hostnames omitted with counts. Source archives are not included."
	if err := writeJSON(filepath.Join(stage, "source.json"), manifest); err != nil {
		return err
	}
	attribution := "UT1 website categorization data\nUniversité Toulouse Capitole — Fabrice Prigent and contributors\nSource: https://dsi.ut-capitole.fr/blacklists/\nLicense linked by provider: Creative Commons Attribution-ShareAlike 4.0 International\nhttps://creativecommons.org/licenses/by-sa/4.0/\nThis import retains domain entries only. Category coverage is incomplete.\n"
	if err := writeFile(filepath.Join(stage, "ATTRIBUTION.txt"), []byte(attribution)); err != nil {
		return err
	}
	return os.Rename(stage, out)
}

// Public feeds can contain malformed legacy names. Normalize only during the
// explicit download/import, recording omissions. User-supplied lists stay strict.
func normalizeDownloadedList(path string) (string, int, int, error) {
	source, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer source.Close()
	destination, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", 0, 0, err
	}
	defer destination.Close()
	hash := sha256.New()
	writer := bufio.NewWriter(io.MultiWriter(destination, hash))
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	accepted, skipped := 0, 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		host, err := normalizeHost(line)
		if err != nil {
			skipped++
			continue
		}
		if _, err := writer.WriteString(host + "\n"); err != nil {
			return "", 0, 0, err
		}
		accepted++
	}
	if scanner.Err() != nil || accepted == 0 {
		return "", 0, 0, errConfig("downloaded domain list is unreadable or contains no valid hostnames")
	}
	if err := writer.Flush(); err != nil {
		return "", 0, 0, err
	}
	if err := destination.Sync(); err != nil {
		return "", 0, 0, err
	}
	if err := destination.Close(); err != nil {
		return "", 0, 0, err
	}
	if err := source.Close(); err != nil {
		return "", 0, 0, err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return "", 0, 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), accepted, skipped, nil
}
func extractDomains(input io.Reader, out, category string) (string, error) {
	compressed := &io.LimitedReader{R: input, N: 64 << 20}
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return "", errConfig("invalid category archive")
	}
	defer gz.Close()
	uncompressed := &io.LimitedReader{R: gz, N: 512 << 20}
	tr := tar.NewReader(uncompressed)
	found := false
	hash := sha256.New()
	for members := 0; members < 1000; members++ {
		h, err := tr.Next()
		if err == io.EOF {
			if found {
				// Tar EOF can precede gzip's checksum/trailer. Consume the rest so
				// a truncated stream or decompression-limit hit cannot look complete.
				if _, err := io.Copy(io.Discard, uncompressed); err != nil || uncompressed.N == 0 || compressed.N == 0 {
					return "", errConfig("category archive is truncated, damaged, or exceeds size limits")
				}
				return hex.EncodeToString(hash.Sum(nil)), nil
			}
			return "", errConfig("category archive contains no domain list")
		}
		if err != nil {
			return "", errConfig("category archive could not be fully decoded")
		}
		name := strings.TrimPrefix(h.Name, "./")
		if name != category+"/domains" && name != "blacklists/"+category+"/domains" {
			continue
		}
		if found || h.Typeflag != tar.TypeReg || h.Size <= 0 || h.Size > 256<<20 {
			return "", errConfig("invalid category domain member")
		}
		found = true
		f, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(io.MultiWriter(f, hash), tr)
		closeErr := f.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return "", errConfig("category archive has too many members")
}
