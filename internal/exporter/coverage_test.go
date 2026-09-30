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

func TestSignalProjectionCoverage(t *testing.T) {
	for _, scenario := range []string{"absent", "explicit-empty", "linked", "invalid"} {
		t.Run(scenario, func(t *testing.T) {
			signal := record("signal", "2026-09-30T00:00:00Z")
			signal["name"] = "Signal coverage fixture"
			for _, field := range projectionFields("SIGNALS") {
				signal[field] = refs()
			}
			switch scenario {
			case "absent":
				delete(signal, "annotations")
			case "linked":
				signal["annotations"] = refs("description")
			case "invalid":
				signal["annotations"] = "malformed"
			}
			annotation := record("description", "")
			annotation["type"], annotation["text"] = "SIGNAL_DESCRIPTION", "Retained signal description."
			private := record("private-signal", "")
			private["url"] = "https://bank.example/private"
			f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal, private}, "ANNOTATIONS": {annotation}}}
			association := record("signal-description-association", "2026-09-30T00:00:00Z")
			association["signal"], association["annotation"] = "signal", "description"
			f.data["SIGNAL_TO_ANNOTATION_ASSOCIATIONS"] = []map[string]any{association}
			srv := f.server(t)
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			materials, _ := SelectMaterials("SIGNALS,ANNOTATIONS")
			policy := DefaultPolicy()
			policy.Deny = []DomainRule{{"bank.example", true}}
			out := filepath.Join(t.TempDir(), "archive")
			manifest, err := Export(context.Background(), client, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy), Metadata: "off"})
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := "complete_for_implemented_scope"
			if scenario == "absent" || scenario == "invalid" {
				wantStatus = "partial"
			}
			if manifest.Status != wantStatus || len(manifest.RelationshipCoverage) != 7 {
				t.Fatalf("incorrect signal coverage status: %s %+v", manifest.Status, manifest.Issues)
			}
			for _, row := range manifest.RelationshipCoverage {
				if row.Material != "SIGNALS" || row.Included != 1 {
					t.Fatal("excluded signal contributed to coverage")
				}
				if row.Field != "annotations" || scenario == "explicit-empty" {
					if row.Empty != 1 {
						t.Fatal("explicit empty projection not preserved")
					}
				} else if scenario == "absent" && row.Absent != 1 || scenario == "invalid" && row.Invalid != 1 || scenario == "linked" && row.Linked != 1 {
					t.Fatalf("annotation coverage lost distinction: %+v", row)
				}
			}
			b, err := os.ReadFile(filepath.Join(out, "coverage.md"))
			if err != nil || !strings.Contains(string(b), "SIGNALS") || strings.Contains(string(b), "bank.example") {
				t.Fatal("signal coverage document missing or leaked excluded content")
			}
		})
	}
}

func TestRebuildSignalProjectionEvidenceCompatibility(t *testing.T) {
	for _, scenario := range []string{"current", "legacy-nil", "incomplete"} {
		t.Run(scenario, func(t *testing.T) {
			signal := record("signal", "2026-09-30T00:00:00Z")
			signal["name"] = "Rebuild signal fixture"
			for _, field := range projectionFields("SIGNALS") {
				signal[field] = refs()
			}
			f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal}}}
			srv := f.server(t)
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			materials, _ := SelectMaterials("SIGNALS")
			source := filepath.Join(t.TempDir(), "source")
			original, err := Export(context.Background(), client, Options{Output: source, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())})
			srv.Close()
			if err != nil || original.Status != "complete_for_implemented_scope" {
				t.Fatal("signal fixture export failed")
			}
			if scenario != "current" {
				file := filepath.Join(source, archiveStateFile)
				b, err := os.ReadFile(file)
				var row archiveRecord
				if err != nil || json.Unmarshal(b, &row) != nil {
					t.Fatal("fixture state unreadable")
				}
				row.ProjectionStates = nil
				if scenario == "incomplete" {
					row.ProjectionStates = map[string]string{"annotations": "empty"}
				}
				b, _ = json.Marshal(row)
				if err := os.WriteFile(file, append(b, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
				original.ArchiveState.StateSHA256, err = fileDigest(context.Background(), file)
				if err != nil {
					t.Fatal(err)
				}
				b, _ = json.Marshal(original)
				if err := os.WriteFile(filepath.Join(source, "manifest.json"), b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			out := filepath.Join(t.TempDir(), "rebuilt")
			m, err := Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: out, Scanner: scanner(t, DefaultPolicy())}})
			if scenario == "incomplete" {
				if err == nil {
					t.Fatal("partially populated evidence map accepted")
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatal("invalid evidence finalized an archive")
				}
				return
			}
			if err != nil || len(m.RelationshipCoverage) != 7 {
				t.Fatalf("valid signal reconstruction failed: %v", err)
			}
			if scenario == "current" && m.Status != "complete_for_implemented_scope" || scenario == "legacy-nil" && m.Status != "partial" {
				t.Fatal("reconstruction falsely certified absent legacy evidence")
			}
			for _, row := range m.RelationshipCoverage {
				if scenario == "current" && row.Empty != 1 || scenario == "legacy-nil" && row.Absent != 1 {
					t.Fatal("original projection evidence was not preserved conservatively")
				}
			}
		})
	}
}
