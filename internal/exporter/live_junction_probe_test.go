package exporter

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Run only when no other exporter is reading OS. All requests are read-only,
// with three summary owners, at most 500 total rows per family and a deadline.
// This proves bounded endpoint availability/consistency, not full coverage.
func TestLiveJunctionReadCapabilities(t *testing.T) {
	base := os.Getenv("PIECES_EXPORT_LIVE_JUNCTION_URL")
	if base == "" {
		t.Skip("set PIECES_EXPORT_LIVE_JUNCTION_URL for bounded read-only OS capability checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := NewClient(base, 10*time.Second, 4<<20)
	if err != nil {
		t.Fatal("invalid local probe configuration")
	}
	if err := c.ConfigurePerformance("adaptive", 5, 500*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	_, err = c.Probe(ctx)
	if err != nil {
		t.Fatal("OS is not ready for a bounded read probe")
	}
	var inventory struct {
		IDs []string `json:"identifiers"`
	}
	if err := c.JSON(ctx, "POST", "/materials/identifiers", map[string]any{"material_type": "WORKSTREAM_SUMMARIES", "limit": 3}, &inventory); err != nil || len(inventory.IDs) == 0 || len(inventory.IDs) > 3 {
		t.Fatal("bounded summary inventory failed")
	}
	result := map[string]any{"sampled_summaries": len(inventory.IDs)}
	annotationOwners := map[string]map[string]bool{}
	for _, family := range []string{"workstream_summary_to_annotation_associations", "workstream_summary_to_person_associations", "pipeline_to_workstream_summary_associations"} {
		counts := map[string]int{}
		total := 0
		for _, id := range inventory.IDs {
			n, err := c.readJunctionCount(ctx, family, "workstream_summary", id)
			if err != nil {
				t.Fatalf("%s count capability failed: %v", family, err)
			}
			counts[id], total = n, total+n
		}
		if total > 500 {
			t.Fatal("probe sample exceeds its row budget")
		}
		rows, err := c.readJunctionBulk(ctx, family, "workstream_summary", counts)
		if err != nil {
			t.Fatalf("%s counted bulk read failed: %v", family, err)
		}
		f, _ := junctionFamilyByName(family)
		_, ownerField, _ := junctionSide(family, "workstream_summary")
		byID := map[string]map[string]any{}
		for _, row := range rows {
			byID[fieldString(row, "id")] = row
			if family == "workstream_summary_to_annotation_associations" {
				id := fieldString(row, "annotation")
				if annotationOwners[id] == nil {
					annotationOwners[id] = map[string]bool{}
				}
				annotationOwners[id][fieldString(row, "workstreamSummary")] = true
			}
		}
		pageRows := 0
		for _, id := range inventory.IDs {
			pageIDs := map[string]bool{}
			// Verify offset windows against the already bounded bulk response;
			// at most two rows per owner, so even high-degree samples stay cheap.
			for offset := 0; offset < min(2, counts[id]); offset++ {
				page, err := c.readJunctionPage(ctx, family, "workstream_summary", id, 1, offset)
				if err != nil || len(page) != 1 {
					t.Fatalf("%s bounded page failed", family)
				}
				row := page[0]
				rowID := fieldString(row, "id")
				match := byID[rowID]
				if match == nil || pageIDs[rowID] || fieldString(row, ownerField) != id || fieldString(row, f.leftField) != fieldString(match, f.leftField) || fieldString(row, f.rightField) != fieldString(match, f.rightField) {
					t.Fatal("page differs from counted bulk endpoints")
				}
				pageIDs[rowID] = true
				pageRows++
			}
			n, err := c.readJunctionCount(ctx, family, "workstream_summary", id)
			if err != nil || n != counts[id] {
				t.Fatal("junction count changed during capability probe")
			}
		}
		result[family] = map[string]int{"counted_rows": total, "bulk_rows": len(rows), "checked_page_rows": pageRows}
	}
	// Read actual canonical text only through the current junction targets.
	// The probe never prints or persists that text, IDs, or private names.
	if len(annotationOwners) == 0 || len(annotationOwners) > 50 {
		t.Fatal("summary-body sample is empty or exceeds its hydration budget")
	}
	refs := []map[string]string{}
	for id := range annotationOwners {
		refs = append(refs, map[string]string{"id": id})
	}
	var hydrated map[string]any
	if err := c.JSON(ctx, "POST", "/annotations/batch/fetch?transferables=true", map[string]any{"annotations": map[string]any{"iterable": refs}}, &hydrated); err != nil {
		t.Fatal("junction annotation hydration failed")
	}
	rows, ok := object(hydrated, "annotations")["iterable"].([]any)
	if !ok || len(rows) != len(annotationOwners) {
		t.Fatal("junction annotations did not hydrate completely")
	}
	seen, withBody := map[string]bool{}, map[string]bool{}
	for _, raw := range rows {
		v, ok := raw.(map[string]any)
		id := fieldString(v, "id")
		if !ok || annotationOwners[id] == nil || seen[id] {
			t.Fatal("junction annotation hydration returned duplicate or unexpected identities")
		}
		seen[id] = true
		if fieldString(v, "text") != "" && (fieldString(v, "type") == "SUMMARY" || fieldString(v, "type") == "DEEP_STUDY_HIERARCHICAL_SUMMARY") {
			for owner := range annotationOwners[id] {
				withBody[owner] = true
			}
		}
	}
	result["sampled_summaries_with_current_body"] = len(withBody)
	result["hydrated_current_annotations"] = len(seen)
	if len(withBody) == 0 {
		t.Fatal("current association sample did not demonstrate a summary body")
	}
	// Check the other side of pipeline membership too: the three summary
	// owners above may legitimately have no pipeline. Inventory at most 20.
	var pipelines struct {
		IDs []string `json:"identifiers"`
	}
	if err := c.JSON(ctx, "POST", "/materials/identifiers", map[string]any{"material_type": "PIPELINES", "limit": 20}, &pipelines); err != nil || len(pipelines.IDs) > 20 {
		t.Fatal("bounded pipeline inventory failed")
	}
	counts, nonempty, total := map[string]int{}, 0, 0
	for _, id := range pipelines.IDs {
		n, err := c.readJunctionCount(ctx, "pipeline_to_workstream_summary_associations", "pipeline", id)
		if err != nil {
			t.Fatal("pipeline-side count capability failed", err)
		}
		if n > 0 {
			nonempty++
		}
		if n > 0 && len(counts) < 3 && n <= 500-total {
			counts[id], total = n, total+n
		}
	}
	verified := 0
	if len(counts) > 0 {
		rows, err := c.readJunctionBulk(ctx, "pipeline_to_workstream_summary_associations", "pipeline", counts)
		if err != nil {
			t.Fatal("pipeline-side counted bulk failed", err)
		}
		verified = len(rows)
		for id, count := range counts {
			n, err := c.readJunctionCount(ctx, "pipeline_to_workstream_summary_associations", "pipeline", id)
			if err != nil || n != count {
				t.Fatal("pipeline membership changed during the probe")
			}
		}
	}
	result["pipeline_side"] = map[string]int{"owners_sampled": len(pipelines.IDs), "nonempty_owners": nonempty, "bulk_owners": len(counts), "verified_rows": verified}
	if _, err := c.Probe(ctx); err != nil {
		t.Fatal("OS did not respond after the read-only probe")
	}
	result["http"] = c.Performance()
	b, _ := json.Marshal(result)
	t.Log(string(b))
	t.Log("Current count/bulk/page endpoints are consistent for three sampled owners; no complete-history coverage claim.")
}
