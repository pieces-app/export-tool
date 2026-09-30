package exporter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Only typed relationships are recovered. Cached prose, credentials,
// embedded projections and source records never replace current OS records.
var cacheRelations = []string{"annotations", "persons", "pipelines", "tags", "events", "sources", "websites", "ranges", "hints", "summaries"}

type cacheTable struct {
	table, material string
	wrapped         bool
	relations       []string
}

// Identifiers below are a fixed SQL allowlist, never supplied by a cache or flag.
var cacheTables = []cacheTable{
	{"workstream_summaries", "WORKSTREAM_SUMMARIES", false, cacheRelations},
	{"annotations", "ANNOTATIONS", true, []string{"summaries", "persons", "signals"}},
	{"persons", "PERSONS", true, []string{"annotations", "summaries"}},
	{"signals", "SIGNALS", true, []string{"annotations", "persons", "pipelines", "summaries", "workstream_events", "websites", "ranges"}},
	// These SDK views contain LocalAnnotation too. Their provider keys are
	// deliberately never read: only the nested OS record can supply an edge.
	{"summaries_annotation_summary", "ANNOTATIONS", true, []string{"summaries", "persons", "signals"}},
	{"summaries_annotation_description", "ANNOTATIONS", true, []string{"summaries", "persons", "signals"}},
}

func (table cacheTable) isProviderView() bool {
	return table.table == "summaries_annotation_summary" || table.table == "summaries_annotation_description"
}

func validCacheEvidenceTable(name, material string) bool {
	for _, table := range cacheTables {
		if table.table == name && table.material == material {
			return true
		}
	}
	return false
}

func cacheFields(material string) []string {
	for _, table := range cacheTables {
		if table.material == material {
			return table.relations
		}
	}
	return nil
}

func ValidateSDKCacheMaterials(materials []Material) error {
	for _, material := range materials {
		if len(cacheFields(material.Type)) > 0 {
			return nil
		}
	}
	return errConfig("SDK cache recovery requires summaries, annotations, persons, or signals in selected materials")
}

const maxCacheRows = 200000
const maxCacheEdges = 2000000
const maxCacheRecordBytes = 8 << 20
const maxCacheReferenceBytes = 256 << 20

type cacheReadBudget struct{ edges, bytes int }

type CacheEvidence struct {
	Cache               int    `json:"cache_number"`
	CachedUpdated       string `json:"cached_summary_updated,omitempty"` // legacy summary evidence
	OSUpdated           string `json:"os_summary_updated,omitempty"`
	Material            string `json:"record_material,omitempty"`
	RecordRef           string `json:"record_ref,omitempty"`
	RecordTable         string `json:"record_table,omitempty"`
	CachedRecordUpdated string `json:"cached_record_updated,omitempty"`
	OSRecordUpdated     string `json:"os_record_updated,omitempty"`
}

func (e CacheEvidence) timestamps() (string, string) {
	if e.Material != "" {
		return e.CachedRecordUpdated, e.OSRecordUpdated
	}
	return e.CachedUpdated, e.OSUpdated
}

func (e CacheEvidence) description() string {
	kind := "summary"
	if e.Material != "" {
		kind = e.Material + " record"
	}
	cached, current := e.timestamps()
	return fmt.Sprintf("Historical attachment: cache %d, %s updated %s; current OS record updated %s. Attachment evidence may be stale; text comes from the retained OS annotation.", e.Cache, kind, cached, current)
}

type CacheCoverage struct {
	Selected          int `json:"selected_caches"`
	Rows              int `json:"rows_read"`
	Invalid           int `json:"invalid_or_oversized_rows"`
	Expired           int `json:"expired_rows"`
	Matching          int `json:"matching_summary_rows"`
	MatchingRecords   int `json:"matching_record_rows"`
	UnknownFields     int `json:"unknown_original_field_eligibility"`
	EmptyViews        int `json:"empty_provider_views"`
	InversePrecedence int `json:"current_inverse_precedence"`
	IdentityMismatch  int `json:"creation_time_mismatch_rows"`
	UnusableTime      int `json:"invalid_or_future_update_rows"`
	Conflicts         int `json:"conflicting_fields"`
	Fields            int `json:"candidate_fields"`
	MissingTargets    int `json:"unavailable_target_references"`
	AddedEdges        int `json:"added_edges_before_privacy"`
	RetainedEdges     int `json:"retained_historical_edges"`
}

type cacheCandidate struct {
	ids      []string
	updated  time.Time
	evidence CacheEvidence
	conflict bool
}
type cacheField struct{ key, relation string }

func openSDKCache(ctx context.Context, path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, errConfig("SDK cache path is invalid")
	}
	st, err := os.Stat(abs)
	if err != nil || !st.Mode().IsRegular() {
		return nil, errConfig("SDK cache must be an existing regular SQLite file")
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, errConfig("SDK cache is unreadable")
	}
	header := make([]byte, 16)
	_, err = io.ReadFull(f, header)
	f.Close()
	if err != nil || string(header) != "SQLite format 3\x00" {
		return nil, errConfig("SDK cache is not a SQLite database")
	}
	pathURI := filepath.ToSlash(abs)
	if !strings.HasPrefix(pathURI, "/") {
		pathURI = "/" + pathURI
	}
	u := url.URL{Scheme: "file", Path: pathURI}
	q := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "trusted_schema(0)", "busy_timeout(250)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, errConfig("SDK cache could not be opened read-only")
	}
	db.SetMaxOpenConns(1)
	// Do not use immutable=1: it can ignore a live cache's WAL. No SDK startup,
	// schema migration, cache expiry deletion, or writable fallback is allowed.
	found := false
	for _, table := range cacheTables {
		var kind string
		err = db.QueryRowContext(ctx, "SELECT type FROM sqlite_master WHERE name = ?", table.table).Scan(&kind)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil || kind != "table" {
			db.Close()
			return nil, errConfig("SDK cache relationship storage must be a table")
		}
		rows, err := db.QueryContext(ctx, "SELECT json, expireAt FROM "+table.table+" LIMIT 0")
		if err != nil {
			db.Close()
			return nil, errConfig("SDK cache has an unsupported relationship-table schema")
		}
		rows.Close()
		found = true
	}
	if !found {
		db.Close()
		return nil, errConfig("SDK cache has no supported relationship tables")
	}
	return db, nil
}

// Validate before launching or closing any app. Paths are local arguments only;
// they are never copied to a public manifest or a terminal error.
func ValidateSDKCaches(ctx context.Context, paths []string) error {
	if len(paths) > 8 {
		return errConfig("at most eight SDK caches can be selected")
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return errConfig("SDK cache must exist and be readable")
		}
		canonical, err = filepath.Abs(canonical)
		if err != nil || seen[canonical] {
			return errConfig("SDK cache paths must be distinct existing files")
		}
		seen[canonical] = true
		check, cancel := context.WithTimeout(ctx, 10*time.Second)
		db, err := openSDKCache(check, canonical)
		if db != nil {
			db.Close()
		}
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *run) recoverSDKCacheRelationships() error {
	if len(r.opts.SDKCaches) == 0 {
		return nil
	}
	r.manifest.SDKCache.Selected = len(r.opts.SDKCaches)
	r.cachedEdges = map[Edge]CacheEvidence{}
	candidates := map[cacheField]*cacheCandidate{}
	budget := cacheReadBudget{}
	for n, path := range r.opts.SDKCaches {
		if err := r.readSDKCache(path, n+1, candidates, &budget); err != nil {
			return err
		}
	}
	keys := make([]cacheField, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].key != keys[j].key {
			return keys[i].key < keys[j].key
		}
		return keys[i].relation < keys[j].relation
	})
	seenEdges := map[Edge]bool{}
	changed := map[string]bool{}
	for _, m := range r.meta {
		for _, e := range m.Edges {
			seenEdges[e] = true
		}
	}
	for _, key := range keys {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		candidate := candidates[key]
		if candidate.conflict {
			r.manifest.SDKCache.Conflicts++
			continue
		}
		r.manifest.SDKCache.Fields++
		for _, id := range candidate.ids {
			target := referenceTypes[key.relation] + "\x00" + id
			meta := r.archiveDependency(target)
			if meta == nil || meta.State == "missing" {
				r.manifest.SDKCache.MissingTargets++
				continue
			}
			// Keep excluded/withheld targets in the private graph until privacy
			// propagation. Skipping them here could leave derived text exposed.
			source := r.meta[key.key]
			if source.State == "included" && meta.State == "included" {
				inverse := inverseAttachmentField(source.Type, meta.Type, key.relation)
				if eligible, known := meta.SupplementableFields[inverse]; inverse != "" && known && !eligible {
					// A current inverse collection (even empty/invalid) takes
					// precedence over a historical attachment. Any current
					// positive edge will supply its own inverse during reconciliation.
					r.manifest.SDKCache.InversePrecedence++
					continue
				}
			}
			edge := Edge{key.key, target, key.relation}
			if !seenEdges[edge] {
				seenEdges[edge] = true
				r.meta[key.key].Edges = append(r.meta[key.key].Edges, edge)
				changed[key.key] = true
				r.cachedEdges[edge] = candidate.evidence
				r.manifest.SDKCache.AddedEdges++
			}
		}
	}
	for key := range changed {
		edges := r.meta[key].Edges
		sort.Slice(edges, func(i, j int) bool {
			if edges[i].Relation != edges[j].Relation {
				return edges[i].Relation < edges[j].Relation
			}
			return edges[i].Target < edges[j].Target
		})
	}
	r.issue("SDK_CACHE", "", "historical_relationships_unverified")
	r.manifest.Warnings = append(r.manifest.Warnings, "Explicitly selected SDK caches supplied historical relationship evidence. Cached links may be stale; no current record or body was replaced. Current OS projections remain unverified, and this archive remains partial.")
	return nil
}

func inverseAttachmentField(source, target, relation string) string {
	if source == "SIGNALS" && signalInverseFields[target] != "" && relation == signalInverseFields[target] {
		return "signals"
	}
	if target == "SIGNALS" && relation == "signals" {
		return signalInverseFields[source]
	}
	switch {
	case source == "ANNOTATIONS" && target == "WORKSTREAM_SUMMARIES" && relation == "summaries":
		return "annotations"
	case source == "WORKSTREAM_SUMMARIES" && target == "ANNOTATIONS" && relation == "annotations":
		return "summaries"
	case source == "PERSONS" && target == "ANNOTATIONS" && relation == "annotations":
		return "persons"
	case source == "ANNOTATIONS" && target == "PERSONS" && relation == "persons":
		return "annotations"
	case source == "PERSONS" && target == "WORKSTREAM_SUMMARIES" && relation == "summaries":
		return "persons"
	case source == "WORKSTREAM_SUMMARIES" && target == "PERSONS" && relation == "persons":
		return "summaries"
	}
	return ""
}

func (r *run) readSDKCache(path string, ordinal int, candidates map[cacheField]*cacheCandidate, budget *cacheReadBudget) error {
	ctx, cancel := context.WithTimeout(r.ctx, 2*time.Minute)
	defer cancel()
	db, err := openSDKCache(ctx, path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return errConfig("SDK cache read transaction failed")
	}
	defer tx.Rollback()
	// One transaction supplies a consistent view of every selected table,
	// including WAL data. Row/reference bounds are shared across table types.
	count := 0
	r.progress.Stage(fmt.Sprintf("Read historical SDK cache %d", ordinal), 0)
	for _, table := range cacheTables {
		var kind string
		err := tx.QueryRowContext(ctx, "SELECT type FROM sqlite_master WHERE name = ?", table.table).Scan(&kind)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil || kind != "table" {
			return errConfig("SDK cache relationship table changed or is unreadable")
		}
		if err := r.readSDKCacheTable(ctx, tx, table, ordinal, &count, candidates, budget); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) readSDKCacheTable(ctx context.Context, tx *sql.Tx, table cacheTable, ordinal int, count *int, candidates map[cacheField]*cacheCandidate, budget *cacheReadBudget) error {
	// Bound returned strings before allocation; SQL table names are constants.
	rows, err := tx.QueryContext(ctx, "SELECT CASE WHEN length(CAST(json AS BLOB)) <= ? THEN json ELSE NULL END, expireAt FROM "+table.table+" LIMIT ?", maxCacheRecordBytes, maxCacheRows-*count+1)
	if err != nil {
		return errConfig("SDK cache relationships could not be read")
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		*count++
		r.manifest.SDKCache.Rows++
		if *count > maxCacheRows {
			return errConfig("SDK cache exceeds the shared row bound; no archive was finalized")
		}
		var raw sql.NullString
		var expiry sql.NullInt64
		if err := rows.Scan(&raw, &expiry); err != nil {
			return errConfig("SDK cache row has an invalid storage encoding")
		}
		if expiry.Valid && expiry.Int64 <= time.Now().UnixMilli() {
			r.manifest.SDKCache.Expired++
			continue
		}
		if !raw.Valid || len(raw.String) > maxCacheRecordBytes {
			r.manifest.SDKCache.Invalid++
			continue
		}
		v := map[string]any{}
		decoder := json.NewDecoder(strings.NewReader(raw.String))
		decoder.UseNumber()
		if decoder.Decode(&v) != nil {
			r.manifest.SDKCache.Invalid++
			continue
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			r.manifest.SDKCache.Invalid++
			continue
		}
		if v == nil {
			if table.isProviderView() {
				r.manifest.SDKCache.EmptyViews++
			} else {
				r.manifest.SDKCache.Invalid++
			}
			continue
		}
		if table.wrapped {
			if value, exists := v["os"]; table.isProviderView() && exists && value == nil {
				r.manifest.SDKCache.EmptyViews++
				continue
			}
			v = object(v, "os")
		}
		if fieldString(v, "id") == "" {
			r.manifest.SDKCache.Invalid++
			continue
		}
		key := table.material + "\x00" + fieldString(v, "id")
		m := r.meta[key]
		if m == nil || m.State == "missing" || m.ArchivePlaceholder {
			if err := r.blockUnavailableCachedRecord(table.material, v, budget); err != nil {
				return err
			}
			continue
		}
		r.manifest.SDKCache.MatchingRecords++
		if table.material == "WORKSTREAM_SUMMARIES" {
			r.manifest.SDKCache.Matching++
		}
		created, ce := time.Parse(time.RFC3339Nano, timestamp(v, "created"))
		currentCreated, cce := time.Parse(time.RFC3339Nano, m.Created)
		if ce != nil || cce != nil || !created.Equal(currentCreated) {
			r.manifest.SDKCache.IdentityMismatch++
			continue
		}
		updated, ue := time.Parse(time.RFC3339Nano, timestamp(v, "updated"))
		currentUpdated, cu := time.Parse(time.RFC3339Nano, m.Updated)
		if ue != nil || cu != nil || updated.After(currentUpdated) {
			r.manifest.SDKCache.UnusableTime++
			continue
		}
		for _, relation := range table.relations {
			state := projectionState(v[relation])
			if state != "empty" && state != "linked" {
				continue
			}
			eligible, known := m.SupplementableFields[relation]
			if !known {
				r.manifest.SDKCache.UnknownFields++
			}
			if !eligible {
				continue
			}
			ids := references(v[relation])
			sort.Strings(ids)
			budget.edges += len(ids)
			for _, id := range ids {
				budget.bytes += len(id)
			}
			if budget.edges > maxCacheEdges || budget.bytes > maxCacheReferenceBytes {
				return errConfig("SDK cache exceeds the relationship read bound; no archive was finalized")
			}
			field := cacheField{key, relation}
			old := candidates[field]
			if old != nil && updated.Before(old.updated) {
				continue
			}
			if old != nil && updated.Equal(old.updated) {
				if !sameIDs(old.ids, ids) {
					old.conflict = true
				}
				continue
			}
			evidence := CacheEvidence{Cache: ordinal}
			if table.material == "WORKSTREAM_SUMMARIES" {
				evidence.CachedUpdated = updated.UTC().Format(time.RFC3339Nano)
				evidence.OSUpdated = currentUpdated.UTC().Format(time.RFC3339Nano)
			} else {
				evidence.Material, evidence.RecordRef = table.material, opaque(table.material, m.ID)
				evidence.RecordTable = table.table
				evidence.CachedRecordUpdated = updated.UTC().Format(time.RFC3339Nano)
				evidence.OSRecordUpdated = currentUpdated.UTC().Format(time.RFC3339Nano)
			}
			candidates[field] = &cacheCandidate{ids: ids, updated: updated, evidence: evidence}
		}
	}
	if rows.Err() != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errConfig("SDK cache reading failed; no archive was finalized")
	}
	return nil
}
