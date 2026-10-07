package exporter

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func renderedURLAuditRun(t *testing.T) *run {
	t.Helper()
	return &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
}

// Each line is what a renderer writes around a URL that record sanitizing
// already redacted: link labels, link destinations, Markdown escapes and
// truncated titles. The final audit must not treat that syntax as a value.
func TestAuditAcceptsRedactedURLsInRenderedSyntax(t *testing.T) {
	r := renderedURLAuditRun(t)
	for name, text := range map[string]string{
		"navigation label":         "- [Password reset https://example.com/reset?token=REDACTED](../websites/a.md) — WEBSITES\n",
		"related record label":     "- summaries: [Maps https://maps.example/js?key=REDACTED](../a.md)\n",
		"rewritten autolink":       "See [https://example.com/reset?token=REDACTED](https://example.com/reset?token=REDACTED) for details.\n",
		"autolink before period":   "Opened [https://example.com/reset?token=REDACTED](https://example.com/reset?token=REDACTED).\n",
		"destination fragment":     "[Callback](https://example.com/cb?apiKey=REDACTED#top)\n",
		"escaped heading fragment": "# Callback https://example.com/cb?token=REDACTED\\#top\n",
		"rendered hard line break": "Opened https://example.com/reset?token=REDACTED\\\nthen continued.\n",
		"signature label":          "- [https://cdn.example/f?Signature=REDACTED](../a.md)\n",
		"title cut inside marker":  "# Notes https://example.com/reset?token=REDACTE…\n",
		"title cut after equals":   "- [Notes https://example.com/reset?token=…](../a.md)\n",
		"label cut after marker":   "- [Notes https://example.com/reset?token=REDACTED…](../a.md)\n",
	} {
		if err := r.auditText(text); err != nil {
			t.Errorf("%s: already-redacted URL failed the final audit: %v", name, err)
		}
	}
}

// The same rendered positions must still reject a credential the renderer
// restored, including one hidden behind an already-redacted link label.
func TestAuditRejectsCredentialsInRenderedSyntax(t *testing.T) {
	r := renderedURLAuditRun(t)
	for name, text := range map[string]string{
		"navigation label":          "- [https://example.com/reset?token=abc123](../a.md)\n",
		"link destination":          "[Reset](https://example.com/reset?token=abc123)\n",
		"destination after label":   "[https://example.com/reset?token=REDACTED](https://example.com/reset?token=abc123)\n",
		"marker followed by value":  "# https://example.com/reset?token=REDACTED2\n",
		"truncated value":           "# https://example.com/reset?token=abc1…\n",
		"escaped earlier parameter": "# https://example.com/reset?view\\_mode=full&token=abc123\n",
		"user information":          "- [https://reader:pw@example.com/](../a.md)\n",
	} {
		if err := r.auditText(text); err == nil || !strings.Contains(err.Error(), "content requiring review") {
			t.Errorf("%s: credential in rendered URL passed the final audit: %v", name, err)
		}
	}
}

// Reproduces a customer export failure: titles and bodies with credential
// URLs were redacted correctly, then rejected by the final output audit.
func TestFilteredExportFinalizesRedactedCredentialURLs(t *testing.T) {
	summaryID := "11111111-2222-4333-8444-555555555555"
	summary := record(summaryID, "2026-09-29T00:00:00Z")
	summary["name"] = "Password reset https://example.com/reset?token=title-secret-value"
	summary["annotations"], summary["persons"], summary["pipelines"], summary["websites"] = refs("body"), refs(), refs(), refs("site")
	body := record("body", "2026-09-29T00:00:00Z")
	body["type"], body["summaries"] = "SUMMARY", refs(summaryID)
	body["text"] = "Setup notes.\n\nOpened <https://example.com/invite?token=body-secret-value> to finish setup."
	site := record("site", "2026-09-29T00:00:00Z")
	site["url"], site["workstream_summaries"] = "https://app.example.com/login?key=site-secret-value", refs(summaryID)
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {summary}, "ANNOTATIONS": {body}, "WEBSITES": {site}, "PERSONS": {}, "PIPELINES": {}}}
	srv := f.server(t)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	selection, err := SelectScope("summaries", "")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "export")
	manifest, err := Export(context.Background(), client, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: selection.Materials, ReferenceOnly: selection.ReferenceOnly, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Metadata: "off", Format: "markdown"})
	if err != nil {
		t.Fatalf("redacted credential URLs blocked finalization: %v", err)
	}
	if manifest.Status != "complete_for_implemented_scope" {
		t.Fatalf("unexpected status: %+v", manifest.Issues)
	}
	var joined strings.Builder
	err = filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		joined.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"title-secret-value", "body-secret-value", "site-secret-value"} {
		if strings.Contains(joined.String(), private) {
			t.Fatalf("credential %q survived the finalized archive", private)
		}
	}
	for _, kept := range []string{"https://example.com/reset?token=REDACTED", "https://example.com/invite?token=REDACTED", "https://app.example.com/login?key=REDACTED"} {
		if !strings.Contains(joined.String(), kept) {
			t.Fatalf("redacted URL %q is missing from the archive", kept)
		}
	}
}
