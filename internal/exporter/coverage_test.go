package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectionCoverageDoesNotConfuseAbsentWithEmpty(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{nil, "absent"}, {"", "invalid"}, {map[string]any{}, "invalid"},
		{map[string]any{"iterable": nil}, "invalid"},
		{map[string]any{"indices": map[string]any{"x": "0"}}, "invalid"},
		{map[string]any{"indices": map[string]any{"x": json.Number("0.5")}}, "invalid"},
		{map[string]any{"iterable": []any{map[string]any{}}}, "invalid"},
		{refs(), "empty"}, {refs("x"), "linked"},
		{map[string]any{"indices": map[string]any{"deleted": -1}}, "empty"},
	} {
		if got := projectionState(tc.value); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}

	summary := record("summary", "2026-09-29T00:00:00Z")
	summary["name"], summary["persons"], summary["pipelines"] = "Coverage fixture", refs(), refs()
	body := record("body", "")
	body["type"], body["text"], body["summaries"] = "SUMMARY", "A body recovered from an inverse edge.", refs("summary")
	private := record("private", "")
	private["url"] = "https://bank.example/private"
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {summary, private}, "ANNOTATIONS": {body}}}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("WORKSTREAM_SUMMARIES,ANNOTATIONS")
	preflight, err := Scan(context.Background(), c, materials, nil)
	if err != nil {
		t.Fatal(err)
	}
	var preview bytes.Buffer
	preflight.Print(&preview, "markdown")
	if !strings.Contains(preview.String(), "absent/invalid core relationship") {
		t.Fatal("preflight hid missing projections")
	}
	policy := DefaultPolicy()
	policy.Deny = []DomainRule{{"bank.example", true}}
	out := filepath.Join(t.TempDir(), "archive")
	manifest, err := Export(context.Background(), c, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy), Metadata: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "partial" {
		t.Fatal("absent body projection claimed complete")
	}
	if len(manifest.RelationshipCoverage) != 3 {
		t.Fatal("missing field coverage")
	}
	for _, row := range manifest.RelationshipCoverage {
		if row.Included != 1 {
			t.Fatal("excluded summary affected retained coverage")
		}
		if row.Field == "annotations" && row.Absent != 1 {
			t.Fatal("inverse recovery falsely certified projection completeness")
		}
		if row.Field != "annotations" && row.Empty != 1 {
			t.Fatal("explicit empty was confused with absent")
		}
	}
	if len(manifest.Issues) != 1 || manifest.Issues[0].Code != "unverified_annotations_projection" {
		t.Fatalf("issues: %+v", manifest.Issues)
	}
	coverage, err := os.ReadFile(filepath.Join(out, "coverage.md"))
	if err != nil || strings.Contains(string(coverage), "bank.example") {
		t.Fatal("coverage report missing or leaked excluded content")
	}
	paths, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/timeline/*.md"))
	found := false
	for _, path := range paths {
		data, _ := os.ReadFile(path)
		found = found || strings.Contains(string(data), "body recovered from an inverse edge")
	}
	if !found {
		t.Fatal("honest partial coverage prevented inverse body recovery")
	}
	if err := os.Rename(out, out+"-moved"); err != nil {
		t.Fatal(err)
	}
	r := &run{ctx: context.Background(), stage: out + "-moved", opts: Options{Mode: "filtered", Scanner: scanner(t, policy)}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
}
