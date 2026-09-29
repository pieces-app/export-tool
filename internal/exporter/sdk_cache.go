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

// Only typed summary relationships are recovered. Cached prose, credentials,
// embedded projections and source records never replace current OS records.
var cacheRelations = []string{"annotations", "persons", "pipelines", "tags", "events", "sources", "websites", "ranges", "hints", "summaries"}

const maxCacheRows = 200000
const maxCacheEdges = 2000000
const maxCacheRecordBytes = 8 << 20
const maxCacheReferenceBytes = 256 << 20

type cacheReadBudget struct{ edges, bytes int }

type CacheEvidence struct {
	Cache         int    `json:"cache_number"`
	CachedUpdated string `json:"cached_summary_updated"`
	OSUpdated     string `json:"os_summary_updated"`
}

type CacheCoverage struct {
	Selected         int `json:"selected_caches"`
	Rows             int `json:"rows_read"`
	Invalid          int `json:"invalid_or_oversized_rows"`
	Expired          int `json:"expired_rows"`
	Matching         int `json:"matching_summary_rows"`
	IdentityMismatch int `json:"creation_time_mismatch_rows"`
	UnusableTime     int `json:"invalid_or_future_update_rows"`
	Conflicts        int `json:"conflicting_fields"`
	Fields           int `json:"candidate_fields"`
	MissingTargets   int `json:"unavailable_target_references"`
	AddedEdges       int `json:"added_edges_before_privacy"`
	RetainedEdges    int `json:"retained_historical_edges"`
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
	var kind string
	err = db.QueryRowContext(ctx, "SELECT type FROM sqlite_master WHERE name = 'workstream_summaries'").Scan(&kind)
	if err != nil || kind != "table" {
		db.Close()
		return nil, errConfig("SDK cache has no workstream_summaries table")
	}
	rows, err := db.QueryContext(ctx, "SELECT json, expireAt FROM workstream_summaries LIMIT 0")
	if err != nil {
		db.Close()
		return nil, errConfig("SDK cache has an unsupported summary-table schema")
	}
	rows.Close()
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
		if m.Type == "WORKSTREAM_SUMMARIES" {
			for _, e := range m.Edges {
				seenEdges[e] = true
			}
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
	// Bound returned strings before allocation; one transaction supplies a
	// consistent view including the WAL. Never dump cached rows to a log/file.
	rows, err := tx.QueryContext(ctx, "SELECT CASE WHEN length(CAST(json AS BLOB)) <= ? THEN json ELSE NULL END, expireAt FROM workstream_summaries LIMIT ?", maxCacheRecordBytes, maxCacheRows+1)
	if err != nil {
		return errConfig("SDK cache summaries could not be read")
	}
	defer rows.Close()
	count := 0
	r.progress.Stage(fmt.Sprintf("Read historical SDK cache %d", ordinal), 0)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		r.manifest.SDKCache.Rows++
		if count > maxCacheRows {
			return errConfig("SDK cache exceeds the summary row bound; no archive was finalized")
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
		if decoder.Decode(&v) != nil || v == nil {
			r.manifest.SDKCache.Invalid++
			continue
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			r.manifest.SDKCache.Invalid++
			continue
		}
		key := "WORKSTREAM_SUMMARIES\x00" + fieldString(v, "id")
		m := r.meta[key]
		if m == nil || m.State == "missing" || m.ArchivePlaceholder {
			if err := r.blockUnavailableCachedSummary(v, budget); err != nil {
				return err
			}
			continue
		}
		r.manifest.SDKCache.Matching++
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
		for _, relation := range cacheRelations {
			if !m.SupplementableFields[relation] {
				continue
			}
			state := projectionState(v[relation])
			if state != "empty" && state != "linked" {
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
			candidates[field] = &cacheCandidate{ids: ids, updated: updated, evidence: CacheEvidence{ordinal, updated.UTC().Format(time.RFC3339Nano), currentUpdated.UTC().Format(time.RFC3339Nano)}}
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
