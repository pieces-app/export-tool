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
			cached, ce := time.Parse(time.RFC3339Nano, p.CachedUpdated)
			current, ue := time.Parse(time.RFC3339Nano, p.OSUpdated)
			if ce != nil || ue != nil || cached.After(current) {
				return errConfig("archive cache-edge timestamps are invalid")
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

// A cached summary absent from the archived inclusion set could have been
// excluded. Its already-archived annotation bodies must not be newly exposed
// through another cached attachment. Conservatively withhold these bodies.
func (r *run) blockUnavailableCachedSummary(v map[string]any, budget *cacheReadBudget) error {
	if !r.rebuilding || r.opts.Mode != "filtered" || r.coverage["WORKSTREAM_SUMMARIES"] == nil {
		return nil
	}
	ids := references(v["annotations"])
	budget.edges += len(ids)
	for _, id := range ids {
		budget.bytes += len(id)
	}
	if budget.edges > maxCacheEdges || budget.bytes > maxCacheReferenceBytes {
		return errConfig("SDK cache exceeds the relationship read bound; no archive was finalized")
	}
	for _, id := range ids {
		if m := r.meta["ANNOTATIONS\x00"+id]; m != nil && m.State == "included" {
			m.State = "withheld"
			r.issue(m.Type, m.ID, "unavailable_cached_summary_dependency")
			r.manifest.Rebuild.BlockedCacheBodies++
		}
	}
	return nil
}
