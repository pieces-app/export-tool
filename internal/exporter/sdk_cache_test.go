package exporter

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cacheFixture(t *testing.T, values []map[string]any, wal bool) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cache space%hash#.db")
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if wal {
		if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec("CREATE TABLE workstream_summaries (key TEXT PRIMARY KEY, json TEXT, expireAt INTEGER, destroyKey TEXT)"); err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO workstream_summaries (key,json) VALUES (?,?)", fieldString(v, "id"), string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	return path, db
}

func TestSDKCacheReadOnlyIncludesWALAndRejectsWrongSchema(t *testing.T) {
	path, writer := cacheFixture(t, []map[string]any{record("fixture", "2026-09-20T00:00:00Z")}, true)
	before := map[string][32]byte{}
	for _, suffix := range []string{"", "-wal"} {
		b, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		before[suffix] = sha256.Sum256(b)
	}
	if err := ValidateSDKCaches(context.Background(), []string{path}); err != nil {
		t.Fatal(err)
	}
	db, err := openSDKCache(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM workstream_summaries").Scan(&count); err != nil || count != 1 {
		t.Fatal("read-only reader did not see committed WAL row")
	}
	if _, err := db.Exec("DELETE FROM workstream_summaries"); err == nil {
		t.Fatal("read-only cache accepted mutation")
	}
	db.Close()
	for suffix, digest := range before {
		b, err := os.ReadFile(path + suffix)
		if err != nil || sha256.Sum256(b) != digest {
			t.Fatal("cache main/WAL bytes changed")
		}
	}
	if err := ValidateSDKCaches(context.Background(), []string{path, path}); err == nil {
		t.Fatal("duplicate cache accepted")
	}
	if _, err := writer.Exec("DROP TABLE workstream_summaries; CREATE VIEW workstream_summaries AS SELECT 1 AS json, NULL AS expireAt"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSDKCaches(context.Background(), []string{path}); err == nil {
		t.Fatal("view accepted as cache table")
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if err := ValidateSDKCaches(context.Background(), []string{missing}); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("read-only validation created database")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidateSDKCaches(ctx, []string{path}); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestSDKCacheReferenceBudgetStopsBeforeApplying(t *testing.T) {
	v := record("summary", "2026-09-20T00:00:00Z")
	v["annotations"] = refs("body")
	path, _ := cacheFixture(t, []map[string]any{v}, false)
	for _, budget := range []cacheReadBudget{{edges: maxCacheEdges}, {bytes: maxCacheReferenceBytes}} {
		r := &run{ctx: context.Background(), meta: map[string]*Meta{"WORKSTREAM_SUMMARIES\x00summary": {Key: "WORKSTREAM_SUMMARIES\x00summary", ID: "summary", Type: "WORKSTREAM_SUMMARIES", State: "included", Created: timestamp(v, "created"), Updated: timestamp(v, "updated"), SupplementableFields: map[string]bool{"annotations": true}}}}
		candidates := map[cacheField]*cacheCandidate{}
		if err := r.readSDKCache(path, 1, candidates, &budget); err == nil {
			t.Fatal("reference bound ignored")
		}
		if len(candidates) > 0 || len(r.meta["WORKSTREAM_SUMMARIES\x00summary"].Edges) > 0 {
			t.Fatal("over-budget candidate applied")
		}
	}
}

func TestSDKCacheRecoveryPrecedencePrivacyAndProvenance(t *testing.T) {
	created := "2026-09-20T00:00:00Z"
	current := func(id string) map[string]any {
		v := record(id, created)
		v["updated"] = map[string]any{"value": "2026-09-29T00:00:00Z"}
		return v
	}
	cached := func(id, updated string, ids ...string) map[string]any {
		v := current(id)
		v["updated"] = map[string]any{"value": updated}
		v["annotations"] = refs(ids...)
		return v
	}
	old := "2026-09-27T00:00:00Z"
	newer := "2026-09-28T00:00:00Z"
	ids := []string{"recover", "empty", "present", "tombstone", "conflict", "wrong-created", "future", "expired", "derived-private", "excluded-parent"}
	summaries := []map[string]any{}
	for _, id := range ids {
		summaries = append(summaries, current(id))
	}
	summaries[1]["annotations"] = refs()
	summaries[2]["annotations"] = refs("current-body")
	summaries[9]["url"] = "https://bank.example/private"
	a := []map[string]any{cached("recover", old, "body", "missing"), cached("empty", old, "body"), cached("present", old, "body"), cached("tombstone", old, "body"), cached("conflict", newer, "body"), cached("wrong-created", old, "body"), cached("future", "2026-10-01T00:00:00Z", "body"), cached("expired", old, "body"), cached("derived-private", old, "derived-body"), cached("excluded-parent", old, "excluded-body")}
	a[0]["tags"] = refs("tag")
	a[0]["persons"] = refs("person")
	a[0]["text"] = "CACHED_PROSE_MUST_NOT_EXPORT"
	a[5]["created"] = map[string]any{"value": "2026-09-19T00:00:00Z"}
	a[8]["events"] = refs("bank-event")
	first, writer := cacheFixture(t, a, true)
	if _, err := writer.Exec("UPDATE workstream_summaries SET expireAt=1 WHERE key='expired'; INSERT INTO workstream_summaries (key,json) VALUES ('broken','{')"); err != nil {
		t.Fatal(err)
	}
	tombstone := cached("tombstone", newer, "body")
	tombstone["annotations"] = map[string]any{"indices": map[string]any{"body": -1}, "iterable": []any{map[string]any{"id": "body"}}}
	second, _ := cacheFixture(t, []map[string]any{tombstone, cached("conflict", newer, "current-body")}, false)
	body := record("body", created)
	body["type"] = "SUMMARY"
	secret := fakeSecret()
	body["text"] = "Current annotation narrative. " + secret
	liveBody := record("current-body", created)
	liveBody["text"] = "Current explicit narrative."
	liveBody["type"] = "SUMMARY"
	derivedBody := record("derived-body", created)
	derivedBody["text"] = "PRIVATE_DERIVED_PROSE"
	derivedBody["type"] = "SUMMARY"
	excludedBody := record("excluded-body", created)
	excludedBody["text"] = "PRIVATE_EXCLUDED_PARENT_PROSE"
	excludedBody["type"] = "SUMMARY"
	event := record("bank-event", created)
	event["url"] = "https://bank.example/activity"
	tag := record("tag", created)
	tag["text"] = "Example tag"
	person := record("person", created)
	person["name"] = "Example person"
	person["annotations"], person["summaries"] = refs(), refs()
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": summaries, "ANNOTATIONS": {body, liveBody, derivedBody, excludedBody}, "WORKSTREAM_EVENTS": {event}, "TAGS": {tag}, "PERSONS": {person}}}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("WORKSTREAM_SUMMARIES,ANNOTATIONS,WORKSTREAM_EVENTS,TAGS,PERSONS")
	policy := DefaultPolicy()
	policy.Deny = []DomainRule{{"bank.example", true}}
	out := filepath.Join(t.TempDir(), "archive")
	manifest, err := Export(context.Background(), c, Options{SDKCaches: []string{first, second}, Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy), Metadata: "off", Format: "both"})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "partial" || manifest.SDKCache.Conflicts != 1 || manifest.SDKCache.MissingTargets != 1 || manifest.SDKCache.Expired != 1 || manifest.SDKCache.Invalid != 1 || manifest.SDKCache.IdentityMismatch != 1 || manifest.SDKCache.UnusableTime != 1 {
		t.Fatalf("unexpected recovery coverage: %+v", manifest.SDKCache)
	}
	var paths map[string]string
	data, _ := os.ReadFile(filepath.Join(out, "link-map.json"))
	if err := json.Unmarshal(data, &paths); err != nil {
		t.Fatal(err)
	}
	readSummary := func(id string) string {
		t.Helper()
		p := paths[opaque("WORKSTREAM_SUMMARIES", id)]
		if p == "" {
			t.Fatal("summary missing", id)
		}
		b, err := os.ReadFile(filepath.Join(out, p))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	recovered := readSummary("recover")
	if !strings.Contains(recovered, "Current annotation narrative.") || !strings.Contains(recovered, "Historical attachment: cache 1") {
		t.Fatal("body/provenance not recovered")
	}
	for _, id := range []string{"empty", "present", "tombstone", "conflict", "wrong-created", "future", "expired"} {
		if strings.Contains(readSummary(id), "Current annotation narrative.") {
			t.Fatal("precedence/identity/conflict rule failed", id)
		}
	}
	if !strings.Contains(readSummary("present"), "Current explicit narrative.") {
		t.Fatal("current body was replaced")
	}
	for _, id := range []string{"derived-private", "excluded-parent"} {
		if paths[opaque("WORKSTREAM_SUMMARIES", id)] != "" {
			t.Fatal("private-derived summary retained")
		}
	}
	for _, id := range []string{"derived-body", "excluded-body"} {
		if paths[opaque("ANNOTATIONS", id)] != "" {
			t.Fatal("private body survived cached provenance")
		}
	}
	graph, _ := os.ReadFile(filepath.Join(out, "relationships.jsonl"))
	if !strings.Contains(string(graph), "historical_client_cache_derived_inverse") || !strings.Contains(string(graph), "cached_summary_updated") {
		t.Fatal("graph lost historical provenance")
	}
	if manifest.SDKCache.RetainedEdges == 0 || manifest.SDKCache.RetainedEdges >= manifest.SDKCache.AddedEdges {
		t.Fatal("privacy/retained-edge counts not distinguished")
	}
	err = filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) == ".pdf" {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		for _, forbidden := range []string{first, secret, "CACHED_PROSE_MUST_NOT_EXPORT", "PRIVATE_DERIVED_PROSE", "PRIVATE_EXCLUDED_PARENT_PROSE"} {
			if strings.Contains(string(b), forbidden) {
				t.Fatal("cache/privacy leak")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	moved := out + "-moved"
	if err := os.Rename(out, moved); err != nil {
		t.Fatal(err)
	}
	r := &run{ctx: context.Background(), stage: moved, opts: Options{Mode: "filtered", Scanner: scanner(t, policy)}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
}

// Explicit local opt-in, with no HTTP calls, archive mutations, cache cleanup,
// record content, cache paths, or identities in the test output.
func TestLiveSDKCacheCandidates(t *testing.T) {
	configPath := os.Getenv("PIECES_EXPORT_LIVE_CACHE_CONFIG")
	if configPath == "" {
		t.Skip("set PIECES_EXPORT_LIVE_CACHE_CONFIG to opt into local cache/staging reads")
	}
	var config struct {
		Caches               []string `json:"caches"`
		Stage                string   `json:"stage"`
		ReconcileAnnotations bool     `json:"reconcile_completed_annotations"`
		CompareProviderViews bool     `json:"compare_provider_views"`
	}
	b, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal("cache probe configuration unreadable")
	}
	if json.Unmarshal(b, &config) != nil || config.Stage == "" || len(config.Caches) == 0 {
		t.Fatal("cache probe configuration invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := ValidateSDKCaches(ctx, config.Caches); err != nil {
		t.Fatal(err)
	}
	r := &run{ctx: ctx, meta: map[string]*Meta{}}
	files, err := filepath.Glob(filepath.Join(config.Stage, "data/summaries/*.json"))
	if err != nil {
		t.Fatal("cannot enumerate staged summaries")
	}
	for _, path := range files {
		v, err := readRecord(path)
		if err != nil {
			t.Fatal("staged summary unreadable")
		}
		id := fieldString(v, "id")
		if id == "" {
			t.Fatal("staged summary missing identity")
		}
		m := &Meta{Key: "WORKSTREAM_SUMMARIES\x00" + id, ID: id, Type: "WORKSTREAM_SUMMARIES", State: "included", Created: timestamp(v, "created"), Updated: timestamp(v, "updated"), SupplementableFields: map[string]bool{}}
		for _, field := range cacheRelations {
			m.SupplementableFields[field] = v[field] == nil
		}
		r.meta[m.Key] = m
	}
	candidates := map[cacheField]*cacheCandidate{}
	budget := cacheReadBudget{}
	for i, path := range config.Caches {
		if err := r.readSDKCache(path, i+1, candidates, &budget); err != nil {
			t.Fatal(err)
		}
	}
	counts := map[string]int{}
	targets := map[string]map[string]bool{}
	conflicts := 0
	for field, candidate := range candidates {
		if candidate.conflict {
			conflicts++
			continue
		}
		if len(candidate.ids) == 0 {
			continue
		}
		counts[field.relation]++
		if targets[field.relation] == nil {
			targets[field.relation] = map[string]bool{}
		}
		for _, id := range candidate.ids {
			targets[field.relation][id] = true
		}
	}
	targetCounts := map[string]int{}
	for field, values := range targets {
		targetCounts[field] = len(values)
	}
	t.Logf("Staged summaries=%d cacheRows=%d matchedRows=%d invalid=%d expired=%d creationMismatch=%d unusableUpdate=%d conflicts=%d; candidate summary fields=%v; distinct referenced IDs=%v. Targets not reconciled; no archive was modified.", len(r.meta), r.manifest.SDKCache.Rows, r.manifest.SDKCache.Matching, r.manifest.SDKCache.Invalid, r.manifest.SDKCache.Expired, r.manifest.SDKCache.IdentityMismatch, r.manifest.SDKCache.UnusableTime, conflicts, counts, targetCounts)
	if !config.ReconcileAnnotations {
		return
	}
	// Only opt in after the annotation fetch phase has completed. This probe
	// checks retained local files, not source completeness or final privacy.
	present := map[string]bool{}
	textPresent := map[string]bool{}
	summaryText := map[string]bool{}
	missing, mismatched, legacyCardMatches := 0, 0, 0
	for id := range targets["annotations"] {
		if ctx.Err() != nil {
			t.Fatal("annotation reconciliation exceeded its time budget")
		}
		path := filepath.Join(config.Stage, "data/annotations", opaque("ANNOTATIONS", id)+".json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			missing++
			if uuidTokenPattern.FindString(id) == id {
				for _, match := range cardPattern.FindAllString(id, -1) {
					if validCard(match) {
						legacyCardMatches++
						break
					}
				}
			}
			continue
		}
		v, err := readRecord(path)
		if err != nil {
			t.Fatal("staged annotation unreadable")
		}
		if fieldString(v, "id") != id {
			mismatched++
			continue
		}
		present[id] = true
		for _, block := range content(v) {
			if strings.TrimSpace(block.Text) != "" {
				textPresent[id] = true
				if fieldString(v, "type") == "SUMMARY" {
					summaryText[id] = true
				}
				break
			}
		}
	}
	allPresent, anyText, anySummaryText := 0, 0, 0
	for field, candidate := range candidates {
		if field.relation != "annotations" || candidate.conflict || len(candidate.ids) == 0 {
			continue
		}
		all, text, body := true, false, false
		for _, id := range candidate.ids {
			all = all && present[id]
			text = text || textPresent[id]
			body = body || summaryText[id]
		}
		if all {
			allPresent++
		}
		if text {
			anyText++
		}
		if body {
			anySummaryText++
		}
	}
	t.Logf("Completed annotation-stage probe: candidate targets=%d matched files=%d missing files=%d identity mismatches=%d nonempty-text annotations=%d SUMMARY-type nonempty-text annotations=%d; summaries with all cached annotation targets present=%d, any annotation text=%d, SUMMARY-type text=%d. Historical attachment candidates only; final privacy and authoritative completeness are not established. No archive was modified.", len(targets["annotations"]), len(present), missing, mismatched, len(textPresent), len(summaryText), allPresent, anyText, anySummaryText)
	t.Logf("Missing annotation UUIDs matching the older payment-card heuristic=%d; this is diagnostic evidence, not proof of the omission reason. Reconcile finalized manifest decisions before any recovery.", legacyCardMatches)
	if config.CompareProviderViews {
		compareCachedProviderViews(t, r, config.Caches, config.Stage, candidates, summaryText)
	}
}
