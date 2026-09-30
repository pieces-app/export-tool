package exporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func pageAssociation(id, event, person string) map[string]any {
	v := record(id, "2026-09-30T00:00:00Z")
	v["workstream_event"], v["person"], v["role"], v["confidence"], v["evidence_type"], v["source_type"] = event, person, "SPEAKER", "HIGH", "EXPLICIT", "AUDIO"
	return v
}

func TestAssociationPageReadContract(t *testing.T) {
	for _, kind := range []string{"person", "workstream_event"} {
		t.Run(kind, func(t *testing.T) {
			id := "synthetic +/é?%endpoint"
			v := pageAssociation("association", "event", "person")
			v[kind] = id
			v["future_metadata"] = json.Number("9007199254740993")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.EscapedPath() != "/workstream_event_to_person_associations/"+kind+"/"+url.PathEscape(id) || r.URL.Query().Get("limit") != "50" || r.URL.Query().Get("offset") != "10" || r.URL.Query().Get("transferables") != "false" {
					t.Error("unexpected page read route or unbounded query")
				}
				json.NewEncoder(w).Encode(map[string]any{"iterable": []any{v}, "limit": 50, "offset": 10, "total": 11})
			}))
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 1<<20)
			page, err := client.readEventPersonPage(context.Background(), kind, id, 50, 10)
			if err != nil || page.total != 11 || len(page.records) != 1 || page.records[0]["future_metadata"] != json.Number("9007199254740993") {
				t.Fatalf("bounded source page lost metadata: %v", err)
			}
		})
	}
}

func TestAssociationPageRejectsInvalidResponses(t *testing.T) {
	for _, scenario := range []string{"missing-total", "missing-limit", "wrong-offset", "string-total", "fraction-total", "negative-total", "unbounded-total", "excessive-rows", "invalid-list", "wrong-person", "invalid-event", "bad-timestamp", "duplicate", "tombstone", "unreturned-index", "rows-beyond-total"} {
		t.Run(scenario, func(t *testing.T) {
			v := pageAssociation("association", "event", "person")
			response := map[string]any{"iterable": []any{v}, "limit": 50, "offset": 0, "total": 1}
			switch scenario {
			case "missing-total":
				delete(response, "total")
			case "missing-limit":
				delete(response, "limit")
			case "wrong-offset":
				response["offset"] = 1
			case "string-total":
				response["total"] = "1"
			case "fraction-total":
				response["total"] = 1.5
			case "negative-total":
				response["total"] = -1
			case "unbounded-total":
				response["total"] = maxAssociationPairs + 1
			case "excessive-rows":
				rows := []any{}
				for i := 0; i < 51; i++ {
					rows = append(rows, v)
				}
				response["iterable"], response["total"] = rows, 51
			case "invalid-list":
				response["iterable"] = map[string]any{}
			case "wrong-person":
				v["person"] = "PRIVATE_OTHER_OWNER_SENTINEL"
			case "invalid-event":
				v["workstream_event"] = ""
			case "bad-timestamp":
				delete(v, "updated")
			case "duplicate":
				response["iterable"], response["total"] = []any{v, v}, 2
			case "tombstone":
				response["indices"] = map[string]any{"association": -1}
			case "unreturned-index":
				response["indices"] = map[string]any{"missing": 0}
			case "rows-beyond-total":
				response["total"] = 0
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(response) }))
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 1<<20)
			page, err := client.readEventPersonPage(context.Background(), "person", "person", 50, 0)
			if err == nil || len(page.records) > 0 || strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatal("invalid paged metadata accepted or sensitive error disclosed")
			}
		})
	}
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1) }))
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 1<<20)
	for _, args := range []struct {
		kind, id      string
		limit, offset int
	}{{"../mutate", "x", 50, 0}, {"person", "..", 50, 0}, {"person", "x", 51, 0}, {"person", "x", 50, -1}, {"person", "x", 50, maxAssociationPairs + 1}} {
		if _, err := client.readEventPersonPage(context.Background(), args.kind, args.id, args.limit, args.offset); err == nil {
			t.Fatal("unsafe page query allowed")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.readEventPersonPage(ctx, "person", "x", 50, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("page read ignored cancellation")
	}
	if count.Load() != 0 {
		t.Fatal("invalid page query contacted source")
	}
}

func pagedAssociationFixture(t *testing.T, count int) (*fakeOS, Policy) {
	t.Helper()
	person := record("person", "2026-09-30T00:00:00Z")
	person["name"], person["annotations"], person["summaries"] = "Example Person", refs(), refs()
	f := &fakeOS{data: map[string][]map[string]any{"PERSONS": {person}, "ANNOTATIONS": {}, "WORKSTREAM_EVENTS": {}, "WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS": {}}}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("event-%03d", i)
		event := record(id, "2026-09-30T00:00:00Z")
		event["text"] = "Approved event narrative."
		v := pageAssociation(fmt.Sprintf("association-%03d", i), id, "person")
		v["explanation"] = "Explicit paginated person evidence."
		if i == 0 {
			event["url"] = "https://bank.example/private"
			v["explanation"] = "Private paginated association sentinel"
		}
		// Neither endpoint snapshot exposes this relationship.
		f.data["WORKSTREAM_EVENTS"] = append(f.data["WORKSTREAM_EVENTS"], event)
		f.data["WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS"] = append(f.data["WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS"], v)
	}
	policy := DefaultPolicy()
	policy.Deny = []DomainRule{{"bank.example", true}}
	return f, policy
}

func assertPagedAssociationArchive(t *testing.T, root string, m Manifest, policy Policy, count int) {
	t.Helper()
	var stats *AssociationPageCoverage
	for _, row := range m.Associations.Families {
		if row.Family == eventPersonFamily {
			stats = row.Pagination
			if row.Lookups != 0 || row.Pairs != 0 {
				t.Fatal("hidden relationships were guessed as pairs or re-fetched after pagination")
			}
		}
	}
	if stats == nil || stats.Reconciled != 1 || stats.Rows != count || stats.Pages != (count+49)/50+2 || stats.Drift+stats.Failed+stats.Unavailable+stats.Unsupported+stats.SchemaUnsupported != 0 {
		t.Fatalf("paged coverage incorrect: %+v", stats)
	}
	found := false
	for _, cov := range m.Coverage {
		if cov.Material == "WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS" {
			found = true
			if cov.Inventoried != count || cov.Fetched != count || cov.Included != count-1 || cov.Withheld != 1 || cov.InitialCount != -1 || cov.FinalCount != -1 {
				t.Fatalf("association read/privacy coverage incorrect: %+v", cov)
			}
		}
	}
	if !found {
		t.Fatal("paged association data missing")
	}
	var links map[string]string
	b, err := os.ReadFile(filepath.Join(root, "link-map.json"))
	if err != nil || json.Unmarshal(b, &links) != nil {
		t.Fatal("missing path map")
	}
	if links[opaque("WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS", "association-000")] != "" {
		t.Fatal("blocked event association exposed")
	}
	b, err = os.ReadFile(filepath.Join(root, "relationships.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	derived := 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var edge PublicEdge
		if json.Unmarshal([]byte(line), &edge) != nil {
			t.Fatal("invalid exported graph")
		}
		if edge.Provenance == "association_record" {
			derived++
			if links[edge.AssociationRef] == "" || !strings.Contains(links[edge.AssociationRef], "workstream_event_to_person_associations") {
				t.Fatal("source-derived edge lacks a retained canonical proof")
			}
		}
	}
	if derived != 2*(count-1) {
		t.Fatalf("missing explicit event/person navigation: %d", derived)
	}
	v, err := readRecord(filepath.Join(root, "data/events", opaque("WORKSTREAM_EVENTS", "event-001")+".json"))
	if err != nil || v["persons"] != nil {
		t.Fatal("source snapshot projections were fabricated")
	}
	check := &run{ctx: context.Background(), stage: root, opts: Options{Mode: "filtered", Scanner: scanner(t, policy)}}
	if err := check.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := check.auditOutput(); err != nil {
		t.Fatal(err)
	}
	if m.Format != "markdown" {
		path := links[opaque("WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS", "association-001")]
		if err := check.auditPDF(filepath.Join(root, pdfPath(path))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAssociationPagesRecoverHiddenGraphAndRebuild(t *testing.T) {
	f, policy := pagedAssociationFixture(t, 123)
	srv := f.server(t)
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("PERSONS,WORKSTREAM_EVENTS,ANNOTATIONS")
	out := filepath.Join(t.TempDir(), "paged export")
	m, err := Export(context.Background(), client, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy), Format: "both"})
	srv.Close()
	if err != nil {
		t.Fatal(err)
	}
	assertPagedAssociationArchive(t, out, m, policy, 123)
	before := archiveHashes(t, out)
	rebuilt := out + "-rebuilt"
	m, err = Rebuild(context.Background(), RebuildOptions{Source: out, Options: Options{Output: rebuilt, Scanner: scanner(t, policy)}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, archiveHashes(t, out)) {
		t.Fatal("paged archive changed during rebuild")
	}
	if err := os.Rename(rebuilt, rebuilt+" moved"); err != nil {
		t.Fatal(err)
	}
	assertPagedAssociationArchive(t, rebuilt+" moved", m, policy, 123)
}

func TestAssociationPagesDetectDrift(t *testing.T) {
	for _, scenario := range []string{"total-change", "repeated-page", "short-page", "head-change", "unsupported", "unavailable", "legacy-schema"} {
		t.Run(scenario, func(t *testing.T) {
			f, policy := pagedAssociationFixture(t, 51)
			base := f.server(t)
			defer base.Close()
			headReads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/workstream_event_to_person_associations/person/person" && r.URL.Query().Get("limit") == "50" {
					offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
					if scenario == "legacy-schema" {
						json.NewEncoder(w).Encode(map[string]any{"iterable": []any{}})
						return
					}
					if scenario == "unsupported" {
						w.WriteHeader(501)
						return
					}
					if scenario == "unavailable" {
						w.WriteHeader(404)
						return
					}
					rows := f.data["WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS"]
					if offset == 0 {
						headReads++
					}
					if scenario == "head-change" && headReads == 2 {
						copy := map[string]any{}
						for k, v := range rows[0] {
							copy[k] = v
						}
						copy["explanation"] = "Changed since first page."
						rows = append([]map[string]any{copy}, rows[1:]...)
					}
					total := len(rows)
					start, end := min(offset, total), min(offset+50, total)
					rows = rows[start:end]
					if offset == 50 {
						switch scenario {
						case "total-change":
							total++
						case "repeated-page":
							rows = f.data["WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS"][:1]
						case "short-page":
							rows = []map[string]any{}
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"iterable": rows, "total": total, "limit": 50, "offset": offset})
					return
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			materials, _ := SelectMaterials("PERSONS,WORKSTREAM_EVENTS,ANNOTATIONS")
			m, err := Export(context.Background(), client, Options{Output: filepath.Join(t.TempDir(), "export"), Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy)})
			if err != nil {
				t.Fatal(err)
			}
			p := m.Associations.Families[0].Pagination
			if m.Status != "partial" || p == nil || p.Reconciled != 0 || p.Drift+p.Unavailable+p.Unsupported+p.SchemaUnsupported != 1 {
				t.Fatalf("unstable pagination was not disclosed: %+v", p)
			}
		})
	}
}

func TestPackagedPagedAssociationCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	f, policy := pagedAssociationFixture(t, 123)
	srv := f.server(t)
	defer srv.Close()
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.json")
	if err := writeJSON(policyPath, policy); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	call := func(args ...string) {
		t.Helper()
		b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 2 {
				t.Fatalf("compiled paged association command failed: %v\n%s", err, b)
			}
		}
	}
	out := filepath.Join(root, "source")
	call("export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--materials", "PERSONS,WORKSTREAM_EVENTS,ANNOTATIONS", "--format", "both", "--policy", policyPath, "--output", out)
	srv.Close()
	m, err := InspectArchive(out)
	if err != nil {
		t.Fatal(err)
	}
	assertPagedAssociationArchive(t, out, m, policy, 123)
	before := archiveHashes(t, out)
	rebuilt := out + "-rebuilt"
	call("rebuild", "--source", out, "--output", rebuilt, "--policy", policyPath, "--format", "both", "--yes")
	m, err = InspectArchive(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, archiveHashes(t, out)) {
		t.Fatal("compiled offline rebuild modified its source")
	}
	if err := os.Rename(rebuilt, rebuilt+" moved"); err != nil {
		t.Fatal(err)
	}
	assertPagedAssociationArchive(t, rebuilt+" moved", m, policy, 123)
	t.Log("compiled pagination recovered 123 source associations without projected edges; privacy removed one; 244 proven navigation edges and moved Markdown/PDF links survived offline rebuild")
}

func TestAssociationPagesReachNewEventsAndRespectOff(t *testing.T) {
	for _, scenario := range []string{"new-event", "missing-event", "off"} {
		t.Run(scenario, func(t *testing.T) {
			f, policy := pagedAssociationFixture(t, 1)
			event := f.data["WORKSTREAM_EVENTS"][0]
			delete(event, "url")
			f.data["WORKSTREAM_EVENTS"] = nil
			base := f.server(t)
			defer base.Close()
			var pageReads atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/workstream_event_to_person_associations/person/person" && r.URL.Query().Get("limit") == "50" {
					pageReads.Add(1)
					if scenario == "new-event" {
						f.data["WORKSTREAM_EVENTS"] = []map[string]any{event}
					}
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			materials, _ := SelectMaterials("PERSONS,WORKSTREAM_EVENTS,ANNOTATIONS")
			opts := Options{Output: filepath.Join(t.TempDir(), "export"), Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy)}
			if scenario == "off" {
				opts.Associations = "off"
			}
			m, err := Export(context.Background(), client, opts)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "off" {
				if pageReads.Load() != 0 {
					t.Fatal("off mode enumerated associations")
				}
				return
			}
			if pageReads.Load() != 3 {
				t.Fatal("one-page history skipped its terminal/head checks")
			}
			if scenario == "missing-event" {
				if m.Status != "partial" || m.Associations.Families[0].Pagination.UnavailableEndpoints != 1 {
					t.Fatal("missing paged endpoint was not reported")
				}
				for _, cov := range m.Coverage {
					if cov.Material == "WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS" && cov.Included != 0 {
						t.Fatal("association retained an unavailable event")
					}
				}
			} else {
				if m.Status != "partial" {
					t.Fatal("new source event did not produce inventory drift")
				}
				for _, cov := range m.Coverage {
					if cov.Material == "WORKSTREAM_EVENTS" && (cov.InitialCount != 0 || cov.FinalCount != 1 || cov.Fetched != 1 || cov.Included != 1) {
						t.Fatal("new event was not resolved before final inventory reconciliation")
					}
				}
			}
		})
	}
}

func TestAssociationPagesRebuildRejectsSubstitutedProof(t *testing.T) {
	f, policy := pagedAssociationFixture(t, 3)
	srv := f.server(t)
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("PERSONS,WORKSTREAM_EVENTS,ANNOTATIONS")
	out := filepath.Join(t.TempDir(), "source")
	m, err := Export(context.Background(), client, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy)})
	srv.Close()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(out, "relationships.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	changed := false
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var edge PublicEdge
		if json.Unmarshal([]byte(line), &edge) != nil {
			t.Fatal("invalid graph fixture")
		}
		if !changed && edge.Provenance == "association_record" {
			one, two := opaque("WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS", "association-001"), opaque("WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS", "association-002")
			if edge.AssociationRef == one {
				edge.AssociationRef = two
			} else {
				edge.AssociationRef = one
			}
			encoded, err := json.Marshal(edge)
			if err != nil {
				t.Fatal(err)
			}
			line = string(encoded)
			changed = true
		}
		lines = append(lines, line)
	}
	if !changed {
		t.Fatal("no explicit proof to test")
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m.ArchiveState.GraphSHA256, err = fileDigest(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = Rebuild(context.Background(), RebuildOptions{Source: out, Options: Options{Output: out + "-rebuilt", Scanner: scanner(t, policy)}})
	if err == nil || !strings.Contains(err.Error(), "association edge contradicts") {
		t.Fatalf("substituted canonical proof was accepted: %v", err)
	}
	if _, err := os.Stat(out + "-rebuilt"); !os.IsNotExist(err) {
		t.Fatal("contradictory proof finalized")
	}
}

func TestAssociationPagesProbeSmallOwnerAndStopLegacySchema(t *testing.T) {
	calls := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls <- r.URL.Path
		json.NewEncoder(w).Encode(map[string]any{"iterable": []any{}})
	}))
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 1<<20)
	family, _ := associationFamilyByName(eventPersonFamily)
	r := &run{ctx: context.Background(), client: client, meta: map[string]*Meta{}, coverage: map[string]*Coverage{}, opts: Options{BatchSize: 50}}
	for id, count := range map[string]int{"a-heavy": 100000, "z-empty": 0} {
		key := "PERSONS\x00" + id
		r.meta[key] = &Meta{ID: id, Key: key, Type: "PERSONS", State: "included", PersonEvidence: &PersonFacts{SourceEventConnections: count}}
	}
	row := AssociationFamilyCoverage{Family: family.name}
	cov := &Coverage{Material: family.material().Type}
	remaining, err := r.exportPersonAssociationPages(family, nil, cov, &row)
	if err != nil || len(remaining) != 0 {
		t.Fatal(err)
	}
	if first := <-calls; !strings.HasSuffix(first, "/person/z-empty") {
		t.Fatal("pagination capability was probed on the heavier collection first")
	}
	select {
	case <-calls:
		t.Fatal("legacy unbounded schema caused another page request")
	default:
	}
	if row.Pagination.SchemaUnsupported != 1 || row.Pagination.NotAttempted != 1 || row.Pagination.Reconciled != 0 {
		t.Fatal("unsupported pagination capability was not disclosed")
	}
}
