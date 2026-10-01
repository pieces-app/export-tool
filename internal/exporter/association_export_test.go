package exporter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func associationFixture(t *testing.T) (*httptest.Server, *atomic.Int32, Policy, string) {
	t.Helper()
	signal, private := signalRecord("signal"), signalRecord("private-signal")
	signal["annotations"], signal["persons"] = refs("body"), refs("person")
	private["annotations"], private["workstream_events"] = refs("private-body"), refs("blocked-event")
	body, privateBody := record("body", "2026-09-30T00:00:00Z"), record("private-body", "2026-09-30T00:00:00Z")
	body["type"], body["text"], body["signals"] = "SIGNAL_DESCRIPTION", "Approved signal body.", refs("signal")
	privateBody["type"], privateBody["text"] = "SIGNAL_DESCRIPTION", "Association private narrative sentinel"
	person, event := record("person", "2026-09-30T00:00:00Z"), record("blocked-event", "2026-09-30T00:00:00Z")
	person["name"], person["annotations"], person["summaries"] = "Unprofiled example", refs(), refs()
	event["url"] = "https://bank.example/private"
	f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal, private}, "ANNOTATIONS": {body, privateBody}, "PERSONS": {person}, "WORKSTREAM_EVENTS": {event}}}
	base := f.server(t)
	t.Cleanup(base.Close)
	count := &atomic.Int32{}
	secret := fakeSecret()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		switch r.URL.Path {
		case "/signal_to_annotation_associations/signal/signal/annotation/body":
			v = record("signal-body-association", "2026-09-30T00:00:00Z")
			v["signal"], v["annotation"] = "signal", "body"
			v["confidence"], v["role"], v["explanation"] = json.Number("0.75"), "SUBJECT", "Approved metadata with synthetic credential: "+secret
			v["future_metadata"] = map[string]any{"value": json.Number("9007199254740993")}
		case "/signal_to_annotation_associations/signal/private-signal/annotation/private-body":
			v = record("private-association", "2026-09-30T00:00:00Z")
			v["signal"], v["annotation"], v["explanation"] = "private-signal", "private-body", "Association private narrative sentinel"
		case "/signal_to_person_associations/signal/signal/person/person":
			v = record("signal-person-association", "2026-09-30T00:00:00Z")
			v["signal"], v["person"], v["role"], v["evidence_type"], v["source_type"] = "signal", "person", "SUBJECT", "EXPLICIT", "TEXT"
		default:
			if strings.Contains(r.URL.Path, "_associations/") && len(strings.Split(strings.Trim(r.URL.Path, "/"), "/")) == 5 {
				t.Error("unexpected association pair lookup (scope, blocked endpoint, or Cartesian product)")
			}
			base.Config.Handler.ServeHTTP(w, r)
			return
		}
		if r.Method != "GET" {
			t.Error("association export used a non-read route")
		}
		count.Add(1)
		json.NewEncoder(w).Encode(v)
	}))
	policy := DefaultPolicy()
	policy.Deny = []DomainRule{{"bank.example", true}}
	return srv, count, policy, secret
}

func associationFixtureOptions(t *testing.T, output string, policy Policy) Options {
	t.Helper()
	materials, err := SelectMaterials("SIGNALS,ANNOTATIONS,PERSONS,WORKSTREAM_EVENTS")
	if err != nil {
		t.Fatal(err)
	}
	return Options{Output: output, Mode: "filtered", Format: "both", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy)}
}

func assertAssociationArchive(t *testing.T, root string, m Manifest, policy Policy, secret string, people bool) {
	t.Helper()
	family, _ := associationFamilyByName("signal_to_annotation_associations")
	material := family.material()
	v, data, err := readArchiveFixtureRecord(root, material.Type, "signal-body-association")
	if err != nil {
		t.Fatal(err)
	}
	if fieldString(v, "signal") != "signal" || fieldString(v, "annotation") != "body" || v["confidence"] != json.Number("0.75") || object(v, "future_metadata")["value"] != json.Number("9007199254740993") {
		t.Fatal("source bindings, metadata, or numeric precision were lost")
	}
	if strings.Contains(fieldString(v, "explanation"), secret) {
		t.Fatal("association credential was not redacted")
	}
	if m.Associations == nil || m.Associations.Mode != "linked" || m.Associations.Enumeration != "observed_pairs_and_person_pages" {
		t.Fatal("association read scope was not recorded")
	}
	for _, row := range m.Associations.Families {
		if row.Failed+row.Unsupported+row.Unavailable+row.Skipped != 0 {
			t.Fatalf("association fixture lookup incomplete: %+v", row)
		}
	}
	var paths map[string]string
	b, err := os.ReadFile(filepath.Join(root, "link-map.json"))
	if err != nil || json.Unmarshal(b, &paths) != nil {
		t.Fatal("missing link map")
	}
	if paths[opaque(material.Type, "private-association")] != "" {
		t.Fatal("association with indirectly blocked endpoints survived")
	}
	personType := "SIGNAL_TO_PERSON_ASSOCIATIONS"
	if (paths[opaque(personType, "signal-person-association")] != "") != people {
		t.Fatal("association disagrees with person selection")
	}
	mdPath := paths[opaque(material.Type, "signal-body-association")]
	b, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(mdPath)))
	if err != nil || !strings.Contains(string(b), "SUBJECT") || !strings.Contains(string(b), "0.75") || !strings.Contains(string(b), "Approved metadata") {
		t.Fatal("association Markdown metadata missing")
	}
	graphBytes, err := os.ReadFile(filepath.Join(root, "relationships.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	edges := 0
	for _, line := range strings.Split(strings.TrimSpace(string(graphBytes)), "\n") {
		var e PublicEdge
		if json.Unmarshal([]byte(line), &e) != nil {
			t.Fatal("invalid graph")
		}
		if e.Source == mdPath {
			edges++
		}
	}
	if edges != 2 {
		t.Fatalf("association needs two canonical endpoint edges, got %d", edges)
	}
	check := &run{ctx: context.Background(), stage: root, opts: Options{Mode: "filtered", Scanner: scanner(t, policy)}}
	if err := check.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := check.auditOutput(); err != nil {
		t.Fatal(err)
	}
	if err := check.auditPDF(filepath.Join(root, pdfPath(mdPath))); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{data, filepath.Join(root, filepath.FromSlash(mdPath)), filepath.Join(root, "index.md")} {
		b, err := os.ReadFile(path)
		if err != nil || strings.Contains(string(b), "Association private narrative sentinel") || strings.Contains(string(b), secret) {
			t.Fatal("private association content survived")
		}
	}
}

func TestAssociationExportPrivacyAndOfflineRebuild(t *testing.T) {
	srv, count, policy, secret := associationFixture(t)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	if err := client.ConfigurePerformance("adaptive", 50, 100*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "export with spaces")
	m, err := Export(context.Background(), client, associationFixtureOptions(t, out, policy))
	if err != nil {
		t.Fatal(err)
	}
	if count.Load() != 3 {
		t.Fatalf("expected three deduplicated observed pair reads, got %d", count.Load())
	}
	assertAssociationArchive(t, out, m, policy, secret, true)
	srv.Close() // Rebuild must work with the source gone.
	before := archiveHashes(t, out)
	rebuilt := out + "-rebuilt"
	m, err = Rebuild(context.Background(), RebuildOptions{Source: out, Options: Options{Output: rebuilt, PeopleMode: "profiles", Scanner: scanner(t, policy), Format: "both"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, archiveHashes(t, out)) {
		t.Fatal("offline rebuild changed its source")
	}
	if err := os.Rename(rebuilt, rebuilt+"-moved"); err != nil {
		t.Fatal(err)
	}
	assertAssociationArchive(t, rebuilt+"-moved", m, policy, secret, false)
}

func TestAssociationExportOffAndScope(t *testing.T) {
	for _, mode := range []string{"off", "scope"} {
		t.Run(mode, func(t *testing.T) {
			srv, count, policy, _ := associationFixture(t)
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			opts := associationFixtureOptions(t, filepath.Join(t.TempDir(), "export"), policy)
			opts.Format = "markdown"
			if mode == "off" {
				opts.Associations = "off"
			} else {
				opts.Materials, _ = SelectMaterials("SIGNALS")
			}
			m, err := Export(context.Background(), client, opts)
			if err != nil {
				t.Fatal(err)
			}
			if count.Load() != 0 || m.Associations == nil {
				t.Fatal("off/unselected endpoint caused a lookup")
			}
		})
	}
}

func TestAssociationExportUnavailableAndUnsupported(t *testing.T) {
	for _, status := range []int{404, 405, 501, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			signal := signalRecord("signal")
			signal["annotations"] = refs("body1", "body2")
			f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal}, "ANNOTATIONS": {record("body1", ""), record("body2", "")}}}
			base := f.server(t)
			defer base.Close()
			var reads atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/signal_to_annotation_associations/") {
					reads.Add(1)
					w.WriteHeader(status)
					w.Write([]byte(`{"id":"invalid"}`))
					return
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			materials, _ := SelectMaterials("SIGNALS,ANNOTATIONS")
			m, err := Export(context.Background(), client, Options{Output: filepath.Join(t.TempDir(), "export"), Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())})
			if err != nil {
				t.Fatal(err)
			}
			if m.Status != "partial" || len(m.Associations.Families) != 1 {
				t.Fatal("unavailable association coverage was certified complete")
			}
			row := m.Associations.Families[0]
			if status == 405 || status == 501 {
				if reads.Load() != 1 || row.Unsupported != 1 || row.Skipped != 1 {
					t.Fatal("unsupported route was retried or hidden")
				}
			} else if reads.Load() != 2 || row.Unavailable+row.Failed != 2 {
				t.Fatal("404/invalid response lost or interpreted as empty association inventory")
			}
		})
	}
}

func TestPackagedAssociationCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to an actual release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	srv, count, policy, secret := associationFixture(t)
	defer srv.Close()
	root := t.TempDir()
	out := filepath.Join(root, "archive with spaces")
	policyPath := filepath.Join(root, "policy.json")
	if err := writeJSON(policyPath, policy); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args := []string{"export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--materials", "SIGNALS,ANNOTATIONS,PERSONS,WORKSTREAM_EVENTS", "--format", "both", "--policy", policyPath, "--output", out}
	result, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Fatalf("actual export failed: %v\n%s", err, result)
		}
	}
	m, err := InspectArchive(out)
	if err != nil {
		t.Fatal(err)
	}
	if count.Load() != 3 {
		t.Fatal("packaged export did not read the three observed pairs")
	}
	assertAssociationArchive(t, out, m, policy, secret, true)
	srv.Close()
	before := archiveHashes(t, out)
	rebuilt := out + "-rebuilt"
	result, err = exec.CommandContext(ctx, binary, "rebuild", "--source", out, "--output", rebuilt, "--policy", policyPath, "--people", "profiles", "--format", "both", "--yes").CombinedOutput()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Fatalf("actual rebuild failed: %v\n%s", err, result)
		}
	}
	if !reflect.DeepEqual(before, archiveHashes(t, out)) {
		t.Fatal("packaged rebuild changed its source")
	}
	m, err = InspectArchive(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(rebuilt, rebuilt+"-moved"); err != nil {
		t.Fatal(err)
	}
	assertAssociationArchive(t, rebuilt+"-moved", m, policy, secret, false)
	t.Log("actual compiled association export, metadata/redaction, blocked endpoints, offline person selection, source hashes, PDFs, and moved Markdown links passed")
}

func TestAssociationPairDiscoveryBoundsAndCancellation(t *testing.T) {
	family, _ := associationFamilyByName("signal_to_annotation_associations")
	signal := &Meta{Type: "SIGNALS", ID: "signal", Key: "SIGNALS\x00signal", State: "included"}
	body := &Meta{Type: "ANNOTATIONS", ID: "body", Key: "ANNOTATIONS\x00body", State: "included"}
	second := &Meta{Type: "ANNOTATIONS", ID: "second", Key: "ANNOTATIONS\x00second", State: "included"}
	signal.Edges = []Edge{{signal.Key, body.Key, "annotations"}, {signal.Key, second.Key, "embedded_markdown"}}
	body.Edges = []Edge{{body.Key, signal.Key, "signals"}}
	r := &run{ctx: context.Background(), meta: map[string]*Meta{signal.Key: signal, body.Key: body, second.Key: second}}
	pairs, err := r.associationPairsBounded(family, 1, 100)
	if err != nil || len(pairs) != 1 || pairs[0].left != "signal" || pairs[0].right != "body" {
		t.Fatal("inverse pair did not deduplicate or prose invented a typed association")
	}
	if _, err := r.associationPairsBounded(family, 1, 3); err == nil {
		t.Fatal("reference byte limit ignored")
	}
	signal.Edges = append(signal.Edges, Edge{signal.Key, second.Key, "annotations"})
	if _, err := r.associationPairsBounded(family, 1, 100); err == nil {
		t.Fatal("pair limit ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.ctx = ctx
	if _, err := r.associationPairs(family); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled graph discovery continued")
	}
}

func TestAssociationAllFamilyGraphBindings(t *testing.T) {
	// The independent source-contract tests check route/wire names. This also
	// ensures every supported source family can round-trip the archive graph.
	for _, family := range associationFamilies {
		t.Run(family.name, func(t *testing.T) {
			v := record("association", "2026-09-30T00:00:00Z")
			v[family.leftField], v[family.rightField] = "left", "right"
			edges := associationEndpoints(family, v)
			for _, edge := range edges {
				typ, _, ok := splitRef(edge.Target)
				if !ok || referenceTypes[edge.Relation] != typ {
					t.Fatal("association endpoint cannot round-trip through typed graph")
				}
			}
			material, ok := materialByType(family.material().Type)
			if !ok || material.Folder != family.material().Folder {
				t.Fatal("association canonical storage cannot be rebuilt")
			}
		})
	}
}

func TestAssociationRebuildRequiresBothGraphEndpoints(t *testing.T) {
	srv, _, policy, _ := associationFixture(t)
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	out := filepath.Join(t.TempDir(), "source")
	opts := associationFixtureOptions(t, out, policy)
	opts.Format = "markdown"
	m, err := Export(context.Background(), client, opts)
	srv.Close()
	if err != nil {
		t.Fatal(err)
	}
	var links map[string]string
	b, err := os.ReadFile(filepath.Join(out, "link-map.json"))
	if err != nil || json.Unmarshal(b, &links) != nil {
		t.Fatal("missing link map")
	}
	associationPath := links[opaque("SIGNAL_TO_ANNOTATION_ASSOCIATIONS", "signal-body-association")]
	graphPath := filepath.Join(out, "relationships.jsonl")
	b, err = os.ReadFile(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	removed := false
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var edge PublicEdge
		if json.Unmarshal([]byte(line), &edge) != nil {
			t.Fatal("invalid graph fixture")
		}
		if edge.Source == associationPath && !removed {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		t.Fatal("association graph edge absent before mutation")
	}
	if err := os.WriteFile(graphPath, []byte(strings.Join(kept, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m.ArchiveState.GraphSHA256, err = fileDigest(context.Background(), graphPath)
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
	if err == nil || !strings.Contains(err.Error(), "association graph omits") {
		t.Fatalf("consistent checksums hid a missing canonical binding: %v", err)
	}
	if _, err := os.Stat(out + "-rebuilt"); !os.IsNotExist(err) {
		t.Fatal("invalid association graph finalized")
	}
}

func TestAssociationPreservationRebuildNarrowsPeople(t *testing.T) {
	signal := signalRecord("signal")
	signal["persons"] = refs("person")
	person := record("person", "2026-09-30T00:00:00Z")
	person["name"], person["annotations"], person["summaries"] = "Example", refs(), refs()
	association := record("association", "2026-09-30T00:00:00Z")
	association["signal"], association["person"] = "signal", "person"
	f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal}, "PERSONS": {person}, "ANNOTATIONS": {}, "SIGNAL_TO_PERSON_ASSOCIATIONS": {association}}}
	srv := f.server(t)
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("SIGNALS,PERSONS,ANNOTATIONS")
	out := filepath.Join(t.TempDir(), "source")
	_, err := Export(context.Background(), client, Options{Output: out, Mode: "preserve", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())})
	srv.Close()
	if err != nil {
		t.Fatal(err)
	}
	before := archiveHashes(t, out)
	for iteration := 0; iteration < 2; iteration++ {
		next := out + "-rebuilt"
		m, err := Rebuild(context.Background(), RebuildOptions{Source: out, Options: Options{Output: next, PeopleMode: "profiles", Scanner: scanner(t, DefaultPolicy())}})
		if err != nil {
			t.Fatal(err)
		}
		for _, cov := range m.Coverage {
			if cov.Material == "SIGNAL_TO_PERSON_ASSOCIATIONS" && (cov.Included != 0 || cov.Omitted != 1) {
				t.Fatal("association selection decision lost across preservation rebuilds")
			}
		}
		if _, _, err := readArchiveFixtureRecord(next, "SIGNAL_TO_PERSON_ASSOCIATIONS", "association"); !os.IsNotExist(err) {
			t.Fatal("omitted person binding survived in raw association JSON")
		}
		if iteration == 0 && !reflect.DeepEqual(before, archiveHashes(t, out)) {
			t.Fatal("preservation source changed")
		}
		out = next
	}
}

func TestAssociationLateCredentialsAndConflictingIdentity(t *testing.T) {
	for _, scenario := range []string{"late-credential", "conflict-filtered", "conflict-preserve"} {
		t.Run(scenario, func(t *testing.T) {
			signal := signalRecord("signal")
			signal["annotations"] = refs("body-one", "body-two")
			one, two := record("body-one", "2026-09-30T00:00:00Z"), record("body-two", "2026-09-30T00:00:00Z")
			marker := "opaque_fixture_credential_value_bounded"
			one["type"], one["text"] = "SIGNAL_DESCRIPTION", "Prior note contains "+marker+" for testing."
			two["type"], two["text"] = "SIGNAL_DESCRIPTION", "Another approved body."
			a, b := record("association-one", "2026-09-30T00:00:00Z"), record("association-two", "2026-09-30T00:00:00Z")
			a["signal"], a["annotation"] = "signal", "body-one"
			b["signal"], b["annotation"] = "signal", "body-two"
			if scenario == "late-credential" {
				b["api_key"] = marker
			} else {
				b["id"] = a["id"]
			}
			f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal}, "ANNOTATIONS": {one, two}, "SIGNAL_TO_ANNOTATION_ASSOCIATIONS": {a, b}}}
			srv := f.server(t)
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			materials, _ := SelectMaterials("SIGNALS,ANNOTATIONS")
			out := filepath.Join(t.TempDir(), "archive")
			mode, prefix := "filtered", "data"
			if scenario == "conflict-preserve" {
				mode, prefix = "preserve", "raw"
			}
			m, err := Export(context.Background(), client, Options{Output: out, Mode: mode, Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "late-credential" {
				v, err := readRecord(filepath.Join(out, prefix, "annotations", opaque("ANNOTATIONS", "body-one")+".json"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(fieldString(v, "text"), marker) || !strings.Contains(fieldString(v, "text"), "REDACTED") {
					t.Fatal("late association credential survived in earlier prose")
				}
			} else {
				if _, _, err := readArchiveFixtureRecord(out, "SIGNAL_TO_ANNOTATION_ASSOCIATIONS", "association-one"); !os.IsNotExist(err) {
					t.Fatal("conflicting association identity retained an earlier binding")
				}
				if m.Status != "partial" || m.Associations.Families[0].Failed != 1 {
					t.Fatal("conflicting association response was not reported")
				}
			}
		})
	}
}
