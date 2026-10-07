package exporter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestObsidianSummaryActivityRangeAndFallback(t *testing.T) {
	zone, _ := time.LoadLocation("America/New_York")
	parse := func(v string) time.Time {
		at, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t.Fatal(err)
		}
		return at
	}
	rangeRecord := &compactRecord{kind: "range", entry: TimelineEntry{ID: "range"}, rangeFrom: parse("2026-09-30T13:00:00Z"), rangeTo: parse("2026-09-30T14:00:00Z")}
	summary := &compactRecord{kind: "summary", entry: TimelineEntry{Created: "2026-10-01T01:00:00Z"}, description: "A retained description.", edges: []compactEdge{{target: rangeRecord, relation: "ranges"}}}
	entry := compactSummaryEntry(summary, zone)
	if entry.detail != "Activity: September 30, 2026 · 9:00 AM – 10:00 AM EDT (UTC-04:00)" || entry.dateFrom.Day() != 30 || entry.preview != summary.description {
		t.Fatal(entry)
	}
	// A DST transition must show both offsets, even when the wall clock repeats.
	rangeRecord.rangeFrom, rangeRecord.rangeTo = parse("2026-11-01T05:30:00Z"), parse("2026-11-01T06:30:00Z")
	entry = compactSummaryEntry(summary, zone)
	if !strings.Contains(entry.detail, "EDT (UTC-04:00)") || !strings.Contains(entry.detail, "EST (UTC-05:00)") {
		t.Fatal(entry.detail)
	}
	// Reversed or missing bounds cannot turn creation time into an activity range.
	rangeRecord.rangeTo = rangeRecord.rangeFrom.Add(-time.Hour)
	entry = compactSummaryEntry(summary, zone)
	if !strings.HasPrefix(entry.detail, "Created: September 30, 2026") || !strings.Contains(entry.detail, "Activity range unavailable") {
		t.Fatal(entry.detail)
	}
	rangeRecord.rangeTo = rangeRecord.rangeFrom.Add(time.Hour)
	other := &compactRecord{kind: "range", entry: TimelineEntry{ID: "other"}, rangeFrom: parse("2026-11-02T13:00:00Z"), rangeTo: parse("2026-11-02T14:00:00Z")}
	summary.edges = append(summary.edges, compactEdge{target: other, relation: "ranges"})
	entry = compactSummaryEntry(summary, zone)
	if !strings.HasPrefix(entry.detail, "Activity span:") || !strings.Contains(entry.detail, "2 recorded ranges") {
		t.Fatal(entry.detail)
	}
}

func TestObsidianSummaryPreviewsPaginateWithDatesAndSafeText(t *testing.T) {
	r := &run{ctx: context.Background(), stage: t.TempDir()}
	entries := []navigationEntry{}
	start := time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)
	for i := 0; i < 120; i++ {
		name := fmt.Sprintf("notes/summary-%03d.md", i)
		if err := r.writeFile(filepath.Join(r.stage, name), []byte("# Memory\n")); err != nil {
			t.Fatal(err)
		}
		at := start.Add(-time.Duration(i) * time.Hour)
		entries = append(entries, navigationEntry{label: fmt.Sprintf("Memory %03d", i), path: name, detail: "Activity: September 30, 2026 · 9:00 AM – 10:00 AM EDT", preview: "Approved description with [[literal]] and [no external link](pieces://persons/missing).", dateFrom: at, dateTo: at})
	}
	if err := r.writeNavigationIndex("index.md", "# Timeline\n\n", entries); err != nil {
		t.Fatal(err)
	}
	index, _ := os.ReadFile(filepath.Join(r.stage, "index.md"))
	if !strings.Contains(string(index), "Entries 1–50") || !strings.Contains(string(index), "Summary dates: September 28, 2026 – September 30, 2026") {
		t.Fatal("page chooser lacks dates", string(index))
	}
	pages, _ := filepath.Glob(filepath.Join(r.stage, "index.pages/page-*.md"))
	if len(pages) != 3 {
		t.Fatal("preview pages should contain at most 50 summaries", len(pages))
	}
	count := 0
	for _, page := range pages {
		b, _ := os.ReadFile(page)
		s := string(b)
		if len(b) > navigationPageBytes || strings.Contains(s, "[[literal]]") {
			t.Fatal("unsafe or oversized preview")
		}
		if !strings.Contains(s, "Summary dates:") || !strings.Contains(s, "\n\n  Activity:") || !strings.Contains(s, "\n\n  Approved description") {
			t.Fatal("preview paragraphs/date label missing")
		}
		count += strings.Count(s, "- [Memory ")
		if links := acceptanceMarkdownLinks(b); len(links) > 52 {
			t.Fatal("description introduced new active links")
		}
	}
	if count != 120 {
		t.Fatal("lost entries", count)
	}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
}

func TestObsidianSummaryPreviewDescriptionAndUndatedRangeIntegration(t *testing.T) {
	f := summaryScopeFixture()
	description := record("description", "")
	description["type"], description["text"] = "DESCRIPTION", "## Approved preview\n\nUseful **description** with [a person](pieces://persons/person). "+strings.Repeat("More context. ", 80)
	f.data["ANNOTATIONS"] = append(f.data["ANNOTATIONS"], description)
	f.data["WORKSTREAM_SUMMARIES"][0]["annotations"] = refs("body", "description")
	span := record("span", "")
	span["from"], span["to"] = map[string]any{"value": "2026-09-28T13:00:00Z"}, map[string]any{"value": "2026-09-28T14:00:00Z"}
	f.data["RANGES"] = []map[string]any{span}
	f.data["WORKSTREAM_SUMMARIES"][0]["ranges"] = refs("span")
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	sel, _ := SelectScope("summaries", "")
	out := filepath.Join(t.TempDir(), "export")
	_, err := Export(context.Background(), c, Options{Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, Output: out, Mode: "filtered", Format: "obsidian", Timezone: "America/New_York", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Metadata: "off", PeopleMode: "profiles"})
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(out, "vault/workstream_summaries/timeline/index.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(index)
	if !strings.Contains(s, "Activity: September 28, 2026 · 9:00 AM – 10:00 AM EDT") || !strings.Contains(s, "Approved preview Useful description with a person.") || !strings.Contains(s, "…") {
		t.Fatal("description/range missing", s)
	}
	if strings.Contains(s, "pieces://") || strings.Contains(s, "Retained persona history") || len(s) > 2000 {
		t.Fatal("preview copied links/profile or unbounded body")
	}
	if _, err := inspectCompactBodies(out); err != nil {
		t.Fatal(err)
	}
}
