package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pdfread "github.com/ledongthuc/pdf"
	"github.com/signintech/gopdf"
	"golang.org/x/image/font/gofont/goregular"
)

func pdfFixtureFont(t *testing.T, size float64) *gopdf.GoPdf {
	t.Helper()
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontData("body", goregular.TTF); err != nil {
		t.Fatal(err)
	}
	pdf.AddPage()
	if err := pdf.SetFont("body", "", size); err != nil {
		t.Fatal(err)
	}
	return pdf
}

func TestPDFChunkedWrappingMatchesOriginalLayout(t *testing.T) {
	for _, size := range []float64{9, 11, 22} {
		pdf := pdfFixtureFont(t, size)
		for i, value := range []string{
			"", "\n", "Short café résumé — unchanged.",
			strings.Repeat("A long paragraph with ordinary words and café accents. ", 300),
			strings.Repeat("unbroken", 500),
			strings.Repeat(" leading   spaces\tand tabs   \n", 100),
			strings.Repeat(" ", 2049),
			strings.Repeat("á", 1025) + "\n\nEnd.",
		} {
			var want, got []string
			for _, line := range strings.Split(strings.ReplaceAll(value, "\t", "    "), "\n") {
				if line == "" {
					want = append(want, "")
					continue
				}
				lines, err := pdf.SplitTextWithWordWrap(line, 507)
				if err != nil {
					t.Fatal(err)
				}
				want = append(want, lines...)
			}
			err := walkPDFLines(context.Background(), pdf, value, 507, func(line string) error { got = append(got, line); return nil })
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("fixture %d at size %.0f changed line breaks: wanted %d lines, got %d", i, size, len(want), len(got))
			}
		}
	}
}

func TestPDFLargeBlockCancellationAndInvalidText(t *testing.T) {
	pdf := pdfFixtureFont(t, 11)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitted := 0
	start := time.Now()
	err := walkPDFLines(ctx, pdf, strings.Repeat("Synthetic large paragraph. ", 500000), 507, func(line string) error {
		emitted++
		if emitted == 3 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || emitted != 3 {
		t.Fatalf("continued after cancellation: %d lines, %v", emitted, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("large-block cancellation did not return promptly")
	}
	if err := walkPDFLines(context.Background(), pdf, strings.Repeat("\x80", 1024), 507, func(string) error { return nil }); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	// Width below one glyph previously caused the library to retry indefinitely.
	if err := walkPDFLines(context.Background(), pdf, "W", 1, func(string) error { return nil }); err == nil {
		t.Fatal("oversized glyph accepted")
	}
}

func TestPDFLimitsRejectWithoutPublishingDocument(t *testing.T) {
	for _, tc := range []struct {
		name, data, flag string
		limits           PDFLimits
		meta             *DocumentMetadata
	}{
		{"input", strings.Repeat("x", (1<<20)+1), "pdf-max-input-mib", PDFLimits{InputMiB: 1}, nil},
		{"pages", "```\n" + strings.Repeat("Synthetic code line\n", 100) + "```\n", "pdf-max-pages", PDFLimits{Pages: 1}, nil},
		{"annotations", strings.Repeat("[Label](https://example.com/"+strings.Repeat("x", 5000)+")\n\n", 240), "pdf-max-output-mib", PDFLimits{OutputMiB: 1}, nil},
		{"writer", "# Small document\n", "pdf-max-output-mib", PDFLimits{OutputMiB: 1}, &DocumentMetadata{Description: strings.Repeat("synthetic description ", 60000)}},
		{"metadata", "# Small document\n", "pdf-max-input-mib", PDFLimits{InputMiB: 1}, &DocumentMetadata{Description: strings.Repeat("x", (1<<20)+1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "preserve", PDFLimits: tc.limits}}
			_, err := r.writePDF("private-title.md", "private-title.pdf", []byte(tc.data), tc.meta)
			if err == nil || !strings.Contains(err.Error(), tc.flag) || strings.Contains(err.Error(), "private-title") {
				t.Fatalf("unsafe/missing limit error: %v", err)
			}
			if _, err := os.Stat(filepath.Join(r.stage, "private-title.pdf")); !os.IsNotExist(err) {
				t.Fatal("over-budget PDF was published")
			}
		})
	}
}

func TestPDFOutputBufferLatchesCompilerErrors(t *testing.T) {
	pdf := pdfFixtureFont(t, 11)
	if err := pdf.Text("Synthetic PDF output"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &pdfOutputBuffer{ctx: ctx, limit: 32}
	_ = pdf.Write(b) // The pinned compiler itself discards some writer errors.
	if b.err == nil || b.Len() > 32 {
		t.Fatal("PDF output cap not retained")
	}
	before := bytes.Clone(b.Bytes())
	_, err := b.Write([]byte("x"))
	if err == nil || !bytes.Equal(before, b.Bytes()) {
		t.Fatal("writer resumed after failure")
	}
	cancel()
	b = &pdfOutputBuffer{ctx: ctx, limit: 1 << 20}
	_ = pdf.Write(b)
	if !errors.Is(b.err, context.Canceled) || b.Len() != 0 {
		t.Fatal("compiler swallowed cancellation")
	}
}

func TestPDFFileReadAndFontBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-input.md")
	if err := os.WriteFile(path, []byte("small"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPDFInput(context.Background(), path, 4); err == nil {
		t.Fatal("oversized input read")
	}
	if b, err := readPDFInput(context.Background(), path, 5); err != nil || string(b) != "small" {
		t.Fatalf("exact bound: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readPDFInput(ctx, path, 5); !errors.Is(err, context.Canceled) {
		t.Fatalf("input cancellation: %v", err)
	}
	if err := ValidatePDFFont(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("font cancellation: %v", err)
	}
	if err := ValidatePDFFont(context.Background(), path); err == nil || strings.Contains(err.Error(), path) {
		t.Fatalf("malformed font: %v", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate((32 << 20) + 1)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePDFFont(context.Background(), path); err == nil {
		t.Fatal("oversized font accepted")
	}
}

func TestPDFCancellationWithinSingleBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &phaseHookWriter{phase: "PDF progress: current document 25 pages", hook: cancel}
	r := &run{ctx: ctx, stage: t.TempDir(), opts: Options{Mode: "preserve", Progress: writer}}
	start := time.Now()
	_, err := r.writePDF("input.md", "output.pdf", []byte(strings.Repeat("Synthetic long paragraph for cancellation testing. ", 30000)), nil)
	if !writer.fired || !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-document cancellation not exercised: %v", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatal("cancellation during one block was too slow")
	}
	if _, err := os.Stat(filepath.Join(r.stage, "output.pdf")); !os.IsNotExist(err) {
		t.Fatal("canceled document published")
	}
}

func TestPDFLimitFailureRetainsMarkdownWithoutFinalizingArchive(t *testing.T) {
	f := &fakeOS{data: map[string][]map[string]any{"ANNOTATIONS": {{"id": "fixture", "text": "```\n" + strings.Repeat("Synthetic retained code line\n", 120) + "```\n"}}}}
	srv := f.server(t)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("ANNOTATIONS")
	out := filepath.Join(t.TempDir(), "archive")
	m, err := Export(context.Background(), client, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 1, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Format: "both", PDFLimits: PDFLimits{Pages: 1}})
	if err == nil || !strings.Contains(err.Error(), "pdf-max-pages") || m.Status != "failed" || !m.Finished.IsZero() {
		t.Fatalf("limit failure claimed completion: %s / %v", m.Status, err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("failed export finalized")
	}
	if _, err := InspectArchive(out + ".partial"); err == nil {
		t.Fatal("failed archive accepted for rebuilding")
	}
	matches, err := filepath.Glob(filepath.Join(out+".partial", "markdown", "annotations", "*.md"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("retained Markdown not found: %v", err)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil || strings.Count(string(b), "Synthetic retained code line") < 120 {
		t.Fatal("Markdown truncated with PDF")
	}
}

func BenchmarkPDFLargeCodeBlockParsing(b *testing.B) {
	for _, lines := range []int{1000, 10000} {
		b.Run(fmt.Sprint(lines), func(b *testing.B) {
			data := []byte("```\n" + strings.Repeat("synthetic code\n", lines) + "```\n")
			b.ReportAllocs()
			for b.Loop() {
				if _, err := markdownBlocks(context.Background(), data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestPackagedPDFLimitsCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the native release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeOS{data: map[string][]map[string]any{"ANNOTATIONS": {{"id": "fixture", "text": "```\n" + strings.Repeat("Synthetic retained code line\n", 120) + "```\n"}}}}
	srv := f.server(t)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := t.TempDir()
	source := filepath.Join(root, "markdown-archive")
	common := []string{"export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--materials", "ANNOTATIONS", "--metadata", "off"}
	call := func(args []string, want int) []byte {
		t.Helper()
		b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != want {
			t.Fatalf("packaged PDF limits exit %d, want %d: %s", code, want, b)
		}
		return b
	}
	failed := filepath.Join(root, "failed-export")
	b := call(append(append([]string{}, common...), "--format", "both", "--pdf-max-pages", "1", "--output", failed), 1)
	if !strings.Contains(string(b), "pdf-max-pages=1") || strings.Contains(string(b), "Export written:") {
		t.Fatal("limit failure missing or reported success")
	}
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatal("limited export finalized")
	}
	call(append(append([]string{}, common...), "--format", "markdown", "--output", source), 0)
	original, err := os.ReadFile(filepath.Join(source, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var md Manifest
	if err := json.Unmarshal(original, &md); err != nil || md.PDFLimits != nil {
		t.Fatal("Markdown archive claimed PDF rendering budgets")
	}
	// Close the fixture now: both subsequent attempts must be entirely offline.
	srv.Close()
	failed = filepath.Join(root, "failed-rebuild")
	call([]string{"rebuild", "--source", source, "--output", failed, "--format", "both", "--pdf-max-pages", "1", "--yes"}, 1)
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatal("limited rebuild finalized")
	}
	out := filepath.Join(root, "pdf-archive")
	call([]string{"rebuild", "--source", source, "--output", out, "--format", "both", "--pdf-max-pages", "20", "--pdf-max-input-mib", "3", "--pdf-max-output-mib", "2", "--yes"}, 0)
	m, err := InspectArchive(out)
	if err != nil || m.PDFLimits == nil || *m.PDFLimits != (PDFLimits{Pages: 20, InputMiB: 3, OutputMiB: 2}) {
		t.Fatalf("effective PDF budgets were not retained: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(source, "manifest.json"))
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("PDF retry changed its source archive")
	}
	t.Log("actual CLI limits fail without finalizing; offline PDF retry succeeds with recorded budgets and unchanged source")
}

func TestPDFLongDocumentLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "layout")
	if path := os.Getenv("PIECES_EXPORT_PDF_LAYOUT_OUTPUT"); path != "" {
		root = path
	}
	var md strings.Builder
	md.WriteString("# Long-document layout\n\n## Paragraph wrapping\n\n")
	md.WriteString(strings.Repeat("Portable archives retain complete paragraphs across pages. Café résumé accents stay readable. ", 120))
	md.WriteString("ENDING_PARAGRAPH_MARKER\n\n## Long code block\n\n```go\n")
	for i := 1; i <= 80; i++ {
		fmt.Fprintf(&md, "\tline_%03d := process(\"synthetic portable archive\")\n", i)
	}
	md.WriteString("FINAL_CODE_LINE\n```\n\n## Table fallback\n\n| Item | Status |\n| --- | --- |\n")
	for i := 1; i <= 25; i++ {
		fmt.Fprintf(&md, "| Item %02d | Retained and checked |\n", i)
	}
	md.WriteString("| FINAL_TABLE_ROW | Complete |\n\n## Navigation\n\n[Companion document](companion.md)\n\nEnd of complete document.\n")
	for path, data := range map[string]string{"index.md": md.String(), "companion.md": "# Companion document\n\n[Return to long document](index.md)\n"} {
		if err := writeFile(filepath.Join(root, path), []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	r := &run{ctx: context.Background(), stage: root, opts: Options{Mode: "preserve"}}
	if err := r.renderPDFs(); err != nil {
		t.Fatal(err)
	}
	f, pdf, err := pdfread.Open(filepath.Join(root, "index.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if pdf.NumPage() < 4 {
		t.Fatal("fixture failed to span pages")
	}
	var text strings.Builder
	for i := 1; i <= pdf.NumPage(); i++ {
		s, err := pdf.Page(i).GetPlainText(nil)
		if err != nil {
			t.Fatal(err)
		}
		text.WriteString(s)
	}
	for _, marker := range []string{"ENDING_PARAGRAPH_MARKER", "FINAL_CODE_LINE", "FINAL_TABLE_ROW", "End of complete document."} {
		if !strings.Contains(text.String(), marker) {
			t.Fatalf("lost content: %s", marker)
		}
	}
	if r.manifest.PDFLimits == nil || *r.manifest.PDFLimits != DefaultPDFLimits() {
		t.Fatal("default PDF budgets missing")
	}
}
