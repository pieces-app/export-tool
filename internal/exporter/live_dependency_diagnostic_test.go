package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"
)

// filterGraph's only record-store operation is removal. This diagnostic store
// discards that operation in memory and rejects every other operation. Never
// point the simulated filter at the original archive or recovery payloads.
type dependencyDiagnosticStore struct{}

func (dependencyDiagnosticStore) Open(context.Context, *Meta) (io.ReadCloser, error) {
	return nil, errConfig("diagnostic must not read canonical payloads")
}
func (dependencyDiagnosticStore) Write(context.Context, *Meta, map[string]any, bool) error {
	return errConfig("diagnostic must not write canonical payloads")
}
func (dependencyDiagnosticStore) Remove(ctx context.Context, _ *Meta) error { return ctx.Err() }
func (dependencyDiagnosticStore) Flush(context.Context) error {
	return errConfig("diagnostic must not flush canonical payloads")
}

func diagnosticDependencyFilter(source *run, removeMissingPersonLinks bool) (*run, int, error) {
	r := &run{ctx: source.ctx, opts: source.opts, meta: map[string]*Meta{}, records: dependencyDiagnosticStore{}}
	removed := 0
	for key, m := range source.meta {
		copy := *m
		copy.Edges = nil
		for _, edge := range m.Edges {
			target := source.meta[edge.Target]
			if removeMissingPersonLinks && edge.Relation == "embedded_markdown" && target != nil && target.Type == "PERSONS" && target.State == "missing" {
				removed++
				continue
			}
			copy.Edges = append(copy.Edges, edge)
		}
		r.meta[key] = &copy
	}
	return r, removed, r.filterGraph()
}

// Explicit, read-only retained-source analysis. OpenRecovery enforces exclusive
// ownership; do not run while an export/replay owns the workspace. Nothing is
// exported, no OS client is used, and no source names/IDs/text leave the test.
func TestDiagnosticRetainedDependencyGaps(t *testing.T) {
	work, keys, archive := os.Getenv("PIECES_EXPORT_DIAGNOSTIC_WORK"), os.Getenv("PIECES_EXPORT_DIAGNOSTIC_KEYS"), os.Getenv("PIECES_EXPORT_DIAGNOSTIC_ARCHIVE")
	if work == "" || keys == "" || archive == "" {
		t.Skip("explicit retained workspace, key directory and finalized archive are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	session, err := OpenRecovery(ctx, RecoveryOptions{Directory: work, KeyDirectory: keys})
	if err != nil {
		t.Fatal("retained capture could not be opened")
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error("diagnostic recovery ownership did not close cleanly")
		}
	}()
	if !session.info.CanResume || session.r.opts.Mode != "filtered" || session.r.legacySignalPrivacy {
		t.Fatal("diagnostic requires a current filtered completed-source capture")
	}
	manifest, err := InspectArchive(archive)
	if err != nil || manifest.ArchiveState == nil {
		t.Fatal("diagnostic requires a finalized archive with reconstruction evidence")
	}
	root, err := os.OpenRoot(archive)
	if err != nil {
		t.Fatal("finalized archive could not be read")
	}
	defer root.Close()
	actualStates := map[string]string{}
	if err := archiveLines(ctx, root, archiveStateFile, manifest.ArchiveState.StateSHA256, func(row archiveRecord) error {
		if row.Material == "WORKSTREAM_SUMMARIES" || row.Material == "ANNOTATIONS" {
			actualStates[row.Ref] = row.State
		}
		return nil
	}); err != nil {
		t.Fatal("final decision evidence could not be authenticated")
	}
	baseline, _, err := diagnosticDependencyFilter(session.r, false)
	if err != nil {
		t.Fatal("in-memory baseline filtering failed")
	}
	before, missing := map[string]int{}, map[string]int{}
	compared := 0
	for key, m := range baseline.meta {
		if session.r.meta[key].State == "missing" {
			missing[m.Type]++
		}
		if m.Type != "WORKSTREAM_SUMMARIES" && m.Type != "ANNOTATIONS" {
			continue
		}
		if actualStates[opaque(m.Type, m.ID)] != m.State {
			t.Fatal("dependency-only baseline does not reproduce final summary/annotation decisions")
		}
		compared++
		if m.State == "withheld" {
			before[m.Type]++
		}
	}
	if compared != len(actualStates) {
		t.Fatal("retained and finalized narrative inventories differ")
	}
	alternative, removed, err := diagnosticDependencyFilter(session.r, true)
	if err != nil {
		t.Fatal("in-memory alternative filtering failed")
	}
	after := map[string]int{}
	for _, m := range alternative.meta {
		if (m.Type == "WORKSTREAM_SUMMARIES" || m.Type == "ANNOTATIONS") && m.State == "withheld" {
			after[m.Type]++
		}
	}
	result := struct {
		Compared int            `json:"reconciled_summary_annotation_decisions"`
		Missing  map[string]int `json:"missing_source_records"`
		Before   map[string]int `json:"actual_withheld_narratives"`
		Removed  int            `json:"embedded_links_to_unavailable_persons"`
		After    map[string]int `json:"counterfactual_withheld_after_removing_only_those_links"`
	}{compared, missing, before, removed, after}
	b, _ := json.Marshal(result)
	t.Log(string(b))
	t.Log("Counterfactual only: no output changed and no missing record was invented. This does not approve a privacy-policy change or certify complete migration.")
}

func TestDependencyDiagnosticOnlyRemovesMissingPersonNavigation(t *testing.T) {
	for _, tc := range []struct {
		typ, state             string
		before, after, removed int
	}{
		{"PERSONS", "missing", 2, 0, 1},
		{"PERSONS", "excluded", 2, 2, 0},
		{"WEBSITES", "missing", 2, 2, 0},
		{"PERSONS", "included", 0, 0, 0},
	} {
		t.Run(fmt.Sprint(tc.typ, "_", tc.state), func(t *testing.T) {
			owner := &Meta{Key: "owner", Type: tc.typ, State: tc.state}
			annotation := &Meta{Key: "annotation", Type: "ANNOTATIONS", AnnotationType: "SUMMARY", State: "included", DataPath: "must-not-be-opened", Edges: []Edge{{"annotation", "owner", "embedded_markdown"}}}
			summary := &Meta{Key: "summary", Type: "WORKSTREAM_SUMMARIES", State: "included", DataPath: "must-not-be-opened", Edges: []Edge{{"summary", "annotation", "annotations"}}}
			r := &run{ctx: context.Background(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}, meta: map[string]*Meta{"owner": owner, "annotation": annotation, "summary": summary}}
			for _, remove := range []bool{false, true} {
				filtered, removed, err := diagnosticDependencyFilter(r, remove)
				if err != nil {
					t.Fatal(err)
				}
				withheld := 0
				for _, key := range []string{"summary", "annotation"} {
					if filtered.meta[key].State == "withheld" {
						withheld++
					}
				}
				want, wantRemoved := tc.before, 0
				if remove {
					want, wantRemoved = tc.after, tc.removed
				}
				if withheld != want || removed != wantRemoved || filtered.meta["owner"].State != tc.state || summary.State != "included" || annotation.State != "included" || len(annotation.Edges) != 1 {
					t.Fatal("diagnostic changed capture or removed a different dependency", withheld, removed)
				}
			}
		})
	}
}
