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
