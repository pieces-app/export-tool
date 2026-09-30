package exporter

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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

func TestLateFailureAndCancellationNeverReturnSuccess(t *testing.T) {
	for _, phase := range []string{"Stage: Render PDFs", "Stage: Native and portable metadata", "Stage: Record archive reconstruction evidence"} {
		t.Run(phase, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "private-fixture-destination")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var hookErr error
			writer := &phaseHookWriter{phase: phase, hook: func() {
				if strings.Contains(phase, "reconstruction") {
					hookErr = os.WriteFile(filepath.Join(out+".partial", archiveStateFile), []byte("synthetic collision"), 0600)
				} else {
					cancel()
				}
			}}
			f := &fakeOS{data: map[string][]map[string]any{"TAGS": {record("tag", "")}}}
			srv := f.server(t)
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			materials, _ := SelectMaterials("TAGS")
			manifest, err := Export(ctx, client, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 1, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Format: "both", Metadata: "off", Progress: writer})
			if hookErr != nil || !writer.fired || err == nil {
				t.Fatalf("failure was not exercised: %v / %v", hookErr, err)
			}
			if strings.Contains(phase, "reconstruction") {
				if !errors.Is(err, os.ErrExist) {
					t.Fatalf("filesystem cause was not retained: %v", err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was not retained: %v", err)
			}
			if manifest.Status != "failed" || !manifest.Finished.IsZero() {
				t.Fatal("failed rendering returned a successful/completed manifest")
			}
			if strings.Contains(err.Error(), out) || strings.Contains(err.Error(), archiveStateFile) {
				t.Fatal("generated private path leaked in the terminal error")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("failed output was finalized")
			}
			if _, err := os.Stat(filepath.Join(out+".partial", "manifest.json")); !os.IsNotExist(err) {
				t.Fatal("failed output wrote a success manifest")
			}
		})
	}
}

func TestRebuildFinalRenameFailurePreservesSourceAndExistingDestination(t *testing.T) {
	source, policy, _ := createRebuildFixture(t)
	before := archiveHashes(t, source)
	for _, emptyDirectory := range []bool{false, true} {
		name := "file"
		if emptyDirectory {
			name = "empty_directory"
		}
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "destination")
			var hookErr error
			var original os.FileInfo
			writer := &phaseHookWriter{phase: "Stage: Record archive reconstruction evidence", hook: func() {
				// Simulate another process occupying the destination after preflight.
				if emptyDirectory {
					hookErr = os.Mkdir(out, 0700)
				} else {
					hookErr = os.WriteFile(out, []byte("unrelated destination sentinel"), 0600)
				}
				if hookErr == nil {
					original, hookErr = os.Lstat(out)
				}
			}}
			manifest, err := Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: out, Scanner: scanner(t, policy), Progress: writer}})
			if hookErr != nil || !writer.fired || err == nil || manifest.Status != "failed" || !manifest.Finished.IsZero() {
				t.Fatalf("late destination collision was not reported as failure: %v / %v", hookErr, err)
			}
			var linkErr *os.LinkError
			if !errors.As(err, &linkErr) || strings.Contains(err.Error(), out) {
				t.Fatal("rename cause lost or private path printed")
			}
			current, statErr := os.Lstat(out)
			if statErr != nil || !os.SameFile(original, current) {
				t.Fatal("existing destination was replaced")
			}
			if emptyDirectory {
				entries, readErr := os.ReadDir(out)
				if readErr != nil || len(entries) != 0 {
					t.Fatal("existing empty directory was changed")
				}
			} else {
				sentinel, readErr := os.ReadFile(out)
				if readErr != nil || string(sentinel) != "unrelated destination sentinel" {
					t.Fatal("existing destination was overwritten")
				}
			}
			if !reflect.DeepEqual(before, archiveHashes(t, source)) {
				t.Fatal("failed rebuild changed its source")
			}
			if _, err := InspectArchive(out + ".partial"); err == nil {
				t.Fatal("provisional manifest in a partial directory was accepted as finalized")
			}
		})
	}
}

type phaseHookWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	phase string
	hook  func()
	fired bool
}

func (w *phaseHookWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if !w.fired && w.phase != "" && strings.Contains(w.buf.String(), w.phase) {
		w.fired = true
		w.hook()
	}
	return len(p), nil
}
