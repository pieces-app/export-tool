package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func groupedRangeFixture(count int) *fakeOS {
	f := junctionFixture()
	family, _ := junctionFamilyByName("workstream_summary_to_range_associations")
	for i := range count {
		id := fmt.Sprintf("range-%04d", i)
		v := record(id, "2026-10-01T12:00:00Z")
		v["from"], v["to"] = map[string]any{"value": "2026-10-01T09:00:00Z"}, map[string]any{"value": "2026-10-01T10:00:00Z"}
		v["future_metadata"] = map[string]any{"counter": json.Number("9007199254740993"), "label": "Retained range metadata"}
		f.data["RANGES"] = append(f.data["RANGES"], v)
		proof := record("range-proof-"+id, "2026-10-01T12:00:00Z")
		proof[family.leftField], proof[family.rightField] = "summary", id
		f.data[family.material().Type] = append(f.data[family.material().Type], proof)
	}
	return f
}

func TestGroupedRangeRejectsConflictingStorageAndNavigation(t *testing.T) {
	for _, scenario := range []string{"old-format", "old-state", "duplicate-row", "truncated-row", "bad-offset", "association-path"} {
		t.Run(scenario, func(t *testing.T) {
			f := groupedRangeFixture(2)
			srv := junctionServer(t, f, nil)
			c, _ := NewClient(srv.URL, time.Second, 8<<20)
			o := junctionExportOptions(t)
			m, err := Export(context.Background(), c, o)
			srv.Close()
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(o.Output, "data/ranges/group-000000.jsonl")
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "old-format":
				m.FormatVersion = 6
			case "old-state":
				m.ArchiveState.Version = 3
			case "duplicate-row":
				data = append(data, []byte(strings.Split(string(data), "\n")[0]+"\n")...)
			case "truncated-row":
				data = data[:len(data)-1]
			case "association-path":
				name = filepath.Join(o.Output, "link-map.json")
				b, _ := os.ReadFile(name)
				var links map[string]string
				if err := json.Unmarshal(b, &links); err != nil {
					t.Fatal(err)
				}
				for _, target := range links {
					if strings.HasPrefix(target, "markdown/associations/") {
						links[opaque("RANGES", "range-0000")] = target
						break
					}
				}
				data, _ = json.Marshal(links)
			case "bad-offset":
				name = filepath.Join(o.Output, archiveStateFile)
				b, _ := os.ReadFile(name)
				lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
				changed := false
				for i, line := range lines {
					var row archiveRecord
					if err := json.Unmarshal([]byte(line), &row); err != nil {
						t.Fatal(err)
					}
					if row.Material == "RANGES" {
						row.DataOffset++
						b, _ := json.Marshal(row)
						lines[i] = string(b)
						changed = true
						break
					}
				}
				if !changed {
					t.Fatal("fixture has no range location evidence")
				}
				data = []byte(strings.Join(lines, "\n") + "\n")
			}
			if scenario != "old-format" && scenario != "old-state" {
				if err := os.WriteFile(name, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "association-path" {
				m.ArchiveState.LinkMapSHA256, err = fileDigest(context.Background(), name)
			}
			if scenario == "bad-offset" {
				m.ArchiveState.StateSHA256, err = fileDigest(context.Background(), name)
			}
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(m)
			if err := os.WriteFile(filepath.Join(o.Output, "manifest.json"), b, 0600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "invalid")
			if _, err := Rebuild(context.Background(), RebuildOptions{Source: o.Output, Options: Options{Output: output, Scanner: scanner(t, DefaultPolicy())}}); err == nil {
				t.Fatal("invalid grouped range archive was accepted")
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("invalid archive finalized")
			}
			requireNoTransientAssociationStorage(t, filepath.Dir(output))
		})
	}
}

func TestGroupedRangeRecordsKeepDocumentsGraphAndExactValues(t *testing.T) {
	f := groupedRangeFixture(123)
	srv := junctionServer(t, f, nil)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	o := junctionExportOptions(t)
	checkpoint, _ := captureTestStore(t)
	o.captureCheckpoint = func(r *run) error { return r.saveCapture(checkpoint) }
	m, err := Export(context.Background(), c, o)
	srv.Close()
	if err != nil || m.FormatVersion != 7 || m.ArchiveState == nil || m.ArchiveState.Version != 4 {
		t.Fatal("grouped ranges did not produce the new archive contract", err)
	}
	before, err := inspectFinalArchive(context.Background(), o.Output)
	if err != nil {
		t.Fatal(err)
	}
	check := func(directory string) {
		t.Helper()
		chunks, err := os.ReadDir(filepath.Join(directory, "data", "ranges"))
		if err != nil || len(chunks) != 3 {
			t.Fatal("123 canonical ranges did not use three chunks", err)
		}
		documents, err := os.ReadDir(filepath.Join(directory, "markdown", "ranges"))
		if err != nil || len(documents) != 123 {
			t.Fatal("individual range documents were lost", err)
		}
		for _, want := range f.data["RANGES"] {
			got, _, err := readArchiveFixtureRecord(directory, "RANGES", fieldString(want, "id"))
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal("canonical range values changed", err)
			}
		}
		after, err := inspectFinalArchive(context.Background(), directory)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("counts, bodies, graph or decisions changed", err)
		}
		if err := (&run{ctx: context.Background(), stage: directory}).validateMarkdownLinks(); err != nil {
			t.Fatal(err)
		}
		requireNoTransientAssociationStorage(t, filepath.Dir(directory))
	}
	check(o.Output)
	for _, mode := range []string{"replay", "rebuild", "rebuild-again"} {
		output := filepath.Join(t.TempDir(), "archive")
		if mode == "replay" {
			_, err = replayCapture(context.Background(), checkpoint, output, nil)
		} else {
			_, err = Rebuild(context.Background(), RebuildOptions{Source: o.Output, Options: Options{Output: output, Scanner: scanner(t, DefaultPolicy())}})
		}
		if err != nil {
			t.Fatal(mode, err)
		}
		moved := output + "-moved"
		if err := os.Rename(output, moved); err != nil {
			t.Fatal(err)
		}
		check(moved)
		o.Output = moved
	}
}

func TestOlderPackagedRangeArchiveUpgrade(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_LEGACY_RANGE_BINARY")
	if binary == "" {
		t.Skip("set an actual pre-format-7 release executable")
	}
	f := groupedRangeFixture(2)
	srv := junctionServer(t, f, nil)
	root := t.TempDir()
	source := filepath.Join(root, "legacy")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--format", "both", "--metadata", "off", "--output", source)
	output, err := cmd.CombinedOutput()
	srv.Close()
	if err != nil {
		t.Fatalf("legacy fixture export failed: %v\n%s", err, output)
	}
	m, err := InspectArchive(source)
	if err != nil || m.FormatVersion != 6 {
		t.Fatal("fixture is not a legacy format-6 archive", err)
	}
	before, err := inspectFinalArchive(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "upgraded")
	m, err = Rebuild(ctx, RebuildOptions{Source: source, Options: Options{Output: destination, Format: "both", Scanner: scanner(t, DefaultPolicy())}})
	if err != nil || m.FormatVersion != 7 || m.ArchiveState.Version != 4 {
		t.Fatal("legacy canonical ranges did not upgrade", err)
	}
	moved := destination + "-moved"
	if err := os.Rename(destination, moved); err != nil {
		t.Fatal(err)
	}
	after, err := inspectFinalArchive(ctx, moved)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("legacy upgrade lost evidence", err)
	}
	if err := (&run{ctx: ctx, stage: moved}).validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := (&run{ctx: ctx, stage: moved, opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}).auditOutput(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupedRangeLatePrivateIdentityIsRemovedBeforePublication(t *testing.T) {
	f := groupedRangeFixture(2)
	srv := junctionServer(t, f, nil)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	o := junctionExportOptions(t)
	checkpoint, _ := captureTestStore(t)
	late := "range-0001"
	o.captureCheckpoint = func(r *run) error {
		m := r.meta["RANGES\x00"+late]
		if m == nil || m.State != "included" {
			t.Fatal("fixture did not initially retain the range")
		}
		v, err := r.readCanonical(m)
		if err != nil || fieldString(v, "id") != late {
			t.Fatal("range did not enter staging", err)
		}
		r.opts.Scanner.remember(late)
		return r.saveCapture(checkpoint)
	}
	if _, err := Export(context.Background(), c, o); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	before, err := inspectFinalArchive(context.Background(), o.Output)
	if err != nil {
		t.Fatal(err)
	}
	if before.States["withheld"] == 0 {
		t.Fatal("late private identity did not withhold staged records")
	}
	requireNoCapturePlaintext(t, o.Output, late)
	if _, _, err := readArchiveFixtureRecord(o.Output, "RANGES", late); !os.IsNotExist(err) {
		t.Fatal("private range was materialized", err)
	}
	if _, _, err := readArchiveFixtureRecord(o.Output, "RANGES", "range-0000"); err != nil {
		t.Fatal("adjacent retained range was lost", err)
	}
	output := filepath.Join(t.TempDir(), "replayed")
	if _, err := replayCapture(context.Background(), checkpoint, output, nil); err != nil {
		t.Fatal(err)
	}
	after, err := inspectFinalArchive(context.Background(), output)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("replay changed late privacy decisions", err)
	}
	requireNoCapturePlaintext(t, output, late)
	requireNoTransientAssociationStorage(t, filepath.Dir(o.Output))
	requireNoTransientAssociationStorage(t, filepath.Dir(output))
}
