package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func putGraphFixtureRecord(t testing.TB, r *run, typ, id, annotationType string, v map[string]any) *Meta {
	t.Helper()
	m := &Meta{Type: typ, ID: id, Key: typ + "\x00" + id, State: "included", Title: id, DataPath: fmt.Sprintf("%s-%s.json", typ, id), Path: fmt.Sprintf("markdown/%s/%s.md", typ, id), Created: "2026-09-30T00:00:00Z", AnnotationType: annotationType}
	v["id"] = id
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.stage, m.DataPath), b, 0600); err != nil {
		t.Fatal(err)
	}
	r.meta[m.Key] = m
	return m
}

// Synthetic staged records isolate graph construction from HTTP, privacy,
// fsync, rendering and PDFs. Extra annotation bodies remain canonical export
// records even though related-summary metadata does not need their text.
func graphScaleFixture(t testing.TB, annotations int) *run {
	t.Helper()
	r := &run{ctx: context.Background(), stage: t.TempDir(), meta: map[string]*Meta{}}
	put := func(typ, id, annotationType string, v map[string]any) *Meta {
		return putGraphFixtureRecord(t, r, typ, id, annotationType, v)
	}
	for i := 0; i < annotations; i++ {
		put("ANNOTATIONS", fmt.Sprintf("body-%06d", i), "WORKSTREAM_SUMMARY", map[string]any{"text": strings.Repeat("Synthetic unrelated body text. ", 550)})
	}
	for i := 0; i < 250; i++ {
		put("WEBSITES", fmt.Sprintf("unused-%03d", i), "", map[string]any{"url": fmt.Sprintf("https://unrelated-%03d.example/path", i), "description": strings.Repeat("Synthetic webpage text. ", 40)})
	}
	website := put("WEBSITES", "shared", "", map[string]any{"url": "https://docs.example/path?public=value"})
	description := put("ANNOTATIONS", "shared-description", "WORKSTREAM_SUMMARY_DESCRIPTION", map[string]any{"text": "Shared approved description"})
	for i := 0; i < 50; i++ {
		m := put("WORKSTREAM_SUMMARIES", fmt.Sprintf("summary-%03d", i), "", map[string]any{"name": "Synthetic summary"})
		m.Edges = []Edge{{m.Key, website.Key, "websites"}, {m.Key, description.Key, "annotations"}}
	}
	return r
}

func TestSummaryGraphMetadataTraversal(t *testing.T) {
	r := graphScaleFixture(t, 4)
	put := func(typ, id, annotationType string, v map[string]any) *Meta {
		return putGraphFixtureRecord(t, r, typ, id, annotationType, v)
	}
	first := r.meta["WORKSTREAM_SUMMARIES\x00summary-000"]
	firstEdges := first.Edges
	first = put("WORKSTREAM_SUMMARIES", "summary-000", "", map[string]any{"description": "Direct description takes precedence"})
	first.Edges = firstEdges
	second := r.meta["WORKSTREAM_SUMMARIES\x00summary-001"]
	blank := put("ANNOTATIONS", "blank", "WORKSTREAM_SUMMARY_DESCRIPTION", map[string]any{"text": ""})
	private := put("ANNOTATIONS", "private", "WORKSTREAM_SUMMARY_DESCRIPTION", map[string]any{"text": "Withheld description"})
	private.State = "withheld"
	second.Edges = append([]Edge{{second.Key, private.Key, "annotations"}, {second.Key, blank.Key, "annotations"}}, second.Edges...)
	tag := put("TAGS", "Project", "", map[string]any{})
	person := put("PERSONS", "Casey", "", map[string]any{})
	source := put("WORKSTREAM_PATTERN_ENGINE_SOURCES", "Editor", "", map[string]any{})
	app := put("APPLICATIONS", "App", "", map[string]any{})
	host := put("WEBSITES", "unicode", "", map[string]any{"name": "WWW.Bücher.Example."})
	invalid := put("WEBSITES", "invalid", "", map[string]any{"url": "https://-.example/path"})
	duplicate := put("WEBSITES", "duplicate", "", map[string]any{"url": "https://DOCS.EXAMPLE./different"})
	blocked := put("WEBSITES", "blocked", "", map[string]any{"url": "https://excluded.example"})
	blocked.State = "excluded"
	event := put("WORKSTREAM_EVENTS", "event", "", map[string]any{})
	window := put("WORKSTREAM_PATTERN_ENGINE_SOURCE_WINDOWS", "window", "", map[string]any{})
	event.Edges = []Edge{{event.Key, first.Key, "events"}, {event.Key, window.Key, "source_windows"}, {event.Key, tag.Key, "tags"}, {event.Key, person.Key, "persons"}, {event.Key, app.Key, "applications"}, {event.Key, host.Key, "websites"}, {event.Key, invalid.Key, "websites"}, {event.Key, duplicate.Key, "websites"}, {event.Key, blocked.Key, "websites"}}
	window.Edges = []Edge{{window.Key, source.Key, "sources"}}
	first.Edges = append(first.Edges, Edge{first.Key, event.Key, "events"})
	second.Edges = append(second.Edges, Edge{second.Key, tag.Key, "tags"}, Edge{second.Key, person.Key, "persons"}, Edge{second.Key, source.Key, "sources"})
	var progress bytes.Buffer
	r.progress = startProgress(&progress, nil)
	g, err := r.buildSummaryGraph()
	r.progress.Close()
	if err != nil {
		t.Fatal(err)
	}
	metadata := g.Metadata[first.Key]
	if len(g.Metadata) != 50 || metadata.Description != "Direct description takes precedence" || g.Metadata[second.Key].Description != "Shared approved description" {
		t.Fatal("summary count or description precedence changed")
	}
	for name, values := range map[string]struct{ got, want []string }{
		"tags":        {metadata.Tags, []string{"Project"}},
		"persons":     {metadata.Persons, []string{"Casey"}},
		"sources":     {metadata.Sources, []string{"App", "Editor"}},
		"websites":    {metadata.Websites, []string{"docs.example", "www.xn--bcher-kva.example"}},
		"native tags": {metadata.NativeTags, []string{"Project", "source:app", "source:editor", "website:docs.example", "website:www.xn--bcher-kva.example"}},
	} {
		if !reflect.DeepEqual(values.got, values.want) {
			t.Fatalf("%s: got %v, want %v", name, values.got, values.want)
		}
	}
	selection, err := g.selectRelated(r.ctx, first, time.Time{}, "relevance", 50)
	if err != nil {
		t.Fatal(err)
	}
	for dimension := range dimensions {
		matches := selection.lists[dimension]
		if len(matches) == 0 || matches[0].mask != 15 || g.chronology[matches[0].rank].meta != second {
			t.Fatal("shared four-dimensional match was lost")
		}
	}
	if !strings.Contains(progress.String(), "Build summary relationships (50 records)") || !strings.Contains(progress.String(), "50/50 (100.0%)") {
		t.Fatal("summary graph progress omitted completed records")
	}
}

func TestSummaryGraphMissingMetadataAndCancellation(t *testing.T) {
	for _, key := range []string{"WORKSTREAM_SUMMARIES\x00summary-000", "ANNOTATIONS\x00shared-description", "WEBSITES\x00shared", "cancel"} {
		t.Run(strings.ReplaceAll(key, "\x00", "/"), func(t *testing.T) {
			r := graphScaleFixture(t, 0)
			if key == "cancel" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				r.ctx = ctx
			} else if err := os.Remove(filepath.Join(r.stage, r.meta[key].DataPath)); err != nil {
				t.Fatal(err)
			}
			_, err := r.buildSummaryGraph()
			if err == nil || key == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("missing metadata or cancellation must prevent graph completion")
			}
		})
	}
}

func TestSummaryGraphDeferredRecordsStillRenderAndValidate(t *testing.T) {
	for _, typ := range []string{"ANNOTATIONS", "WEBSITES"} {
		for _, corrupt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/corrupt=%t", typ, corrupt), func(t *testing.T) {
				r := &run{ctx: context.Background(), stage: t.TempDir(), meta: map[string]*Meta{}, opts: Options{Mode: "preserve", Timezone: "UTC", Format: "markdown", Metadata: "off", Naming: "opaque"}}
				putGraphFixtureRecord(t, r, "WORKSTREAM_SUMMARIES", "summary", "", map[string]any{"name": "Summary"})
				m := putGraphFixtureRecord(t, r, typ, "unrelated", "WORKSTREAM_SUMMARY", map[string]any{"text": "Complete unrelated canonical content", "url": "https://unrelated.example"})
				if corrupt {
					if err := os.WriteFile(filepath.Join(r.stage, m.DataPath), []byte("]"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := r.buildSummaryGraph(); err != nil {
					t.Fatal("unreferenced canonical content should not be loaded for summary metadata")
				}
				err := r.render()
				if corrupt {
					var syntax *json.SyntaxError
					if !errors.As(err, &syntax) {
						t.Fatalf("unrelated corrupt content must still fail canonical rendering: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{m.Path, m.DataPath} {
					b, err := os.ReadFile(filepath.Join(r.stage, path))
					if err != nil || !bytes.Contains(b, []byte("Complete unrelated canonical content")) {
						t.Fatalf("unrelated canonical data/document was lost: %v", err)
					}
				}
			})
		}
	}
}

func BenchmarkSummaryGraphStagedRecords(b *testing.B) {
	for _, n := range []int{200, 2000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			r := graphScaleFixture(b, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				g, err := r.buildSummaryGraph()
				if err != nil || len(g.Metadata) != 50 {
					b.Fatalf("graph construction failed: %v", err)
				}
			}
		})
	}
}
