package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func junctionFixture() *fakeOS {
	date := "2026-09-30T12:00:00Z"
	s, p, a, profile, pipeline := record("summary", date), record("person", date), record("body", date), record("profile", date), record("pipe", date)
	s["name"], s["parentHierarchicalType"] = "Current summary", "TEMPORAL_DAY_HIERARCHICAL_SUMMARY"
	p["name"], pipeline["name"] = "Example Person", "Daily Standup"
	a["type"], a["text"] = "SUMMARY", "Current linked narrative."
	profile["type"], profile["text"] = "HIERARCHICAL_PROFILE_SUMMARY", "Current profile history."
	f := &fakeOS{data: map[string][]map[string]any{
		"WORKSTREAM_SUMMARIES": {s}, "PERSONS": {p}, "ANNOTATIONS": {a, profile}, "PIPELINES": {pipeline},
	}}
	for i := 0; i < 20; i++ {
		v := record(fmt.Sprintf("unrelated-%d", i), date)
		v["text"] = "Unrelated annotation must not be exported."
		f.data["ANNOTATIONS"] = append(f.data["ANNOTATIONS"], v)
	}
	for i, link := range [][3]string{
		{"workstream_summary_to_annotation_associations", "summary", "body"},
		{"workstream_summary_to_person_associations", "summary", "person"},
		{"pipeline_to_workstream_summary_associations", "pipe", "summary"},
		{"person_to_annotation_associations", "person", "profile"},
	} {
		family, _ := junctionFamilyByName(link[0])
		v := record(fmt.Sprintf("association-%d", i), date)
		v[family.leftField], v[family.rightField] = link[1], link[2]
		f.data[family.material().Type] = append(f.data[family.material().Type], v)
	}
	return f
}

// Exercise the real Export flow with both the newer direct collections and the
// older material routes. Mutations operate on synthetic server responses only.
func junctionServer(t *testing.T, f *fakeOS, mutate func(*http.Request, map[string]any)) *httptest.Server {
	t.Helper()
	base := f.server(t)
	t.Cleanup(base.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 3 {
			family, ok := junctionFamilyByName(parts[0])
			if ok && (parts[1] == family.leftRoute || parts[1] == family.rightRoute) && (len(parts) == 3 || len(parts) == 4 && parts[3] == "count") {
				field := family.leftField
				if parts[1] == family.rightRoute {
					field = family.rightField
				}
				owners := map[string]bool{parts[2]: true}
				if parts[2] == "bulk" {
					var body struct {
						Iterable []string `json:"iterable"`
					}
					if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("invalid bulk request")
					}
					owners = map[string]bool{}
					for _, id := range body.Iterable {
						owners[id] = true
					}
				}
				rows := []map[string]any{}
				for _, v := range f.data[family.material().Type] {
					if owners[fieldString(v, field)] {
						rows = append(rows, v)
					}
				}
				out := map[string]any{}
				if len(parts) == 4 {
					out["id"], out["count"] = parts[2], len(rows)
				} else {
					if parts[2] != "bulk" {
						offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
						limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
						rows = rows[min(offset, len(rows)):min(offset+limit, len(rows))]
					}
					indices := map[string]int{}
					for i, v := range rows {
						indices[fieldString(v, "id")] = i
					}
					out["iterable"], out["indices"] = rows, indices
				}
				if mutate != nil {
					mutate(r, out)
				}
				_ = json.NewEncoder(w).Encode(out)
				return
			}
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, base.URL+r.URL.RequestURI(), r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func junctionExportOptions(t *testing.T) Options {
	t.Helper()
	sel, _ := SelectScope("summaries", "")
	return Options{Scope: sel.Name, Materials: sel.Materials, ReferenceOnly: sel.ReferenceOnly, Output: filepath.Join(t.TempDir(), "archive"), Mode: "filtered", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, PeopleMode: "profiles", Format: "markdown", Metadata: "off", Scanner: scanner(t, DefaultPolicy())}
}

func TestCurrentJunctionExportAndOfflineRebuild(t *testing.T) {
	f := junctionFixture()
	srv := junctionServer(t, f, nil)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	o := junctionExportOptions(t)
	store, _ := captureTestStore(t)
	o.captureCheckpoint = func(r *run) error { return r.saveCapture(store) }
	m, err := Export(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "complete_for_implemented_scope" {
		t.Fatalf("unexpected coverage gaps: %+v", m.Issues)
	}
	if m.ArchiveState == nil || m.ArchiveState.Version != 2 {
		t.Fatal("archive does not fence incompatible older rebuilders")
	}
	for _, typ := range f.requestedMaterials {
		if typ == "ANNOTATIONS" {
			t.Fatal("enumerated unrelated annotations despite complete current owner reads")
		}
	}
	if o.ReferenceOnly["ANNOTATIONS"] {
		t.Fatal("mutated caller selection")
	}
	for _, cov := range m.Coverage {
		if cov.Material == "ANNOTATIONS" && (cov.Fetched != 2 || cov.InventoryMode != "references") {
			t.Fatalf("wrong annotations: %+v", cov)
		}
	}
	for _, row := range m.RelationshipCoverage {
		if row.Unresolved != 0 || row.JunctionReconciled != row.Included {
			t.Fatalf("current completeness missing: %+v", row)
		}
	}
	if m.People.UnknownSummaries != 0 || m.People.Selected != 1 {
		t.Fatalf("person evidence unresolved: %+v", m.People)
	}
	assertJunctionArchive := func(path string) {
		t.Helper()
		var links map[string]string
		b, _ := os.ReadFile(filepath.Join(path, "link-map.json"))
		if err := json.Unmarshal(b, &links); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(path, links[opaque("WORKSTREAM_SUMMARIES", "summary")]))
		if err != nil || !strings.Contains(string(body), "Current linked narrative.") {
			t.Fatal("current body missing from summary", err)
		}
		if !strings.Contains(links[opaque("WORKSTREAM_SUMMARIES", "summary")], "/timeline/") || !strings.Contains(string(body), "Daily Standup") {
			t.Fatal("temporal summary classification or explicit pipeline navigation lost")
		}
		graph, _ := os.ReadFile(filepath.Join(path, "relationships.jsonl"))
		if !strings.Contains(string(graph), `"association_record"`) {
			t.Fatal("canonical association provenance missing")
		}
		r := &run{ctx: context.Background(), stage: path}
		if err := r.validateMarkdownLinks(); err != nil {
			t.Fatal(err)
		}
	}
	assertJunctionArchive(o.Output)
	requests := c.Performance().Requests
	replayed := filepath.Join(t.TempDir(), "replayed")
	replay, err := replayCapture(context.Background(), store, replayed, nil)
	if err != nil || replay.People.UnknownSummaries != 0 || c.Performance().Requests != requests {
		t.Fatal("completed-source recovery lost junction evidence or contacted source", err)
	}
	assertJunctionArchive(replayed)
	for i := 0; i < 2; i++ {
		out := filepath.Join(t.TempDir(), "rebuilt")
		next, err := Rebuild(context.Background(), RebuildOptions{Source: o.Output, Options: Options{Output: out, Scanner: scanner(t, DefaultPolicy())}})
		if err != nil {
			t.Fatal(err)
		}
		if next.People.UnknownSummaries != 0 || len(next.Junctions) != len(m.Junctions) {
			t.Fatal("rebuild lost current source evidence")
		}
		if next.ArchiveState.Version != 2 {
			t.Fatal("rebuild lost current-junction compatibility boundary")
		}
		assertJunctionArchive(out)
		o.Output = out
	}
}

func TestCurrentJunctionDriftAndInvalidBulkNeverFinalize(t *testing.T) {
	for _, damage := range []string{"count_change", "truncated", "wrong_owner", "contradictory_projection"} {
		t.Run(damage, func(t *testing.T) {
			f := junctionFixture()
			if damage == "contradictory_projection" {
				f.data["WORKSTREAM_SUMMARIES"][0]["annotations"] = refs("unrelated-1")
			}
			counts := 0
			srv := junctionServer(t, f, func(req *http.Request, out map[string]any) {
				if !strings.HasPrefix(req.URL.Path, "/workstream_summary_to_annotation_associations/") {
					return
				}
				if strings.HasSuffix(req.URL.Path, "/count") {
					counts++
					if damage == "count_change" && counts == 2 {
						out["count"] = 2
					}
				} else if damage == "truncated" {
					out["truncated"] = true
				} else if damage == "wrong_owner" {
					out["iterable"].([]map[string]any)[0]["workstreamSummary"] = "unexpected"
				}
			})
			c, _ := NewClient(srv.URL, time.Second, 8<<20)
			o := junctionExportOptions(t)
			if _, err := Export(context.Background(), c, o); err == nil {
				t.Fatal("invalid current graph accepted")
			}
			if _, err := os.Stat(o.Output); !os.IsNotExist(err) {
				t.Fatal("invalid run finalized")
			}
		})
	}
}

func TestCurrentJunctionDeniedOwnerWithholdsSharedBody(t *testing.T) {
	f := junctionFixture()
	denied := record("denied-summary", "2026-09-30T12:00:00Z")
	denied["url"] = "https://bank.example/private"
	f.data["WORKSTREAM_SUMMARIES"] = append(f.data["WORKSTREAM_SUMMARIES"], denied)
	family, _ := junctionFamilyByName("workstream_summary_to_annotation_associations")
	v := record("denied-binding", "2026-09-30T12:00:00Z")
	v[family.leftField], v[family.rightField] = "denied-summary", "body"
	f.data[family.material().Type] = append(f.data[family.material().Type], v)
	srv := junctionServer(t, f, nil)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	o := junctionExportOptions(t)
	p := DefaultPolicy()
	p.Deny = []DomainRule{{"bank.example", true}}
	o.Scanner = scanner(t, p)
	m, err := Export(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, cov := range m.Coverage {
		if cov.Material == "ANNOTATIONS" && cov.Withheld != 1 {
			t.Fatalf("shared body escaped denial: %+v", cov)
		}
	}
	if err := filepath.WalkDir(o.Output, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if strings.Contains(string(b), "Current linked narrative.") {
			t.Fatal("denied shared body leaked into output")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentJunctionRegistryKeepsLegacyBindings(t *testing.T) {
	for _, legacy := range associationFamilies {
		current, ok := associationFamilyByType(legacy.material().Type)
		if !ok || current != legacy {
			t.Fatalf("legacy binding changed: %s", legacy.name)
		}
	}
}

func TestCurrentJunctionEmptyBeatsHistoricalBody(t *testing.T) {
	f := junctionFixture()
	f.data["WORKSTREAM_SUMMARY_TO_ANNOTATION_ASSOCIATIONS"] = nil
	// Retain the cached target through a different, current relationship, so
	// this checks precedence rather than merely skipping an unavailable target.
	v := record("person-body", "2026-09-30T12:00:00Z")
	v["person"], v["annotation"] = "person", "body"
	f.data["PERSON_TO_ANNOTATION_ASSOCIATIONS"] = append(f.data["PERSON_TO_ANNOTATION_ASSOCIATIONS"], v)
	cached := record("summary", "2026-09-30T12:00:00Z")
	cached["annotations"] = refs("body")
	cache, _ := cacheFixture(t, []map[string]any{cached}, false)
	srv := junctionServer(t, f, nil)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	o := junctionExportOptions(t)
	m, err := Export(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if m.SDKCache.AddedEdges != 0 {
			t.Fatal("cache resurrected an attachment superseded by a current empty set")
		}
		var links map[string]string
		b, _ := os.ReadFile(filepath.Join(o.Output, "link-map.json"))
		if err := json.Unmarshal(b, &links); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(o.Output, links[opaque("WORKSTREAM_SUMMARIES", "summary")]))
		if err != nil || strings.Contains(string(body), "Current linked narrative.") {
			t.Fatal("stale summary body restored", err)
		}
		if i == 0 {
			out := filepath.Join(t.TempDir(), "rebuilt")
			m, err = Rebuild(context.Background(), RebuildOptions{Source: o.Output, Options: Options{Output: out, SDKCaches: []string{cache}, Scanner: scanner(t, DefaultPolicy())}})
			if err != nil {
				t.Fatal(err)
			}
			o.Output = out
		}
	}
}

func TestCurrentJunctionPagedOwnerReconciliation(t *testing.T) {
	for _, damage := range []string{"", "repeated_row", "changed_head", "extra_tail"} {
		t.Run(damage, func(t *testing.T) {
			f := junctionFixture()
			family, _ := junctionFamilyByName("workstream_summary_to_annotation_associations")
			rows := []map[string]any{}
			for i := 0; i < 51; i++ {
				v := record(fmt.Sprintf("page-edge-%03d", i), "2026-09-30T12:00:00Z")
				v[family.leftField], v[family.rightField] = "summary", "body"
				rows = append(rows, v)
			}
			f.data[family.material().Type] = rows
			headReads := 0
			srv := junctionServer(t, f, func(req *http.Request, out map[string]any) {
				if strings.HasSuffix(req.URL.Path, "/count") {
					return
				}
				offset := req.URL.Query().Get("offset")
				if offset == "0" {
					headReads++
				}
				var replace map[string]any
				if damage == "repeated_row" && offset == "50" || damage == "extra_tail" && offset == "51" {
					replace = rows[0]
				} else if damage == "changed_head" && headReads == 2 {
					replace = record("new-head", "2026-09-30T12:00:00Z")
					replace[family.leftField], replace[family.rightField] = "summary", "body"
				}
				if replace != nil {
					out["iterable"] = []map[string]any{replace}
					out["indices"] = map[string]int{fieldString(replace, "id"): 0}
				}
			})
			c, _ := NewClient(srv.URL, time.Second, 8<<20)
			owner := &Meta{Type: "WORKSTREAM_SUMMARIES", ID: "summary", Key: "WORKSTREAM_SUMMARIES\x00summary"}
			r := &run{ctx: context.Background(), client: c, opts: Options{Mode: "preserve"}, stage: t.TempDir(), meta: map[string]*Meta{owner.Key: owner}, coverage: map[string]*Coverage{}, junctionFamiliesRead: map[string]bool{}}
			stats := &JunctionCoverage{}
			err := r.pageJunctionOwner(summaryJunctionPlans[0], "summary", 51, stats, map[string][32]byte{})
			if damage == "" {
				if err != nil || stats.PageReads != 4 || stats.Rows != 51 || !owner.JunctionFields["annotations"] {
					t.Fatal("paged current relationships did not reconcile", err, stats)
				}
			} else if err == nil || owner.JunctionFields["annotations"] {
				t.Fatal("unstable paged relationships certified", damage, err)
			}
		})
	}
}

func TestPackagedCurrentJunctionCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the native release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	f := junctionFixture()
	srv := junctionServer(t, f, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out := filepath.Join(t.TempDir(), "linked summaries")
	// No scope/people arguments: verify the shipping defaults use this path.
	b, err := exec.CommandContext(ctx, binary, "export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--format", "markdown", "--metadata", "off", "--output", out).CombinedOutput()
	if err != nil {
		t.Fatalf("packaged junction export failed: %v\n%s", err, b)
	}
	m, err := InspectArchive(out)
	if err != nil || m.Scope.Name != "summaries" || m.People.Mode != "profiles" || len(m.Junctions) != 11 || m.ArchiveState == nil || m.ArchiveState.Version != 2 {
		t.Fatal("packaged default selection or current traversal failed", err)
	}
	for _, cov := range m.Coverage {
		if cov.Material == "ANNOTATIONS" && (cov.InventoryMode != "references" || cov.Included != 2) {
			t.Fatalf("packaged path included unrelated annotations: %+v", cov)
		}
	}
	report, err := inspectFinalArchive(ctx, out)
	if err != nil || report.Summaries != 1 || report.SummariesWithBody != 1 || report.PersonsWithProfile != 1 || report.HistoricalEdges != 0 {
		t.Fatal("packaged final archive did not reconcile", err, report)
	}
	for _, call := range f.calls {
		if strings.Contains(call, " /workstream_event/") || strings.Contains(call, " /workstream_events/") || strings.Contains(call, " /person/person/annotations") {
			t.Fatal("packaged current traversal read unnecessary event/profile history", call)
		}
	}
}
