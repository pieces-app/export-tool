package exporter

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func withSecretScanBudget(t *testing.T, budget time.Duration) {
	t.Helper()
	previous := secretScanBudget
	secretScanBudget = budget
	t.Cleanup(func() { secretScanBudget = previous })
}

// Dev notes that mention many services make gitleaks run most of its rules,
// each over the whole fragment. This text mentions every rule keyword.
func keywordDenseNotes(t *testing.T, s *Scanner, size int) string {
	t.Helper()
	seen := map[string]bool{}
	for _, rule := range s.detector.Config.Rules {
		for _, k := range rule.Keywords {
			seen[strings.ToLower(k)] = true
		}
	}
	keywords := make([]string, 0, len(seen))
	for k := range seen {
		keywords = append(keywords, k)
	}
	sort.Strings(keywords)
	words := strings.Fields("the summary covered work on the export tool and reviewed notes about meetings plans design tests release")
	rng := mathrand.New(mathrand.NewSource(7))
	var b strings.Builder
	for b.Len() < size {
		for i := 0; i < 12; i++ {
			b.WriteString(words[rng.Intn(len(words))] + " ")
		}
		b.WriteString(keywords[rng.Intn(len(keywords))] + " config.\n")
	}
	return b.String()[:size]
}

// Reproduces "secret scan did not finish after one full retry": one call over
// a large keyword-dense value outlasts the budget twice. Bounded windows keep
// each call far below it.
func TestKeywordDenseTextFinishesWithinTheScanBudget(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	text := keywordDenseNotes(t, s, 256<<10)
	withSecretScanBudget(t, 3*time.Second)
	stats := ScanResult{}
	if _, err := s.cleanString(context.Background(), "text", text, &stats); err != nil {
		t.Fatalf("keyword-dense 256 KiB value did not finish scanning: %v (timeout retries %d)", err, stats.TimeoutRetries)
	}
}

func TestSecretScanWindowsAreBoundedOverlappingAndComplete(t *testing.T) {
	lines := strings.Repeat("A summary line about export work and nothing secret at all.\n", 40000)[:2<<20]
	words := strings.Repeat("unbroken prose without any line breaks ", 9000)[:300<<10]
	blob := strings.Repeat("x", 300<<10)
	for name, text := range map[string]string{"lines": lines, "words": words, "blob": blob, "short": "token=abc"} {
		windows := secretScanWindows(text)
		if len(windows) == 0 || windows[0][0] != 0 || windows[len(windows)-1][1] != len(text) {
			t.Fatalf("%s: windows %v do not cover the text", name, windows)
		}
		for i, w := range windows {
			if size := w[1] - w[0]; size > secretScanWindowBytes {
				t.Fatalf("%s: window %d has %d bytes, above %d", name, i, size, secretScanWindowBytes)
			}
			if i > 0 {
				previous := windows[i-1]
				if w[0] <= previous[0] || w[0] > previous[1] {
					t.Fatalf("%s: window %d %v does not follow %v", name, i, w, previous)
				}
				if overlap := previous[1] - w[0]; overlap < secretScanOverlapBytes {
					t.Fatalf("%s: windows %d and %d overlap by %d bytes, below %d", name, i-1, i, overlap, secretScanOverlapBytes)
				}
			}
			// Cut at line breaks so single-line rules see the same context as in the full text.
			if name == "lines" && (w[0] > 0 && text[w[0]-1] != '\n' || w[1] < len(text) && text[w[1]-1] != '\n') {
				t.Fatalf("lines: window %d %v is not cut at line breaks", i, w)
			}
			if name == "words" && (w[0] > 0 && text[w[0]-1] != ' ' || w[1] < len(text) && text[w[1]-1] != ' ') {
				t.Fatalf("words: window %d %v is not cut after a space", i, w)
			}
		}
	}
}

func generatedPrivateKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

// A token and a multi-line private key placed across the first window
// boundaries must still be redacted, as when the value is scanned whole.
func TestSecretsAcrossScanWindowBoundariesAreRedacted(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	filler := strings.Repeat("Ordinary narrative line with nothing sensitive in it.\n", 2400)
	token := fakeSecret()
	key := generatedPrivateKey(t)
	body := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(key), "-----BEGIN RSA PRIVATE KEY-----"), "-----END RSA PRIVATE KEY-----")
	first := secretScanWindowBytes - 40
	second := 2*secretScanWindowBytes - len(key)/2
	text := filler[:first] + "deploy token " + token + "\n" + filler[:second-first] + key + filler
	stats := ScanResult{}
	clean, err := s.cleanString(context.Background(), "text", text, &stats)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(clean, token) {
		t.Fatal("token across a window boundary was not redacted")
	}
	for _, line := range strings.Fields(body) {
		if len(line) > 40 && strings.Contains(clean, line) {
			t.Fatal("private key across a window boundary was not redacted")
		}
	}
	if stats.Redactions < 2 {
		t.Fatalf("expected both secrets redacted, got %d redactions", stats.Redactions)
	}
}

// A timeout in the final audit names the output file it was scanning, like a
// finding does, so a failed export can be diagnosed from its error alone.
func TestAuditTimeoutNamesTheOutputFile(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: s}}
	relative := filepath.Join("workstream_summaries", "timeline", "000001.Long_note.md")
	path := filepath.Join(r.stage, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(keywordDenseNotes(t, s, 64<<10)), 0600); err != nil {
		t.Fatal(err)
	}
	withSecretScanBudget(t, 50*time.Millisecond)
	err := r.auditOutputFile(path)
	if !errors.Is(err, errSecretScanIncomplete) {
		t.Fatalf("expected the scan to time out, got %v", err)
	}
	want := "secret scan did not finish after one full retry in " + filepath.ToSlash(relative) + "; partial directory was not finalized"
	if err.Error() != want {
		t.Fatalf("timeout error %q does not name the file; want %q", err, want)
	}
}
