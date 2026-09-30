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
	local        *localMeasurements
	phaseStart   map[string]OperationMeasurement
	phases       map[string]PhaseMeasurement
	phaseOrder   []string
}

func startProgress(w io.Writer, c *Client) *Progress {
	return startMeasuredProgress(w, c, nil)
}

func startMeasuredProgress(w io.Writer, c *Client, local *localMeasurements) *Progress {
	if w == nil {
		if local == nil {
			return nil
		}
		w = io.Discard
	}
	safe := &lockedOutput{out: w}
	p := &Progress{out: safe, client: c, local: local, phases: map[string]PhaseMeasurement{}, stop: make(chan struct{}), exited: make(chan struct{})}
	if c != nil && c.pacer != nil {
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
	p.recordPhase(time.Now())
	p.stage = name
	p.done = 0
	p.total = total
	p.started = time.Now()
	p.phaseStart = p.local.snapshot()
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
	fmt.Fprintf(p.out, "Progress: %s | elapsed %s", p.stage, elapsed.Round(time.Second))
	if p.total > 0 {
		fmt.Fprintf(p.out, " | %d/%d (%.1f%%)", p.done, p.total, float64(p.done)*100/float64(p.total))
		if p.done > 0 && elapsed.Seconds() > 0 {
			rate := float64(p.done) / elapsed.Seconds()
			remaining := time.Duration(float64(max(0, p.total-p.done)) / rate * float64(time.Second))
			fmt.Fprintf(p.out, " | %.1f records/s | phase ETA %s", rate, remaining.Round(time.Second))
		}
	} else if p.done > 0 {
		fmt.Fprintf(p.out, " | %d processed (total unknown)", p.done)
	}
	if p.client == nil {
		p.printLocal()
		fmt.Fprintln(p.out)
		return
	}
	stats := p.client.Performance()
	fmt.Fprintf(p.out, " | last HTTP p95 %.0fms | requests %d | retries %d | backoffs %d", stats.P95MS, stats.Requests, stats.Retries, stats.Backoffs)
	p.printLocal()
	fmt.Fprintln(p.out)
}

func (p *Progress) printLocal() {
	if p.local == nil {
		return
	}
	counts := p.local.snapshot()
	writes, syncs := counts["artifact_write"], counts["artifact_sync"]
	fmt.Fprintf(p.out, " | file writes %d | flush time %s", writes.Calls, (time.Duration(syncs.Milliseconds) * time.Millisecond).Round(time.Millisecond))
}

func (p *Progress) phaseAt(now time.Time) PhaseMeasurement {
	if p.stage == "" {
		return PhaseMeasurement{}
	}
	row := PhaseMeasurement{Name: p.stage, Visits: 1, Milliseconds: now.Sub(p.started).Milliseconds(), Processed: int64(p.done), KnownTotal: int64(p.total), Operations: map[string]OperationMeasurement{}}
	if p.total == 0 {
		row.UnknownTotalVisits = 1
	}
	addMeasurements(row.Operations, p.local.snapshot(), p.phaseStart)
	return row
}

func mergePhase(a, b PhaseMeasurement) PhaseMeasurement {
	a.Visits += b.Visits
	a.Milliseconds += b.Milliseconds
	a.Processed += b.Processed
	a.KnownTotal += b.KnownTotal
	a.UnknownTotalVisits += b.UnknownTotalVisits
	if a.Operations == nil {
		a.Operations = map[string]OperationMeasurement{}
	}
	addMeasurements(a.Operations, b.Operations, nil)
	return a
}

func (p *Progress) recordPhase(now time.Time) {
	if p.stage == "" {
		return
	}
	row, exists := p.phases[p.stage]
	if !exists {
		row.Name = p.stage
		p.phaseOrder = append(p.phaseOrder, p.stage)
	}
	p.phases[p.stage] = mergePhase(row, p.phaseAt(now))
}

func (p *Progress) measurements() []PhaseMeasurement {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	rows := make([]PhaseMeasurement, 0, len(p.phaseOrder)+1)
	active := p.phaseAt(time.Now())
	found := false
	for _, name := range p.phaseOrder {
		row := p.phases[name]
		copy := map[string]OperationMeasurement{}
		addMeasurements(copy, row.Operations, nil)
		row.Operations = copy
		if name == p.stage {
			row = mergePhase(row, active)
			found = true
		}
		rows = append(rows, row)
	}
	if p.stage != "" && !found {
		rows = append(rows, active)
	}
	return rows
}
func (p *Progress) Close() {
	if p == nil {
		return
	}
	close(p.stop)
	<-p.exited
	p.print()
}
