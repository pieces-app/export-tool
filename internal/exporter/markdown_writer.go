package exporter

import (
	"context"
	"sync"
)

const defaultFileWorkers = 2
const maxAsyncMarkdownBytes = 4 << 20

func ValidateFileWorkers(workers int) error {
	if workers < 1 || workers > 4 {
		return errConfig("file-workers must be 1–4")
	}
	return nil
}

type markdownWrite struct {
	path string
	data []byte
	done func()
}

// Only the producer submits jobs or calls Flush/Close. Workers own accepted
// byte slices until completion. There is no queued backlog: at most workers
// payloads of maxBytes each are active, in addition to the producer's document.
// Oversized documents run alone, retaining the existing single-document cost.
// Every write still uses the ordinary exclusive, synced artifact writer.
type markdownWriter struct {
	ctx       context.Context
	cancel    context.CancelFunc
	workers   int
	maxBytes  int
	write     func(string, []byte) error
	jobs      chan markdownWrite
	active    sync.WaitGroup
	routines  sync.WaitGroup
	mu        sync.Mutex
	err       error
	closeOnce sync.Once
	closeErr  error
}

func newMarkdownWriter(ctx context.Context, workers, maxBytes int, write func(string, []byte) error) *markdownWriter {
	ctx, cancel := context.WithCancel(ctx)
	w := &markdownWriter{ctx: ctx, cancel: cancel, workers: workers, maxBytes: maxBytes, write: write}
	if workers == 1 {
		return w
	}
	w.jobs = make(chan markdownWrite)
	for worker := 0; worker < workers; worker++ {
		w.routines.Add(1)
		go func() {
			defer w.routines.Done()
			for job := range w.jobs {
				w.perform(job)
				w.active.Done()
			}
		}()
	}
	return w
}

func (w *markdownWriter) Err() error {
	w.mu.Lock()
	err := w.err
	w.mu.Unlock()
	if err != nil {
		return err
	}
	return w.ctx.Err()
}

func (w *markdownWriter) perform(job markdownWrite) {
	if w.Err() != nil {
		return
	}
	if err := w.write(job.path, job.data); err != nil {
		w.mu.Lock()
		if w.err == nil {
			w.err = err
		}
		w.mu.Unlock()
		w.cancel()
		return
	}
	if job.done != nil {
		job.done()
	}
}

// Submit transfers ownership of data. Success means accepted, not yet synced;
// the producer must Flush/Close before reading output or entering another phase.
func (w *markdownWriter) Submit(path string, data []byte, done func()) error {
	if err := w.Err(); err != nil {
		return err
	}
	job := markdownWrite{path: path, data: data, done: done}
	if w.workers == 1 || len(data) > w.maxBytes {
		if err := w.Flush(); err != nil {
			return err
		}
		w.perform(job)
		return w.Err()
	}
	w.active.Add(1)
	select {
	case w.jobs <- job:
		return w.Err()
	case <-w.ctx.Done():
		w.active.Done()
		return w.Err()
	}
}

func (w *markdownWriter) Flush() error {
	w.active.Wait()
	return w.Err()
}

func (w *markdownWriter) Close() error {
	w.closeOnce.Do(func() {
		if w.jobs != nil {
			close(w.jobs)
			w.routines.Wait()
		}
		w.closeErr = w.Err()
		w.cancel()
	})
	return w.closeErr
}
