package exporter

func eventPersonEvidenceEdges(m *Meta) []Edge {
	if m.Type != "WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS" || len(m.AssociationEndpoints) != 2 {
		return nil
	}
	event, person := m.AssociationEndpoints[0].Target, m.AssociationEndpoints[1].Target
	return []Edge{{event, person, "persons"}, {person, event, "workstream_events"}}
}

// Retained source association records establish explicit event/person edges,
// even when ordinary endpoint snapshots omit their projections. Their proof
// remains a canonical association record; never label this as cached evidence.
func (r *run) reconcileEventPersonAssociationEdges() {
	if r.associationEdges == nil {
		r.associationEdges = map[Edge]string{}
	}
	for e, key := range r.associationEdges {
		if proof := r.meta[key]; proof != nil && proof.State == "included" {
			continue
		}
		if source := r.meta[e.Source]; source != nil {
			out := source.Edges[:0]
			for _, prior := range source.Edges {
				if prior != e {
					out = append(out, prior)
				}
			}
			source.Edges = out
		}
		delete(r.associationEdges, e)
	}
	for _, m := range r.meta {
		if m.State != "included" {
			continue
		}
		for _, e := range eventPersonEvidenceEdges(m) {
			source, target := r.meta[e.Source], r.meta[e.Target]
			if source != nil && source.State == "included" && target != nil && target.State == "included" && r.addEdge(e.Source, e.Target, e.Relation) {
				r.associationEdges[e] = m.Key
			}
		}
	}
}
