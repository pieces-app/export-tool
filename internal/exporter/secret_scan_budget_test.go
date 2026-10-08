package exporter

import (
	"context"
	"encoding/base64"
	"errors"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/report"
)

func withSecretScanBudget(t *testing.T, budget time.Duration) {
	t.Helper()
	previous := secretScanBudget
	secretScanBudget = budget
	t.Cleanup(func() { secretScanBudget = previous })
}

func withDetectFragment(t *testing.T, hook func(context.Context, *detect.Detector, detect.Fragment) []report.Finding) {
	t.Helper()
	previous := detectFragment
	detectFragment = hook
	t.Cleanup(func() { detectFragment = previous })
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

// A detector call's time grows with its fragment, so its deadline must too.
// With a fixed 10 s deadline, real 1 MiB notes failed the export.
func TestScanBudgetGrowsWithTheScannedText(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	var remaining []time.Duration
	withDetectFragment(t, func(ctx context.Context, d *detect.Detector, f detect.Fragment) []report.Finding {
		deadline, _ := ctx.Deadline()
		remaining = append(remaining, time.Until(deadline))
		return d.DetectContext(ctx, f)
	})
	for _, value := range []string{"short note", strings.Repeat("A summary line about export work.\n", 32000)[:1<<20]} {
		if _, err := s.cleanString(context.Background(), "text", value, &ScanResult{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(remaining) != 2 {
		t.Fatalf("expected one detector call per value, got %d", len(remaining))
	}
	if short := remaining[0]; short > 2*secretScanBudget {
		t.Fatalf("a short value got %v, want about %v", short, secretScanBudget)
	}
	if large := remaining[1]; large < 16*secretScanBudget {
		t.Fatalf("a 1 MiB value got only %v to scan, want at least %v", large, 16*secretScanBudget)
	}
}

// Reproduces "secret scan did not finish after one full retry" with real
// scanning: 1 MiB that mentions every rule keyword took over 10 s twice.
func TestKeywordDenseMegabyteFinishesScanning(t *testing.T) {
	if testing.Short() {
		t.Skip("scans 1 MiB with every gitleaks rule")
	}
	s := scanner(t, DefaultPolicy())
	stats := ScanResult{}
	if _, err := s.cleanString(context.Background(), "text", keywordDenseNotes(t, s, 1<<20), &stats); err != nil {
		t.Fatalf("keyword-dense 1 MiB value did not finish scanning: %v (timeout retries %d)", err, stats.TimeoutRetries)
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
	if err := os.WriteFile(path, []byte(strings.Repeat("Long note text.\n", 4096)), 0600); err != nil {
		t.Fatal(err)
	}
	withSecretScanBudget(t, 250*time.Millisecond)
	withDetectFragment(t, func(ctx context.Context, d *detect.Detector, f detect.Fragment) []report.Finding {
		if len(f.Raw) > 4096 {
			<-ctx.Done() // the note's content never finishes; its name does
			return nil
		}
		return d.DetectContext(ctx, f)
	})
	err := r.auditOutputFile(path)
	if !errors.Is(err, errSecretScanIncomplete) {
		t.Fatalf("expected the scan to time out, got %v", err)
	}
	want := "secret scan did not finish after one full retry in " + relative + "; partial directory was not finalized"
	if err.Error() != want {
		t.Fatalf("timeout error %q does not name the file; want %q", err, want)
	}
}

// syntheticPrivateKeyBlock returns a PGP private key block of about size bytes.
func syntheticPrivateKeyBlock(size int) string {
	rng := mathrand.New(mathrand.NewSource(11))
	var b strings.Builder
	// Joined at run time, so this source file never holds a key-shaped block.
	kind := "PGP PRIVATE " + "KEY BLOCK"
	b.WriteString("-----BEGIN " + kind + "-----\n\n")
	line := make([]byte, 48)
	for b.Len() < size {
		rng.Read(line)
		b.WriteString(base64.StdEncoding.EncodeToString(line) + "\n")
	}
	b.WriteString("-----END " + kind + "-----\n")
	return b.String()
}

// The audit reads files in 1 MiB pieces. A key block longer than the old
// 4 KiB overlap that spans a read boundary must still be found.
func TestAuditFindsAPrivateKeyAcrossAReadBoundary(t *testing.T) {
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
	key := syntheticPrivateKeyBlock(10 << 10)
	before := auditReadBytes - 6<<10
	text := strings.Repeat("Ordinary narrative line.\n", before/25+1)[:before] + key + strings.Repeat("More narrative.\n", 400)
	path := filepath.Join(r.stage, "note.md")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutputFile(path); err == nil || !strings.Contains(err.Error(), "content requiring review") {
		t.Fatalf("a 10 KiB private key across a 1 MiB read boundary was not found: %v", err)
	}
}

// Gitleaks finds a token inside base64-encoded text by decoding the whole run.
// It must be withheld at every alignment, so no change may split such a run.
func TestEncodedSecretInLongBlobIsWithheld(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	plain := strings.Repeat("harmless line of text\n", 5000) + "deploy token " + fakeSecret() + "\n" + strings.Repeat("more harmless text\n", 2000)
	blob := base64.StdEncoding.EncodeToString([]byte(plain))
	for shift := 0; shift < 4; shift++ {
		value := "attachment" + strings.Repeat("x", shift) + " " + blob + " end"
		clean, err := s.cleanString(context.Background(), "text", value, &ScanResult{})
		if err != nil {
			t.Fatal(err)
		}
		if clean != "[WITHHELD:ENCODED_SECRET]" {
			t.Errorf("shift %d: a token encoded in a %d-byte blob was not withheld", shift, len(blob))
		}
	}
}
