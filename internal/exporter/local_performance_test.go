package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func readLocalPerformance(t *testing.T, directory string) *LocalPerformanceReport {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(directory, localPerformanceFile))
	if err != nil {
		t.Fatal(err)
	}
	var report LocalPerformanceReport
	if err := json.Unmarshal(b, &report); err != nil || report.Version != 1 {
		t.Fatalf("invalid performance report: %v", err)
	}
	for _, private := range []string{directory, "PRIVATE_EXCLUDED_RECORD_ID", "PRIVATE_EXCLUDED_TEXT", fakeSecret()} {
		if strings.Contains(string(b), private) {
			t.Fatal("performance report contains private fixture data")
		}
	}
	return &report
}

func TestLocalPerformanceIOAndFailures(t *testing.T) {
	r := &run{stage: t.TempDir(), local: newLocalMeasurements()}
	p := filepath.Join(r.stage, "record.json")
	if err := r.writeJSON(p, map[string]any{"number": json.Number("123456789012345678")}); err != nil {
		t.Fatal(err)
	}
	if err := r.writeJSON(p, nil); !errors.Is(err, os.ErrExist) {
		t.Fatal("exclusive write behavior changed")
	}
	v, err := r.readRecord(p)
	if err != nil || v["number"] != json.Number("123456789012345678") {
		t.Fatal("measured read lost numeric precision")
	}
	if err := r.rewriteJSON(p, map[string]any{"changed": true}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(filepath.Join(r.stage, "stream.jsonl"), []int{1, 2}, r.local); err != nil {
		t.Fatal(err)
	}
	stats := r.local.snapshot()
	if stats["artifact_write"].Calls != 4 || stats["artifact_write"].Failures != 1 || stats["artifact_sync"].Calls != 3 || stats["canonical_rename"].Calls != 1 || stats["canonical_json_read"].Calls != 1 || stats["canonical_json_read"].Bytes == 0 {
		t.Fatalf("incorrect persistence accounting: %+v", stats)
	}
	b, _ := os.ReadFile(p)
	if stats["artifact_write"].Bytes != stats["canonical_json_read"].Bytes+int64(len(b))+4 {
		t.Fatal("logical bytes do not match actual generated/read files")
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := r.local.syncFile(f); err == nil || r.local.snapshot()["artifact_sync"].Failures != 1 {
		t.Fatal("sync failure was hidden")
	}
}

func TestLocalPerformanceRepeatedPhasesAndSnapshots(t *testing.T) {
	m := newLocalMeasurements()
	var output bytes.Buffer
	p := startMeasuredProgress(&output, nil, m)
	defer p.Close()
	p.Stage("Audit filtered output", 0)
	m.record("record_scan", 1700*time.Microsecond, 10, nil)
	p.Add(3)
	p.Stage("Render Markdown", 2)
	m.record("artifact_write", 2800*time.Microsecond, 20, nil)
	p.Add(2)
	p.Stage("Audit filtered output", 0)
	m.record("record_scan", 1700*time.Microsecond, 15, nil)
	p.Add(4)
	rows := p.measurements()
	if len(rows) != 2 || rows[0].Visits != 2 || rows[0].Processed != 7 || rows[0].UnknownTotalVisits != 2 || rows[0].Operations["record_scan"].Milliseconds != 3 || rows[1].KnownTotal != 2 || rows[1].Operations["artifact_write"].Calls != 1 {
		t.Fatalf("phase aggregation changed: %+v", rows)
	}
	rows[0].Operations["record_scan"] = OperationMeasurement{Calls: 999}
	if p.measurements()[0].Operations["record_scan"].Calls != 2 {
		t.Fatal("snapshot aliases live counters")
	}
	p.print()
	// Use the same lock as the progress writer when reading the buffer.
	safe := p.out.(*lockedOutput)
	safe.mu.Lock()
	text := output.String()
	safe.mu.Unlock()
	if !strings.Contains(text, "4 processed (total unknown)") || !strings.Contains(text, "file writes 1") {
		t.Fatal("unknown-size phase progress or file counters missing")
	}
}

func TestLocalPerformanceExportAndOfflineRebuild(t *testing.T) {
	source, policy, original := createRebuildFixture(t)
	first := readLocalPerformance(t, source)
	if !reflect.DeepEqual(first, original.LocalPerformance) || first.State != "finalizing" || first.Operations["privacy_unchanged"].Calls == 0 || first.Operations["render_unchanged"].Calls == 0 || len(first.Phases) < 5 {
		t.Fatalf("export measurements missing, inconsistent, or not collected without terminal output: %+v", first)
	}
	out := filepath.Join(t.TempDir(), "rebuilt")
	m, err := Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: out, Scanner: scanner(t, policy)}})
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := readLocalPerformance(t, out)
	if !reflect.DeepEqual(first, m.Rebuild.SourceLocalPerformance) || !reflect.DeepEqual(rebuilt, m.LocalPerformance) || rebuilt.HTTP.Requests != 0 || rebuilt.Operations["record_scan"].Calls == 0 {
		t.Fatal("offline rebuild must keep source measurements separate from fresh local counters")
	}
}

func TestLocalPerformanceFinalizationCancellationAndCollision(t *testing.T) {
	source, policy, _ := createRebuildFixture(t)
	for _, collision := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "report_collision"}[collision], func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "output")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var hookErr error
			w := &phaseHookWriter{phase: "Stage: Prepare final diagnostic reports", hook: func() {
				if collision {
					hookErr = os.WriteFile(filepath.Join(out+".partial", localPerformanceFile), []byte("unrelated sentinel"), 0600)
				} else {
					cancel()
				}
			}}
			m, err := Rebuild(ctx, RebuildOptions{Source: source, Options: Options{Output: out, Scanner: scanner(t, policy), Progress: w}})
			if hookErr != nil || !w.fired || err == nil || m.Status != "failed" {
				t.Fatalf("late failure was hidden: %v / %v", hookErr, err)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("failed run finalized")
			}
			if collision {
				b, _ := os.ReadFile(filepath.Join(out+".partial", localPerformanceFile))
				if !errors.Is(err, os.ErrExist) || string(b) != "unrelated sentinel" {
					t.Fatal("report collision overwritten or hidden")
				}
			} else if !errors.Is(err, context.Canceled) || readLocalPerformance(t, out+".partial").State != "stopped_before_finalization" {
				t.Fatal("canceled run lacks best-effort stopped measurements")
			}
		})
	}
}
