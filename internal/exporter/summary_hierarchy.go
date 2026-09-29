package exporter

import (
	"errors"
	"net/url"
	"sort"
)

// SummaryHierarchyCoverage reports only the hierarchy recovered through the
// dedicated read endpoints. It deliberately says nothing about summary bodies,
// people, or pipelines, whose association projections are independent.
type SummaryHierarchyCoverage struct {
	Available          bool `json:"available"`
	ParentCandidates   int  `json:"parent_candidates"`
	ChildCandidates    int  `json:"child_candidates"`
	ParentsRead        int  `json:"parents_read"`
	EdgesRecovered     int  `json:"edges_recovered"`
	InventoriesMatched bool `json:"inventories_matched"`
}

func (r *run) addEdge(source, target, relation string) bool {
	m := r.meta[source]
	if m == nil {
		return false
	}
	e := Edge{Source: source, Target: target, Relation: relation}
	for _, existing := range m.Edges {
		if existing == e {
			return false
		}
	}
	m.Edges = append(m.Edges, e)
	sort.Slice(m.Edges, func(i, j int) bool {
		if m.Edges[i].Relation != m.Edges[j].Relation {
			return m.Edges[i].Relation < m.Edges[j].Relation
		}
		return m.Edges[i].Target < m.Edges[j].Target
	})
	return true
}

func hierarchyIDs(v map[string]any) ([]string, error) {
	// A missing/malformed collection must never be mistaken for an empty graph.
	if nested := object(v, "workstreamSummaries"); nested != nil {
		v = nested
	}
	items, ok := v["iterable"].([]any)
	if !ok {
		return nil, errConfig("hierarchy response omitted its iterable")
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok || fieldString(m, "id") == "" {
			return nil, errConfig("hierarchy response contains an invalid reference")
		}
	}
	return references(v), nil
}

// resolveSummaryHierarchy obtains an edge list without rereading every summary
// snapshot. The global endpoints identify all possible parents/children; each
// parent then supplies its immediate children, preserving the server's DAG.
func (r *run) resolveSummaryHierarchy() error {
	if _, selected := r.coverage["WORKSTREAM_SUMMARIES"]; !selected {
		return nil
	}
	var parentResponse, childResponse map[string]any
	if err := r.client.JSON(r.ctx, "GET", "/workstream_summaries/parent/identifiers?transferables=false", nil, &parentResponse); err != nil {
		if errors.Is(err, ErrOSBusy) || r.ctx.Err() != nil {
			return err
		}
		r.issue("WORKSTREAM_SUMMARIES", "", "summary_hierarchy_parent_inventory_failed")
		r.manifest.Warnings = append(r.manifest.Warnings, "Summary hierarchy endpoint was unavailable; parent/child links may be incomplete. Summary snapshots are not treated as proof that no hierarchy exists.")
		return nil
	}
	if err := r.client.JSON(r.ctx, "GET", "/workstream_summaries/child/identifiers?transferables=false", nil, &childResponse); err != nil {
		if errors.Is(err, ErrOSBusy) || r.ctx.Err() != nil {
			return err
		}
		r.issue("WORKSTREAM_SUMMARIES", "", "summary_hierarchy_child_inventory_failed")
		r.manifest.Warnings = append(r.manifest.Warnings, "Summary hierarchy endpoint was only partly available; parent/child links may be incomplete.")
		return nil
	}
	parents, parentErr := hierarchyIDs(parentResponse)
	children, childErr := hierarchyIDs(childResponse)
	if parentErr != nil || childErr != nil {
		r.issue("WORKSTREAM_SUMMARIES", "", "summary_hierarchy_invalid_inventory")
		return nil
	}
	r.manifest.SummaryHierarchy.Available = true
	r.manifest.SummaryHierarchy.ParentCandidates = len(parents)
	r.manifest.SummaryHierarchy.ChildCandidates = len(children)
	r.progress.Stage("Recover summary hierarchy", len(parents))

	summaryMaterial, _ := materialByType("WORKSTREAM_SUMMARIES")
	seenChildren := map[string]bool{}
	expectedChildren := map[string]bool{}
	for _, id := range children {
		expectedChildren[id] = true
	}
	matched := true
	for _, parentID := range parents {
		parentKey := "WORKSTREAM_SUMMARIES\x00" + parentID
		if r.meta[parentKey] == nil {
			if r.opts.Scanner == nil {
				return errConfig("summary hierarchy diagnostic inventory changed; stopped without fetching content")
			}
			if err := r.fetch(summaryMaterial, []string{parentID}); err != nil {
				return err
			}
		}
		parent := r.meta[parentKey]
		if parent == nil || parent.State == "missing" {
			r.issue("WORKSTREAM_SUMMARIES", parentID, "summary_hierarchy_parent_not_read")
			r.progress.Add(1)
			continue
		}
		var out map[string]any
		path := "/workstream_summary/" + url.PathEscape(parentID) + "/child/identifiers?transferables=false"
		if err := r.client.JSON(r.ctx, "GET", path, nil, &out); err != nil {
			if errors.Is(err, ErrOSBusy) || r.ctx.Err() != nil {
				return err
			}
			r.issue("WORKSTREAM_SUMMARIES", parentID, "summary_hierarchy_children_read_failed")
			r.progress.Add(1)
			continue
		}
		childIDs, err := hierarchyIDs(out)
		if err != nil {
			r.issue("WORKSTREAM_SUMMARIES", parentID, "summary_hierarchy_invalid_children")
			r.progress.Add(1)
			continue
		}
		r.manifest.SummaryHierarchy.ParentsRead++
		if len(childIDs) == 0 {
			matched = false
			r.issue("WORKSTREAM_SUMMARIES", parentID, "summary_hierarchy_parent_without_children")
		}
		for _, childID := range childIDs {
			if childID == parentID {
				matched = false
				r.issue("WORKSTREAM_SUMMARIES", parentID, "summary_hierarchy_self_edge")
				continue
			}
			if !expectedChildren[childID] {
				matched = false
				r.issue("WORKSTREAM_SUMMARIES", childID, "summary_hierarchy_child_inventory_mismatch")
			}
			childKey := "WORKSTREAM_SUMMARIES\x00" + childID
			if r.meta[childKey] == nil {
				if r.opts.Scanner == nil {
					return errConfig("summary hierarchy diagnostic inventory changed; stopped without fetching content")
				}
				if err := r.fetch(summaryMaterial, []string{childID}); err != nil {
					return err
				}
			}
			r.addEdge(parentKey, childKey, "children")
			r.manifest.SummaryHierarchy.EdgesRecovered++
			r.addEdge(childKey, parentKey, "parents")
			seenChildren[childID] = true
		}
		r.progress.Add(1)
	}
	for _, childID := range children {
		if !seenChildren[childID] {
			matched = false
			r.issue("WORKSTREAM_SUMMARIES", childID, "summary_hierarchy_child_not_recovered")
		}
	}
	r.manifest.SummaryHierarchy.InventoriesMatched = matched && r.manifest.SummaryHierarchy.ParentsRead == len(parents)
	if r.manifest.SummaryHierarchy.EdgesRecovered > 0 {
		r.manifest.Warnings = append(r.manifest.Warnings, "Summary parent/child edges were read through the dedicated hierarchy endpoints. The server can suppress internal association errors, so matching returned inventories is not proof of database-level completeness. Annotation-body, person, and pipeline relationships have separate coverage limits.")
	}
	return nil
}
