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

func TestReverseAnnotationBodyAndPersonaSummaryTraversal(t *testing.T) {
	person := record("person", "")
	person["name"] = "Example Person"
	persona := record("persona", "2026-09-29T00:00:00Z")
	persona["type"], persona["text"] = "HIERARCHICAL_PROFILE_SUMMARY", "Retained persona prose."
	persona["persons"], persona["summaries"] = refs("person"), refs("summary")
	body := record("body", "2026-09-29T00:00:00Z")
	body["type"], body["text"], body["summaries"] = "SUMMARY", "Narrative from an inverse attachment.", refs("summary")
	summary := record("summary", "2026-09-29T00:00:00Z")
	f := &fakeOS{data: map[string][]map[string]any{"PERSONS": {person}, "ANNOTATIONS": {persona, body}, "WORKSTREAM_SUMMARIES": {summary}}}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	mats, _ := SelectMaterials("PERSONS,ANNOTATIONS,WORKSTREAM_SUMMARIES")
	out := filepath.Join(t.TempDir(), "export")
	_, err := Export(context.Background(), c, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: mats, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), PeopleMode: "profiles"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(out, "link-map.json"))
	var paths map[string]string
	if err := json.Unmarshal(data, &paths); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(out, paths[opaque("WORKSTREAM_SUMMARIES", "summary")]))
	if !strings.Contains(string(data), "Narrative from an inverse attachment.") || !strings.Contains(string(data), "derived inverse") {
		t.Fatal("reverse attachment did not render the body with provenance")
	}
	profiles, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/personas/related_persons/*/profile.md"))
	if len(profiles) != 1 {
		t.Fatal("profile navigation missing")
	}
	data, _ = os.ReadFile(profiles[0])
	if !strings.Contains(string(data), "Workstream summaries linked to profile annotations") || !strings.Contains(string(data), filepath.Base(paths[opaque("WORKSTREAM_SUMMARIES", "summary")])) {
		t.Fatal("persona annotation to workstream summary traversal missing")
	}
}

func TestDescriptorClassificationAvoidsTitleGuessingAndCollisions(t *testing.T) {
	r := &run{opts: Options{Naming: "readable"}}
	if got := r.summaryFolder(&Meta{SummaryKind: "UNKNOWN", Title: "Daily standup"}); got != "workstream_summaries/timeline" {
		t.Fatal("title was used as a pipeline classifier")
	}
	for _, descriptor := range []string{"", "standup", "morning_brief", "time_tracker", "end_of_day_recap", "week_recap"} {
		m := &Meta{SummaryKind: "SPECIFIC_HIERARCHICAL_SUMMARY", SummaryDescriptor: descriptor}
		folder := r.summaryFolder(m)
		if strings.Contains(folder, ".") || !strings.HasPrefix(folder, "workstream_summaries/single_click_summaries/") {
			t.Fatalf("built-in descriptor did not get its named folder: %q", folder)
		}
	}
	a := r.summaryFolder(&Meta{SummaryKind: "SPECIFIC_HIERARCHICAL_SUMMARY", SummaryDescriptor: "custom/value"})
	b := r.summaryFolder(&Meta{SummaryKind: "SPECIFIC_HIERARCHICAL_SUMMARY", SummaryDescriptor: "custom_value"})
	if a == b {
		t.Fatal("different custom descriptors merged after normalization")
	}
	r.opts.Naming = "opaque"
	if strings.Contains(r.summaryDescriptorFolder(&Meta{SummaryDescriptor: "private_custom_pipeline"}), "private") {
		t.Fatal("opaque folder leaked custom descriptor")
	}
}

func TestCustomDescriptorUsesOnlyIncludedPipelineNames(t *testing.T) {
	r := &run{opts: Options{Naming: "readable"}, meta: map[string]*Meta{
		"PIPELINES\x00one":     {State: "included", Title: "Weekly review"},
		"PIPELINES\x00two":     {State: "included", Title: "Weekly review"},
		"PIPELINES\x00private": {State: "excluded", Title: "Private pipeline name"},
	}}
	a, b := &Meta{SummaryDescriptor: "custom_pipeline_one"}, &Meta{SummaryDescriptor: "custom_pipeline_two"}
	if !strings.HasPrefix(r.summaryDescriptorFolder(a), "weekly_review.") || r.summaryDescriptorLabel(a) != "Weekly review" {
		t.Fatal("known custom descriptor did not use its approved display name")
	}
	if r.summaryDescriptorFolder(a) == r.summaryDescriptorFolder(b) {
		t.Fatal("same-name pipelines merged")
	}
	for _, descriptor := range []string{"custom_pipeline_private", "custom_pipeline_absent", "arbitrary"} {
		m := &Meta{SummaryDescriptor: descriptor}
		if r.summaryDescriptorLabel(m) != descriptor || !strings.HasPrefix(r.summaryDescriptorFolder(m), descriptor+".") {
			t.Fatal("unavailable pipeline did not retain its descriptor fallback")
		}
	}
	r.opts.Naming = "opaque"
	if !strings.HasPrefix(r.summaryDescriptorFolder(a), "pipeline.") {
		t.Fatal("opaque path exposed pipeline name")
	}
}
