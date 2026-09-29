package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func numericPrefixUUID(t *testing.T) string {
	t.Helper()
	for digit := 0; digit < 10; digit++ {
		id := fmt.Sprintf("41111111-1111-400%d-a000-000000000000", digit)
		for _, match := range cardPattern.FindAllString(id, -1) {
			if validCard(match) {
				return id
			}
		}
	}
	t.Fatal("synthetic UUID did not reproduce numeric-prefix false positive")
	return ""
}

func TestUUIDNumericPrefixIsNotAPaymentCard(t *testing.T) {
	id := numericPrefixUUID(t)
	s := scanner(t, DefaultPolicy())
	for _, text := range []string{id, strings.ToUpper(id), "prefix " + id + " suffix", "000000.Title.2026-09-29." + id + ".md", "https://example.test/records/" + id} {
		stats := ScanResult{}
		got, err := s.cleanString(context.Background(), "", text, &stats)
		if err != nil || got != text || stats.Redactions != 0 {
			t.Fatal("UUID mistaken for payment card")
		}
	}
	for _, value := range []string{"4111111111111111", "4111-1111-1111-1111", "4111 1111 1111 1111"} {
		stats := ScanResult{}
		text := id + "; card: " + value + "; another id: " + id
		got, err := s.cleanString(context.Background(), "", text, &stats)
		if err != nil || strings.Contains(got, value) || strings.Count(got, id) != 2 || stats.Redactions != 1 {
			t.Fatal("UUID exemption hid an adjacent card")
		}
	}
	v, stats, err := s.Sanitize(context.Background(), map[string]any{"id": id, "references": map[string]any{id: 0}, "number": json.Number("4111111111111111")})
	if err != nil || fieldString(v, "id") != id || stats.Redactions != 1 {
		t.Fatal("typed UUID/key or numeric-card handling regressed")
	}
	if _, ok := v["number"].(json.Number); ok {
		t.Fatal("numeric card survived")
	}
	s.remember(id)
	known := ScanResult{}
	clean, err := s.cleanString(context.Background(), "", id, &known)
	if err != nil || clean == id || known.Redactions == 0 {
		t.Fatal("UUID exemption hid a known credential")
	}
}

func TestUUIDSummarySurvivesRecordAndFinalOutputAudit(t *testing.T) {
	id := numericPrefixUUID(t)
	summary := record(id, "2026-09-29T00:00:00Z")
	summary["name"] = "UUID coverage"
	summary["annotations"], summary["persons"], summary["pipelines"] = refs("body"), refs(), refs()
	body := record("body", "2026-09-29T00:00:00Z")
	body["text"], body["type"], body["summaries"] = "Retained UUID narrative.", "SUMMARY", refs(id)
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {summary}, "ANNOTATIONS": {body}}}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("WORKSTREAM_SUMMARIES,ANNOTATIONS")
	out := filepath.Join(t.TempDir(), "export")
	manifest, err := Export(context.Background(), c, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Metadata: "off", Format: "both"})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "complete_for_implemented_scope" {
		t.Fatalf("UUID archive became partial: %+v", manifest.Issues)
	}
	for _, coverage := range manifest.Coverage {
		if coverage.Included != 1 || coverage.Withheld != 0 {
			t.Fatal("UUID record withheld")
		}
	}
	files, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/timeline/*"+id+".md"))
	if len(files) != 1 {
		t.Fatal("canonical UUID filename lost")
	}
	data, _ := os.ReadFile(files[0])
	if !strings.Contains(string(data), "Retained UUID narrative.") {
		t.Fatal("UUID summary attachment lost")
	}
}
