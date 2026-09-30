package exporter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in, aggregate-only local probe. Run only after both SIGNALS and
// ANNOTATIONS have finished fetching. This neither contacts OS nor changes an
// archive, and its findings precede final privacy/coverage reconciliation.
func TestLiveSignalStageCoverage(t *testing.T) {
	stage := os.Getenv("PIECES_EXPORT_LIVE_SIGNAL_STAGE")
	if stage == "" {
		t.Skip("set PIECES_EXPORT_LIVE_SIGNAL_STAGE after signal and annotation fetch phases finish")
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		t.Fatal("staged archive could not be opened")
	}
	defer root.Close()
	entries, err := os.ReadDir(filepath.Join(stage, "data/signals"))
	if err != nil || len(entries) > 20000 {
		t.Fatal("signal directory unavailable or exceeds the 20,000-record probe limit")
	}
	fields := []string{"pipelines", "summaries", "workstream_events", "persons", "websites", "ranges", "annotations"}
	states, totals := map[string]map[string]int{}, map[string]int{}
	for _, field := range fields {
		states[field] = map[string]int{}
	}
	origins, categories := map[string]int{}, map[string]int{}
	enum := func(value string, allowed []string) string {
		for _, candidate := range allowed {
			if value == candidate {
				return value
			}
		}
		return "absent_or_unrecognized"
	}
	annotationKinds := map[string]int{}
	annotationPresent := map[string]bool{}
	descriptions := map[string]bool{}
	deadline := time.Now().Add(2 * time.Minute)
	withDescription, multipleDescriptions, withName, dated, emptyVector, nonemptyVector := 0, 0, 0, 0, 0, 0
	for _, entry := range entries {
		if time.Now().After(deadline) {
			t.Fatal("signal probe exceeded its time budget")
		}
		ref := strings.TrimSuffix(entry.Name(), ".json")
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || !validDigest(ref) {
			t.Fatal("unexpected staged signal directory entry")
		}
		b, err := archiveRead(root, "data/signals/"+entry.Name(), 8<<20)
		var v map[string]any
		if err != nil || decodeArchiveJSON(b, &v) != nil || fieldString(v, "id") == "" || opaque("SIGNALS", fieldString(v, "id")) != ref {
			t.Fatal("unreadable, oversized, or identity-mismatched staged signal")
		}
		if strings.TrimSpace(fieldString(v, "name")) != "" {
			withName++
		}
		if timestamp(v, "created") != "" {
			dated++
		}
		if vector, ok := v["signalsVector"].([]any); ok {
			if len(vector) == 0 {
				emptyVector++
			} else {
				nonemptyVector++
			}
		}
		origins[enum(fieldString(v, "origin"), []string{"UNKNOWN", "REALTIME_WORKSTREAM_PATTERN_ENGINE", "HIERARCHICAL_ROLLUP"})]++
		categories[enum(fieldString(v, "category"), []string{"UNKNOWN", "ORGANIZATION", "LOCATION", "CONCEPT", "PRODUCT", "EVENT", "ARTIFACT", "INTENT", "TASK", "PROBLEM", "DECISION"})]++
		for _, field := range fields {
			states[field][projectionState(v[field])]++
			totals[field] += len(references(v[field]))
		}
		matched := 0
		for _, id := range references(v["annotations"]) {
			if time.Now().After(deadline) || len(annotationPresent) >= 200000 {
				t.Fatal("annotation reconciliation exceeded its time or distinct-target budget")
			}
			if _, seen := annotationPresent[id]; !seen {
				path := "data/annotations/" + opaque("ANNOTATIONS", id) + ".json"
				if _, err := root.Stat(path); os.IsNotExist(err) {
					annotationPresent[id] = false
					continue
				}
				b, err := archiveRead(root, path, 8<<20)
				var a map[string]any
				if err != nil || decodeArchiveJSON(b, &a) != nil || fieldString(a, "id") != id {
					t.Fatal("unreadable, oversized, or identity-mismatched staged annotation")
				}
				annotationPresent[id] = true
				typ := enum(fieldString(a, "type"), []string{"SIGNAL_DESCRIPTION"})
				annotationKinds[typ]++
				descriptions[id] = typ == "SIGNAL_DESCRIPTION" && strings.TrimSpace(fieldString(a, "text")) != ""
			}
			if descriptions[id] {
				matched++
			}
		}
		if matched > 0 {
			withDescription++
		}
		if matched > 1 {
			multipleDescriptions++
		}
	}
	missing := 0
	for _, present := range annotationPresent {
		if !present {
			missing++
		}
	}
	// Only fixed labels and numeric counts leave this probe. No content, IDs,
	// record filenames, private enum-like values, or cache paths are logged.
	result := map[string]any{"staged_signals": len(entries), "with_name": withName, "with_created_timestamp": dated,
		"origin_counts": origins, "category_counts": categories, "projection_states": states, "active_reference_occurrences": totals,
		"distinct_annotation_targets": len(annotationPresent), "missing_annotation_targets": missing, "annotation_type_counts": annotationKinds,
		"signals_with_retained_description": withDescription, "signals_with_multiple_descriptions": multipleDescriptions,
		"explicit_empty_vectors": emptyVector, "nonempty_vectors": nonemptyVector}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal("aggregate encoding failed")
	}
	t.Log(string(b))
}
