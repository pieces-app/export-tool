package exporter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pieces-app/export-tool/internal/recovery"
)

// Test-only materialization still uses individual output files. The production
// grouped public format is separate pending work, so this cannot claim an
// end-to-end speedup or enable the transactional backend in the CLI yet.
type materializingTransactionFixture struct {
	*transactionalCanonicalRecords
	r         *run
	published map[string]bool
}

func (s *materializingTransactionFixture) Flush(ctx context.Context) error {
	if err := s.transactionalCanonicalRecords.Flush(ctx); err != nil {
		return err
	}
	files := fileCanonicalRecords{run: s.r}
	for _, m := range s.r.meta {
		ref, err := canonicalStageRef(m)
		if err != nil {
			return err
		}
		if m.State != "included" {
			if s.known[ref] {
				return fmt.Errorf("excluded canonical payload was retained")
			}
			if s.published[m.Key] {
				if err := files.Remove(ctx, m); err != nil {
					return err
				}
				delete(s.published, m.Key)
			}
			continue
		}
		f, err := s.Open(ctx, m)
		if err != nil {
			return err
		}
		var v map[string]any
		d := json.NewDecoder(f)
		d.UseNumber()
		err = d.Decode(&v)
		f.Close()
		if err != nil {
			return err
		}
		if err := files.Write(ctx, m, v, s.published[m.Key]); err != nil {
			return err
		}
		s.published[m.Key] = true
	}
	return nil
}

func TestCanonicalTransactionsExportCaptureAndReplay(t *testing.T) {
	ctx := context.Background()
	f := junctionFixture()
	f.data["PERSONS"] = append(f.data["PERSONS"], record("unprofiled", "2026-09-30T00:00:00Z"))
	srv := junctionServer(t, f, nil)
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	staging, stagingOptions := captureTestStore(t)
	checkpoint, _ := captureTestStore(t)
	o := captureTestOptions(t, DefaultPolicy())
	o.Format = "both"
	late := "Current linked narrative."
	o.captureCheckpoint = func(r *run) error {
		transactions, err := newTransactionalCanonicalRecords(ctx, staging, r.local)
		if err != nil {
			return err
		}
		t.Cleanup(transactions.discardPending)
		for _, m := range r.meta {
			if m.State != "included" {
				continue
			}
			v, err := r.readCanonical(m)
			if err != nil {
				return err
			}
			if err := transactions.Write(ctx, m, v, false); err != nil {
				return err
			}
			if err := r.removeCanonical(m); err != nil {
				return err
			}
		}
		r.records = &materializingTransactionFixture{transactions, r, map[string]bool{}}
		r.opts.Scanner.remember(late)
		return r.saveCapture(checkpoint)
	}
	m, err := Export(ctx, client, o)
	if err != nil || m.People.Selected != 1 || m.People.Omitted != 1 || m.LocalPerformance.Operations["canonical_transaction"].Calls == 0 {
		t.Fatal("transactional canonical export did not reconcile", err)
	}
	srv.Close()
	out := filepath.Join(t.TempDir(), "replayed")
	replayed, err := replayCapture(ctx, checkpoint, out, nil)
	if err != nil || replayed.People.Selected != m.People.Selected || replayed.People.Omitted != m.People.Omitted || replayed.Performance.Requests != 0 {
		t.Fatal("transaction-backed source capture lost replay evidence", err)
	}
	for _, root := range []string{o.Output, out} {
		report, err := inspectFinalArchive(ctx, root)
		if err != nil || report.Summaries != 1 || report.SummariesWithBody != 1 || report.PersonsWithProfile != 1 {
			t.Fatal("transaction-backed archive lost graph/body coverage", err, report)
		}
		requireNoCapturePlaintext(t, root, late)
	}
	requireNoCapturePlaintext(t, stagingOptions.Directory, late, "Example Person", "Current profile history.")
}

// Capturing the same 128 canonical records, then reading them through the same
// interface, compares the actual adapter paths. Store/key initialization and
// final public materialization are outside the timer; this is not export ETA.
func BenchmarkCanonicalRecordCapture(b *testing.B) {
	for _, transactional := range []bool{false, true} {
		b.Run(fmt.Sprintf("transactions_%t", transactional), func(b *testing.B) {
			ctx := context.Background()
			text := strings.Repeat("Approved canonical record fixture. ", 120)
			metas, values := make([]*Meta, 128), make([]map[string]any, 128)
			for i := range metas {
				metas[i], values[i] = canonicalFixture(i, text)
			}
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				b.StopTimer()
				r := &run{ctx: ctx, stage: b.TempDir(), local: newLocalMeasurements()}
				var store *recovery.Store
				if transactional {
					parent := b.TempDir()
					var err error
					store, err = recovery.CreateStore(ctx, recovery.Options{Directory: filepath.Join(parent, "work"), KeyDirectory: filepath.Join(parent, "keys"), Binding: sha256.Sum256([]byte("canonical capture benchmark v1"))}, []byte(canonicalTransactionState))
					if err != nil {
						b.Fatal(err)
					}
					r.records, err = newTransactionalCanonicalRecords(ctx, store, r.local)
					if err != nil {
						store.Close()
						b.Fatal(err)
					}
				}
				b.StartTimer()
				for i, m := range metas {
					if err := r.writeCanonical(m, values[i], false); err != nil {
						b.Fatal(err)
					}
				}
				if err := r.canonicalRecords().Flush(ctx); err != nil {
					b.Fatal(err)
				}
				for _, m := range metas {
					f, err := r.canonicalRecords().Open(ctx, m)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := io.Copy(io.Discard, f); err != nil {
						b.Fatal(err)
					}
					f.Close()
				}
				b.StopTimer()
				stats := r.local.snapshot()
				b.ReportMetric(float64(stats["artifact_sync"].Calls), "file-syncs/op")
				b.ReportMetric(float64(stats["canonical_transaction"].Calls), "commits/op")
				if store != nil {
					if err := store.Close(); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
