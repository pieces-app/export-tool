package exporter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDestinationFailureStopsHydrationWithoutFinalizing(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		name := "unwritable_destination"
		if cancelRun {
			name = "cancel_during_fetch"
		}
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "archive")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var batches atomic.Int32
			f := &fakeOS{data: map[string][]map[string]any{"TAGS": {record("first", ""), record("second", "")}}}
			f.beforeBatch = func() {
				if batches.Add(1) != 1 {
					return
				}
				if cancelRun {
					cancel()
					return
				}
				// Portable destination failure: a file now occupies the directory
				// records would use. No source mutation or disk filling required.
				if err := os.WriteFile(filepath.Join(out+".partial", "data"), []byte("fixture"), 0600); err != nil {
					t.Error(err)
				}
			}
			srv := f.server(t)
			defer srv.Close()
			c, _ := NewClient(srv.URL, time.Second, 8<<20)
			materials, _ := SelectMaterials("TAGS")
			manifest, err := Export(ctx, c, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 1, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())})
			if err == nil {
				t.Fatal("destination failure/cancellation was hidden")
			}
			if cancelRun && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			if !cancelRun && !strings.Contains(err.Error(), "could not write an export record") {
				t.Fatalf("unexpected failure: %v", err)
			}
			if strings.Contains(err.Error(), out) {
				t.Fatal("private destination printed in error")
			}
			if batches.Load() != 1 {
				t.Fatalf("continued fetching after failure: %d batches", batches.Load())
			}
			if manifest.Status == "complete_for_implemented_scope" {
				t.Fatal("failed export claimed success")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("failed archive was finalized")
			}
			if _, err := os.Stat(out + ".partial"); err != nil {
				t.Fatal("partial evidence was removed")
			}
		})
	}
}
