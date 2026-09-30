package exporter

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func wrappedCacheTable(t *testing.T, db *sql.DB, table string, records []map[string]any) {
	t.Helper()
	allowed := false
	for _, spec := range cacheTables {
		allowed = allowed || spec.wrapped && spec.table == table
	}
	if !allowed {
		t.Fatal("unsupported fixture table")
	}
	if _, err := db.Exec("CREATE TABLE " + table + " (key TEXT PRIMARY KEY, json TEXT, expireAt INTEGER)"); err != nil {
		t.Fatal(err)
	}
	for i, v := range records {
		// Deliberately different UI identities, timestamps and prose. Only os
		// is the typed source record; the SQLite key is not a source identity.
		wrapper := map[string]any{"os": v, "pfd": "UI_ONLY_ID", "id": "UI_ONLY_ID", "updated": "2099-01-01T00:00:00Z", "text": "CACHED_UI_PROSE_MUST_NOT_EXPORT"}
		b, _ := json.Marshal(wrapper)
		if _, err := db.Exec("INSERT INTO "+table+" (key,json) VALUES (?,?)", fmt.Sprint(i), string(b)); err != nil {
			t.Fatal(err)
		}
	}
}

func wrappedCurrent(id string) map[string]any {
	v := record(id, "2026-09-20T00:00:00Z")
	v["updated"] = map[string]any{"value": "2026-09-29T00:00:00Z"}
	return v
}

func wrappedHistorical(id, field string, ids ...string) map[string]any {
	v := wrappedCurrent(id)
	v["updated"] = map[string]any{"value": "2026-09-27T00:00:00Z"}
	v[field] = refs(ids...)
	v["text"] = "CACHED_OS_PROSE_MUST_NOT_EXPORT"
	return v
}

func TestWrappedSDKCacheCandidateRulesAndSharedBounds(t *testing.T) {
	for _, table := range cacheTables[1:] {
		t.Run(table.table, func(t *testing.T) {
			field := table.relations[0]
			ids := []string{"good", "empty", "malformed", "newer-empty", "tombstone", "conflict", "wrong-created", "future", "unknown"}
			r := &run{ctx: context.Background(), meta: map[string]*Meta{}}
			var historical []map[string]any
			for _, id := range ids {
				v := wrappedCurrent(id)
				fields := map[string]bool{field: true}
				if id == "empty" || id == "malformed" {
					fields[field] = false
				}
				if id == "unknown" {
					fields = nil
				}
				key := table.material + "\x00" + id
				r.meta[key] = &Meta{Key: key, ID: id, Type: table.material, State: "included", Created: timestamp(v, "created"), Updated: timestamp(v, "updated"), SupplementableFields: fields}
				v = wrappedHistorical(id, field, "target")
				if id == "wrong-created" {
					v["created"] = map[string]any{"value": "2026-09-19T00:00:00Z"}
				}
				if id == "future" {
					v["updated"] = map[string]any{"value": "2026-10-01T00:00:00Z"}
				}
				historical = append(historical, v)
			}
			first, db := cacheFixture(t, nil, true)
			wrappedCacheTable(t, db, table.table, historical)
			// A cache with just the wrapped table must work, too.
			if _, err := db.Exec("DROP TABLE workstream_summaries"); err != nil {
				t.Fatal(err)
			}
			if err := ValidateSDKCaches(r.ctx, []string{first}); err != nil {
				t.Fatal(err)
			}
			for _, raw := range []string{`{"id":"good"}`, `{"os":null}`, `{"os":[]}`, `{"os":{"id":"good"}} trailing`} {
				if _, err := db.Exec("INSERT INTO "+table.table+" (key,json) VALUES (?,?)", raw, raw); err != nil {
					t.Fatal(err)
				}
			}
			second, db2 := cacheFixture(t, nil, false)
			empty := wrappedHistorical("newer-empty", field)
			tomb := wrappedHistorical("tombstone", field, "target")
			tomb[field] = map[string]any{"indices": map[string]any{"target": -1}, "iterable": []any{map[string]any{"id": "target"}}}
			for _, v := range []map[string]any{empty, tomb} {
				v["updated"] = map[string]any{"value": "2026-09-28T00:00:00Z"}
			}
			wrappedCacheTable(t, db2, table.table, []map[string]any{empty, tomb, wrappedHistorical("conflict", field, "other")})
			candidates := map[cacheField]*cacheCandidate{}
			budget := cacheReadBudget{}
			for i, path := range []string{first, second} {
				if err := r.readSDKCache(path, i+1, candidates, &budget); err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range []string{"empty", "malformed", "wrong-created", "future", "unknown"} {
				if candidates[cacheField{table.material + "\x00" + id, field}] != nil {
					t.Fatalf("ineligible %s produced a candidate", id)
				}
			}
			for _, id := range []string{"newer-empty", "tombstone"} {
				if c := candidates[cacheField{table.material + "\x00" + id, field}]; c == nil || len(c.ids) != 0 {
					t.Fatal("newer empty/tombstone did not suppress old attachment")
				}
			}
			if c := candidates[cacheField{table.material + "\x00conflict", field}]; c == nil || !c.conflict {
				t.Fatal("equal-time conflict was merged")
			}
			good := candidates[cacheField{table.material + "\x00good", field}]
			if good == nil || good.evidence.Material != table.material || good.evidence.RecordRef != opaque(table.material, "good") || good.evidence.CachedUpdated != "" {
				t.Fatal("wrapper identity/type provenance was not retained")
			}
			if r.manifest.SDKCache.Invalid != 4 || r.manifest.SDKCache.UnknownFields != 1 || r.manifest.SDKCache.IdentityMismatch != 1 || r.manifest.SDKCache.UnusableTime != 1 || r.manifest.SDKCache.Matching != 0 {
				t.Fatalf("unexpected aggregate coverage: %+v", r.manifest.SDKCache)
			}
			for _, budget := range []cacheReadBudget{{edges: maxCacheEdges}, {bytes: maxCacheReferenceBytes}} {
				if err := r.readSDKCache(first, 1, map[cacheField]*cacheCandidate{}, &budget); err == nil {
					t.Fatal("wrapped reference budget was not enforced")
				}
			}
			tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			count := maxCacheRows
			err = r.readSDKCacheTable(r.ctx, tx, table, 1, &count, map[cacheField]*cacheCandidate{}, &cacheReadBudget{})
			tx.Rollback()
			if err == nil || !strings.Contains(err.Error(), "shared row bound") {
				t.Fatal("row bound was reset at a table boundary")
			}
		})
	}
}

func wrappedExportFixture(t *testing.T) (*fakeOS, string) {
	t.Helper()
	f := &fakeOS{data: map[string][]map[string]any{}}
	for _, id := range []string{"safe", "inverse", "private", "shared"} {
		v := wrappedCurrent(id)
		v["name"] = id
		f.data["SIGNALS"] = append(f.data["SIGNALS"], v)
	}
	for id, typ := range map[string]string{"safe-body": "SIGNAL_DESCRIPTION", "inverse-body": "SIGNAL_DESCRIPTION", "private-body": "SIGNAL_DESCRIPTION", "summary-body": "SUMMARY", "profile": "HIERARCHICAL_PROFILE_SUMMARY"} {
		v := wrappedCurrent(id)
		v["type"], v["text"] = typ, "Current retained narrative for "+id+"."
		if id == "private-body" {
			v["text"] = "Private signal narrative sentinel"
		}
		f.data["ANNOTATIONS"] = append(f.data["ANNOTATIONS"], v)
	}
	event := wrappedCurrent("bank-event")
	event["url"] = "https://bank.example/private"
	f.data["WORKSTREAM_EVENTS"] = []map[string]any{event}
	f.data["WORKSTREAM_SUMMARIES"] = []map[string]any{wrappedCurrent("summary")}
	p := wrappedCurrent("person")
	p["name"] = "Fixture Person"
	f.data["PERSONS"] = []map[string]any{p}
	cache, db := cacheFixture(t, nil, true)
	private := wrappedHistorical("private", "annotations", "private-body")
	private["workstream_events"] = refs("bank-event")
	wrappedCacheTable(t, db, "signals", []map[string]any{wrappedHistorical("safe", "annotations", "safe-body"), private, wrappedHistorical("shared", "annotations", "private-body")})
	body := wrappedHistorical("summary-body", "summaries", "summary")
	body["persons"] = refs("person")
	wrappedCacheTable(t, db, "annotations", []map[string]any{body, wrappedHistorical("inverse-body", "signals", "inverse")})
	person := wrappedHistorical("person", "annotations", "profile")
	person["summaries"] = refs("summary")
	wrappedCacheTable(t, db, "persons", []map[string]any{person})
	return f, cache
}

func assertWrappedRecovered(t *testing.T, output string, m Manifest, policy Policy) {
	t.Helper()
	if m.Status != "partial" || m.SDKCache.RetainedEdges == 0 || m.SignalDigest.WithDescription != 2 {
		t.Fatalf("recovery/digest coverage mismatch: %+v %+v", m.SDKCache, m.SignalDigest)
	}
	var links map[string]string
	b, _ := os.ReadFile(filepath.Join(output, "link-map.json"))
	if json.Unmarshal(b, &links) != nil {
		t.Fatal("invalid link map")
	}
	for _, id := range []string{"private", "shared"} {
		if links[opaque("SIGNALS", id)] != "" {
			t.Fatal("private/shared signal survived cached dependency")
		}
	}
	if links[opaque("ANNOTATIONS", "private-body")] != "" {
		t.Fatal("private description survived")
	}
	b, err := os.ReadFile(filepath.Join(output, links[opaque("WORKSTREAM_SUMMARIES", "summary")]))
	if err != nil || !strings.Contains(string(b), "Current retained narrative for summary-body.") || !strings.Contains(string(b), "ANNOTATIONS record updated") {
		t.Fatal("inverse cached summary body/provenance missing")
	}
	profiles, _ := filepath.Glob(filepath.Join(output, "workstream_summaries/personas/related_persons/*/profile.md"))
	if len(profiles) != 1 {
		t.Fatal("cached person association did not create profile navigation")
	}
	b, _ = os.ReadFile(profiles[0])
	if !strings.Contains(string(b), "Current retained narrative for profile.") || !strings.Contains(string(b), "Historical cache links") {
		t.Fatal("cached person annotation was not rendered")
	}
	for _, part := range m.SignalDigest.Parts {
		b, err := os.ReadFile(filepath.Join(output, part.Path))
		if err != nil || !strings.Contains(string(b), "SIGNALS record updated") || !strings.Contains(string(b), "ANNOTATIONS record updated") {
			t.Fatal("digest omitted typed historical description provenance")
		}
	}
	for path := range archiveHashes(t, output) {
		if filepath.Ext(path) == ".pdf" {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(output, path))
		for _, marker := range []string{"CACHED_UI_PROSE_MUST_NOT_EXPORT", "CACHED_OS_PROSE_MUST_NOT_EXPORT", "UI_ONLY_ID"} {
			if strings.Contains(string(b), marker) {
				t.Fatal("cached wrapper/content leaked")
			}
		}
	}
	assertNoSignalPrivateText(t, output)
	r := &run{ctx: context.Background(), stage: output, opts: Options{Mode: "filtered", Scanner: scanner(t, policy)}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
}

func TestWrappedSDKCacheExportAndOfflineRebuild(t *testing.T) {
	for _, offline := range []bool{false, true} {
		t.Run(fmt.Sprintf("offline-%t", offline), func(t *testing.T) {
			f, cache := wrappedExportFixture(t)
			srv := f.server(t)
			c, _ := NewClient(srv.URL, time.Second, 8<<20)
			materials, _ := SelectMaterials("SIGNALS,ANNOTATIONS,WORKSTREAM_EVENTS,WORKSTREAM_SUMMARIES,PERSONS")
			policy := DefaultPolicy()
			policy.Deny = []DomainRule{{"bank.example", true}}
			source := filepath.Join(t.TempDir(), "source")
			o := Options{Output: source, Mode: "filtered", Format: "both", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy), Metadata: "off"}
			if !offline {
				o.SDKCaches = []string{cache}
			}
			m, err := Export(context.Background(), c, o)
			srv.Close()
			if err != nil {
				t.Fatal(err)
			}
			output := source
			before := archiveHashes(t, source)
			if offline {
				output = filepath.Join(t.TempDir(), "recovered")
				m, err = Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: output, Scanner: scanner(t, policy), SDKCaches: []string{cache}, Format: "both"}})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, archiveHashes(t, source)) {
					t.Fatal("recovery modified source archive")
				}
			}
			assertWrappedRecovered(t, output, m, policy)
			moved := output + "-moved"
			if err := os.Rename(output, moved); err != nil {
				t.Fatal(err)
			}
			assertWrappedRecovered(t, moved, m, policy)
			again := filepath.Join(t.TempDir(), "again")
			m, err = Rebuild(context.Background(), RebuildOptions{Source: moved, Options: Options{Output: again, Scanner: scanner(t, policy), Format: "both"}})
			if err != nil {
				t.Fatal(err)
			}
			assertWrappedRecovered(t, again, m, policy)
		})
	}
}

func TestWrappedSDKCacheUnavailableRecordsAndModes(t *testing.T) {
	for _, scenario := range []string{"signal-missing", "annotation-missing", "unselected-signal", "strict", "preserve", "missing-event"} {
		t.Run(scenario, func(t *testing.T) {
			signal, body := wrappedCurrent("signal"), wrappedCurrent("body")
			signal["name"], body["type"], body["text"] = "Example signal", "SIGNAL_DESCRIPTION", "Private signal narrative sentinel"
			f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal}, "ANNOTATIONS": {body}, "WORKSTREAM_EVENTS": {}}}
			switch scenario {
			case "signal-missing", "unselected-signal":
				f.data["SIGNALS"] = nil
			case "annotation-missing":
				f.data["ANNOTATIONS"] = nil
			}
			cache, db := cacheFixture(t, nil, false)
			cached := wrappedHistorical("signal", "annotations", "body")
			if scenario == "missing-event" {
				cached["workstream_events"] = refs("absent-event")
			}
			wrappedCacheTable(t, db, "signals", []map[string]any{cached})
			wrappedCacheTable(t, db, "annotations", []map[string]any{wrappedHistorical("body", "signals", "signal")})
			selected := "SIGNALS,ANNOTATIONS,WORKSTREAM_EVENTS"
			if scenario == "unselected-signal" {
				selected = "ANNOTATIONS"
			}
			materials, _ := SelectMaterials(selected)
			policy := DefaultPolicy()
			policy.Deny = []DomainRule{{"bank.example", true}}
			policy.StrictDerived = scenario == "strict"
			mode := "filtered"
			if scenario == "preserve" {
				mode = "preserve"
			}
			srv := f.server(t)
			c, _ := NewClient(srv.URL, time.Second, 8<<20)
			source := filepath.Join(t.TempDir(), "source")
			_, err := Export(context.Background(), c, Options{Output: source, Mode: mode, Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy)})
			srv.Close()
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "recovered")
			m, err := Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: output, Scanner: scanner(t, policy), SDKCaches: []string{cache}, Format: "both"}})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "preserve" {
				if m.SignalDigest.WithDescription != 1 {
					t.Fatal("preserve mode did not recover description")
				}
			} else if scenario == "unselected-signal" {
				for _, cov := range m.Coverage {
					if cov.Material == "ANNOTATIONS" && cov.Included != 1 {
						t.Fatal("intentional scope omission became a privacy exclusion")
					}
				}
			} else {
				assertNoSignalPrivateText(t, output)
				if m.SignalDigest.Entries != 0 {
					t.Fatal("unavailable dependency left a generated signal included")
				}
			}
		})
	}
}

func TestWrappedSDKCacheUnknownLegacyEligibilityStaysUnknown(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("format4-%t", legacy), func(t *testing.T) {
			signal, body := wrappedCurrent("signal"), wrappedCurrent("body")
			body["type"], body["text"] = "SIGNAL_DESCRIPTION", "Current description text."
			f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal}, "ANNOTATIONS": {body}}}
			srv := f.server(t)
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			materials, _ := SelectMaterials("SIGNALS,ANNOTATIONS")
			policy := DefaultPolicy()
			source := filepath.Join(t.TempDir(), "source")
			m, err := Export(context.Background(), client, Options{Output: source, Mode: "filtered", Materials: materials, Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy)})
			srv.Close()
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				m.FormatVersion, m.ArchiveState = 4, nil
			} else {
				stateFile := filepath.Join(source, archiveStateFile)
				data, _ := os.ReadFile(stateFile)
				var out bytes.Buffer
				enc := json.NewEncoder(&out)
				for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
					var row archiveRecord
					if err := json.Unmarshal(line, &row); err != nil {
						t.Fatal(err)
					}
					row.SupplementableFields = nil
					if err := enc.Encode(row); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(stateFile, out.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
				m.ArchiveState.StateSHA256, err = fileDigest(context.Background(), stateFile)
				if err != nil {
					t.Fatal(err)
				}
			}
			manifestBytes, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "manifest.json"), manifestBytes, 0600); err != nil {
				t.Fatal(err)
			}
			// Rebuild once before selecting a cache, so synthetic unknown states
			// cannot turn into affirmative eligibility on the next rebuild.
			intermediate := filepath.Join(t.TempDir(), "intermediate")
			_, err = Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: intermediate, Scanner: scanner(t, policy)}})
			if err != nil {
				t.Fatal(err)
			}
			cache, db := cacheFixture(t, nil, false)
			wrappedCacheTable(t, db, "signals", []map[string]any{wrappedHistorical("signal", "annotations", "body")})
			wrappedCacheTable(t, db, "annotations", []map[string]any{wrappedHistorical("body", "signals", "signal")})
			output := filepath.Join(t.TempDir(), "recovered")
			m, err = Rebuild(context.Background(), RebuildOptions{Source: intermediate, Options: Options{Output: output, SDKCaches: []string{cache}, Scanner: scanner(t, policy)}})
			if err != nil {
				t.Fatal(err)
			}
			if m.SignalDigest.WithDescription != 0 || m.SDKCache.AddedEdges != 0 || m.SDKCache.UnknownFields != 2 {
				t.Fatalf("unknown original fields became eligible: %+v", m.SDKCache)
			}
		})
	}
}

func TestPackagedWrappedCacheCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the native release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	f, cache := wrappedExportFixture(t)
	srv := f.server(t)
	defer srv.Close()
	policy := DefaultPolicy()
	policy.Deny = []DomainRule{{"bank.example", true}}
	root := t.TempDir()
	policyFile := filepath.Join(root, "policy.json")
	if err := writeJSON(policyFile, policy); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	call := func(args ...string) {
		t.Helper()
		out, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Fatalf("expected partial exit 2: %v\n%s", err, out)
		}
		if strings.Contains(string(out), cache) {
			t.Fatal("private cache path appeared in terminal output")
		}
	}
	source := filepath.Join(root, "source")
	call("export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--materials", "SIGNALS,ANNOTATIONS,WORKSTREAM_EVENTS,WORKSTREAM_SUMMARIES,PERSONS", "--format", "markdown", "--metadata", "off", "--policy", policyFile, "--output", source)
	srv.Close()
	before := archiveHashes(t, source)
	recovered := filepath.Join(root, "recovered")
	call("rebuild", "--source", source, "--output", recovered, "--sdk-cache", cache, "--policy", policyFile, "--format", "both", "--metadata", "off", "--yes")
	if !reflect.DeepEqual(before, archiveHashes(t, source)) {
		t.Fatal("packaged rebuild modified its source")
	}
	moved := recovered + " moved"
	if err := os.Rename(recovered, moved); err != nil {
		t.Fatal(err)
	}
	m, err := InspectArchive(moved)
	if err != nil {
		t.Fatal(err)
	}
	assertWrappedRecovered(t, moved, m, policy)
	again := filepath.Join(root, "again")
	call("rebuild", "--source", moved, "--output", again, "--policy", policyFile, "--format", "both", "--metadata", "off", "--yes")
	m, err = InspectArchive(again)
	if err != nil {
		t.Fatal(err)
	}
	assertWrappedRecovered(t, again, m, policy)
	t.Log("actual package: wrapped summary/signal/person recovery, excluded/shared dependencies, historical provenance, source immutability, repeated offline rebuild, PDFs and moved links passed")
}

func TestActualLegacyWrappedCacheEligibility(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_LEGACY_WRAPPED_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_LEGACY_WRAPPED_BINARY to the native 0.9.1-dev executable")
	}
	signal, body := wrappedCurrent("signal"), wrappedCurrent("body")
	body["type"], body["text"] = "SIGNAL_DESCRIPTION", "Retained description text."
	f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal}, "ANNOTATIONS": {body}}}
	srv := f.server(t)
	defer srv.Close()
	source := filepath.Join(t.TempDir(), "source")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, binary, "export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--materials", "SIGNALS,ANNOTATIONS", "--format", "markdown", "--metadata", "off", "--output", source).CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("old writer failed: %v\n%s", err, b)
	}
	srv.Close()
	old, err := InspectArchive(source)
	if err != nil || old.ToolVersion != "0.9.1-dev" {
		t.Fatal("expected actual 0.9.1-dev archive")
	}
	cache, db := cacheFixture(t, nil, false)
	wrappedCacheTable(t, db, "signals", []map[string]any{wrappedHistorical("signal", "annotations", "body")})
	wrappedCacheTable(t, db, "annotations", []map[string]any{wrappedHistorical("body", "signals", "signal")})
	before := archiveHashes(t, source)
	m, err := Rebuild(ctx, RebuildOptions{Source: source, Options: Options{Output: filepath.Join(t.TempDir(), "rebuild"), SDKCaches: []string{cache}, Scanner: scanner(t, DefaultPolicy())}})
	if err != nil || m.SDKCache.UnknownFields != 2 || m.SDKCache.AddedEdges != 0 || m.SignalDigest.WithDescription != 0 {
		t.Fatalf("old archive fabricated original cache eligibility: %v %+v", err, m.SDKCache)
	}
	if !reflect.DeepEqual(before, archiveHashes(t, source)) {
		t.Fatal("old archive was modified")
	}
	t.Log("actual 0.9.1-dev archive remains partial: unknown wrapped eligibility counted and not inferred; source unchanged")
}
