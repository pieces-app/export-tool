package exporter

import (
	"context"
	"fmt"
	"io"
	"time"
)

type BenchmarkResult struct {
	Reads, Records int
	Duration       time.Duration
	BudgetReached  bool
	Performance    PerformanceStats
}

func FetchBatch(ctx context.Context, c *Client, m Material, ids []string) ([]map[string]any, error) {
	if m.Batch == "" || len(ids) == 0 {
		return nil, errConfig("batch sample requires a readable material and identifiers")
	}
	want := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		want = append(want, map[string]string{"id": id})
	}
	var out map[string]any
	if err := c.JSON(ctx, "POST", m.Batch+"?transferables=true", map[string]any{m.Field: map[string]any{"iterable": want}}, &out); err != nil {
		return nil, err
	}
	collection, ok := out[m.Field].(map[string]any)
	if !ok {
		return nil, errConfig("batch sample has no collection")
	}
	list, ok := collection["iterable"].([]any)
	if !ok {
		return nil, errConfig("batch sample has no iterable")
	}
	expected := map[string]bool{}
	for _, id := range ids {
		expected[id] = true
	}
	found := []map[string]any{}
	for _, item := range list {
		v, ok := item.(map[string]any)
		if !ok || !expected[fieldString(v, "id")] {
			return nil, errConfig("batch sample contains an unexpected identifier")
		}
		delete(expected, fieldString(v, "id"))
		found = append(found, v)
	}
	return found, nil
}
func SampleIDs(ctx context.Context, c *Client, m Material, limit int) ([]string, error) {
	var out struct {
		IDs []string `json:"identifiers"`
	}
	if err := c.JSON(ctx, "POST", "/materials/identifiers", map[string]any{"material_type": m.Type, "limit": limit}, &out); err != nil {
		return nil, err
	}
	if len(out.IDs) > limit {
		return nil, errConfig("OS ignored identifier sample limit; bounded sampling stopped")
	}
	return unique(out.IDs), nil
}

// Benchmark reads bounded, possibly warm-cache samples and never writes records.
// It is not a stress test and does not prove a server's maximum capacity.
func Benchmark(ctx context.Context, c *Client, materials []Material, duration time.Duration, maxReads int, out io.Writer) (result BenchmarkResult, err error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	defer func() { result.Duration = time.Since(started); result.Performance = c.Performance() }()
	fmt.Fprintln(out, "Bounded read calibration: one request at a time; response samples stay in memory. Repeated samples may be cached.")
	for _, m := range materials {
		if m.Batch == "" || m.Type == "SENSITIVES" {
			continue
		}
		if result.Reads >= maxReads {
			result.BudgetReached = true
			break
		}
		ids, e := SampleIDs(ctx, c, m, 50)
		if e != nil {
			return result, e
		}
		if len(ids) == 0 {
			continue
		}
		for round := 0; round < 8 && result.Reads < maxReads; round++ {
			size := min(len(ids), c.BatchSize(m, 50))
			start := time.Now()
			records, e := FetchBatch(ctx, c, m, ids[:size])
			elapsed := time.Since(start)
			if e != nil {
				return result, e
			}
			result.Reads++
			result.Records += len(records)
			stats := c.Performance()
			fmt.Fprintf(out, "  %-28s %2d records | %s | %.1f records/s | next batch ≤%d | recent p95 %.0fms\n", m.Type, len(records), elapsed.Round(time.Millisecond), float64(len(records))/max(0.001, elapsed.Seconds()), c.BatchSize(m, 50), stats.P95MS)
			if ctx.Err() != nil {
				result.BudgetReached = true
				return result, ctx.Err()
			}
		}
	}
	fmt.Fprintf(out, "Calibration read %d records in %d batches (%s). No export files created. Export pacing continues adapting to actual reads.\n", result.Records, result.Reads, time.Since(started).Round(time.Millisecond))
	return result, nil
}
