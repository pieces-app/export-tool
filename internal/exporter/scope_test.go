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

func TestSelectScope(t *testing.T) {
	for _, name := range []string{"", "all", "summaries"} {
		s, err := SelectScope(name, "")
		if err != nil {
			t.Fatal(err)
		}
		if name != "summaries" && len(s.Materials) != len(Materials) {
			t.Fatal("all-data default changed")
		}
		if name == "summaries" && (len(s.InventoryMaterials()) != 4 || len(s.ReferenceOnly) != 6) {
			t.Fatal("unexpected summary roots/supporting types")
		}
	}
	for _, input := range [][2]string{{"summaries", "all"}, {"all", "TAGS"}, {"events", ""}, {"", "NO_SUCH_TYPE"}} {
		if _, err := SelectScope(input[0], input[1]); err == nil {
			t.Fatal("invalid or ambiguous selection accepted")
		}
	}
	s, err := SelectScope("", "WORKSTREAM_SUMMARIES,ANNOTATIONS")
	if err != nil || s.Name != "custom" || len(s.Materials) != 2 || len(s.ReferenceOnly) != 0 {
		t.Fatal("custom selection changed")
	}
}

func summaryScopeFixture() *fakeOS {
	s := record("summary", "2026-09-29T12:00:00Z")
	s["name"], s["parentHierarchicalType"] = "Summary without event history", "TEMPORAL_DAY_HIERARCHICAL_SUMMARY"
	s["annotations"], s["persons"], s["pipelines"] = refs("body"), refs("person"), refs()
	s["events"], s["hints"] = refs("omitted-event"), refs("omitted-hint")
	s["sources"] = refs("source")
	tags := []map[string]any{}
	ids := []string{}
	for i := 0; i < 53; i++ {
		id := fmt.Sprintf("tag-%d", i)
		v := record(id, "")
		v["name"] = fmt.Sprintf("Topic %d", i)
		tags, ids = append(tags, v), append(ids, id)
	}
	s["tags"] = refs(ids...)
	unused := record("unused-tag", "")
	unused["name"] = "UNRELATED_TAG_NOT_EXPORTED"
	tags = append(tags, unused)
	a := record("body", "2026-09-29T12:00:00Z")
	a["type"], a["text"], a["summaries"] = "SUMMARY", "Actual summary narrative with [a person](pieces://persons/person).", refs("summary")
	p := record("person", "")
	p["name"], p["annotations"], p["summaries"] = "Example Person", refs("profile"), refs("summary")
	profile := record("profile", "2026-09-29T12:00:00Z")
	profile["type"], profile["text"], profile["persons"] = "HIERARCHICAL_PROFILE_SUMMARY", "Retained persona history.", refs("person")
	e := record("omitted-event", "")
	e["text"], e["url"] = "EVENT_BODY_NOT_EXPORTED", "https://bank.example/event-only-origin"
	source := record("source", "")
	source["name"] = "Example Editor"
	return &fakeOS{data: map[string][]map[string]any{
		"WORKSTREAM_SUMMARIES": {s}, "ANNOTATIONS": {a, profile}, "PERSONS": {p}, "PIPELINES": {},
		"WORKSTREAM_EVENTS": {e}, "HINTS": {record("omitted-hint", "")}, "TAGS": tags,
		"WORKSTREAM_PATTERN_ENGINE_SOURCES": {source},
	}, currentUserID: "user", userPersons: map[string]string{"user": "person"}}
}

func assertSummaryScopeRequests(t *testing.T, f *fakeOS) {
	t.Helper()
	for _, typ := range f.requestedMaterials {
		switch typ {
		case "WORKSTREAM_SUMMARIES", "ANNOTATIONS", "PERSONS", "PIPELINES":
		default:
			t.Fatalf("enumerated an unselected/reference-only collection: %s", typ)
		}
	}
	tagBatches := 0
	for _, call := range f.calls {
		for _, forbidden := range []string{" /workstream_events/", " /workstream_event/", " /hints/", " /workstream_pattern_engine/source_windows", " /signals/"} {
			if strings.Contains(call, forbidden) {
				t.Fatalf("scope read omitted content: %s", call)
			}
		}
		if call == "POST /tags/batch/fetch" {
			tagBatches++
		}
	}
	if tagBatches == 0 || tagBatches > 20 {
		t.Fatalf("supporting tags not read in bounded batches: %d", tagBatches)
	}
}

func TestSummaryScopeBodiesProfilesAndReferencedLabelsWithoutEvents(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprint("strict=", strict), func(t *testing.T) {
			f := summaryScopeFixture()
			srv := f.server(t)
			defer srv.Close()
			c, _ := NewClient(srv.URL, time.Second, 8<<20)
			sel, _ := SelectScope("summaries", "")
			p := DefaultPolicy()
			p.Deny, p.StrictDerived = []DomainRule{{"bank.example", true}}, strict
			out := filepath.Join(t.TempDir(), "archive")
			m, err := Export(context.Background(), c, Options{Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, Output: out, Mode: "filtered", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, p), Format: "both"})
			if err != nil {
				t.Fatal(err)
			}
			if m.Status != "complete_for_implemented_scope" || m.Scope.Name != "summaries" {
				t.Fatalf("intentional omissions treated as failures: %+v", m.Issues)
			}
			assertSummaryScopeRequests(t, f)
			for _, cov := range m.Coverage {
				if cov.Material == "WORKSTREAM_EVENTS" {
					t.Fatal("events included in scope")
				}
				if cov.Material == "TAGS" && (cov.Fetched != 53 || cov.InitialCount != -1 || cov.FinalCount != -1 || cov.InventoryMode != "references") {
					t.Fatalf("supporting inventory misreported: %+v", cov)
				}
			}
			paths := map[string]string{}
			b, _ := os.ReadFile(filepath.Join(out, "link-map.json"))
			if err := json.Unmarshal(b, &paths); err != nil {
				t.Fatal(err)
			}
			if paths[opaque("TAGS", "unused-tag")] != "" || paths[opaque("WORKSTREAM_EVENTS", "omitted-event")] != "" {
				t.Fatal("unrelated content exported")
			}
			summaryPath := paths[opaque("WORKSTREAM_SUMMARIES", "summary")]
			if strict {
				if summaryPath != "" || paths[opaque("ANNOTATIONS", "body")] != "" {
					t.Fatal("strict derived filtering bypassed without event history")
				}
			} else {
				b, err := os.ReadFile(filepath.Join(out, summaryPath))
				if err != nil || !strings.Contains(string(b), "Actual summary narrative") || !strings.Contains(string(b), "Example Editor") {
					t.Fatal("summary body/direct source missing without events")
				}
				if m.Scope.OmittedReferences["WORKSTREAM_EVENTS"] != 1 || m.Scope.OmittedReferences["HINTS"] != 1 {
					t.Fatal("intentional graph omissions not counted")
				}
				if !strings.HasPrefix(paths[opaque("ANNOTATIONS", "profile")], "workstream_summaries/personas/users/") {
					t.Fatal("verified persona history misplaced")
				}
			}
			moved := out + "-moved"
			if err := os.Rename(out, moved); err != nil {
				t.Fatal(err)
			}
			r := &run{ctx: context.Background(), stage: moved, opts: Options{Scanner: scanner(t, p)}}
			if err := r.validateMarkdownLinks(); err != nil {
				t.Fatal(err)
			}
			if err := r.auditOutput(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSummaryScopeMissingSelectedTargetStillPartial(t *testing.T) {
	f := summaryScopeFixture()
	f.data["WORKSTREAM_SUMMARIES"][0]["annotations"] = refs("missing-body")
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	sel, _ := SelectScope("summaries", "")
	m, err := Export(context.Background(), c, Options{Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, Output: filepath.Join(t.TempDir(), "archive"), Mode: "filtered", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())})
	if err != nil || m.Status != "partial" {
		t.Fatalf("missing selected target was mistaken for scope omission: %v, %s", err, m.Status)
	}
}
