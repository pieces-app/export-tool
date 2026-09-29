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

func TestReadableSummaryGraphPDFAndMetadata(t *testing.T) {
	a := record("00000000-0000-4000-8000-000000000001", "2026-09-28T14:00:00Z")
	a["name"] = "First / Summary"
	b := record("00000000-0000-4000-8000-000000000002", "2026-09-29T14:00:00Z")
	b["name"] = "Newer résumé"
	for _, v := range []map[string]any{a, b} {
		v["parentHierarchicalType"] = "TEMPORAL_DAY_HIERARCHICAL_SUMMARY"
		v["tags"] = refs("tag")
		v["persons"] = refs("person")
		v["sources"] = refs("source")
		v["websites"] = refs("website")
	}
	annotation := record("body", "2026-09-29T14:00:00Z")
	annotation["type"] = "SUMMARY"
	annotation["text"] = "# A readable heading\n\nA paragraph with [Alex](pieces://persons/person).\n\n- First task\n- Second task\n\n```go\nfmt.Println(\"portable export\")\n```\n\n| Item | State |\n| --- | --- |\n| Links | Ready |\n"
	description := record("description", "2026-09-29T14:00:00Z")
	description["type"] = "WORKSTREAM_SUMMARY_DESCRIPTION"
	description["text"] = "Reviewed the export graph and document layout."
	b["annotations"] = refs("body", "description")
	a["annotations"], a["pipelines"], b["pipelines"] = refs(), refs(), refs()
	tag := record("tag", "")
	tag["text"] = "Export planning"
	person := record("person", "")
	person["name"] = "Alex"
	person["summaries"] = refs()
	persona := record("persona", "2026-09-29T14:00:00Z")
	persona["type"] = "HIERARCHICAL_PROFILE_SUMMARY"
	persona["person"] = map[string]any{"id": "person"}
	persona["text"] = "# Retained persona\n\nAlex works on portable exports and document links.\n\n- Reviews the migration checklist.\n- Tests navigation after moving the archive.\n"
	source := record("source", "")
	source["readable"] = "Visual Studio Code"
	website := record("website", "")
	website["url"] = "https://EXAMPLE.com/path?view=normal"
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {a, b}, "TAGS": {tag}, "PERSONS": {person}, "WORKSTREAM_PATTERN_ENGINE_SOURCES": {source}, "WEBSITES": {website}, "ANNOTATIONS": {annotation, description, persona}}}
	srv := f.server(t)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("WORKSTREAM_SUMMARIES,TAGS,PERSONS,WORKSTREAM_PATTERN_ENGINE_SOURCES,WEBSITES,ANNOTATIONS")
	out := filepath.Join(t.TempDir(), "export")
	if path := os.Getenv("PIECES_EXPORT_FIXTURE_OUTPUT"); path != "" {
		out = path
	}
	manifest, err := Export(context.Background(), client, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Format: "both", Metadata: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "complete_for_implemented_scope" {
		t.Fatalf("fixture partial: %+v", manifest.Issues)
	}
	paths, err := filepath.Glob(filepath.Join(out, "workstream_summaries/timeline/000000.*.md"))
	if err != nil || len(paths) != 2 {
		t.Fatalf("missing ranked summary and relationship sibling: %v", paths)
	}
	var main string
	for _, path := range paths {
		if !strings.Contains(path, "relationships_graph") {
			main = path
		}
	}
	if !strings.Contains(filepath.Base(main), "Newer_résumé.2026-09-29.00000000-0000-4000-8000-000000000002") {
		t.Fatal("unexpected readable filename")
	}
	data, _ := os.ReadFile(main)
	for _, d := range dimensions {
		if !strings.Contains(string(data), "#### Related Summaries by "+d) {
			t.Fatal("missing relationship dimension")
		}
	}
	var metadata DocumentMetadata
	encoded, err := os.ReadFile(strings.TrimSuffix(main, ".md") + ".metadata.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Description != "Reviewed the export graph and document layout." || len(metadata.Websites) != 1 || metadata.Websites[0] != "example.com" {
		t.Fatalf("metadata missing or unnormalized")
	}
	if _, err := os.Stat(filepath.Join(out, "index.pdf")); err != nil {
		t.Fatal(err)
	}
	// Moving the whole folder must not break generated Markdown/PDF links.
	if os.Getenv("PIECES_EXPORT_FIXTURE_OUTPUT") == "" {
		moved := out + "-moved"
		if err := os.Rename(out, moved); err != nil {
			t.Fatal(err)
		}
		run := &run{ctx: context.Background(), stage: moved, opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
		if err := run.validateMarkdownLinks(); err != nil {
			t.Fatal(err)
		}
		if err := run.auditOutput(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLargeSharedGroupUsesCompleteIndex(t *testing.T) {
	r := &run{stage: t.TempDir(), meta: map[string]*Meta{}}
	g := &summaryGraph{Written: map[string]bool{}, Keys: map[string]map[string]map[string]string{}, Index: map[string]map[string][]*Meta{}}
	for _, d := range dimensions {
		g.Index[d] = map[string][]*Meta{}
	}
	for i := 0; i < 150; i++ {
		m := &Meta{Key: fmt.Sprint(i), ID: fmt.Sprint(i), State: "included", Title: fmt.Sprintf("Summary %d", i), Path: fmt.Sprintf("markdown/summaries/%06d.md", i)}
		r.meta[m.Key] = m
		g.Index["Tags"]["common"] = append(g.Index["Tags"]["common"], m)
	}
	m := r.meta["0"]
	g.Keys[m.Key] = map[string]map[string]string{"Tags": {"common": "Shared topic"}}
	rendered, err := g.render(r, m, m.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(rendered, "shared: Shared topic") != 50 || !strings.Contains(rendered, "all 150 summaries") {
		t.Fatal("related list limit lost full-graph navigation")
	}
	indexes, err := filepath.Glob(filepath.Join(r.stage, "markdown/relationships/tags/*.md"))
	if err != nil || len(indexes) != 1 {
		t.Fatal("missing shared-group index")
	}
	data, _ := os.ReadFile(indexes[0])
	if strings.Count(string(data), "- [Summary") != 150 {
		t.Fatal("full index silently truncated")
	}
}

func TestPDFRejectsUnfilteredSecret(t *testing.T) {
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
	if _, err := r.writePDF("input.md", "output.pdf", []byte("# Secret fixture\n\n"+fakeSecret()), nil); err == nil {
		t.Fatal("semantic PDF audit accepted a leaked secret")
	}
}

func TestFilenameOrderingAndBounds(t *testing.T) {
	for _, device := range []string{"CON", "prn", "AUX", "nul", "COM1", "Lpt9", "COM¹"} {
		if !strings.HasPrefix(safeTitle(device, 48), "_") {
			t.Fatalf("Windows device name accepted for a folder: %s", device)
		}
	}
	r := &run{opts: Options{Timezone: "UTC", Naming: "readable"}, meta: map[string]*Meta{}}
	for i := 0; i < 125; i++ {
		id := strings.Repeat("x", i+1)
		r.meta[id] = &Meta{ID: id, Type: "WORKSTREAM_SUMMARIES", State: "included", Title: strings.Repeat("Résumé / CON:*?.", 50), Created: time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC).Format(time.RFC3339)}
	}
	r.assignPaths()
	for _, m := range r.meta {
		if len(filepath.Base(relationshipPath(m.Path))) > 240 || strings.ContainsAny(filepath.Base(m.Path), "<>:\"/\\|?*") {
			t.Fatal("unsafe or long filename")
		}
	}
}
