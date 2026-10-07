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

// A title is the first line of its field. Carriage returns end a line too;
// otherwise title escaping turns "\r" into a space and can join two digit
// groups into a card-shaped number the record scan never saw.
func TestTitleEndsAtAnyLineBreak(t *testing.T) {
	website, _ := materialByType("WEBSITES")
	for in, want := range map[string]string{
		"2000000\r0000030":    "2000000",
		"first\r\nsecond":     "first",
		"first\nsecond":       "first",
		"single line title":   "single line title",
		"trailing break\n":    "trailing break",
		"carriage only\rrest": "carriage only",
	} {
		if got := title(map[string]any{"name": in}, website); got != want {
			t.Errorf("title(%q) = %q, want %q", in, got, want)
		}
	}
}

// Summary descriptions are rendered on one line. Flattening can join digit
// groups across a line break; the flattened text is scanned again and the
// export still finalizes. The canonical JSON keeps the original text.
func TestFilteredExportFinalizesFlattenedDescription(t *testing.T) {
	f := summaryScopeFixture()
	description := record("description", "2026-09-29T12:00:00Z")
	description["type"], description["text"], description["summaries"] = "DESCRIPTION", "Order 2000000\n0000030 total", refs("summary")
	f.data["ANNOTATIONS"] = append(f.data["ANNOTATIONS"], description)
	f.data["WORKSTREAM_SUMMARIES"][0]["annotations"] = refs("body", "description")
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	sel, _ := SelectScope("summaries", "")
	out := filepath.Join(t.TempDir(), "export")
	m, err := Export(context.Background(), c, Options{Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, Output: out, Mode: "filtered", Format: "markdown", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Metadata: "off", PeopleMode: "profiles"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "complete_for_implemented_scope" {
		t.Fatalf("unexpected status %q: %+v", m.Status, m.Issues)
	}
	markdown, canonical := readTree(t, out, ".md"), readTree(t, out, ".json")
	if !strings.Contains(markdown, "Description: "+md("Order [REDACTED:PAYMENT_CARD] total")) {
		t.Fatal("flattened description was not scanned again")
	}
	if !strings.Contains(canonical, `Order 2000000\n0000030 total`) {
		t.Fatal("canonical description text changed")
	}
}

// Obsidian strips Markdown syntax from titles and flattens descriptions into
// one-line previews. Both can create digit runs the record scan never saw.
func TestObsidianRescansStrippedTitlesAndFlattenedPreviews(t *testing.T) {
	f := summaryScopeFixture()
	f.data["WORKSTREAM_SUMMARIES"][0]["name"] = "Visa 4111 *1111* 1111 1111"
	description := record("description", "2026-09-29T12:00:00Z")
	description["type"], description["text"], description["summaries"] = "DESCRIPTION", "Order 2000000\n0000030 total", refs("summary")
	f.data["ANNOTATIONS"] = append(f.data["ANNOTATIONS"], description)
	f.data["WORKSTREAM_SUMMARIES"][0]["annotations"] = refs("body", "description")
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	sel, _ := SelectScope("summaries", "")
	out := filepath.Join(t.TempDir(), "export")
	m, err := Export(context.Background(), c, Options{Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, Output: out, Mode: "filtered", Format: "obsidian", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Metadata: "off", PeopleMode: "profiles"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "complete_for_implemented_scope" {
		t.Fatalf("unexpected status %q: %+v", m.Status, m.Issues)
	}
	vault := readTree(t, filepath.Join(out, "vault"), ".md")
	for _, leaked := range []string{"4111 1111 1111 1111", "2000000 0000030"} {
		if strings.Contains(vault, leaked) {
			t.Errorf("vault shows card-shaped text %q created by display formatting", leaked)
		}
	}
	if !strings.Contains(vault, "Visa [REDACTED:PAYMENT_CARD]") && !strings.Contains(vault, md("Visa [REDACTED:PAYMENT_CARD]")) {
		t.Error("stripped title was not scanned again")
	}
}

// File names are sanitized from titles and audited as text. Dropping a
// title's own underscores can put a run of digits at a word boundary that the
// record scan never saw.
func TestFileNamesFromTitlesAreScannedAgain(t *testing.T) {
	r := &run{ctx: context.Background(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
	for in, want := range map[string]string{
		"8700000030000000_": "REDACTED_PAYMENT_CARD",
		"Quarterly plan_":   "Quarterly_plan",
		"plain":             "plain",
	} {
		if got := r.safeName(in, 90); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
	preserve := &run{ctx: context.Background(), opts: Options{Mode: "preserve"}}
	if got := preserve.safeName("8700000030000000_", 90); got != "8700000030000000" {
		t.Errorf("preserve mode changed a file name: %q", got)
	}
	w := record("w1", "2026-09-29T12:00:00Z")
	w["name"], w["url"] = "8700000030000000_", "https://example.com/"
	f := &fakeOS{data: map[string][]map[string]any{"WEBSITES": {w}}}
	srv := f.server(t)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	mats, _ := SelectMaterials("WEBSITES")
	if _, err := Export(context.Background(), client, Options{Output: filepath.Join(t.TempDir(), "result"), Mode: "filtered", Timezone: "UTC", Version: "test", Format: "markdown", Materials: mats, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())}); err != nil {
		t.Fatal(err)
	}
}

// Obsidian derives readable note names from titles and shows underscores as
// spaces, which the payment card pattern accepts between digit groups.
func TestObsidianReadableNoteNamesAreScannedAgain(t *testing.T) {
	f := summaryScopeFixture()
	f.data["WORKSTREAM_SUMMARIES"][0]["name"] = "Invoice 4111.1111.1111.1111"
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	sel, _ := SelectScope("summaries", "")
	out := filepath.Join(t.TempDir(), "export")
	if _, err := Export(context.Background(), c, Options{Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, Output: out, Mode: "filtered", Format: "obsidian", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Metadata: "off", PeopleMode: "profiles"}); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(filepath.Join(out, "vault"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && strings.Contains(filepath.Base(path), "4111 1111 1111 1111") {
			t.Errorf("vault note name shows a card-shaped number: %s", filepath.Base(path))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// Title escaping writes "<" and ">" as "&lt;" and "&gt;". The audit must read
// them back, or "http://&lt;?token" gains a host and a credential parameter
// that the record scan, which stopped the URL at "<", never saw.
func TestFinalAuditReadsEscapedAngleBracketsAsRendered(t *testing.T) {
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
	if err := r.auditOutputFile(auditFixtureFile(t, r, "angle.md", "# http://&lt;?token\n")); err != nil {
		t.Fatalf("escaped angle bracket failed the final audit: %v", err)
	}
	if err := r.auditOutputFile(auditFixtureFile(t, r, "leak.md", "# https://example.com/r?token=onetime1&gt;\n")); err == nil {
		t.Fatal("credential before an escaped angle bracket passed the final audit")
	}
}

// File names are audited too. A byte cut inside a run of digits can leave a
// 13 to 19 digit run that the record scan never saw.
func TestSafeTitleDoesNotCutInsideDigitRun(t *testing.T) {
	if got, want := safeTitle("01000000000000040000 more", 19), "untitled"; got != want {
		t.Errorf("leading digit run cut to a card-shaped length: got %q, want %q", got, want)
	}
	if got, want := safeTitle("Order 01000000000000040000 more", 25), "Order"; got != want {
		t.Errorf("digit run cut inside: got %q, want %q", got, want)
	}
	// A cut that ends a digit run exactly, or lands outside one, is unchanged.
	if got, want := safeTitle("Order 0100000000000004 more", 22), "Order_0100000000000004"; got != want {
		t.Errorf("complete digit run was dropped: got %q, want %q", got, want)
	}
	if got, want := safeTitle("Order 12345 total and more", 15), "Order_12345_tot"; got != want {
		t.Errorf("cut outside a digit run moved: got %q, want %q", got, want)
	}
}

func readTree(t *testing.T, root, ext string) string {
	t.Helper()
	var b strings.Builder
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ext {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b.Write(data)
		b.WriteByte('\n')
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
