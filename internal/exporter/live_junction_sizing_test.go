package exporter

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Opt-in cardinality sampling only. No bodies, caches, files, source mutations,
// or private record values are emitted. Estimates are not measured totals.
func TestLiveJunctionSizing(t *testing.T) {
	base := os.Getenv("PIECES_EXPORT_LIVE_JUNCTION_SIZING")
	if base == "" {
		t.Skip("set PIECES_EXPORT_LIVE_JUNCTION_SIZING to an explicit local OS URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := NewClient(base, 10*time.Second, 4<<20)
	if err != nil {
		t.Fatal("invalid live sizing client")
	}
	if err := c.ConfigurePerformance("adaptive", 50, 250*time.Millisecond, nil); err != nil {
		t.Fatal("invalid sizing pacing")
	}
	if _, err := c.Probe(ctx); err != nil {
		t.Fatal("OS sizing probe unavailable")
	}
	samples := map[string][]string{}
	totals := map[string]int{}
	for _, typ := range []string{"WORKSTREAM_SUMMARIES", "PERSONS", "PIPELINES"} {
		m, _ := materialByType(typ)
		ids, err := c.IDs(ctx, m, Window{})
		if err != nil || len(ids) > 20000 {
			t.Fatal("sizing inventory unavailable or exceeds bound")
		}
		totals[typ] = len(ids)
		count := min(30, len(ids))
		for i := 0; i < count; i++ {
			samples[typ] = append(samples[typ], ids[i*len(ids)/count])
		}
	}
	type size struct {
		Family   string `json:"family"`
		Side     string `json:"side"`
		Owners   int    `json:"owners_in_inventory"`
		Sampled  int    `json:"sampled_owners"`
		Rows     int    `json:"sampled_rows"`
		Maximum  int    `json:"largest_sampled_owner"`
		Estimate int    `json:"rough_rows_from_sample_mean"`
	}
	results := []size{}
	for _, p := range summaryJunctionPlans {
		row := size{Family: p.family, Side: p.side, Owners: totals[p.owner], Sampled: len(samples[p.owner])}
		for _, id := range samples[p.owner] {
			count, err := c.readJunctionCount(ctx, p.family, p.side, id)
			if err != nil {
				t.Fatal("current junction sizing read failed; no completion claim")
			}
			row.Rows += count
			row.Maximum = max(row.Maximum, count)
		}
		if row.Sampled > 0 {
			row.Estimate = row.Rows * row.Owners / row.Sampled
		}
		results = append(results, row)
	}
	if _, err := c.Probe(ctx); err != nil {
		t.Fatal("OS health unavailable after sizing")
	}
	b, _ := json.Marshal(struct {
		Sides []size           `json:"sides"`
		HTTP  PerformanceStats `json:"http"`
	}{results, c.Performance()})
	t.Log(string(b))
	t.Log("Small identity-spaced sample: rough estimates only; both sides of the same family overlap. No bodies or export artifacts were read or written.")
}
