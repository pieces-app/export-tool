package exporter

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Record sanitizing must redact a URL's credential values without taking the
// prose or Markdown around the URL with it, and without skipping a second URL
// that the permissive URL pattern ran into.
func TestURLRedactionPreservesSurroundingSyntax(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	for _, c := range []struct {
		name, in, want string
		redactions     int
	}{
		{"sentence punctuation", "Reset at (https://example.com/reset?token=onetime1).", "Reset at (https://example.com/reset?token=REDACTED).", 1},
		{"markdown link destination", "[reset](https://example.com/r?token=onetime1)", "[reset](https://example.com/r?token=REDACTED)", 1},
		{"url label and destination", "[https://example.com/r?token=onetime1](https://example.com/r?token=onetime1)", "[https://example.com/r?token=REDACTED](https://example.com/r?token=REDACTED)", 2},
		{"balanced path parentheses", "See https://example.com/wiki/A_(b)?token=onetime1)", "See https://example.com/wiki/A_(b)?token=REDACTED)", 1},
		{"bracketed IPv6 host", "http://[::1]:39300/cb?token=onetime1", "http://[::1]:39300/cb?token=REDACTED", 1},
		{"balanced value parentheses", "https://example.com/r?token=ab(c)", "https://example.com/r?token=REDACTED", 1},
		// "](" inside a query is not link syntax when a credential follows it.
		{"credential after bracket inside query", "https://example.com/r?q=a](b&token=onetime1", "https://example.com/r?q=a%5D%28b&token=REDACTED", 1},
		{"credential after bracket inside path", "https://example.com/a](b?token=onetime1", "https://example.com/a](b?token=REDACTED", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			stats := ScanResult{}
			got, err := s.cleanString(context.Background(), "text", c.in, &stats)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want || stats.Redactions != c.redactions {
				t.Fatalf("got %q with %d redactions, want %q with %d", got, stats.Redactions, c.want, c.redactions)
			}
		})
	}
}

func TestDeniedHostIsDetectedInMarkdownLinkLabel(t *testing.T) {
	p := DefaultPolicy()
	p.Deny = []DomainRule{{Domain: "bank.example", Subdomains: true}}
	stats := ScanResult{}
	if _, err := scanner(t, p).cleanString(context.Background(), "text", "[https://bank.example](https://bank.example)", &stats); err != nil {
		t.Fatal(err)
	}
	if !stats.Denied {
		t.Fatal("a denied host written as a Markdown link label was not detected")
	}
}

// Under an allow-only policy, a URL that ends at its host and is written as a
// link label must be read as that host, not as an invalid host such as
// "example.com](..", which allow-only treats as denied.
func TestFinalAuditReadsAllowedHostInLinkLabel(t *testing.T) {
	p := DefaultPolicy()
	p.SourceMode = "allow_only"
	p.Allow = []DomainRule{{Domain: "example.com"}}
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, p)}}
	if err := r.auditText("- [https://example.com](../websites/000001.example.2026-09-29.w1.md) — WEBSITES\n"); err != nil {
		t.Fatalf("allowed host in a link label failed the final audit: %v", err)
	}
	if err := r.auditText("- [https://other.example](../websites/w2.md)\n"); err == nil {
		t.Fatal("host outside the allow list passed the final audit")
	}
}

// Generated documents place already-sanitized URLs directly before Markdown
// syntax. The final audit must read the same URL the record scan approved.
func TestFinalAuditAcceptsRedactedURLsInGeneratedMarkdown(t *testing.T) {
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
	for _, rendered := range []string{
		"- [https://example.com/r?token=REDACTED](../websites/000001.r.2026-09-29.w1.md) — 12:00:00 +00:00 · WEBSITES\n",
		"Sources: https://example.com/r?token=REDACTED, Other source\n",
		"- [\\*\\*https://example.com/r?token=REDACTED\\*\\*](x.md)\n",
		"[[https://example.com/r?token=REDACTED]]\n",
		"https://example.com/r?token=REDACTED…\n",
		// Generated labels escape "#", so a fragment follows the value as "\#".
		"# https://example.com/r?token=REDACTED\\#section\n",
		// A title that is itself a link with a URL label; titles escape "]".
		"# \\[https://example.com/r?token=REDACTED\\](https://example.com/r?token=REDACTED)\n",
		// A "](" inside the path belongs to the URL; the label's "](" does not.
		"- [https://example.com/a\\](b?token=REDACTED](../websites/w.md)\n",
	} {
		if err := r.auditText(rendered); err != nil {
			t.Errorf("already-redacted URL in %q failed the final audit: %v", rendered, err)
		}
	}
	for _, leaked := range []string{
		"- [https://example.com/r?token=onetime1](x.md)\n",
		"Sources: https://example.com/r?token=onetime1, Other source\n",
		"[reset](https://example.com/r?token=onetime1)\n",
		// Escaping must not hide a credential parameter name from the audit.
		"- [https://example.com/r?access\\_token=onetime1](x.md)\n",
	} {
		if err := r.auditText(leaked); err == nil {
			t.Errorf("unredacted credential in %q passed the final audit", leaked)
		}
	}
}

// Generated titles escape "_" as "\_". The backslash must not create a word
// boundary that the record scan never saw, such as one that ends a 16-digit
// run for the payment card pattern.
func TestFinalAuditReadsEscapedUnderscoreAsRendered(t *testing.T) {
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
	if err := r.auditOutputFile(auditFixtureFile(t, r, "batch.md", "# Batch 4111111111111111\\_final\n")); err != nil {
		t.Fatalf("number joined to an escaped underscore failed the final audit: %v", err)
	}
	if err := r.auditOutputFile(auditFixtureFile(t, r, "card.md", "# Batch 4111111111111111 final\n")); err == nil {
		t.Fatal("standalone card number passed the final audit")
	}
	// Record JSON is not Markdown: a backslash there is part of the value the
	// record scan read, so it still ends the number.
	if err := r.auditOutputFile(auditFixtureFile(t, r, "batch.json", `{"name":"Batch 4111111111111111\\_final"}`)); err == nil {
		t.Fatal("card number ended by a literal backslash in JSON passed the final audit")
	}
}

// Display truncation must not end inside a URL's query: a partial redaction
// marker or parameter name rescans as a new credential value.
func TestDisplayTruncationKeepsURLQueriesWhole(t *testing.T) {
	website, _ := materialByType("WEBSITES")
	path := strings.Repeat("p", 92)
	if got, want := title(map[string]any{"url": "https://example.com/" + path + "?token=REDACTED&z=1"}, website), "https://example.com/"+path+"…"; got != want {
		t.Errorf("URL title cut inside its query:\n got %q\nwant %q", got, want)
	}
	path = strings.Repeat("p", 80)
	if got, want := title(map[string]any{"name": "Reset link https://example.com/" + path + "?token=REDACTED"}, website), "Reset link https://example.com/"+path+"…"; got != want {
		t.Errorf("title cut inside an embedded URL query:\n got %q\nwant %q", got, want)
	}
	// A cut inside the host keeps the whole host, so domain rules see it.
	if got, want := title(map[string]any{"name": strings.Repeat("x", 115) + " https://example.com/r?token=REDACTED"}, website), strings.Repeat("x", 115)+" https://example.com…"; got != want {
		t.Errorf("title cut inside a URL host:\n got %q\nwant %q", got, want)
	}
	path = strings.Repeat("p", 132)
	entry := navigationEntry{label: "https://example.com/" + path + "?token=REDACTED", path: "markdown/websites/w.md"}
	if got, want := entry.markdown("index.md"), "- [https://example.com/"+path+"…](markdown/websites/w.md)\n"; got != want {
		t.Errorf("navigation label cut inside its URL query:\n got %q\nwant %q", got, want)
	}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	path = strings.Repeat("p", 325)
	summary := &compactRecord{entry: TimelineEntry{Type: "WORKSTREAM_SUMMARIES", ID: "s1", Path: "vault/s1.md"}}
	if err := compactSummaryDescription(root, summary, map[string]any{"description": "Preview https://example.com/" + path + "?token=REDACTED"}, nil); err != nil {
		t.Fatal(err)
	}
	if want := "Preview https://example.com/" + path + "…"; summary.description != want {
		t.Errorf("summary preview cut inside its URL query:\n got %q\nwant %q", summary.description, want)
	}
}

func TestFinalAuditErrorNamesFileAndFindingWithoutValue(t *testing.T) {
	denying := DefaultPolicy()
	denying.Deny = []DomainRule{{Domain: "bank.example", Subdomains: true}}
	secret := fakeSecret()
	for _, c := range []struct {
		name, file, text, value string
		policy                  Policy
		want                    []string
	}{
		{"URL parameter", "markdown/websites/reset.md", "Reset: https://example.com/reset?token=onetime1\n", "onetime1", DefaultPolicy(), []string{"markdown/websites/reset.md", "URL credential parameter"}},
		{"detector rule", "markdown/websites/key.md", "Key: " + secret + "\n", secret, DefaultPolicy(), []string{"markdown/websites/key.md", "github-pat"}},
		{"file name", "markdown/websites/" + secret + ".md", "Approved.\n", secret, DefaultPolicy(), []string{"markdown/websites/", "github-pat"}},
		{"denied domain", "markdown/websites/bank.md", "Statement: https://private.bank.example/account\n", "private.bank.example", denying, []string{"markdown/websites/bank.md", "denied domain"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, c.policy)}}
			if err := os.MkdirAll(filepath.Join(r.stage, "markdown", "websites"), 0700); err != nil {
				t.Fatal(err)
			}
			path := auditFixtureFile(t, r, filepath.FromSlash(c.file), c.text)
			err := r.auditOutputFile(path)
			if err == nil {
				t.Fatal("finding was not reported")
			}
			message := err.Error()
			if !strings.Contains(message, "content requiring review") {
				t.Fatalf("not reported as a review finding: %v", err)
			}
			for _, want := range c.want {
				if !strings.Contains(message, want) {
					t.Errorf("error does not name %q: %v", want, err)
				}
			}
			if strings.Contains(message, c.value) {
				t.Errorf("error exposes the detected value: %v", err)
			}
		})
	}
}

// One website whose only title is its URL used to stop the whole export: the
// index listed it as a Markdown link and the final audit read "](" as part of
// the already-redacted token value.
func TestFilteredExportFinalizesTitlesEndingInRedactedURLs(t *testing.T) {
	short := record("w1", "2026-09-29T12:00:00Z")
	short["url"] = "https://example.com/magic-link?token=onetime1"
	path := strings.Repeat("p", 92)
	long := record("w2", "2026-09-29T13:00:00Z")
	long["url"] = "https://example.com/" + path + "?token=onetime2&z=1"
	f := &fakeOS{data: map[string][]map[string]any{"WEBSITES": {short, long}}}
	srv := f.server(t)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	mats, _ := SelectMaterials("WEBSITES")
	out := filepath.Join(t.TempDir(), "result")
	m, err := Export(context.Background(), client, Options{Output: out, Mode: "filtered", Timezone: "UTC", Version: "test", Format: "markdown", Materials: mats, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "complete_for_implemented_scope" {
		t.Fatalf("unexpected status %q: %+v", m.Status, m.Issues)
	}
	var markdown, all strings.Builder
	if err := filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		all.Write(b)
		if strings.HasSuffix(p, ".md") {
			markdown.Write(b)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[https://example.com/magic-link?token=REDACTED](", "[https://example.com/" + path + "…]("} {
		if !strings.Contains(markdown.String(), want) {
			t.Errorf("navigation does not list %q", want)
		}
	}
	if strings.Contains(all.String(), "onetime1") || strings.Contains(all.String(), "onetime2") {
		t.Fatal("credential value reached the archive")
	}
}

// Cutting a long title can leave a value the record scan never saw: here a
// 20-digit run becomes a Luhn-valid 19-digit run followed by an ellipsis.
func TestFilteredExportFinalizesShortenedNumericTitle(t *testing.T) {
	w := record("w1", "2026-09-29T12:00:00Z")
	w["name"] = strings.Repeat("0", 100) + " 01000000000000040000"
	w["url"] = "https://example.com/"
	f := &fakeOS{data: map[string][]map[string]any{"WEBSITES": {w}}}
	srv := f.server(t)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	mats, _ := SelectMaterials("WEBSITES")
	out := filepath.Join(t.TempDir(), "result")
	if _, err := Export(context.Background(), client, Options{Output: out, Mode: "filtered", Timezone: "UTC", Version: "test", Format: "markdown", Materials: mats, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())}); err != nil {
		t.Fatal(err)
	}
}

// Every title the renderer can receive has passed the record scan. Rendering it
// into headings, links and comma lists must not create a final-audit finding.
func FuzzGeneratedTitlesPassFinalAudit(f *testing.F) {
	for _, seed := range []string{
		"https://example.com/magic-link?token=onetime1",
		"Reset at (https://example.com/reset?token=onetime1).",
		"[reset](https://example.com/r?token=onetime1)",
		"[https://example.com/r?token=onetime1](https://example.com/r?token=onetime1)",
		"**https://example.com/r?token=onetime1**",
		"https://example.com/" + strings.Repeat("p", 92) + "?token=onetime2&z=1",
		"Reset link https://example.com/" + strings.Repeat("p", 80) + "?token=onetime1",
		"https://example.com/wiki/A_(b)?token=onetime1)",
		"http://[::1]:39300/cb?token=onetime1",
		"https://maps.example.com/embed?q=paris&key=onetime1",
		"Plain title without links",
	} {
		f.Add(seed)
	}
	s, err := NewScanner(DefaultPolicy(), f.TempDir())
	if err != nil {
		f.Fatal(err)
	}
	website, _ := materialByType("WEBSITES")
	f.Fuzz(func(t *testing.T, name string) {
		if !utf8.ValidString(name) || len(name) > 4096 {
			t.Skip()
		}
		stats := ScanResult{}
		clean, err := s.cleanString(context.Background(), "name", name, &stats)
		if err != nil || stats.Denied {
			t.Skip()
		}
		r := &run{ctx: context.Background(), opts: Options{Mode: "filtered", Scanner: s}}
		label, err := r.displayTitle(map[string]any{"name": clean}, website)
		if err != nil {
			t.Fatal(err)
		}
		// The same text can also be a multi-line summary description.
		metadata, err := r.metadataMarkdown(&DocumentMetadata{Description: clean, Sources: []string{label, "Other source"}})
		if err != nil {
			t.Fatal(err)
		}
		for _, rendered := range []string{
			"# " + md(label) + "\n",
			navigationEntry{label: label, path: "markdown/websites/w1.md", detail: "12:00:00 +00:00 · WEBSITES"}.markdown("markdown/days/2026-09-29.md"),
			metadata,
		} {
			if err := r.auditMarkdown(rendered); err != nil {
				t.Fatalf("title from %q rendered as %q failed the final audit: %v", name, rendered, err)
			}
		}
		// File names derived from the title are audited as text.
		if path := "markdown/websites/000001." + r.safeName(label, 90) + ".2026-09-29.w1.md"; r.auditText(path) != nil {
			t.Fatalf("title from %q produced file name %q that failed the final audit", name, path)
		}
	})
}
