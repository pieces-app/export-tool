package exporter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDisplayTimestamp(t *testing.T) {
	for _, tc := range []struct{ input, zone, want string }{
		{"2025-04-23T15:42:26.392376Z", "UTC", "April 23, 2025 at 3:42:26 PM UTC"},
		{"2025-04-23T15:42:26.392376Z", "America/New_York", "April 23, 2025 at 11:42:26 AM EDT (UTC-04:00)"},
		{"2025-01-23T02:42:26Z", "America/New_York", "January 22, 2025 at 9:42:26 PM EST (UTC-05:00)"},
		{"2025-04-23T15:42:26+05:30", "UTC", "April 23, 2025 at 10:12:26 AM UTC"},
		{"", "UTC", "undated"},
		{"invalid-source-date", "UTC", "invalid-source-date"},
	} {
		zone, err := time.LoadLocation(tc.zone)
		if err != nil {
			t.Fatal(err)
		}
		if got := displayTimestamp(tc.input, zone); got != tc.want {
			t.Fatalf("displayTimestamp(%q, %q) = %q; want %q", tc.input, tc.zone, got, tc.want)
		}
	}
}

func TestSummaryProfileReferencesThroughRebuild(t *testing.T) {
	const stamp = "2025-04-23T15:42:26.392376Z"
	const readable = "April 23, 2025 at 11:42:26 AM EDT (UTC-04:00)"
	first, second := record("first", stamp), record("second", stamp)
	first["annotations"], second["annotations"] = refs("body", "owned", "shared", "ownerless"), refs("body", "owned")
	person, other := record("person", stamp), record("other-person", stamp)
	person["name"], other["name"] = "Primary person", "Other person"
	body := record("body", stamp)
	body["type"], body["text"] = "SUMMARY", "The summary's own narrative stays inline."
	owned, shared, ownerless := record("owned", stamp), record("shared", stamp), record("ownerless", stamp)
	owned["name"], shared["name"], ownerless["name"] = "Owner report", "Shared report", "Report without an owner"
	owned["type"], owned["text"], owned["persons"] = "HIERARCHICAL_PROFILE_SUMMARY", "Distinct retained owner profile prose.", refs("person")
	shared["type"], shared["text"], shared["persons"] = "PROFILE_DESCRIPTION", "Distinct retained shared profile prose.", refs("person", "other-person")
	ownerless["type"], ownerless["text"] = "HIERARCHICAL_PROFILE_SUMMARY", "Distinct retained ownerless profile prose."
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {first, second}, "PERSONS": {person, other}, "ANNOTATIONS": {body, owned, shared, ownerless}}}
	srv := f.server(t)
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("WORKSTREAM_SUMMARIES,PERSONS,ANNOTATIONS")
	out := filepath.Join(t.TempDir(), "original")
	_, err := Export(context.Background(), client, Options{Output: out, Format: "markdown", Mode: "filtered", Timezone: "America/New_York", Materials: materials, PeopleMode: "profiles", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Metadata: "off"})
	srv.Close()
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := filepath.Join(t.TempDir(), "rebuilt")
	if _, err := Rebuild(context.Background(), RebuildOptions{Source: out, Options: Options{Output: rebuilt, Scanner: scanner(t, DefaultPolicy())}}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{out, rebuilt} {
		moved := dir + "-moved"
		if err := os.Rename(dir, moved); err != nil {
			t.Fatal(err)
		}
		var paths map[string]string
		encoded, _ := os.ReadFile(filepath.Join(moved, "link-map.json"))
		if err := json.Unmarshal(encoded, &paths); err != nil {
			t.Fatal(err)
		}
		path := func(kind, id string) string { return paths[opaque(kind, id)] }
		for id, annotations := range map[string][]string{"first": {"owned", "shared", "ownerless"}, "second": {"owned"}} {
			summary := path("WORKSTREAM_SUMMARIES", id)
			doc, err := os.ReadFile(filepath.Join(moved, summary))
			if err != nil || !strings.Contains(string(doc), fieldString(body, "text")) || !strings.Contains(string(doc), "Created: "+readable) {
				t.Fatal("summary narrative or readable timestamp missing", err)
			}
			if strings.Contains(string(doc), "Distinct retained") || strings.Count(string(doc), "## Persona context") != 1 {
				t.Fatal("profile prose was duplicated in a summary or its metadata")
			}
			links := acceptanceMarkdownLinks(doc)
			for _, annotation := range annotations {
				if !links[relative(summary, path("ANNOTATIONS", annotation))] {
					t.Fatal("summary does not link its exact profile version")
				}
			}
			canonical, _ := os.ReadFile(filepath.Join(moved, "data/summaries", opaque("WORKSTREAM_SUMMARIES", id)+".json"))
			if !strings.Contains(string(canonical), stamp) {
				t.Fatal("display formatting changed canonical source precision")
			}
		}
		for _, annotation := range []map[string]any{owned, shared, ownerless} {
			doc, err := os.ReadFile(filepath.Join(moved, path("ANNOTATIONS", fieldString(annotation, "id"))))
			if err != nil || !strings.Contains(string(doc), fieldString(annotation, "text")) {
				t.Fatal("linked profile text was not preserved", err)
			}
		}
		counts, err := inspectFinalArchive(context.Background(), moved)
		if err != nil || counts.SummariesWithBody != 2 || counts.LinkedProfileAnnotations != 4 || counts.RenderedAnnotationBodies != 2 {
			t.Fatal("independent body/profile/link verification failed", counts, err)
		}
		// Neither a missing reference nor a truncated ownerless report may pass.
		for _, target := range []string{path("WORKSTREAM_SUMMARIES", "first"), path("ANNOTATIONS", "ownerless")} {
			file := filepath.Join(moved, target)
			original, _ := os.ReadFile(file)
			damaged := []byte("# Removed profile text\n")
			if target == path("WORKSTREAM_SUMMARIES", "first") {
				damaged = []byte(strings.ReplaceAll(string(original), relative(target, path("ANNOTATIONS", "ownerless")), "index.md"))
			}
			if err := os.WriteFile(file, damaged, 0600); err != nil {
				t.Fatal(err)
			}
			_, checkErr := inspectFinalArchive(context.Background(), moved)
			if err := os.WriteFile(file, original, 0600); err != nil {
				t.Fatal(err)
			}
			if checkErr == nil {
				t.Fatal("damaged profile text/reference passed acceptance")
			}
		}
	}
}
