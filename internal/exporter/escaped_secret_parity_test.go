package exporter

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/report"
)

// Readers and the final audit see Markdown and HTML escapes resolved. Stored
// text that already contains them literally must be redacted the same way
// the audit would read it (issue #21).
func TestSanitizeRedactsSecretsHiddenByLiteralEscapes(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	// Built at run time so this source file holds no key-shaped literal.
	token := strings.Repeat("a", 0) + "abcdefghij" + "1234567890"
	for name, body := range map[string]string{
		"backslash underscore": `notes: secret\_value: ` + token + ` and my\_notes stay`,
		"html greater-than":    `config secret key&gt;` + token + ` end &gt; keep`,
	} {
		t.Run(name, func(t *testing.T) {
			clean, stats, err := s.Sanitize(context.Background(), map[string]any{"id": "x", "name": "Event", "readable": body})
			if err != nil {
				t.Fatal(err)
			}
			got := fieldString(clean, "readable")
			if strings.Contains(got, token) || stats.Redactions == 0 {
				t.Fatalf("secret survived sanitizing: %q (redactions %d)", got, stats.Redactions)
			}
			if !strings.Contains(got, "[REDACTED:SECRET]") {
				t.Fatalf("secret was not redacted in place: %q", got)
			}
			for _, keep := range []string{`my\_notes stay`, `end &gt; keep`, `end`} {
				if strings.Contains(body, keep) && !strings.Contains(got, keep) {
					t.Fatalf("harmless text %q was lost: %q", keep, got)
				}
			}
			audit := ScanResult{}
			note := "# Event\n\n## readable\n\n" + rewriteMarkdown(got, "markdown/events/x.md", map[string]*Meta{}) + "\n"
			if _, err := s.cleanString(context.Background(), "", markdownAuditUnescape.Replace(note), &audit); err != nil || audit.Redactions > 0 {
				t.Fatalf("rendered note still fails the audit: %v %v", err, audit.Findings)
			}
		})
	}
}

func TestSanitizePreservesHarmlessEscapes(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	body := `my\_notes &gt; yours &lt; theirs, api\_docs updated`
	clean, stats, err := s.Sanitize(context.Background(), map[string]any{"id": "x", "readable": body})
	if err != nil || stats.Redactions != 0 || fieldString(clean, "readable") != body {
		t.Fatalf("harmless escapes changed: %q (redactions %d, err %v)", fieldString(clean, "readable"), stats.Redactions, err)
	}
}

func TestEscapedDuplicateFindingsRedactInPlace(t *testing.T) {
	secret := "ghp_duplicate_secret"
	stored := `before ghp\_duplicate\_secret after`
	original := detectFragment
	detectFragment = func(ctx context.Context, d *detect.Detector, f detect.Fragment) []report.Finding {
		if strings.Contains(f.Raw, secret) {
			return []report.Finding{
				{RuleID: "one", Secret: secret},
				{RuleID: "two", Secret: secret},
			}
		}
		return nil
	}
	defer func() { detectFragment = original }()

	s := scanner(t, DefaultPolicy())
	got, err := s.cleanString(context.Background(), "text", stored, &ScanResult{})
	if err != nil {
		t.Fatal(err)
	}
	if got == "[WITHHELD:ENCODED_SECRET]" || strings.Contains(got, "duplicate") || !strings.Contains(got, "before ") || !strings.Contains(got, " after") {
		t.Fatalf("duplicate escaped findings were not redacted in place: %q", got)
	}
}

func TestKnownCredentialHiddenByEscapesRedacts(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	secret := "my_app_secret"
	s.known[secret] = true
	got, err := s.cleanString(context.Background(), "text", `before my\_app\_secret after`, &ScanResult{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, secret) || strings.Contains(got, `my\_app\_secret`) || !strings.Contains(got, "before ") || !strings.Contains(got, " after") {
		t.Fatalf("known escaped credential was not redacted in place: %q", got)
	}
}

// Reproduces the reported failure: a summary annotation whose stored text
// holds a Markdown-escaped key. The export must finalize with it redacted.
func TestFilteredExportFinalizesEscapedSecretText(t *testing.T) {
	summaryID := "11111111-2222-4333-8444-555555555555"
	summary := record(summaryID, "2026-09-29T00:00:00Z")
	summary["name"] = "Deploy notes"
	summary["annotations"], summary["persons"], summary["pipelines"] = refs("body"), refs(), refs()
	body := record("body", "2026-09-29T00:00:00Z")
	body["type"], body["summaries"] = "SUMMARY", refs(summaryID)
	hidden := "zyxwvutsrq" + "0987654321"
	body["text"] = "Copied from the wiki:\n\nsecret\\_value: " + hidden + " and more\\_text"
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {summary}, "ANNOTATIONS": {body}, "PERSONS": {}, "PIPELINES": {}}}
	srv := f.server(t)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	selection, _ := SelectScope("summaries", "")
	out := filepath.Join(t.TempDir(), "export")
	manifest, err := Export(context.Background(), client, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: selection.Materials, ReferenceOnly: selection.ReferenceOnly, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Metadata: "off", Format: "markdown"})
	if err != nil {
		t.Fatalf("export failed at the audit: %v", err)
	}
	if manifest.Status != "complete_for_implemented_scope" {
		t.Fatalf("unexpected status: %+v", manifest.Issues)
	}
	var joined strings.Builder
	_ = filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(path)
			joined.Write(b)
		}
		return nil
	})
	if strings.Contains(joined.String(), hidden) {
		t.Fatal("the escaped secret survived into the archive")
	}
	if !strings.Contains(joined.String(), "more") {
		t.Fatal("surrounding text was lost")
	}
}

// Two detector rules often report the same secret. After the first in-place
// redaction the second must not withhold the whole field.
func TestOverlappingEscapedFindingsStayRedactedInPlace(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	token := "abcdefghij" + "1234567890"
	previous := detectFragment
	t.Cleanup(func() { detectFragment = previous })
	detectFragment = func(ctx context.Context, d *detect.Detector, f detect.Fragment) []report.Finding {
		findings := d.DetectContext(ctx, f)
		if strings.Contains(f.Raw, "secret_value: "+token) {
			// A second rule reporting the same value, and a third reporting part of it.
			findings = append(findings, report.Finding{RuleID: "another-rule", Secret: token}, report.Finding{RuleID: "partial-rule", Secret: token[2:]})
		}
		return findings
	}
	body := `notes: secret\_value: ` + token + ` and my\_notes stay`
	clean, stats, err := s.Sanitize(context.Background(), map[string]any{"id": "x", "readable": body})
	if err != nil {
		t.Fatal(err)
	}
	got := fieldString(clean, "readable")
	if got == "[WITHHELD:ENCODED_SECRET]" {
		t.Fatalf("overlapping findings withheld the whole field (redactions %d)", stats.Redactions)
	}
	if strings.Contains(got, token[2:]) || !strings.Contains(got, `my\_notes stay`) {
		t.Fatalf("unexpected result: %q", got)
	}
}

// A credential learned from the source must be caught behind escapes too,
// because the audit resolves them and checks learned credentials as well.
func TestKnownCredentialHiddenByEscapesIsRedacted(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	known := "qwertyuiop_" + "0192837465"
	s.remember(known)
	body := `copied: qwertyuiop\_0192837465 end`
	clean, stats, err := s.Sanitize(context.Background(), map[string]any{"id": "x", "readable": body})
	if err != nil {
		t.Fatal(err)
	}
	got := fieldString(clean, "readable")
	if stats.Redactions == 0 || strings.Contains(got, `qwertyuiop\_0192837465`) || !strings.HasSuffix(got, " end") {
		t.Fatalf("known credential behind an escape survived: %q (redactions %d)", got, stats.Redactions)
	}
}
