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

func TestPeopleSelectionConsolidationAndPersonaHistory(t *testing.T) {
	person := func(id string) map[string]any {
		v := record(id, "")
		v["annotations"] = refs()
		v["summaries"] = refs()
		v["workstream_events"] = refs()
		v["type"] = map[string]any{"basic": map[string]any{"name": "Same Name", "email": "shared@example.test"}}
		return v
	}
	a, b, profile, low, high, summaryPerson := person("account-a"), person("account-b"), person("profile"), person("low"), person("high"), person("summary-person")
	for _, p := range []map[string]any{a, b} {
		p["type"].(map[string]any)["platform"] = map[string]any{"id": "account"}
	}
	high["workstream_events"] = refs("e1", "e2")
	desc := record("profile-text", "2026-09-01T00:00:00Z")
	desc["type"] = "PROFILE_DESCRIPTION"
	desc["text"] = "A factual profile."
	desc["persons"] = refs("profile") // reverse-only link
	persona := record("persona-text", "2026-09-02T00:00:00Z")
	persona["type"] = "HIERARCHICAL_PROFILE_SUMMARY"
	persona["text"] = "Retained persona text."
	persona["person"] = map[string]any{"id": "profile"}
	summary := record("summary", "2026-09-03T00:00:00Z")
	summary["name"] = "A summary kept in every people mode"
	summary["persons"] = refs("summary-person", "low")
	// 'low' must have no meaningful summary edge; narrative mentions alone do not qualify.
	summary["persons"] = refs("summary-person")
	summary["description"] = "Mention [Low](pieces://persons/low) without a typed person association."
	f := &fakeOS{data: map[string][]map[string]any{"PERSONS": {a, b, profile, low, high, summaryPerson}, "ANNOTATIONS": {desc, persona}, "WORKSTREAM_SUMMARIES": {summary}, "WORKSTREAM_EVENTS": {record("e1", ""), record("e2", "")}}}
	srv := f.server(t)
	defer srv.Close()
	for mode, want := range map[string]int{"profiles": 3, "connected": 5, "all": 6} {
		t.Run(mode, func(t *testing.T) {
			c, _ := NewClient(srv.URL, time.Second, 4<<20)
			materials, _ := SelectMaterials("PERSONS,ANNOTATIONS,WORKSTREAM_SUMMARIES,WORKSTREAM_EVENTS")
			out := filepath.Join(t.TempDir(), "export")
			manifest, err := Export(context.Background(), c, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), PeopleMode: mode, MinPersonConnections: 2})
			if err != nil {
				t.Fatal(err)
			}
			if manifest.People.Selected != want || manifest.People.PlatformGroups != 1 {
				t.Fatalf("selection/grouping incorrect: %+v", manifest.People)
			}
			paths, _ := filepath.Glob(filepath.Join(out, "markdown/persons/*.md"))
			if len(paths) != want {
				t.Fatal("omitted people still rendered")
			}
			summaries, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/timeline/0*.md"))
			if len(summaries) == 0 {
				t.Fatal("people selection removed summaries")
			}
			index, _ := os.ReadFile(filepath.Join(out, "workstream_summaries/personas/related_persons/index.md"))
			if !strings.Contains(string(index), "2 identity record(s)") {
				t.Fatal("same platform identity was not consolidated in navigation")
			}
			pages, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/personas/related_persons/*/profile.md"))
			found := false
			for _, path := range pages {
				data, _ := os.ReadFile(path)
				if strings.Contains(string(data), "Retained persona text.") && strings.Contains(string(data), "A factual profile.") {
					found = true
				}
			}
			if !found {
				t.Fatal("persona/profile prose or reverse links lost")
			}
		})
	}
}
func TestPeopleSelectionKeepsUnknownAndDoesNotTrustExtractionMetadata(t *testing.T) {
	p := personFacts(map[string]any{"id": "p", "extraction": map[string]any{"aliases": []any{"Alias"}}})
	classifyPerson(p, map[string]string{})
	if selectedPerson(p, "profiles", 10) {
		t.Fatal("extraction metadata treated as a persona")
	}
	p.Annotations = []string{"unread"}
	classifyPerson(p, map[string]string{})
	if !selectedPerson(p, "profiles", 10) {
		t.Fatal("unread annotation caused silent omission")
	}
}

func TestProjectedPeopleUseDirectProfileQueriesAndPreserveUnknownConnectivity(t *testing.T) {
	p := record("projected", "")
	p["type"] = map[string]any{"basic": map[string]any{"name": "Projected Person"}}
	rows := []map[string]any{}
	for i := 0; i < 75; i++ {
		a := record(fmt.Sprint(i), time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC).Format(time.RFC3339))
		a["type"] = "HIERARCHICAL_PROFILE_SUMMARY"
		a["person"] = map[string]any{"id": "projected"}
		a["text"] = "Retained persona history."
		rows = append(rows, a)
	}
	f := &fakeOS{data: map[string][]map[string]any{"PERSONS": {p}, "ANNOTATIONS": rows}}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	values, complete, err := personAnnotations(context.Background(), c, "projected", "HIERARCHICAL_PROFILE_SUMMARY", true)
	if err != nil || !complete || len(values) != 75 {
		t.Fatalf("windowed history incomplete: %d %v %v", len(values), complete, err)
	}
	facts := personFacts(p)
	if !facts.Projected || !selectedPerson(facts, "connected", 10) {
		t.Fatal("missing embedded relationships treated as empty")
	}
	values, err = personEvidence(context.Background(), c, facts, false)
	if err != nil || len(values) != 1 || facts.UnknownAnnotations {
		t.Fatal("direct profile evidence unavailable")
	}
	// Dense timestamp ties must be reported as incomplete, never skipped.
	for _, a := range rows {
		a["created"] = map[string]any{"value": "2026-01-01T00:00:00Z"}
	}
	values, complete, err = personAnnotations(context.Background(), c, "projected", "HIERARCHICAL_PROFILE_SUMMARY", true)
	if err != nil || complete || len(values) != 50 {
		t.Fatal("same-timestamp saturation was silently skipped")
	}
}
