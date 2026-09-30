package exporter

import (
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

func (r *run) restoreArchiveState(root *os.Root, state *ArchiveState, byRef map[string]*Meta) error {
	seen := map[string]bool{}
	matched := 0
	err := archiveLines(r.ctx, root, archiveStateFile, state.StateSHA256, func(row archiveRecord) error {
		if !validDigest(row.Ref) || seen[row.Ref] || r.coverage[row.Material] == nil {
			return errConfig("archive reconstruction record is duplicate or invalid")
		}
		seen[row.Ref] = true
		if row.State != "included" {
			if row.State != "missing" && row.State != "withheld" && row.State != "excluded" && row.State != "omitted" || byRef[row.Ref] != nil {
				return errConfig("archive reconstruction decision conflicts with included data")
			}
			r.priorDecisions = append(r.priorDecisions, archiveRecord{Ref: row.Ref, Material: row.Material, State: row.State})
			return nil
		}
		m := byRef[row.Ref]
		if m == nil || m.Type != row.Material || row.DataSHA256 != m.ArchiveDataSHA256 || !validDigest(row.DataSHA256) || row.Redactions < 0 {
			return errConfig("archive included record is missing, altered, or has invalid evidence")
		}
		// Earlier format-5 writers did not record signal projection evidence.
		// Pruned canonical JSON cannot tell us whether an empty field was
		// originally empty. Accept the old row but keep all seven fields unknown.
		// A partially populated evidence map is still invalid, as for other types.
		originalProjections := row.ProjectionStates
		if m.Type == "SIGNALS" && row.ProjectionStates == nil {
			row.ProjectionStates = map[string]string{}
			for _, field := range projectionFields(m.Type) {
				row.ProjectionStates[field] = "absent"
			}
		}
		for _, field := range projectionFields(m.Type) {
			switch row.ProjectionStates[field] {
			case "absent", "empty", "linked", "invalid":
			default:
				return errConfig("archive original projection evidence is incomplete")
			}
		}
		m.Redactions += row.Redactions
		m.ProjectionStates = row.ProjectionStates
		m.SupplementableFields = row.SupplementableFields
		// Missing eligibility stays unknown, including on repeated rebuilds.
		// Older rebuilders also wrote synthetic "absent" projection states;
		// those alone cannot authorize new historical attachments.
		allowed := map[string]bool{}
		for _, field := range cacheFields(m.Type) {
			allowed[field] = true
		}
		for field, eligible := range m.SupplementableFields {
			if !allowed[field] {
				return errConfig("archive cache eligibility contains an unsupported field")
			}
			// False remains valid for legacy unknown projections. It narrows
			// recovery; only an affirmative eligibility conflicts with presence.
			if state, known := originalProjections[field]; known && eligible && state != "absent" {
				return errConfig("archive cache eligibility conflicts with original projection evidence")
			}
		}
		m.RelationshipProjectionUnknown = row.RelationshipProjectionUnknown
		m.PersonProjection = row.PersonProjection
		if row.PersonEvidence != nil {
			p := row.PersonEvidence
			if m.Type != "PERSONS" || p.SourceEventConnections < 0 {
				return errConfig("archive person evidence is invalid")
			}
			m.PersonEvidence = &PersonFacts{UnknownAnnotations: p.UnknownAnnotations, UnknownConnections: p.UnknownConnections, SourceEventConnections: p.SourceEventConnections}
		}
		if row.VerifiedUser {
			if m.Type != "PERSONS" {
				return errConfig("archive user mapping points to a non-person record")
			}
			r.userPersonIDs[m.ID] = true
		}
		matched++
		return nil
	})
	if err != nil {
		return err
	}
	if matched != len(byRef) {
		return errConfig("archive reconstruction evidence omits included records")
	}
	return nil
}

func (r *run) restoreArchiveGraph(root *os.Root, state *ArchiveState, byPath map[string]*Meta) error {
	expected := ""
	if state != nil {
		expected = state.GraphSHA256
	}
	err := archiveLines(r.ctx, root, "relationships.jsonl", expected, func(row PublicEdge) error {
		source, target := byPath[row.Source], byPath[row.Target]
		if source == nil || target == nil || row.Relation != "embedded_markdown" && referenceTypes[row.Relation] == "" {
			return errConfig("archive graph has an unresolved or invalid edge")
		}
		e := Edge{source.Key, target.Key, row.Relation}
		source.Edges = append(source.Edges, e)
		switch row.Provenance {
		case "":
		case "derived_inverse":
			r.derivedEdges[e] = true
		case "historical_client_cache", "historical_client_cache_derived_inverse":
			p := row.CacheEvidence
			if p == nil || p.Cache < 1 || p.Cache > r.manifest.SDKCache.Selected {
				return errConfig("archive cache-edge provenance is incomplete")
			}
			cachedText, currentText := p.timestamps()
			cached, ce := time.Parse(time.RFC3339Nano, cachedText)
			current, ue := time.Parse(time.RFC3339Nano, currentText)
			if ce != nil || ue != nil || cached.After(current) {
				return errConfig("archive cache-edge timestamps are invalid")
			}
			owner := source
			if row.Provenance == "historical_client_cache_derived_inverse" {
				owner = target
			}
			if p.Material != "" {
				updated, err := time.Parse(time.RFC3339Nano, owner.Updated)
				if len(cacheFields(p.Material)) == 0 || p.Material != owner.Type || p.RecordRef != opaque(owner.Type, owner.ID) || p.CachedUpdated != "" || p.OSUpdated != "" || err != nil || !updated.Equal(current) {
					return errConfig("archive cache-edge record provenance is invalid")
				}
				// Older typed evidence predates table labels. Present labels must
				// identify an implemented canonical source for the owner material.
				if p.RecordTable != "" && !validCacheEvidenceTable(p.RecordTable, p.Material) {
					return errConfig("archive cache-edge table provenance is invalid")
				}
			} else if owner.Type != "WORKSTREAM_SUMMARIES" || p.RecordRef != "" || p.RecordTable != "" || p.CachedRecordUpdated != "" || p.OSRecordUpdated != "" {
				return errConfig("archive legacy cache-edge owner is invalid")
			}
			if prior, exists := r.cachedEdges[e]; exists && prior != *p {
				return errConfig("archive graph has conflicting cache-edge evidence")
			}
			r.cachedEdges[e] = *p
			r.derivedEdges[e] = row.Provenance == "historical_client_cache_derived_inverse"
		default:
			return errConfig("archive graph provenance is unsupported")
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, m := range byPath {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		seen := map[Edge]bool{}
		out := m.Edges[:0]
		for _, e := range m.Edges {
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
		m.Edges = out
		for _, expected := range m.AssociationEndpoints {
			if !seen[expected] {
				return errConfig("archive association graph omits a canonical endpoint")
			}
		}
		sort.Slice(m.Edges, func(i, j int) bool {
			if m.Edges[i].Relation != m.Edges[j].Relation {
				return m.Edges[i].Relation < m.Edges[j].Relation
			}
			return m.Edges[i].Target < m.Edges[j].Target
		})
	}
	return nil
}

func resolveArchiveLink(from, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || strings.HasPrefix(u.Path, "/") {
		return ""
	}
	resolved := path.Join(path.Dir(from), u.Path)
	if !safeArchivePath(resolved) {
		return ""
	}
	return resolved
}

// Rebuild has no original bytes/identities for excluded or missing records.
// A selected-but-absent cache target is a blocking dependency, not an approved
// source. This can withhold extra generated content; it never resurrects it.
func (r *run) archiveDependency(key string) *Meta {
	if existing := r.meta[key]; existing != nil {
		return existing
	}
	typ, id, ok := splitRef(key)
	if !r.rebuilding || !ok || r.coverage[typ] == nil {
		return nil
	}
	m := &Meta{Key: key, ID: id, Type: typ, State: "withheld", ArchivePlaceholder: true}
	r.meta[key] = m
	r.manifest.Rebuild.UnavailableTargets++
	return m
}

// An unavailable cached owner or body could have been excluded. Its retained
// generated counterparts must not become newly exposed through another link.
func (r *run) blockUnavailableCachedRecord(material string, v map[string]any, budget *cacheReadBudget) error {
	if !r.rebuilding || r.opts.Mode != "filtered" || r.coverage[material] == nil {
		return nil
	}
	var relations []string
	switch material {
	case "WORKSTREAM_SUMMARIES", "SIGNALS":
		relations = []string{"annotations"}
	case "ANNOTATIONS":
		relations = []string{"summaries", "signals"}
	}
	for _, relation := range relations {
		ids := references(v[relation])
		budget.edges += len(ids)
		for _, id := range ids {
			budget.bytes += len(id)
		}
		if budget.edges > maxCacheEdges || budget.bytes > maxCacheReferenceBytes {
			return errConfig("SDK cache exceeds the relationship read bound; no archive was finalized")
		}
		for _, id := range ids {
			if m := r.meta[referenceTypes[relation]+"\x00"+id]; m != nil && m.State == "included" {
				m.State = "withheld"
				reason := "unavailable_cached_record_dependency"
				if material == "WORKSTREAM_SUMMARIES" {
					reason = "unavailable_cached_summary_dependency"
				}
				r.issue(m.Type, m.ID, reason)
				r.manifest.Rebuild.BlockedCacheBodies++
			}
		}
	}
	return nil
}
