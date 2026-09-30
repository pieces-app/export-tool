package exporter

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Probe only: provider keys are historical UI bindings, not current OS edges.
// The exact prefixes come from the SDK's generated Riverpod persist methods.
// These views must not override an explicit empty/conflicting source field.
func compareCachedProviderViews(t *testing.T, r *run, caches []string, stage string, candidates map[cacheField]*cacheCandidate, summaryText map[string]bool) {
	t.Helper()
	type view struct{ table, prefix, annotationType string }
	views := []view{{"summaries_annotation_summary", "SummaryRollupAnnotationNotifier(", "SUMMARY"}, {"summaries_annotation_description", "SummaryDescriptionAnnotationNotifier(", "DESCRIPTION"}}
	for _, spec := range views {
		counts := map[string]int{}
		pairs, novelSummaries, extraSummaryBodies := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, cache := range caches {
			db, err := openSDKCache(r.ctx, cache)
			if err != nil {
				t.Fatal("provider probe cache could not be opened read-only")
			}
			err = func() error {
				defer db.Close()
				tx, err := db.BeginTx(r.ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					return err
				}
				defer tx.Rollback()
				var kind string
				err = tx.QueryRowContext(r.ctx, "SELECT type FROM sqlite_master WHERE name = ?", spec.table).Scan(&kind)
				if err == sql.ErrNoRows {
					return nil
				}
				if err != nil || kind != "table" {
					return errConfig("unsupported provider table")
				}
				rows, err := tx.QueryContext(r.ctx, "SELECT CASE WHEN length(CAST(key AS BLOB)) <= 8192 THEN key ELSE NULL END, CASE WHEN length(CAST(json AS BLOB)) <= ? THEN json ELSE NULL END, expireAt FROM "+spec.table+" LIMIT ?", maxCacheRecordBytes, maxCacheRows+1)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					counts["rows"]++
					if counts["rows"] > maxCacheRows || r.ctx.Err() != nil {
						return errConfig("provider probe exceeded its row/time bound")
					}
					var key, raw sql.NullString
					var expires sql.NullInt64
					if err := rows.Scan(&key, &raw, &expires); err != nil {
						return err
					}
					if !key.Valid || !raw.Valid || expires.Valid && expires.Int64 <= time.Now().UnixMilli() || !strings.HasPrefix(key.String, spec.prefix) || !strings.HasSuffix(key.String, ")") {
						counts["invalid_or_expired"]++
						continue
					}
					var wrapper map[string]any
					if decodeArchiveJSON([]byte(raw.String), &wrapper) != nil {
						counts["invalid"]++
						continue
					}
					annotation := object(wrapper, "os")
					id := fieldString(annotation, "id")
					summaryID := strings.TrimSuffix(strings.TrimPrefix(key.String, spec.prefix), ")")
					m := r.meta["WORKSTREAM_SUMMARIES\x00"+summaryID]
					if id == "" || m == nil {
						counts["empty_or_unavailable"]++
						continue
					}
					path := filepath.Join(stage, "data/annotations", opaque("ANNOTATIONS", id)+".json")
					before, err := os.Lstat(path)
					if os.IsNotExist(err) {
						counts["empty_or_unavailable"]++
						continue
					}
					if err != nil || !before.Mode().IsRegular() || before.Size() > maxCacheRecordBytes {
						return errConfig("provider probe staged annotation unavailable")
					}
					f, err := os.Open(path)
					if err != nil {
						return err
					}
					b, err := io.ReadAll(io.LimitReader(f, maxCacheRecordBytes+1))
					f.Close()
					after, se := os.Stat(path)
					if err != nil || se != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || len(b) > maxCacheRecordBytes {
						return errConfig("provider probe staged annotation changed during read")
					}
					var current map[string]any
					if json.Unmarshal(b, &current) != nil || fieldString(current, "id") != id {
						return errConfig("provider probe staged identity is invalid")
					}
					created, ce := time.Parse(time.RFC3339Nano, timestamp(annotation, "created"))
					currentCreated, cce := time.Parse(time.RFC3339Nano, timestamp(current, "created"))
					updated, ue := time.Parse(time.RFC3339Nano, timestamp(annotation, "updated"))
					currentUpdated, cue := time.Parse(time.RFC3339Nano, timestamp(current, "updated"))
					if ce != nil || cce != nil || !created.Equal(currentCreated) || ue != nil || cue != nil || updated.After(currentUpdated) {
						counts["identity_or_time_rejected"]++
						continue
					}
					typ := fieldString(current, "type")
					if strings.TrimSpace(fieldString(current, "text")) == "" || typ != spec.annotationType && !(spec.annotationType == "SUMMARY" && typ == "DEEP_STUDY_HIERARCHICAL_SUMMARY") {
						counts["wrong_or_empty_body"]++
						continue
					}
					pair := m.Key + "\x00" + id
					if pairs[pair] {
						continue
					}
					pairs[pair] = true
					candidate := candidates[cacheField{m.Key, "annotations"}]
					if !m.SupplementableFields["annotations"] || candidate != nil && (candidate.conflict || len(candidate.ids) == 0) {
						counts["ineligible_or_conflicting"]++
						continue
					}
					matched, body := false, false
					explicitInverse := false
					for _, linkedSummary := range references(annotation["summaries"]) {
						explicitInverse = explicitInverse || linkedSummary == summaryID
					}
					if candidate != nil {
						for _, cachedID := range candidate.ids {
							matched = matched || cachedID == id
							body = body || summaryText[cachedID]
						}
					}
					if matched {
						counts["pair_already_in_summary_cache"]++
					} else {
						counts["pair_not_in_summary_cache"]++
						if explicitInverse {
							counts["novel_pair_has_explicit_inverse"]++
						} else {
							counts["novel_pair_only_ui_binding"]++
						}
						novelSummaries[m.Key] = true
						if typ == "SUMMARY" && !body {
							extraSummaryBodies[m.Key] = true
						}
					}
				}
				return rows.Err()
			}()
			if err != nil {
				t.Fatal("bounded provider-view comparison could not complete; no private values emitted")
			}
		}
		t.Logf("Provider view %s: aggregate=%v; distinct validated pairs=%d; summaries with novel pairs=%d; additional SUMMARY-body candidates versus summary-cache route=%d. Historical UI evidence only; not imported, not compared with inverse annotation recovery, and not a final privacy result.", spec.table, counts, len(pairs), len(novelSummaries), len(extraSummaryBodies))
	}
}
