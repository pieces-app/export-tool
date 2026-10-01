package exporter

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
)

// Current ObjectBox snapshots omit the collections backed by these junctions.
// Limit automatic traversal to summary/profile navigation and summary origins;
// the 87-family reader registry alone does not imply complete graph coverage.
type junctionPlan struct{ family, side, owner, relation string }

var summaryJunctionPlans = []junctionPlan{
	{"workstream_summary_to_annotation_associations", "workstream_summary", "WORKSTREAM_SUMMARIES", "annotations"},
	{"workstream_summary_to_person_associations", "workstream_summary", "WORKSTREAM_SUMMARIES", "persons"},
	{"workstream_summary_to_person_associations", "person", "PERSONS", "summaries"},
	{"pipeline_to_workstream_summary_associations", "pipeline", "PIPELINES", "summaries"},
	{"pipeline_to_workstream_summary_associations", "workstream_summary", "WORKSTREAM_SUMMARIES", "pipelines"},
	{"person_to_annotation_associations", "person", "PERSONS", "annotations"},
	{"tag_to_workstream_summary_associations", "workstream_summary", "WORKSTREAM_SUMMARIES", "tags"},
	{"workstream_summary_to_website_associations", "workstream_summary", "WORKSTREAM_SUMMARIES", "websites"},
	{"workstream_summary_to_workstream_pattern_engine_source_associations", "workstream_summary", "WORKSTREAM_SUMMARIES", "sources"},
	{"workstream_summary_to_anchor_associations", "workstream_summary", "WORKSTREAM_SUMMARIES", "anchors"},
	{"workstream_summary_to_range_associations", "workstream_summary", "WORKSTREAM_SUMMARIES", "ranges"},
}

type JunctionCoverage struct {
	Family      string `json:"family"`
	Side        string `json:"side"`
	Owners      int    `json:"owners"`
	Reconciled  int    `json:"reconciled"`
	Rows        int    `json:"rows"`
	CountReads  int    `json:"count_reads"`
	BulkReads   int    `json:"bulk_reads"`
	PageReads   int    `json:"page_reads"`
	Unsupported bool   `json:"unavailable_or_unsupported"`
}

func validateJunctionFields(typ string, fields map[string]bool) error {
	for field, complete := range fields {
		allowed := false
		for _, p := range summaryJunctionPlans {
			allowed = allowed || p.owner == typ && p.relation == field
		}
		if !allowed || !complete {
			return errConfig("invalid current junction coverage evidence")
		}
	}
	return nil
}

func junctionUnavailable(err error) bool {
	var api *APIError
	return errors.As(err, &api) && (api.Status == 404 || api.Status == 405 || api.Status == 501)
}

// A 404 on the initial count is a capability miss, never an empty relationship.
// Once a side works, any invalid response/drift/read failure aborts this capture:
// finalizing with missing privacy dependencies would be unsafe. No retries of
// a changing traversal and no Cartesian pair search are performed here.
func (r *run) resolveCurrentSummaryJunctions() error {
	if r.opts.Associations == "off" {
		return nil
	}
	r.junctionFamiliesRead = map[string]bool{}
	seenRecords := map[string][32]byte{}
	visited := map[junctionPlan]map[string]bool{}
	stats := map[junctionPlan]*JunctionCoverage{}
	for _, p := range summaryJunctionPlans {
		f, _ := junctionFamilyByName(p.family)
		if r.coverage[f.leftType] != nil && r.coverage[f.rightType] != nil {
			visited[p] = map[string]bool{}
			stats[p] = &JunctionCoverage{Family: p.family, Side: p.side}
		}
	}
	defer func() {
		for _, p := range summaryJunctionPlans {
			if row := stats[p]; row != nil && row.Owners > 0 {
				r.manifest.Junctions = append(r.manifest.Junctions, *row)
			}
		}
	}()
	for pass := 0; pass < 8; pass++ {
		progress := false
		for _, p := range summaryJunctionPlans {
			row := stats[p]
			if row == nil || row.Unsupported {
				continue
			}
			owners := []string{}
			for _, m := range r.meta {
				// Excluded owners also matter: shared text must inherit denials.
				if m.Type == p.owner && m.State != "missing" && !visited[p][m.ID] {
					owners = append(owners, m.ID)
				}
			}
			sort.Strings(owners)
			if len(owners) == 0 {
				continue
			}
			progress = true
			row.Owners += len(owners)
			r.progress.Stage("Read current relationships "+p.family+" "+p.side, len(owners))
			batch, total := map[string]int{}, 0
			flush := func() error {
				if len(batch) == 0 {
					return nil
				}
				if err := r.readJunctionOwners(p, batch, row, seenRecords); err != nil {
					return err
				}
				for id := range batch {
					visited[p][id] = true
				}
				r.progress.Add(len(batch))
				batch, total = map[string]int{}, 0
				return nil
			}
			for _, id := range owners {
				row.CountReads++
				count, err := r.client.readJunctionCount(r.ctx, p.family, p.side, id)
				if err != nil {
					if row.CountReads == 1 && junctionUnavailable(err) {
						row.Unsupported = true
						break
					}
					return err
				}
				if count > maxJunctionBulkRows {
					if err := flush(); err != nil {
						return err
					}
					if err := r.pageJunctionOwner(p, id, count, row, seenRecords); err != nil {
						return err
					}
					visited[p][id] = true
					r.progress.Add(1)
					continue
				}
				if len(batch) == maxJunctionBulkIDs || total+count > maxJunctionBulkRows {
					if err := flush(); err != nil {
						return err
					}
				}
				batch[id], total = count, total+count
			}
			if err := flush(); err != nil {
				return err
			}
		}
		if !progress {
			return nil
		}
		if err := r.resolveReferences(); err != nil {
			return err
		}
		// Materialize edges after hydration, including denied endpoints, before
		// caches and privacy propagation. No raw relationship fields are edited.
		for _, m := range r.meta {
			if _, ok := seenRecords[m.Key]; !ok {
				continue
			}
			for _, e := range associationEvidenceEdges(m) {
				if r.meta[e.Source] != nil {
					r.addEdge(e.Source, e.Target, e.Relation)
					r.recordAssociationEdge(e, m.Key)
				}
			}
		}
	}
	return errConfig("current junction traversal exceeded the closure bound; export was not finalized")
}

func (r *run) retainJunctionRows(p junctionPlan, rows []map[string]any, stats *JunctionCoverage, seen map[string][32]byte) error {
	f, _ := junctionFamilyByName(p.family)
	stats.Rows += len(rows)
	if stats.Rows > maxAssociationPairs {
		return errConfig("current junction traversal exceeded the row bound; export was not finalized")
	}
	cov := r.coverage[f.material().Type]
	if cov == nil {
		cov = &Coverage{Material: f.material().Type, InventoryMode: "current_junction_owners", InitialCount: -1, FinalCount: -1}
	}
	for _, v := range rows {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		key := f.material().Type + "\x00" + fieldString(v, "id")
		digest := sha256.Sum256(b)
		if prior, ok := seen[key]; ok {
			if prior != digest {
				return errConfig("current junction record changed between reads; export was not finalized")
			}
			continue
		}
		if len(seen) >= maxAssociationPairs || len(key)+64 > maxAssociationPairBytes-r.junctionIdentityBytes {
			return errConfig("current junction traversal exceeded the global identity bound; export was not finalized")
		}
		stored, err := r.storeAssociationValue(f, v, cov)
		if err != nil {
			return err
		}
		if !stored {
			return errConfig("current junction identity conflicts with an existing record")
		}
		cov.Inventoried++
		seen[key] = digest
		r.junctionIdentityBytes += len(key) + 64
	}
	return nil
}

func (r *run) completeJunctionOwner(p junctionPlan, id string, count int, peers map[string]bool, stats *JunctionCoverage) error {
	stats.CountReads++
	final, err := r.client.readJunctionCount(r.ctx, p.family, p.side, id)
	if err != nil {
		return err
	}
	if final != count {
		return errConfig("current junction count changed during export; export was not finalized")
	}
	m := r.meta[p.owner+"\x00"+id]
	if state := m.ProjectionStates[p.relation]; state == "empty" || state == "linked" {
		original := map[string]bool{}
		for _, e := range m.Edges {
			if e.Relation == p.relation {
				_, peer, ok := splitRef(e.Target)
				if ok {
					original[peer] = true
				}
			}
		}
		if len(original) != len(peers) {
			return errConfig("current junction contradicts the original relationship projection; export was not finalized")
		}
		for peer := range original {
			if !peers[peer] {
				return errConfig("current junction contradicts the original relationship projection; export was not finalized")
			}
		}
	}
	if m.JunctionFields == nil {
		m.JunctionFields = map[string]bool{}
	}
	m.JunctionFields[p.relation] = true
	if m.Type == "WORKSTREAM_SUMMARIES" || m.Type == "PIPELINES" {
		m.RelationshipProjectionUnknown = false
		for _, field := range projectionFields(m.Type) {
			if !m.JunctionFields[field] && m.ProjectionStates[field] != "empty" && m.ProjectionStates[field] != "linked" {
				m.RelationshipProjectionUnknown = true
			}
		}
	}
	r.junctionFamiliesRead[p.family] = true
	stats.Reconciled++
	return nil
}

func (r *run) readJunctionOwners(p junctionPlan, counts map[string]int, stats *JunctionCoverage, seen map[string][32]byte) error {
	f, ownerField, _ := junctionSide(p.family, p.side)
	peerField := f.rightField
	if ownerField == f.rightField {
		peerField = f.leftField
	}
	peers := map[string]map[string]bool{}
	total := 0
	ids := []string{}
	for id, n := range counts {
		total += n
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if total > 0 {
		stats.BulkReads++
		rows, err := r.client.readJunctionBulk(r.ctx, p.family, p.side, counts)
		if err != nil {
			return err
		}
		if err := r.retainJunctionRows(p, rows, stats, seen); err != nil {
			return err
		}
		for _, v := range rows {
			id := fieldString(v, ownerField)
			if peers[id] == nil {
				peers[id] = map[string]bool{}
			}
			peers[id][fieldString(v, peerField)] = true
		}
	}
	for _, id := range ids {
		if err := r.completeJunctionOwner(p, id, counts[id], peers[id], stats); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) pageJunctionOwner(p junctionPlan, id string, count int, stats *JunctionCoverage, seen map[string][32]byte) error {
	f, ownerField, _ := junctionSide(p.family, p.side)
	peerField := f.rightField
	if ownerField == f.rightField {
		peerField = f.leftField
	}
	peers := map[string]bool{}
	identities, bytes := map[string]bool{}, 0
	var first [32]byte
	for offset := 0; offset < count; offset += 50 {
		stats.PageReads++
		rows, err := r.client.readJunctionPage(r.ctx, p.family, p.side, id, 50, offset)
		if err != nil {
			return err
		}
		if len(rows) != min(50, count-offset) {
			return errConfig("current junction page size differs from count; export was not finalized")
		}
		if offset == 0 {
			b, _ := json.Marshal(rows)
			first = sha256.Sum256(b)
		}
		for _, v := range rows {
			key := fieldString(v, "id")
			if identities[key] {
				return errConfig("current junction pages repeat a row; export was not finalized")
			}
			identities[key] = true
			peer := fieldString(v, peerField)
			if !peers[peer] {
				bytes += len(peer)
			}
			peers[peer] = true
			bytes += len(key)
			if bytes > maxAssociationPairBytes {
				return errConfig("current junction pagination exceeds the identity memory bound")
			}
		}
		if err := r.retainJunctionRows(p, rows, stats, seen); err != nil {
			return err
		}
	}
	stats.PageReads++
	tail, err := r.client.readJunctionPage(r.ctx, p.family, p.side, id, 1, count)
	if err != nil {
		return err
	}
	if len(tail) != 0 {
		return errConfig("current junction has unexpected trailing rows; export was not finalized")
	}
	stats.PageReads++
	head, err := r.client.readJunctionPage(r.ctx, p.family, p.side, id, 50, 0)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(head)
	if sha256.Sum256(b) != first {
		return errConfig("current junction page head changed; export was not finalized")
	}
	return r.completeJunctionOwner(p, id, count, peers, stats)
}
