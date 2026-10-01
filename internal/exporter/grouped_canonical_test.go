package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGroupedCanonicalPreservesIndividualDocumentsAndCapture(t *testing.T) {
	ctx := context.Background()
	f := groupedRangeFixture(61)
	for i := range 61 {
		sid, aid := fmt.Sprintf("summary-%03d", i), fmt.Sprintf("body-%03d", i)
		s, a := record(sid, "2026-10-01T12:00:00Z"), record(aid, "2026-10-01T12:00:00Z")
		s["name"], s["parentHierarchicalType"] = "Individually readable summary", "TEMPORAL_DAY_HIERARCHICAL_SUMMARY"
		a["text"], a["type"] = fmt.Sprintf("Retained exact narrative %d. [Profile](pieces://persons/person).", i), "SUMMARY"
		a["future_metadata"] = map[string]any{"counter": json.Number("9007199254740993"), "sequence": []any{true, "text", nil}}
		f.data["WORKSTREAM_SUMMARIES"] = append(f.data["WORKSTREAM_SUMMARIES"], s)
		f.data["ANNOTATIONS"] = append(f.data["ANNOTATIONS"], a)
		for _, pair := range [][3]string{{"workstream_summary_to_annotation_associations", sid, aid}, {"workstream_summary_to_person_associations", sid, "person"}} {
			family, _ := junctionFamilyByName(pair[0])
			v := record(pair[0]+"-"+sid, "2026-10-01T12:00:00Z")
			v[family.leftField], v[family.rightField] = pair[1], pair[2]
			f.data[family.material().Type] = append(f.data[family.material().Type], v)
		}
	}
	srv := junctionServer(t, f, nil)
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	o := junctionExportOptions(t)
	checkpoint, _ := captureTestStore(t)
	o.captureCheckpoint = func(r *run) error { return r.saveCapture(checkpoint) }
	m, err := Export(ctx, client, o)
	srv.Close()
	if err != nil || m.FormatVersion != 8 || m.ArchiveState == nil || m.ArchiveState.Version != 5 {
		t.Fatal("grouped canonical export failed", err)
	}
	expected, err := inspectFinalArchive(ctx, o.Output)
	if err != nil || expected.Summaries != 62 || expected.SummariesWithBody != 62 || expected.Persons != 1 {
		t.Fatal("source documents/body coverage differ", err)
	}
	compare := func(directory string) {
		t.Helper()
		report, err := inspectFinalArchive(ctx, directory)
		if err != nil || !reflect.DeepEqual(report, expected) {
			t.Fatal("grouping lost record, body, profile or graph evidence", err)
		}
		manifest, err := InspectArchive(directory)
		if err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(directory)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		used := map[string]bool{}
		links, err := archiveLinkMap(ctx, root, report.Included, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range manifest.Coverage {
			if c.Included == 0 {
				continue
			}
			material, _ := materialByType(c.Material)
			count := 0
			err := archiveCollection(ctx, root, material, manifest.Mode, manifest.FormatVersion, func(ref string, b []byte, path string, offset int64) error {
				if !strings.HasSuffix(path, ".jsonl") {
					t.Fatal("ordinary canonical file remained")
				}
				if _, association := associationFamilyByType(material.Type); !association {
					if used[links[ref]] || strings.Contains(links[ref], "/group-") {
						t.Fatal("user documents unexpectedly share a page")
					}
					used[links[ref]] = true
				}
				count++
				return nil
			})
			if err != nil || count != c.Included {
				t.Fatal("canonical chunks lost identities", err)
			}
		}
		for _, typ := range []string{"WORKSTREAM_SUMMARIES", "ANNOTATIONS"} {
			material, _ := materialByType(typ)
			files, err := os.ReadDir(filepath.Join(directory, "data", material.Folder))
			if err != nil || len(files) != 2 {
				t.Fatal("ordinary multi-chunk material was not consolidated", err)
			}
		}
		for _, want := range f.data["ANNOTATIONS"] {
			id := fieldString(want, "id")
			if strings.HasPrefix(id, "unrelated-") {
				continue
			}
			got, _, err := readArchiveFixtureRecord(directory, "ANNOTATIONS", id)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal("annotation body or exact unknown fields changed", err)
			}
		}
		requireNoTransientAssociationStorage(t, filepath.Dir(directory))
	}
	compare(o.Output)
	original := archiveHashes(t, o.Output)
	replay := filepath.Join(t.TempDir(), "replayed")
	if _, err := replayCapture(ctx, checkpoint, replay, nil); err != nil {
		t.Fatal(err)
	}
	compare(replay)
	for path, hash := range archiveHashes(t, replay) {
		if path != "manifest.json" && path != localPerformanceFile && original[path] != hash {
			t.Fatal("captured replay changed a document or canonical record")
		}
	}
	for range 2 {
		output := filepath.Join(t.TempDir(), "rebuilt")
		if _, err := Rebuild(ctx, RebuildOptions{Source: replay, Options: Options{Output: output, Scanner: scanner(t, DefaultPolicy())}}); err != nil {
			t.Fatal(err)
		}
		moved := output + "-moved"
		if err := os.Rename(output, moved); err != nil {
			t.Fatal(err)
		}
		compare(moved)
		replay = moved
	}
}
