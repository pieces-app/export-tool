package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPacerGrowthBackoffAndCircuit(t *testing.T) {
	c, _ := NewClient("http://127.0.0.1:1", time.Second, 1024)
	if err := c.ConfigurePerformance("adaptive", 50, 500*time.Millisecond, io.Discard); err != nil {
		t.Fatal(err)
	}
	m, _ := materialByType("TAGS")
	key := routeKey(m.Batch)
	observe := func(d time.Duration, status, n int) bool {
		c.pacer.flight <- struct{}{}
		return c.pacer.after(key, d, 100, status, false, n)
	}
	if c.BatchSize(m, 50) != 5 {
		t.Fatal("must start small")
	}
	observe(20*time.Millisecond, 200, 5)
	observe(20*time.Millisecond, 200, 5)
	if c.BatchSize(m, 50) != 10 {
		t.Fatal("healthy batches did not grow")
	}
	observe(800*time.Millisecond, 200, 10)
	if c.BatchSize(m, 50) != 5 || c.Performance().Backoffs != 1 {
		t.Fatal("slow batch did not back off")
	}
	for i := 0; i < 3; i++ {
		observe(time.Millisecond, 429, 1)
	}
	if !c.Performance().Stopped {
		t.Fatal("overload circuit did not open")
	}
	if err := c.pacer.before(context.Background()); !errors.Is(err, ErrOSBusy) {
		t.Fatal("open circuit accepted work")
	}
	if strings.Contains(routeKey("/person/PRIVATE_ID/annotations?x=secret"), "PRIVATE") {
		t.Fatal("record identity in pacing log key")
	}
}

func TestPerformanceMetricsPassFinalPrivacyAudit(t *testing.T) {
	c, _ := NewClient("http://127.0.0.1:1", time.Second, 1024)
	_ = c.ConfigurePerformance("adaptive", 50, 500*time.Millisecond, io.Discard)
	// The previous float accumulator serialized this as 0.015511415999999998,
	// whose fractional digits pass Luhn validation and blocked finalization.
	c.pacer.waited = 15_511_416 * time.Nanosecond
	stats := c.Performance()
	if stats.WaitSeconds != 0.015511 {
		t.Fatal("reported waiting time must use microsecond precision")
	}
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Scanner: scanner(t, DefaultPolicy())}}
	if err := writeJSON(filepath.Join(r.stage, "manifest.json"), map[string]any{"performance": stats}); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal("generated performance metrics did not pass privacy audit")
	}
	if err := writeJSON(filepath.Join(r.stage, "unfiltered.json"), map[string]any{"value": json.Number("4111111111111111")}); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err == nil {
		t.Fatal("numeric source secrets must still fail the final audit")
	}
}
func TestClientPacingAllowsOnlyOneOutstandingRequest(t *testing.T) {
	var active, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		io.WriteString(w, "{}")
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 1024)
	_ = c.ConfigurePerformance("adaptive", 50, 500*time.Millisecond, io.Discard)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out any
			if err := c.JSON(context.Background(), "GET", "/test", nil, &out); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak.Load() != 1 {
		t.Fatalf("%d simultaneous requests", peak.Load())
	}
}
func TestOverloadedBatchStopsWithoutSingularFallback(t *testing.T) {
	batches, singular := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v any
		switch r.URL.Path {
		case "/.well-known/health":
			v = "ok:macos"
		case "/.well-known/version":
			v = "12.6.29"
		case "/materials/metrics":
			v = map[string]any{"total_count": 1}
		case "/materials/identifiers":
			v = map[string]any{"identifiers": []string{"tag"}}
		case "/tags/batch/fetch":
			batches++
			http.Error(w, "private busy details", 503)
			return
		default:
			singular++
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(v)
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 1024)
	var logs bytes.Buffer
	_ = c.ConfigurePerformance("adaptive", 50, 500*time.Millisecond, &logs)
	materials, _ := SelectMaterials("TAGS")
	_, err := Export(context.Background(), c, Options{Output: filepath.Join(t.TempDir(), "export"), Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Progress: &logs})
	if !errors.Is(err, ErrOSBusy) || batches != 3 || singular != 0 {
		t.Fatalf("overload caused fallback or continued: err=%v batches=%d singular=%d", err, batches, singular)
	}
	if strings.Contains(logs.String(), "private busy details") {
		t.Fatal("private error body logged")
	}
}
func TestBenchmarkIsBoundedAndDoesNotLogRecordBodies(t *testing.T) {
	f := &fakeOS{data: map[string][]map[string]any{"PERSONS": {{"id": "person", "name": "PRIVATE_NAME"}}}}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 1<<20)
	_ = c.ConfigurePerformance("adaptive", 50, 500*time.Millisecond, io.Discard)
	materials, _ := SelectMaterials("PERSONS")
	var out bytes.Buffer
	result, err := Benchmark(context.Background(), c, materials, time.Second, 2, &out)
	if err != nil || result.Reads != 2 || result.Records != 2 {
		t.Fatalf("bad bounded sample: %+v %v", result, err)
	}
	if strings.Contains(out.String(), "PRIVATE_NAME") {
		t.Fatal("sample body was logged")
	}
}
