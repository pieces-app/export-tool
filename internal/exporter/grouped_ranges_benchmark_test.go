package exporter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Includes staging/key initialization, durable capture, public materialization,
// reading every published row and private-stage cleanup. Excludes OS reads,
// scanner/privacy decisions, Markdown/PDF, metadata and final archive audits.
func BenchmarkRangeCanonicalStorage(b *testing.B) {
	for _, grouped := range []bool{false, true} {
		b.Run(fmt.Sprintf("grouped=%t", grouped), func(b *testing.B) {
			b.ReportAllocs()
			parent := b.TempDir()
			ctx := context.Background()
			for n := 0; n < b.N; n++ {
				stage := filepath.Join(parent, fmt.Sprint(n))
				if err := os.Mkdir(stage, 0700); err != nil {
					b.Fatal(err)
				}
				r := &run{ctx: ctx, stage: stage, opts: Options{Mode: "preserve"}, meta: map[string]*Meta{}, local: newLocalMeasurements()}
				if !grouped {
					r.records = &fileCanonicalRecords{run: r}
				}
				for i := range 128 {
					id := fmt.Sprintf("range-%04d", i)
					m := &Meta{Key: "RANGES\x00" + id, ID: id, Type: "RANGES", Folder: "ranges", State: "included", DataPath: "raw/ranges/" + opaque("RANGES", id) + ".json"}
					v := map[string]any{"id": id, "description": strings.Repeat("measured canonical metadata ", 150)}
					if err := r.writeCanonical(m, v, false); err != nil {
						b.Fatal(err)
					}
					r.meta[m.Key] = m
				}
				if err := r.canonicalRecords().Flush(ctx); err != nil {
					b.Fatal(err)
				}
				if err := r.prepareAssociationGroups(); err != nil {
					b.Fatal(err)
				}
				if err := r.closeCanonicalStage(); err != nil {
					b.Fatal(err)
				}
				for _, m := range r.meta {
					v, err := r.readCanonical(m)
					if err != nil || fieldString(v, "id") != m.ID {
						b.Fatal("published record could not be read", err)
					}
				}
				b.ReportMetric(float64(r.local.snapshot()["artifact_sync"].Calls), "public-syncs/op")
			}
		})
	}
}
