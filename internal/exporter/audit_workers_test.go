package exporter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAuditWorkersBoundAndPDFBarrier(t *testing.T) {
	for _, workers := range []int{1, 2, 4} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"a0.md", "a1.md", "a2.md", "a3.md", "middle.pdf", "z0.json", "z1.jsonl"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("synthetic"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var mu sync.Mutex
			active, peak, built := 0, 0, 0
			seen := map[string]int{}
			started := make(chan struct{}, workers)
			release := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- auditOutputFiles(ctx, root, workers, func(ctx context.Context) func(string) error {
					built++ // Factories are constructed by the producer before any visits.
					return func(path string) error {
						name := filepath.Base(path)
						mu.Lock()
						active++
						peak = max(peak, active)
						seen[name]++
						pdfOverlapped := filepath.Ext(path) == ".pdf" && active != 1
						mu.Unlock()
						defer func() { mu.Lock(); active--; mu.Unlock() }()
						if pdfOverlapped {
							return errors.New("PDF audit overlapped a text audit")
						}
						if strings.HasPrefix(name, "a") {
							select {
							case started <- struct{}{}:
							default:
							}
							select {
							case <-release:
							case <-ctx.Done():
								return ctx.Err()
							}
						}
						return nil
					}
				})
			}()
			for i := 0; i < workers; i++ {
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("workers failed to reach bounded concurrency")
				}
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if active != 0 || peak != workers || built != workers || len(seen) != 7 {
				t.Fatal("workers, barriers or file coverage differ", active, peak, built, len(seen))
			}
			for _, count := range seen {
				if count != 1 {
					t.Fatal("file was scanned more than once")
				}
			}
		})
	}
}

func TestAuditWorkersFailureJoinsAndPreservesCause(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a-bad.md", "b-wait.md", "c-unused.md"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	want := errors.New("synthetic audit failure")
	otherStarted := make(chan struct{})
	var joined sync.WaitGroup
	err := auditOutputFiles(ctx, root, 2, func(ctx context.Context) func(string) error {
		return func(path string) error {
			joined.Add(1)
			defer joined.Done()
			if filepath.Base(path) == "a-bad.md" {
				select {
				case <-otherStarted:
					return want
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if filepath.Base(path) == "b-wait.md" {
				close(otherStarted)
			}
			<-ctx.Done()
			return ctx.Err()
		}
	})
	if !errors.Is(err, want) {
		t.Fatal("first scan failure was replaced", err)
	}
	joined.Wait()
}

func TestAuditWorkersCancellationAtLastFile(t *testing.T) {
	for _, workers := range []int{1, 2, 4} {
		ctx, cancel := context.WithCancel(context.Background())
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "last.md"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		err := auditOutputFiles(ctx, root, workers, func(context.Context) func(string) error {
			return func(string) error { cancel(); return nil }
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatal("last-file cancellation became success", workers, err)
		}
	}
}

func TestAuditWorkersPrivateInputsAndCacheInvalidation(t *testing.T) {
	for _, workers := range []int{1, 2, 4} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			r := newAuditTestRun(t)
			r.opts.FileWorkers = workers
			for i := 0; i < 48; i++ {
				auditFixtureFile(t, r, fmt.Sprintf("record-%02d.json", i), fmt.Sprintf(`{"id":"synthetic-%02d","text":"ordinary late marker","url":"https://example.test/path"}`, i))
			}
			for i := 0; i < 2; i++ {
				if err := r.auditOutput(); err != nil {
					t.Fatal(err)
				}
			}
			stats := r.local.snapshot()
			if stats["audit_content_scan"].Calls != 48 || stats["audit_reused"].Calls != 48 || len(r.auditCache.entries) != 48 {
				t.Fatal("parallel audit lost or duplicated checks", stats)
			}
			r.opts.Scanner.remember("ordinary late marker")
			if err := r.auditOutput(); err == nil || !strings.Contains(err.Error(), "content requiring review") || r.auditCache != nil {
				t.Fatal("workers ignored learned credentials or retained failed cache", err)
			}
		})
	}
}

func TestAuditWorkersRejectInvalidTraversal(t *testing.T) {
	for _, workers := range []int{-1, 0, 5} {
		if err := auditOutputFiles(context.Background(), t.TempDir(), workers, nil); err == nil {
			t.Fatal("invalid worker bound accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := auditOutputFiles(ctx, t.TempDir(), 2, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled audit built workers", err)
	}
}
