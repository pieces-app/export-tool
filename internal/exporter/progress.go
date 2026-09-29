package exporter

import (
	"fmt"
	"io"
	"sync"
	"time"
)

type lockedOutput struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *lockedOutput) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out.Write(b)
}

type Progress struct {
	mu           sync.Mutex
	out          io.Writer
	client       *Client
	stage        string
	done, total  int
	started      time.Time
	stop, exited chan struct{}
}

func startProgress(w io.Writer, c *Client) *Progress {
	if w == nil {
		return nil
	}
	safe := &lockedOutput{out: w}
	p := &Progress{out: safe, client: c, stop: make(chan struct{}), exited: make(chan struct{})}
	if c.pacer != nil {
		c.pacer.output = safe
	}
	go func() {
		defer close(p.exited)
		timer := time.NewTicker(2 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-timer.C:
				p.print()
			}
		}
	}()
	return p
}
func (p *Progress) Stage(name string, total int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stage = name
	p.done = 0
	p.total = total
	p.started = time.Now()
	fmt.Fprintf(p.out, "Stage: %s", name)
	if total > 0 {
		fmt.Fprintf(p.out, " (%d records)", total)
	}
	fmt.Fprintln(p.out)
}
func (p *Progress) Add(n int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.done += n
	p.mu.Unlock()
}
func (p *Progress) print() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stage == "" {
		return
	}
	elapsed := time.Since(p.started)
	stats := p.client.Performance()
	fmt.Fprintf(p.out, "Progress: %s | elapsed %s", p.stage, elapsed.Round(time.Second))
	if p.total > 0 {
		fmt.Fprintf(p.out, " | %d/%d (%.1f%%)", p.done, p.total, float64(p.done)*100/float64(p.total))
		if p.done > 0 && elapsed.Seconds() > 0 {
			rate := float64(p.done) / elapsed.Seconds()
			remaining := time.Duration(float64(max(0, p.total-p.done)) / rate * float64(time.Second))
			fmt.Fprintf(p.out, " | %.1f records/s | phase ETA %s", rate, remaining.Round(time.Second))
		}
	}
	fmt.Fprintf(p.out, " | OS p95 %.0fms | requests %d | retries %d | backoffs %d\n", stats.P95MS, stats.Requests, stats.Retries, stats.Backoffs)
}
func (p *Progress) Close() {
	if p == nil {
		return
	}
	close(p.stop)
	<-p.exited
	p.print()
}
