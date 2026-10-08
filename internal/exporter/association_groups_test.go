package exporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func readArchiveFixtureRecord(directory, typ, id string) (map[string]any, string, error) {
	m, err := InspectArchive(directory)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	material, ok := materialByType(typ)
	if !ok {
		return nil, "", errConfig("unknown fixture material")
	}
	var result map[string]any
	var name string
	err = archiveCollection(context.Background(), root, material, m.Mode, m.FormatVersion, func(ref string, b []byte, file string, _ int64) error {
		if ref == opaque(typ, id) {
			name = filepath.Join(directory, file)
			return decodeArchiveJSON(b, &result)
		}
		return nil
	})
	if err == nil && result == nil {
		err = os.ErrNotExist
	}
	return result, name, err
}

func groupedJunctionFixture(count int) *fakeOS {
	f := junctionFixture()
	family, _ := junctionFamilyByName("workstream_summary_to_annotation_associations")
	f.data[family.material().Type] = nil
	for i := range count {
		id := fmt.Sprintf("grouped-body-%04d", i)
		body := record(id, "2026-09-30T12:00:00Z")
		body["type"], body["text"] = "SUMMARY", "Grouped narrative "+id
		f.data["ANNOTATIONS"] = append(f.data["ANNOTATIONS"], body)
		v := record(fmt.Sprintf("grouped-proof-%04d", i), "2026-09-30T12:00:00Z")
		v[family.leftField], v[family.rightField] = "summary", id
		v["confidence"] = json.Number("0.875")
		v["future_metadata"] = map[string]any{"counter": json.Number("9007199254740993")}
		f.data[family.material().Type] = append(f.data[family.material().Type], v)
	}
	return f
}

func requireNoTransientAssociationStorage(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pieces-export-stage-") {
			t.Fatal("temporary association database/key directory was retained")
		}
	}
}

func TestGroupedAssociationCleanupPreservesReplacementDirectory(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("TMPDIR", parent)
	r := &run{ctx: context.Background(), stage: filepath.Join(parent, "archive.partial")}
	s := r.canonicalRecords().(*associationCanonicalRecords)
	if err := s.ensure(r.ctx); err != nil {
		t.Fatal(err)
	}
	defer r.cleanupCanonicalStage()
	original := s.parent + "-moved"
	if err := os.Rename(s.parent, original); err != nil {
		t.Skip("platform does not allow moving this open directory")
	}
	if err := os.MkdirAll(filepath.Join(s.parent, "records"), 0700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(s.parent, "records", "unrelated.txt")
	if err := os.WriteFile(keep, []byte("keep this unrelated file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.closeCanonicalStage(); err == nil {
		t.Fatal("replacement directory identity was accepted")
	}
	b, err := os.ReadFile(keep)
	if err != nil || string(b) != "keep this unrelated file" {
		t.Fatal("cleanup touched the replacement directory", err)
	}
	for _, name := range []string{"records", "keys"} {
		if _, err := os.Stat(filepath.Join(original, name)); !os.IsNotExist(err) {
			t.Fatal("held original storage was not cleaned")
		}
	}
}

func TestGroupedAssociationsPreserveIdentitiesAcrossExportReplayRebuild(t *testing.T) {
	transient := t.TempDir()
	t.Setenv("TMPDIR", transient)
	f := groupedJunctionFixture(123)
	srv := junctionServer(t, f, nil)
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	o := junctionExportOptions(t)
	o.Format = "both"
	checkpoint, _ := captureTestStore(t)
	o.captureCheckpoint = func(r *run) error { return r.saveCapture(checkpoint) }
	m, err := Export(context.Background(), client, o)
	srv.Close()
	if err != nil || m.FormatVersion != 6 || m.ArchiveState.Version != 3 {
		t.Fatal("grouped export failed", err)
	}
	requireNoTransientAssociationStorage(t, transient)
	family, _ := junctionFamilyByName("workstream_summary_to_annotation_associations")
	files, err := os.ReadDir(filepath.Join(o.Output, "data", family.material().Folder))
	if err != nil || len(files) != 3 {
		t.Fatal("123 association rows did not become three canonical chunks", err)
	}
	files, err = os.ReadDir(filepath.Join(o.Output, "markdown", family.material().Folder))
	if err != nil || len(files) != 3 {
		t.Fatal("123 association records did not become three navigation pages", err)
	}
	var links map[string]string
	b, _ := os.ReadFile(filepath.Join(o.Output, "link-map.json"))
	if err := json.Unmarshal(b, &links); err != nil {
		t.Fatal(err)
	}
	first, second := opaque(family.material().Type, "grouped-proof-0000"), opaque(family.material().Type, "grouped-proof-0001")
	if links[first] == "" || links[first] != links[second] {
		t.Fatal("fixture does not exercise shared document identity")
	}
	root, _ := os.OpenRoot(o.Output)
	defer root.Close()
	proofs := map[string]int{}
	if err := archiveLines(context.Background(), root, "relationships.jsonl", m.ArchiveState.GraphSHA256, func(e PublicEdge) error {
		if e.Source == links[first] {
			proofs[e.SourceRef]++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 50 || proofs[first] != 2 || proofs[second] != 2 {
		t.Fatal("shared navigation conflated association endpoint edges")
	}
	before, err := inspectFinalArchive(context.Background(), o.Output)
	if err != nil {
		t.Fatal(err)
	}
	for mode := range 3 {
		out := filepath.Join(t.TempDir(), "archive")
		if mode == 0 {
			_, err = replayCapture(context.Background(), checkpoint, out, nil)
		} else {
			_, err = Rebuild(context.Background(), RebuildOptions{Source: o.Output, Options: Options{Output: out, Scanner: scanner(t, DefaultPolicy())}})
		}
		if err != nil {
			t.Fatal("grouped reconstruction failed", err)
		}
		after, err := inspectFinalArchive(context.Background(), out)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("grouped reconstruction changed counts, graph, bodies or decisions", err)
		}
		requireNoTransientAssociationStorage(t, transient)
		moved := out + "-moved"
		if err := os.Rename(out, moved); err != nil {
			t.Fatal(err)
		}
		check := &run{ctx: context.Background(), stage: moved}
		if err := check.validateMarkdownLinks(); err != nil {
			t.Fatal(err)
		}
		o.Output = moved
	}
}

func TestGroupedAssociationBoundsAndEncryptedStagingCleanup(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("TMPDIR", parent)
	r := &run{ctx: context.Background(), stage: filepath.Join(parent, "archive.partial"), opts: Options{Mode: "preserve"}, meta: map[string]*Meta{}, local: newLocalMeasurements()}
	if err := os.Mkdir(r.stage, 0700); err != nil {
		t.Fatal(err)
	}
	defer r.cleanupCanonicalStage()
	family, _ := junctionFamilyByName("workstream_summary_to_annotation_associations")
	material := family.material()
	for i := range 6 {
		id := fmt.Sprintf("association-%d", i)
		m := &Meta{Key: material.Type + "\x00" + id, ID: id, Type: material.Type, Folder: material.Folder, State: "included", DataPath: "raw/" + material.Folder + "/" + opaque(material.Type, id) + ".json"}
		v := map[string]any{"id": id, "summary": "summary", "annotation": "body", "explanation": strings.Repeat("private staging marker; ", 140000)}
		if i == 5 {
			v["explanation"] = strings.Repeat("x", 8<<20)
		}
		if err := r.writeCanonical(m, v, false); err != nil {
			t.Fatal(err)
		}
		r.meta[m.Key] = m
	}
	if err := r.canonicalRecords().Flush(r.ctx); err != nil {
		t.Fatal(err)
	}
	requireNoCapturePlaintext(t, r.canonicalStage.parent, "private staging marker")
	if err := r.prepareAssociationGroups(); err != nil {
		t.Fatal(err)
	}
	if len(r.associationGroups) != 4 || len(r.associationGroups[3].members) != 1 {
		t.Fatal("canonical byte limit did not split groups")
	}
	if err := r.closeCanonicalStage(); err != nil {
		t.Fatal(err)
	}
	requireNoTransientAssociationStorage(t, parent)
	for _, m := range r.meta {
		v, err := r.readCanonical(m)
		size := len("private staging marker; ") * 140000
		if m.ID == "association-5" {
			size = 8 << 20
		}
		if err != nil || fieldString(v, "id") != m.ID || len(fieldString(v, "explanation")) != size {
			t.Fatal("materialized row cannot be read independently", err)
		}
	}
}

func TestGroupedAssociationLegacyArchiveUpgrade(t *testing.T) {
	f := groupedJunctionFixture(3)
	srv := junctionServer(t, f, nil)
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	o := junctionExportOptions(t)
	o.captureCheckpoint = func(r *run) error {
		files := &fileCanonicalRecords{run: r}
		for _, m := range r.meta {
			if _, ok := associationFamilyByType(m.Type); !ok || m.State != "included" {
				continue
			}
			v, err := r.readCanonical(m)
			if err != nil {
				return err
			}
			if err := files.Write(r.ctx, m, v, false); err != nil {
				return err
			}
		}
		r.records = files // Produce the actual legacy per-record format for this fixture.
		return nil
	}
	m, err := Export(context.Background(), client, o)
	if err != nil || m.FormatVersion != 5 || m.ArchiveState.Version != 2 {
		t.Fatal("legacy fixture failed", err)
	}
	before, err := inspectFinalArchive(context.Background(), o.Output)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "upgraded")
	next, err := Rebuild(context.Background(), RebuildOptions{Source: o.Output, Options: Options{Output: out, Scanner: scanner(t, DefaultPolicy())}})
	if err != nil || next.FormatVersion != 6 || next.ArchiveState.Version != 3 {
		t.Fatal("legacy archive did not upgrade", err)
	}
	after, err := inspectFinalArchive(context.Background(), out)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("legacy upgrade lost retained data", err)
	}
}

func TestGroupedAssociationArchiveRejectsConflictingEvidence(t *testing.T) {
	for _, scenario := range []string{"missing-record-ref", "wrong-ref-shared-path", "wrong-row-offset", "duplicate-row", "truncated-row", "extra-empty-chunk", "symlink", "old-reader-version"} {
		t.Run(scenario, func(t *testing.T) {
			transient := t.TempDir()
			t.Setenv("TMPDIR", transient)
			f := groupedJunctionFixture(2)
			srv := junctionServer(t, f, nil)
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			o := junctionExportOptions(t)
			m, err := Export(context.Background(), client, o)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(o.Output, "data/associations/workstream_summary_to_annotation_associations/group-000000.jsonl")
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing-record-ref", "wrong-ref-shared-path":
				file = filepath.Join(o.Output, "relationships.jsonl")
				data, _ = os.ReadFile(file)
				lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
				changed := false
				for i, line := range lines {
					var row PublicEdge
					if err := json.Unmarshal([]byte(line), &row); err != nil {
						t.Fatal(err)
					}
					if row.SourceRef != opaque("WORKSTREAM_SUMMARY_TO_ANNOTATION_ASSOCIATIONS", "grouped-proof-0000") || row.Relation != "annotations" {
						continue
					}
					if scenario == "missing-record-ref" {
						row.SourceRef = ""
					} else {
						row.SourceRef = opaque("WORKSTREAM_SUMMARY_TO_ANNOTATION_ASSOCIATIONS", "grouped-proof-0001")
					}
					b, _ := json.Marshal(row)
					lines[i], changed = string(b), true
				}
				if !changed {
					t.Fatal("tamper fixture did not find edge")
				}
				data = []byte(strings.Join(lines, "\n") + "\n")
			case "wrong-row-offset":
				file = filepath.Join(o.Output, archiveStateFile)
				data, _ = os.ReadFile(file)
				lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
				for i, line := range lines {
					var row archiveRecord
					if err := json.Unmarshal([]byte(line), &row); err != nil {
						t.Fatal(err)
					}
					if row.DataLength == 0 {
						continue
					}
					row.DataOffset++
					b, _ := json.Marshal(row)
					lines[i] = string(b)
					break
				}
				data = []byte(strings.Join(lines, "\n") + "\n")
			case "duplicate-row":
				data = append(data, []byte(strings.Split(string(data), "\n")[0]+"\n")...)
			case "truncated-row":
				data = data[:len(data)-1]
			case "extra-empty-chunk":
				file = filepath.Join(filepath.Dir(file), "group-000001.jsonl")
				data = nil
			case "symlink":
				outside := filepath.Join(t.TempDir(), "row.jsonl")
				if err := os.WriteFile(outside, data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, file); err != nil {
					t.Skip("symlinks unavailable")
				}
			case "old-reader-version":
				m.FormatVersion = 5
			}
			if scenario != "symlink" && scenario != "old-reader-version" {
				if err := os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing-record-ref" || scenario == "wrong-ref-shared-path" {
				m.ArchiveState.GraphSHA256, err = fileDigest(context.Background(), file)
			}
			if scenario == "wrong-row-offset" {
				m.ArchiveState.StateSHA256, err = fileDigest(context.Background(), file)
			}
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(m)
			if err := os.WriteFile(filepath.Join(o.Output, "manifest.json"), b, 0600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "invalid-rebuild")
			_, err = Rebuild(context.Background(), RebuildOptions{Source: o.Output, Options: Options{Output: out, Scanner: scanner(t, DefaultPolicy())}})
			if err == nil {
				t.Fatal("conflicting grouped archive finalized")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("invalid archive has final output")
			}
			requireNoTransientAssociationStorage(t, transient)
		})
	}
}

func TestGroupedAssociationFailureNeverFinalizesOrRetainsPrivateStage(t *testing.T) {
	for _, scenario := range []string{"canceled", "store-closed", "public-file-exists"} {
		t.Run(scenario, func(t *testing.T) {
			transient := t.TempDir()
			t.Setenv("TMPDIR", transient)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := junctionFixture()
			srv := junctionServer(t, f, nil)
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			o := junctionExportOptions(t)
			o.captureCheckpoint = func(r *run) error {
				switch scenario {
				case "canceled":
					cancel()
				case "store-closed":
					return r.canonicalStage.store.Close()
				case "public-file-exists":
					path := filepath.Join(r.stage, "data/associations/workstream_summary_to_annotation_associations/group-000000.jsonl")
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						return err
					}
					return os.WriteFile(path, []byte("existing fixture\n"), 0600)
				}
				return nil
			}
			m, err := Export(ctx, client, o)
			if err == nil || m.Status == "complete_for_implemented_scope" {
				t.Fatal("failed storage finalized")
			}
			if _, err := os.Stat(o.Output); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("failed export has a finalized destination")
			}
			requireNoTransientAssociationStorage(t, transient)
		})
	}
}
