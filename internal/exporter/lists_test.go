package exporter

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func categoryArchive(t *testing.T, members map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, body := range members {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCategoryImportReadsOnlyExactDomainMember(t *testing.T) {
	dir := t.TempDir()
	body := "bank.example\n"
	archive := categoryArchive(t, map[string]string{"../../escaped": "bad", "bank/urls": "bank.example/private", "bank/domains": body})
	path := filepath.Join(dir, "bank.domains")
	hash, err := extractDomains(bytes.NewReader(archive), path, "bank")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != body {
		t.Fatal("wrong domain content")
	}
	want := sha256.Sum256([]byte(body))
	if hash != hex.EncodeToString(want[:]) {
		t.Fatal("incorrect list checksum")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("extracted an unexpected archive member")
	}
}

func TestCategoryImportRejectsTruncatedOrCorruptGzip(t *testing.T) {
	archive := categoryArchive(t, map[string]string{"bank/domains": "bank.example\n"})
	corrupt := append([]byte(nil), archive...)
	corrupt[len(corrupt)-8] ^= 0xff
	for _, data := range [][]byte{archive[:len(archive)-4], corrupt} {
		if _, err := extractDomains(bytes.NewReader(data), filepath.Join(t.TempDir(), "out"), "bank"); err == nil {
			t.Fatal("accepted a damaged category archive")
		}
	}
}

func TestDownloadedListNormalizesAndReportsMalformedEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "domains")
	if err := os.WriteFile(path, []byte("# comment\nBANK.EXAMPLE.\nw-w-i-s-.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, accepted, skipped, err := normalizeDownloadedList(path)
	if err != nil || accepted != 1 || skipped != 1 || hash == "" {
		t.Fatalf("unexpected import result: %d %d %v", accepted, skipped, err)
	}
	entries, actualHash, err := loadDomains(path)
	if err != nil || len(entries) != 1 || entries[0] != "bank.example" || actualHash != hash {
		t.Fatal("normalized feed cannot be loaded offline")
	}
}
