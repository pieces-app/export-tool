package exporter

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pdfread "github.com/ledongthuc/pdf"
)

// Check the actual PDF click rectangles, not only that their destinations exist.
// A missing/filtered link becomes plain text before PDF rendering; that text
// must not inherit another link's destination merely by sharing a paragraph.
func TestPDFClickableAreasExcludeUnlinkedText(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		direct     bool
	}{
		{"paragraph", "Missing record beside [Target](target.md).", false},
		{"list_context", "- Missing record beside [Target](target.md).", false},
		{"heading_context", "## Missing record beside [Target](target.md)", false},
		{"table", "| Context | Link |\n| --- | --- |\n| Missing record | [Target](target.md) |", false},
		{"standalone", "[Target](target.md)", true},
		{"list_link_only", "- [Target](target.md)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for path, data := range map[string]string{"index.md": tc.text, "target.md": "# Target"} {
				if err := writeFile(filepath.Join(root, path), []byte(data)); err != nil {
					t.Fatal(err)
				}
			}
			r := &run{ctx: context.Background(), stage: root, opts: Options{Mode: "preserve"}}
			if err := r.renderPDFs(); err != nil {
				t.Fatal(err)
			}
			f, doc, err := pdfread.Open(filepath.Join(root, "index.pdf"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			page := doc.Page(1)
			annotations := page.V.Key("Annots")
			if annotations.Len() != 1 {
				t.Fatalf("expected one navigation action, got %d", annotations.Len())
			}
			a := annotations.Index(0)
			if a.Key("A").Key("F").Key("UF").Text() != "pdf/target.pdf" {
				t.Fatal("link destination changed")
			}
			rect := a.Key("Rect")
			x0, y0, x1, y1 := rect.Index(0).Float64(), rect.Index(1).Float64(), rect.Index(2).Float64(), rect.Index(3).Float64()
			x0, x1 = min(x0, x1), max(x0, x1)
			y0, y1 = min(y0, y1), max(y0, y1)
			var label strings.Builder
			// The reader's GetTextByRow does not handle the writer's TD operator.
			// Read positioned text from the emitted content stream instead, using
			// the PDF's own font encoding. This is specific to this writer fixture.
			var x, y float64
			var encoding pdfread.TextEncoding
			show := func(raw string) {
				if encoding == nil {
					t.Fatal("text drawn without a font")
				}
				if x >= x0 && x <= x1 && y >= y0 && y <= y1 {
					label.WriteString(encoding.Decode(raw))
				}
			}
			pdfread.Interpret(page.V.Key("Contents"), func(stack *pdfread.Stack, op string) {
				args := make([]pdfread.Value, stack.Len())
				for i := len(args) - 1; i >= 0; i-- {
					args[i] = stack.Pop()
				}
				switch op {
				case "BT":
					x, y = 0, 0
				case "TD", "Td":
					x, y = x+args[0].Float64(), y+args[1].Float64()
				case "Tf":
					font := page.Font(args[0].Name())
					encoding = font.Encoder()
				case "TJ":
					for i := 0; i < args[0].Len(); i++ {
						if value := args[0].Index(i); value.Kind() == pdfread.String {
							show(value.RawString())
						}
					}
				case "Tj":
					show(args[0].RawString())
				case "Tm", "cm", "T*", "'", "\"":
					t.Fatalf("click fixture needs support for new position operator %s", op)
				}
			})
			want := "Open: Target"
			if tc.direct {
				want = "Target"
			}
			got := strings.TrimSpace(strings.TrimPrefix(label.String(), "• "))
			if got != want {
				t.Fatalf("clickable text %q, expected only %q", got, want)
			}
			text, err := page.GetPlainText(nil)
			if err != nil || !tc.direct && !strings.Contains(text, "Missing record") {
				t.Fatal("unlinked context disappeared")
			}
		})
	}
}

func TestPDFLocalNavigationAndRelocation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "original")
	accent := "docs/café (review)#%.md"
	documents := map[string]string{
		"index.md":   "# Navigation\n\n[Accented summary](" + uriPath(accent) + "#heading)\n\n[Unsupported filename](docs/附件.md)\n\n[Manifest](manifest.json)\n\n[Website](https://example.com/approved)\n\n[Manifest](manifest.json) · [Accented summary](" + uriPath(accent) + ")\n",
		accent:       "# Accented summary\n\n[Return to export index](../index.md)\n",
		"docs/附件.md": "# Plain text fallback\n",
	}
	for path, data := range documents {
		if err := writeFile(filepath.Join(root, path), []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	r := &run{ctx: context.Background(), stage: root, opts: Options{Mode: "unfiltered"}}
	if err := r.renderPDFs(); err != nil {
		t.Fatal(err)
	}
	if len(r.manifest.Issues) != 1 || r.manifest.Issues[0].Code != "unsupported_pdf_link_filename" {
		t.Fatal("unsupported navigation was not reported")
	}
	f, pdf, err := pdfread.Open(filepath.Join(root, "index.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	annotations := pdf.Page(1).V.Key("Annots")
	if annotations.Len() != 3 {
		t.Fatalf("expected two PDF links and one web link only, got %d", annotations.Len())
	}
	local := annotations.Index(0).Key("A")
	if local.Key("S").Name() != "GoToR" || local.Key("F").Key("UF").Text() != pdfPath(accent) || local.Key("D").Index(0).Int64() != 0 {
		t.Fatal("PDF did not navigate to the mirrored document's first page")
	}
	if annotations.Index(1).Key("A").Key("URI").Text() != "https://example.com/approved" {
		t.Fatal("external link changed")
	}
	text, err := pdf.Page(1).GetPlainText(nil)
	f.Close()
	if err != nil || !strings.Contains(text, "Unsupported filename") || !strings.Contains(text, "Manifest") || !strings.Contains(text, "Open: Accented summary") {
		t.Fatal("unlinked labels disappeared")
	}
	moved := filepath.Join(filepath.Dir(root), "moved archive")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	r.stage = moved
	for path := range documents {
		if err := r.auditPDF(filepath.Join(moved, pdfPath(path))); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(moved, pdfPath(accent))); err != nil {
		t.Fatal(err)
	}
	if err := r.auditPDF(filepath.Join(moved, "index.pdf")); err == nil {
		t.Fatal("missing PDF destination passed after relocation")
	}
}

func TestPDFRejectsUnsafeFileActions(t *testing.T) {
	for _, tc := range []struct{ name, destination string }{
		{"outside", "../../../outside.pdf"},
		{"absolute", "/tmp/outside.pdf"},
		{"windows", "C:/outside.pdf"},
		{"backslash", `..\outside.pdf`},
		{"non_pdf", "script.exe"},
		{"launch", "target.pdf"},
		{"mismatched_unicode", "target.pdf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "unfiltered"}}
			// Create a genuine writer-produced PDF, then replace its action with
			// the tested variant while retaining exact object/xref offsets.
			if err := writeFile(filepath.Join(r.stage, "target.md"), []byte("# Target")); err != nil {
				t.Fatal(err)
			}
			if _, err := r.writePDF("input.md", "source.pdf", []byte("[Target](target.md)"), nil); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(r.stage, "source.pdf"))
			if err != nil {
				t.Fatal(err)
			}
			start := bytes.Index(data, []byte("/A << /S /GoToR"))
			end := start + bytes.Index(data[start:], []byte("\nendobj"))
			action, ok := pdfFileAction(tc.destination)
			if !ok || start < 0 || end < start {
				t.Fatal("invalid test fixture")
			}
			if tc.name == "launch" {
				action = strings.Replace(action, "/GoToR", "/Launch", 1)
			}
			if tc.name == "mismatched_unicode" {
				action = strings.Replace(action, "/UF <FEFF0074", "/UF <FEFF0075", 1)
			}
			if len(action) > end-start {
				t.Fatal("test action exceeded reserved slot")
			}
			copy(data[start:end], action+strings.Repeat(" ", end-start-len(action)))
			if err := os.WriteFile(filepath.Join(r.stage, "source.pdf"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := r.auditPDFFile(filepath.Join(r.stage, "source.pdf"), false, false); err == nil {
				t.Fatal("unsafe action passed validation")
			}
		})
	}
}

func TestPDFActionSlotsFailClosed(t *testing.T) {
	slots := pdfActionSlots{}
	action, _ := pdfFileAction("target.pdf")
	slots.reserve(action)
	var original string
	for key := range slots {
		original = key
	}
	for _, data := range [][]byte{nil, []byte(original + original), []byte(strings.Replace(original, "slot:0:", "slot:9:", 1))} {
		if _, err := slots.apply(data); err == nil {
			t.Fatal("missing, duplicate, or unknown reserved action passed")
		}
	}
	data := []byte("object prefix " + original + " suffix xref offsets")
	result, err := slots.apply(data)
	if err != nil || len(result) != len(data) || !bytes.HasSuffix(result, []byte(" suffix xref offsets")) || !bytes.Contains(result, []byte(action)) {
		t.Fatal("action replacement changed indexed object offsets")
	}
}
