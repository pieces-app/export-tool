package exporter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSummaryHierarchyUsesDedicatedDAGEndpoints(t *testing.T) {
	a, b, child := record("parent-a", ""), record("parent-b", ""), record("child", "")
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {a, b, child}}, summaryChildren: map[string][]string{"parent-a": {"child"}, "parent-b": {"child"}}}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	mats, _ := SelectMaterials("WORKSTREAM_SUMMARIES")
	out := filepath.Join(t.TempDir(), "export")
	manifest, err := Export(context.Background(), c, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: mats, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())})
	if err != nil {
		t.Fatal(err)
	}
	h := manifest.SummaryHierarchy
	if !h.Available || !h.InventoriesMatched || h.ParentCandidates != 2 || h.ChildCandidates != 1 || h.ParentsRead != 2 || h.EdgesRecovered != 2 {
		t.Fatalf("unexpected hierarchy coverage: %+v", h)
	}
	var paths map[string]string
	data, _ := os.ReadFile(filepath.Join(out, "link-map.json"))
	if err := json.Unmarshal(data, &paths); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(out, paths[opaque("WORKSTREAM_SUMMARIES", "child")]))
	if strings.Count(string(body), "- parents:") != 2 {
		t.Fatal("child did not retain both DAG parents")
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, "GET /workstream_summary/") && !strings.HasSuffix(call, "/child/identifiers") {
			t.Fatalf("unnecessary singular snapshot read: %s", call)
		}
	}
}

func TestSummaryHierarchyRejectsMissingOrInconsistentCollections(t *testing.T) {
	for _, scenario := range []string{"invalid", "unavailable", "inconsistent"} {
		t.Run(scenario, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if scenario == "unavailable" {
					w.WriteHeader(404)
					return
				}
				out := map[string]any{}
				if scenario == "inconsistent" {
					out["iterable"] = []any{}
					if strings.Contains(req.URL.Path, "/workstream_summaries/child/") {
						out["iterable"] = []any{map[string]any{"id": "unrecovered"}}
					}
				}
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer srv.Close()
			c, _ := NewClient(srv.URL, time.Second, 8<<20)
			r := &run{ctx: context.Background(), client: c, meta: map[string]*Meta{}, coverage: map[string]*Coverage{"WORKSTREAM_SUMMARIES": {}}}
			if err := r.resolveSummaryHierarchy(); err != nil {
				t.Fatal(err)
			}
			if len(r.manifest.Issues) == 0 || r.manifest.SummaryHierarchy.InventoriesMatched {
				t.Fatal("unknown hierarchy reported as reconciled")
			}
		})
	}
}

// Explicit opt-in, read-only integration probe. It stores no record content and
// prints only aggregate counts. The deadline and normal pacer bound server load.
func TestLiveSummaryHierarchy(t *testing.T) {
	base := os.Getenv("PIECES_EXPORT_LIVE_HIERARCHY_URL")
	if base == "" {
		t.Skip("set PIECES_EXPORT_LIVE_HIERARCHY_URL to explicitly opt into local reads")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	c, err := NewClient(base, 8*time.Second, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ConfigurePerformance("adaptive", 5, 500*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	m, _ := materialByType("WORKSTREAM_SUMMARIES")
	ids, err := c.IDs(ctx, m, Window{})
	if err != nil {
		t.Fatal(err)
	}
	r := &run{ctx: ctx, client: c, meta: map[string]*Meta{}, coverage: map[string]*Coverage{"WORKSTREAM_SUMMARIES": {}}}
	for _, id := range ids {
		key := "WORKSTREAM_SUMMARIES\x00" + id
		r.meta[key] = &Meta{Key: key, ID: id, Type: m.Type, State: "included"}
	}
	if err := r.resolveSummaryHierarchy(); err != nil {
		t.Fatal(err)
	}
	t.Logf("Summary IDs=%d hierarchy=%+v issues=%d performance=%+v", len(ids), r.manifest.SummaryHierarchy, len(r.manifest.Issues), c.Performance())
	if _, err := c.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.manifest.Issues) > 0 {
		t.Fatal("live hierarchy inventory reconciliation failed (no IDs or contents logged)")
	}
}
