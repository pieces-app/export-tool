package exporter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrOSBusy = errors.New("Pieces OS is too slow or unavailable; stopped requesting data to let it recover; retry later with a smaller batch ceiling")

type PerformanceStats struct {
	Requests    int     `json:"requests"`
	Bytes       int64   `json:"response_bytes"`
	Retries     int     `json:"retries"`
	Slow        int     `json:"slow_requests"`
	Backoffs    int     `json:"backoffs"`
	WaitSeconds float64 `json:"wait_seconds"`
	LastMS      float64 `json:"last_request_ms"`
	P95MS       float64 `json:"recent_p95_ms"`
	PeakMS      float64 `json:"peak_request_ms"`
	Stopped     bool    `json:"stopped"`
}

type routePace struct {
	batch, healthy, verySlow int
	delay                    time.Duration
}
type Pacer struct {
	mu        sync.Mutex
	flight    chan struct{}
	ceiling   int
	target    time.Duration
	adaptive  bool
	routes    map[string]*routePace
	stats     PerformanceStats
	waited    time.Duration
	latencies []float64
	next      time.Time
	output    io.Writer
}

func (c *Client) ConfigurePerformance(mode string, ceiling int, target time.Duration, output io.Writer) error {
	if (mode != "adaptive" && mode != "conservative") || ceiling < 1 || ceiling > 50 || target < 50*time.Millisecond || target > 5*time.Second {
		return errConfig("performance must be adaptive or conservative; batch ceiling 1–50; target latency 50ms–5s")
	}
	c.pacer = &Pacer{flight: make(chan struct{}, 1), ceiling: ceiling, target: target, adaptive: mode == "adaptive", routes: map[string]*routePace{}, output: output}
	return nil
}
func routeKey(path string) string {
	path, _, _ = strings.Cut(path, "?")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 1 && parts[0] != ".well-known" && parts[0] != "materials" && !strings.Contains(path, "/batch") {
		// Never retain IDs or other record-derived path fragments in logs/stats.
		return "/" + parts[0] + "/record"
	}
	return path
}
func (p *Pacer) route(key string) *routePace {
	q := p.routes[key]
	if q == nil {
		q = &routePace{batch: min(5, p.ceiling)}
		if !p.adaptive {
			q.delay = 100 * time.Millisecond
		}
		p.routes[key] = q
	}
	return q
}
func (c *Client) BatchSize(m Material, ceiling int) int {
	if c.pacer == nil {
		return ceiling
	}
	p := c.pacer
	p.mu.Lock()
	defer p.mu.Unlock()
	return min(ceiling, p.route(routeKey(m.Batch)).batch)
}
func (p *Pacer) before(ctx context.Context) error {
	select {
	case p.flight <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	p.mu.Lock()
	stop, delay := p.stats.Stopped, time.Until(p.next)
	p.mu.Unlock()
	if stop {
		<-p.flight
		return ErrOSBusy
	}
	if delay > 0 {
		start := time.Now()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			<-p.flight
			return ctx.Err()
		case <-timer.C:
		}
		p.mu.Lock()
		p.waited += time.Since(start)
		p.mu.Unlock()
	}
	return nil
}
func (p *Pacer) after(key string, elapsed time.Duration, bytes int, status int, failed bool, batchItems int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer func() { <-p.flight }()
	q := p.route(key)
	p.stats.Requests++
	p.stats.Bytes += int64(bytes)
	ms := float64(elapsed) / float64(time.Millisecond)
	p.stats.LastMS = ms
	p.stats.PeakMS = max(p.stats.PeakMS, ms)
	p.latencies = append(p.latencies, ms)
	if len(p.latencies) > 128 {
		p.latencies = p.latencies[1:]
	}
	overloaded := failed || status == 429 || (status >= 500 && status != 593 && status != 501)
	slow := elapsed > p.target || bytes > 8<<20
	previous := q.batch
	if overloaded || slow {
		p.stats.Slow++
		p.stats.Backoffs++
		q.healthy = 0
		q.batch = max(1, q.batch/2)
		q.delay = min(5*time.Second, max(100*time.Millisecond, max(q.delay*2, elapsed/2)))
		if overloaded || elapsed > max(3*time.Second, p.target*6) {
			q.verySlow++
		} else {
			q.verySlow = 0
		}
		if q.verySlow >= 3 {
			p.stats.Stopped = true
		}
	} else {
		q.verySlow = 0
		if batchItems >= q.batch && status >= 200 && status < 300 && p.adaptive {
			q.healthy++
			if q.healthy >= 2 && elapsed < p.target*3/4 {
				q.batch = min(p.ceiling, q.batch*2)
				q.healthy = 0
			}
		}
		if p.adaptive {
			// Leave a small share of request time available to OS capture/writers.
			q.delay = max(time.Millisecond, max(q.delay/2, elapsed/4))
		} else {
			q.delay = max(q.delay/2, 100*time.Millisecond)
		}
	}
	p.next = time.Now().Add(q.delay)
	if p.output != nil && (previous != q.batch || overloaded || slow) {
		fmt.Fprintf(p.output, "Pacing %s: batch ceiling %d → %d, latency %s, pause %s\n", key, previous, q.batch, elapsed.Round(time.Millisecond), q.delay.Round(time.Millisecond))
	}
	return p.stats.Stopped
}
func (c *Client) Performance() PerformanceStats {
	if c.pacer == nil {
		return PerformanceStats{}
	}
	p := c.pacer
	p.mu.Lock()
	defer p.mu.Unlock()
	stats := p.stats
	// Keep accumulated waits exact internally and publish microsecond precision.
	// Summing float seconds can serialize long rounding tails that resemble a
	// payment-card number to the final privacy audit. Do not weaken that audit.
	stats.WaitSeconds = float64(p.waited.Round(time.Microsecond)/time.Microsecond) / 1e6
	v := append([]float64(nil), p.latencies...)
	sort.Float64s(v)
	if len(v) > 0 {
		stats.P95MS = v[min(len(v)-1, (len(v)*95)/100)]
	}
	return stats
}
func (c *Client) stopBusy() error {
	if c.pacer != nil {
		c.pacer.mu.Lock()
		c.pacer.stats.Stopped = true
		c.pacer.mu.Unlock()
	}
	return ErrOSBusy
}
func batchItemCount(body any) int {
	m, ok := body.(map[string]any)
	if !ok {
		return 0
	}
	for _, v := range m {
		if group, ok := v.(map[string]any); ok {
			switch a := group["iterable"].(type) {
			case []map[string]string:
				return len(a)
			case []any:
				return len(a)
			}
		}
	}
	return 0
}
