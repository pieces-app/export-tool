package exporter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func missingJunctionFixture(profile bool) *fakeOS {
	f := junctionFixture()
	family, _ := junctionFamilyByName("workstream_summary_to_person_associations")
	v := record("unavailable-person-binding", "2026-09-30T12:00:00Z")
	v[family.leftField], v[family.rightField] = "summary", "unavailable-person"
	f.data[family.material().Type] = append(f.data[family.material().Type], v)
	if profile {
		family, _ = junctionFamilyByName("person_to_annotation_associations")
		v = record("unavailable-person-profile-binding", "2026-09-30T12:00:00Z")
		v[family.leftField], v[family.rightField] = "unavailable-person", "available-profile"
		f.data[family.material().Type] = append(f.data[family.material().Type], v)
		body := record("available-profile", "2026-09-30T12:00:00Z")
		body["text"], body["type"] = "Retained profile annotation; its owner snapshot is unavailable.", "HIERARCHICAL_PROFILE_SUMMARY"
		f.data["ANNOTATIONS"] = append(f.data["ANNOTATIONS"], body)
	}
	return f
}

func TestMissingJunctionOwnerDoesNotForceGlobalAnnotationInventory(t *testing.T) {
	for _, profile := range []bool{false, true} {
		t.Run(fmt.Sprint(profile), func(t *testing.T) {
			f := missingJunctionFixture(profile)
			srv := junctionServer(t, f, nil)
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			o := junctionExportOptions(t)
			store, _ := captureTestStore(t)
			o.captureCheckpoint = func(r *run) error {
				missing := r.meta["PERSONS\x00unavailable-person"]
				if missing == nil || missing.State != "missing" || !missing.JunctionFields["annotations"] || missing.DataPath != "" {
					return fmt.Errorf("missing owner was invented or its indexed annotations were not checked")
				}
				return r.saveCapture(store)
			}
			m, err := Export(context.Background(), client, o)
			if err != nil || m.Status != "partial" {
				t.Fatal("missing source record was repaired or export failed", err)
			}
			for _, typ := range f.requestedMaterials {
				if typ == "ANNOTATIONS" {
					t.Fatal("unavailable owner triggered a global annotation inventory")
				}
			}
			for _, cov := range m.Coverage {
				if cov.Material == "ANNOTATIONS" {
					want := 2
					if profile {
						want++
					}
					if cov.InventoryMode != "references" || cov.Included != want {
						t.Fatal("linked annotation coverage differs", cov)
					}
				}
			}
			for _, row := range m.Junctions {
				if row.Family == "person_to_annotation_associations" && (row.Owners != 2 || row.Reconciled != 2) {
					t.Fatal("missing owner lacks counted evidence", row)
				}
			}
			srv.Close()
			before, err := inspectFinalArchive(context.Background(), o.Output)
			if err != nil {
				t.Fatal(err)
			}
			for i := range 2 {
				out := filepath.Join(t.TempDir(), "archive")
				if i == 0 {
					_, err = replayCapture(context.Background(), store, out, nil)
				} else {
					_, err = Rebuild(context.Background(), RebuildOptions{Source: o.Output, Options: Options{Output: out, Scanner: scanner(t, DefaultPolicy())}})
				}
				if err != nil {
					t.Fatal("missing-owner source evidence did not replay", err)
				}
				after, err := inspectFinalArchive(context.Background(), out)
				if err != nil || after.Included != before.Included || after.States["missing"] != before.States["missing"] || after.Edges != before.Edges || after.SummariesWithBody != before.SummariesWithBody {
					t.Fatal("reconstruction lost records, decisions or graph edges", err)
				}
			}
		})
	}
}

func TestMissingJunctionOwnerCannotInventEmptyEvidence(t *testing.T) {
	f := missingJunctionFixture(true)
	srv := junctionServer(t, f, func(req *http.Request, out map[string]any) {
		if strings.HasSuffix(req.URL.Path, "/person/unavailable-person/count") && strings.Contains(req.URL.Path, "person_to_annotation_associations") {
			out["count"] = -1
		}
	})
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	o := junctionExportOptions(t)
	if _, err := Export(context.Background(), client, o); err == nil {
		t.Fatal("unresolved missing-owner evidence was treated as empty")
	}
	if _, err := os.Stat(o.Output); !os.IsNotExist(err) {
		t.Fatal("unresolved owner finalized")
	}
	requireNoTransientAssociationStorage(t, filepath.Dir(o.Output))
}

func TestMissingSummaryOwnerPreservesCaptureEvidence(t *testing.T) {
	f := junctionFixture()
	family, _ := junctionFamilyByName("workstream_summary_to_person_associations")
	v := record("unavailable-summary-binding", "2026-09-30T12:00:00Z")
	v[family.leftField], v[family.rightField] = "unavailable-summary", "person"
	f.data[family.material().Type] = append(f.data[family.material().Type], v)
	srv := junctionServer(t, f, nil)
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	o := junctionExportOptions(t)
	store, _ := captureTestStore(t)
	o.captureCheckpoint = func(r *run) error {
		missing := r.meta["WORKSTREAM_SUMMARIES\x00unavailable-summary"]
		if missing == nil || missing.State != "missing" || !missing.JunctionFields["annotations"] {
			return fmt.Errorf("unavailable summary annotation side was not checked")
		}
		return r.saveCapture(store)
	}
	m, err := Export(context.Background(), client, o)
	if err != nil || m.Status != "partial" {
		t.Fatal("unavailable summary was repaired or failed", err)
	}
	for _, typ := range f.requestedMaterials {
		if typ == "ANNOTATIONS" {
			t.Fatal("unavailable summary forced global annotation inventory")
		}
	}
	srv.Close()
	out := filepath.Join(t.TempDir(), "replay")
	if _, err := replayCapture(context.Background(), store, out, nil); err != nil {
		t.Fatal("missing-summary junction evidence did not survive capture", err)
	}
	counts, err := inspectFinalArchive(context.Background(), out)
	if err != nil || counts.Summaries != 1 || counts.States["missing"] != 1 {
		t.Fatal("missing summary decision did not reconcile", err)
	}
}

func TestPackagedMissingJunctionOwnerCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to a compiled candidate")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	f := missingJunctionFixture(true)
	srv := junctionServer(t, f, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	parent := t.TempDir()
	out, work, keys := filepath.Join(parent, "archive"), filepath.Join(parent, "work"), filepath.Join(parent, "keys")
	runPartial := func(args ...string) {
		t.Helper()
		b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		var status *exec.ExitError
		if !errors.As(err, &status) || status.ExitCode() != 2 {
			t.Fatalf("missing-owner CLI did not retain partial status: %v\n%s", err, b)
		}
	}
	runPartial("export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--format", "markdown", "--metadata", "off", "--output", out, "--work", work, "--recovery-keys", keys)
	annotationReads := 0
	for _, typ := range f.requestedMaterials {
		if typ == "ANNOTATIONS" {
			annotationReads++
		}
	}
	// The CLI preflight counts annotations and samples at most three IDs.
	// A global fallback would add its own metrics/identifier requests.
	if annotationReads != 2 {
		t.Fatal("compiled CLI performed annotation inventory beyond bounded preflight", annotationReads)
	}
	srv.Close()
	replayed := filepath.Join(parent, "replayed")
	runPartial("resume", "--work", work, "--recovery-keys", keys, "--output", replayed, "--yes")
	for _, directory := range []string{out, replayed} {
		m, err := InspectArchive(directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, cov := range m.Coverage {
			if cov.Material == "ANNOTATIONS" && (cov.InventoryMode != "references" || cov.Included != 3) {
				t.Fatal("compiled source/replay lost linked-only coverage", cov)
			}
		}
		counts, err := inspectFinalArchive(ctx, directory)
		if err != nil || counts.States["missing"] != 1 || counts.SummariesWithBody != 1 {
			t.Fatal("compiled archive decisions or bodies did not reconcile", err)
		}
	}
	requireNoTransientAssociationStorage(t, parent)
}
