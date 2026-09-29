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

func TestOrganizedCanonicalPathsSharedMembershipAndPrivacy(t *testing.T) {
	makeSummary := func(id, kind string) map[string]any {
		v := record(id, "2026-09-29T12:00:00Z")
		v["name"], v["parentHierarchicalType"] = "Same title", kind
		return v
	}
	temporal := makeSummary("temporal", "TEMPORAL_DAY_HIERARCHICAL_SUMMARY")
	unknown := makeSummary("legacy", "UNKNOWN")
	study := makeSummary("study", "DEEP_STUDY_HIERARCHICAL_SUMMARY")
	shared := makeSummary("shared", "SPECIFIC_HIERARCHICAL_SUMMARY")
	shared["parentHierarchicalTypeDescriptor"] = "standup"
	shared["pipelines"] = refs("pipeline-b", "pipeline-a")
	shared["persons"] = refs("person-a", "person-b")
	pa, pb := record("pipeline-a", ""), record("pipeline-b", "")
	pa["name"], pb["name"] = "Daily / Standup", "Daily / Standup"
	pb["summaries"] = refs("study") // reverse-only association supplies navigation
	private := record("private-pipeline", "")
	private["name"], private["url"] = "PRIVATE_PIPELINE_NAME", "https://bank.example/private"
	private["summaries"] = refs("legacy") // an excluded pipeline must not name a folder
	personA, personB := record("person-a", ""), record("person-b", "")
	personA["name"], personB["name"] = "Alex Example", "Alex Example"
	personB["type"] = map[string]any{"platform": map[string]any{"id": "user-a"}} // Must not override the exact user-to-person mapping.
	one := record("single-history", "2026-09-28T00:00:00Z")
	one["type"], one["text"], one["persons"] = "HIERARCHICAL_PROFILE_SUMMARY", "A retained individual profile.", refs("person-a")
	both := record("shared-history", "2026-09-29T00:00:00Z")
	both["type"], both["text"], both["persons"] = "PROFILE_DESCRIPTION", "Shared profile evidence.", refs("person-a", "person-b")
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {temporal, unknown, study, shared}, "PIPELINES": {pa, pb, private}, "PERSONS": {personA, personB}, "ANNOTATIONS": {one, both}}, currentUserID: "user-a", userPersons: map[string]string{"user-a": "person-a"}}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("WORKSTREAM_SUMMARIES,PIPELINES,PERSONS,ANNOTATIONS")
	policy := DefaultPolicy()
	policy.Deny = []DomainRule{{"bank.example", true}}
	out := filepath.Join(t.TempDir(), "export")
	manifest, err := Export(context.Background(), c, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy), Format: "both", Metadata: "off", PeopleMode: "profiles"})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "partial" {
		t.Fatalf("unexpected coverage issues: %+v", manifest.Issues)
	}
	for _, issue := range manifest.Issues {
		if !strings.HasPrefix(issue.Code, "unverified_") {
			t.Fatalf("unexpected non-projection issue: %+v", issue)
		}
	}
	var paths map[string]string
	data, _ := os.ReadFile(filepath.Join(out, "link-map.json"))
	if err := json.Unmarshal(data, &paths); err != nil {
		t.Fatal(err)
	}
	path := func(typ, id string) string { return paths[opaque(typ, id)] }
	if !strings.HasPrefix(path("WORKSTREAM_SUMMARIES", "temporal"), "workstream_summaries/timeline/") || !strings.HasPrefix(path("WORKSTREAM_SUMMARIES", "legacy"), "workstream_summaries/timeline/") {
		t.Fatal("temporal/unknown classification lost")
	}
	if !strings.HasPrefix(path("WORKSTREAM_SUMMARIES", "shared"), "workstream_summaries/single_click_summaries/daily_standups/") || !strings.HasPrefix(path("WORKSTREAM_SUMMARIES", "study"), "workstream_summaries/hierarchical_summaries/deep_study_hierarchical_summary/") {
		t.Fatal("descriptor/type canonical placement lost")
	}
	indexes, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/pipeline_associations/Daily_Standup.*.md"))
	if len(indexes) != 2 {
		t.Fatal("same-name pipeline identities merged")
	}
	for _, index := range indexes {
		data, _ := os.ReadFile(index)
		if !strings.Contains(string(data), filepath.Base(path("WORKSTREAM_SUMMARIES", "shared"))) {
			t.Fatal("shared summary missing from a pipeline index")
		}
	}
	if !strings.HasPrefix(path("ANNOTATIONS", "single-history"), "workstream_summaries/personas/users/") || !strings.Contains(path("ANNOTATIONS", "single-history"), "/profile_summaries/") || !strings.HasPrefix(path("ANNOTATIONS", "shared-history"), "markdown/annotations/") {
		t.Fatal("single-owner/shared persona history placement incorrect")
	}
	personIndexes, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/personas/*/*/related_workstream_summaries/index.md"))
	if len(personIndexes) != 2 {
		t.Fatal("same-name people were merged or their folders lost")
	}
	userProfiles, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/personas/users/*/profile.md"))
	relatedProfiles, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/personas/related_persons/*/profile.md"))
	if len(userProfiles) != 1 || len(relatedProfiles) != 1 {
		t.Fatal("platform ID equality was mistaken for the authoritative user-person mapping")
	}
	for _, index := range personIndexes {
		data, _ := os.ReadFile(index)
		if !strings.Contains(string(data), filepath.Base(path("WORKSTREAM_SUMMARIES", "shared"))) {
			t.Fatal("person-associated summary not navigable")
		}
	}
	if path("PIPELINES", "private-pipeline") != "" {
		t.Fatal("excluded pipeline retained in path map")
	}
	data, _ = os.ReadFile(filepath.Join(out, "workstream_summaries/pipeline_associations/index.md"))
	if strings.Contains(string(data), "PRIVATE_PIPELINE_NAME") {
		t.Fatal("private pipeline leaked into navigation")
	}
	// Full link/PDF audit must survive relocation with the deeper paths.
	moved := out + "-moved"
	if err := os.Rename(out, moved); err != nil {
		t.Fatal(err)
	}
	r := &run{ctx: context.Background(), stage: moved, opts: Options{Mode: "filtered", Scanner: scanner(t, policy)}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
}
