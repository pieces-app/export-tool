package exporter

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Each worker owns its auditor and buffers. The unbuffered queue bounds active
// text files to workers, without collecting the archive's filenames in memory.
// PDFs run alone after all text jobs settle: their parser/resource limits must
// not be multiplied by the text-worker setting. Every return joins the workers.
func auditOutputFiles(ctx context.Context, root string, workers int, newAuditor func(context.Context) func(string) error) error {
	if err := ValidateFileWorkers(workers); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	visits := make([]func(string) error, workers)
	for i := range visits {
		visits[i] = newAuditor(ctx)
	}
	var routines, active sync.WaitGroup
	var failed sync.Once
	var firstError error
	fail := func(err error) {
		if err != nil {
			failed.Do(func() {
				firstError = err
				cancel()
			})
		}
	}
	jobs := make(chan string)
	if workers > 1 {
		for _, visit := range visits {
			routines.Add(1)
			go func(visit func(string) error) {
				defer routines.Done()
				for path := range jobs {
					err := ctx.Err()
					if err == nil {
						err = visit(path)
					}
					fail(err)
					active.Done()
				}
			}(visit)
		}
	}
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errConfig("unexpected symlink in export output")
		}
		if workers == 1 || filepath.Ext(path) == ".pdf" {
			active.Wait()
			if err := ctx.Err(); err != nil {
				return err
			}
			return visits[0](path)
		}
		active.Add(1)
		select {
		case jobs <- path:
			return nil
		case <-ctx.Done():
			active.Done()
			return ctx.Err()
		}
	})
	fail(walkErr)
	close(jobs)
	routines.Wait()
	if firstError == nil {
		return ctx.Err()
	}
	return firstError
}
