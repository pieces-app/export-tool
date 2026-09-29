package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type ScanMaterial struct {
	UnknownProjections int     `json:"sampled_unknown_projections"`
	Type               string  `json:"type"`
	Count              int     `json:"count"`
	Status             string  `json:"status"`
	Sampled            int     `json:"sampled"`
	SampleSeconds      float64 `json:"sample_seconds"`
	SampleBytes        int     `json:"sample_bytes"`
}
type Preflight struct {
	Materials               []ScanMaterial `json:"materials"`
	Total, Unknown, Sampled int
	Low, High               time.Duration
	Duration                time.Duration
}

// Scan uses counts and at most three hydrated records per generic material.
// Sampling is explicitly bounded; it is not complete inventory enumeration.
func Scan(ctx context.Context, c *Client, materials []Material, progress io.Writer) (Preflight, error) {
	start := time.Now()
	p := Preflight{}
	predicted := 0.0
	for _, m := range materials {
		if c.Performance().Stopped {
			return p, ErrOSBusy
		}
		if ctx.Err() != nil {
			return p, ctx.Err()
		}
		item := ScanMaterial{Type: m.Type, Count: -1, Status: "unknown"}
		if m.SnapshotOnly {
			var out map[string]any
			if c.JSON(ctx, "GET", m.Collection, nil, &out) == nil {
				if iterable, ok := out["iterable"].([]any); ok {
					item.Count = len(iterable)
					item.Status = "snapshot"
				}
			}
		} else {
			n, err := c.Count(ctx, m, Window{})
			if err == nil {
				item.Count = n
				item.Status = "counted"
			}
		}
		if item.Count < 0 {
			p.Unknown++
		} else {
			p.Total += item.Count
		}
		if item.Count > 0 && !m.SnapshotOnly && m.Collection != "" {
			var ids struct {
				IDs []string `json:"identifiers"`
			}
			if c.JSON(ctx, "POST", "/materials/identifiers", map[string]any{"material_type": m.Type, "limit": 3}, &ids) == nil {
				if len(ids.IDs) > 3 {
					ids.IDs = ids.IDs[:3]
				}
				sampleStart := time.Now()
				records := []any{}
				if m.Batch != "" {
					want := []map[string]string{}
					for _, id := range ids.IDs {
						want = append(want, map[string]string{"id": id})
					}
					var out map[string]any
					if len(want) > 0 && c.JSON(ctx, "POST", m.Batch+"?transferables=true", map[string]any{m.Field: map[string]any{"iterable": want}}, &out) == nil {
						collection, _ := out[m.Field].(map[string]any)
						records, _ = collection["iterable"].([]any)
					}
				}
				item.SampleSeconds = time.Since(sampleStart).Seconds()
				item.Sampled = len(records)
				for _, raw := range records {
					v, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					for _, state := range projectionStates(m.Type, v) {
						if state == "absent" || state == "invalid" {
							item.UnknownProjections++
						}
					}
				}
				data, _ := json.Marshal(records)
				item.SampleBytes = len(data)
				if item.Sampled > 0 {
					p.Sampled += item.Sampled
					// Conservative serial-per-record extrapolation plus three privacy/render passes.
					predicted += float64(item.Count) * (item.SampleSeconds/float64(item.Sampled) + 0.004 + float64(item.SampleBytes)/float64(item.Sampled)/(4<<20))
				}
			}
			if item.Sampled == 0 {
				predicted += float64(item.Count) * 0.025
			}
		}
		if item.Count > 0 && m.Collection == "" {
			item.Status = "inventory_only"
		}
		p.Materials = append(p.Materials, item)
		if progress != nil {
			if item.Count < 0 {
				fmt.Fprintf(progress, "  %-40s unknown (read failed)\n", m.Type)
			} else {
				fmt.Fprintf(progress, "  %-40s %d\n", m.Type, item.Count)
			}
		}
	}
	p.Duration = time.Since(start)
	if c.Performance().Stopped {
		return p, ErrOSBusy
	}
	if p.Sampled > 0 {
		p.Low = time.Duration(max(1, predicted*0.5)) * time.Second
		p.High = time.Duration(max(5, predicted*3+10)) * time.Second
	}
	return p, nil
}
func (p Preflight) Print(w io.Writer, format string) {
	fmt.Fprintf(w, "\nInventory: %d records in known collections; %d collections unknown.\n", p.Total, p.Unknown)
	fmt.Fprintln(w, "Personas are stored among annotations; the annotation count is not a persona count.")
	unsampled := 0
	for _, item := range p.Materials {
		if item.Count > 0 && item.Sampled == 0 {
			unsampled++
		}
		if item.Status == "inventory_only" {
			fmt.Fprintf(w, "Coverage limitation: %s has %d records but no implemented read endpoint.\n", item.Type, item.Count)
		}
		if item.UnknownProjections > 0 {
			fmt.Fprintf(w, "Coverage limitation: %s sample has %d absent/invalid core relationship fields across %d records; a complete graph is unverified.\n", item.Type, item.UnknownProjections, item.Sampled)
		}
	}
	fmt.Fprintf(w, "%d nonempty collections have no read sample; their cost is assumed or unavailable.\n", unsampled)
	if p.Sampled == 0 {
		fmt.Fprintln(w, "Estimated duration: unavailable (no usable read samples).")
	} else {
		high := p.High
		if format != "markdown" {
			high *= 3
		}
		fmt.Fprintf(w, "Estimated duration: %s–%s, based on %d sampled records.\n", p.Low.Round(time.Second), high.Round(time.Second), p.Sampled)
		fmt.Fprintln(w, "Rough estimate includes assumed filtering/render overhead; large graphs, PDFs, and retries can take longer.")
	}
}
