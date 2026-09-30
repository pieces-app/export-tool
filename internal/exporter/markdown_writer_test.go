package exporter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func receiveWithin[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic writer did not reach the expected barrier")
		var zero T
		return zero
	}
}

func TestMarkdownWriterBoundedAndOversized(t *testing.T) {
	entered := make(chan int, 4)
	release := make(chan struct{}, 4)
	var active, peak, completed atomic.Int32
	w := newMarkdownWriter(context.Background(), 2, 64, func(_ string, data []byte) error {
		n := active.Add(1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- len(data)
		<-release
		active.Add(-1)
		return nil
	})
	defer func() { close(release); _ = w.Close() }()
	for i := 0; i < 2; i++ {
		if err := w.Submit("synthetic", make([]byte, 64), func() { completed.Add(1) }); err != nil {
			t.Fatal(err)
		}
		if receiveWithin(t, entered) != 64 {
			t.Fatal("wrong ordinary document")
		}
	}
	large := make(chan error, 1)
	go func() { large <- w.Submit("oversized", make([]byte, 65), func() { completed.Add(1) }) }()
	release <- struct{}{}
	// Releasing only one slot must not let the oversized write overlap the
	// remaining ordinary write. Its entry is checked after both are released.
	release <- struct{}{}
	if receiveWithin(t, entered) != 65 || active.Load() != 1 {
		t.Fatal("oversized document did not run alone")
	}
	release <- struct{}{}
	if err := receiveWithin(t, large); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil || active.Load() != 0 || peak.Load() != 2 || completed.Load() != 3 {
		t.Fatalf("bounded writer did not finish all writes: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal("Close is not idempotent")
	}
}

func TestMarkdownWriterFailureAndCancellationDrain(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan string, 4)
			release := make(chan struct{})
			var active, callbacks atomic.Int32
			w := newMarkdownWriter(ctx, 2, 64, func(path string, _ []byte) error {
				active.Add(1)
				defer active.Add(-1)
				entered <- path
				<-release
				if fail {
					return io.ErrShortWrite
				}
				return nil
			})
			if err := w.Submit("first", []byte("approved"), func() { callbacks.Add(1) }); err != nil {
				t.Fatal(err)
			}
			receiveWithin(t, entered)
			if !fail {
				cancel()
			}
			closed := make(chan error, 1)
			go func() { closed <- w.Close() }()
			select {
			case <-closed:
				t.Fatal("Close returned while a file write was still active")
			default:
			}
			close(release)
			err := receiveWithin(t, closed)
			want := error(context.Canceled)
			if fail {
				want = io.ErrShortWrite
			}
			if !errors.Is(err, want) || active.Load() != 0 || (fail && callbacks.Load() != 0) {
				t.Fatalf("write/cancellation cause or completion lost: %v", err)
			}
		})
	}
}

func TestMarkdownWriterSerialAndPreCanceled(t *testing.T) {
	var calls int
	w := newMarkdownWriter(context.Background(), 1, 64, func(string, []byte) error { calls++; return nil })
	if err := w.Submit("first", nil, nil); err != nil || calls != 1 {
		t.Fatal("serial Submit returned before the write finished")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w = newMarkdownWriter(ctx, 2, 64, func(string, []byte) error { calls++; return nil })
	if !errors.Is(w.Submit("canceled", nil, nil), context.Canceled) || !errors.Is(w.Close(), context.Canceled) || calls != 1 {
		t.Fatal("pre-canceled pool performed work")
	}
}

func TestMarkdownWriterCancellationUnblocksProducer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var calls atomic.Int32
	w := newMarkdownWriter(ctx, 2, 64, func(string, []byte) error {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return nil
	})
	for i := 0; i < 2; i++ {
		if err := w.Submit("occupied", nil, nil); err != nil {
			t.Fatal(err)
		}
		receiveWithin(t, entered)
	}
	submitted := make(chan error, 1)
	go func() { submitted <- w.Submit("must-not-write", nil, nil) }()
	cancel()
	err := receiveWithin(t, submitted)
	close(release)
	if !errors.Is(err, context.Canceled) || !errors.Is(w.Close(), context.Canceled) || calls.Load() != 2 {
		t.Fatal("cancellation failed to unblock a producer without writing the pending job")
	}
}

func TestMarkdownWriterArchiveEquivalence(t *testing.T) {
	source, policy, _ := createRebuildFixture(t)
	original := archiveHashes(t, source)
	var reference map[string][32]byte
	for _, workers := range []int{1, 2, 4} {
		out := filepath.Join(t.TempDir(), "rebuilt")
		m, err := Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: out, FileWorkers: workers, Scanner: scanner(t, policy)}})
		if err != nil {
			t.Fatal(err)
		}
		if m.FileWorkers != workers || m.LocalPerformance.FileWorkers != workers {
			t.Fatal("writer setting missing from manifest/diagnostics")
		}
		stats := m.LocalPerformance.Operations
		if stats["artifact_write"].Calls != stats["artifact_sync"].Calls || stats["artifact_sync"].Failures != 0 {
			t.Fatal("not every ordinary output write was synced")
		}
		current := archiveHashes(t, out)
		delete(current, "manifest.json")
		delete(current, localPerformanceFile)
		if reference == nil {
			reference = current
		} else if !reflect.DeepEqual(reference, current) {
			t.Fatal("writer concurrency changed canonical files, graph, Markdown, or metadata")
		}
	}
	if !reflect.DeepEqual(original, archiveHashes(t, source)) {
		t.Fatal("offline source was changed")
	}
}

func TestMarkdownWriterExportFailureStaysPartial(t *testing.T) {
	out := filepath.Join(t.TempDir(), "archive")
	f := &fakeOS{data: map[string][]map[string]any{"TAGS": {record("first", ""), record("second", "")}}}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("TAGS")
	var hookErr error
	w := &phaseHookWriter{phase: "Stage: Render Markdown", hook: func() {
		hookErr = os.WriteFile(filepath.Join(out+".partial", "markdown"), []byte("unrelated sentinel"), 0600)
	}}
	m, err := Export(context.Background(), c, Options{Output: out, FileWorkers: 2, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Progress: w})
	if hookErr != nil || !w.fired || err == nil || m.Status != "failed" {
		t.Fatalf("background persistence failure was hidden: %v / %v", hookErr, err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("failed archive finalized")
	}
	diagnostics := readLocalPerformance(t, out+".partial")
	if diagnostics.State != "stopped_before_finalization" || diagnostics.Operations["artifact_write"].Failures == 0 {
		t.Fatal("write failure was missing from final diagnostic snapshot")
	}
}
