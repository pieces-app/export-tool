package exporter

func associationEvidenceEdges(m *Meta) []Edge {
	f, supported := associationFamilyByType(m.Type)
	if !supported || len(m.AssociationEndpoints) != 2 {
		return nil
	}
	left, right := m.AssociationEndpoints[0].Target, m.AssociationEndpoints[1].Target
	leftRelation, rightRelation := associationRelation(f.leftType), associationRelation(f.rightType)
	if f.name == eventPersonFamily {
		leftRelation = "workstream_events" // Retain the older archive graph label.
	}
	return []Edge{{left, right, rightRelation}, {right, left, leftRelation}}
}

// Source dependencies must exist BEFORE graph filtering, even when their proof
// or owner was denied. Otherwise a shared summary body could escape a denial.
func (r *run) attachAssociationDependencies() {
	if r.associationEdges == nil {
		r.associationEdges = map[Edge]string{}
	}
	for _, m := range r.meta {
		for _, e := range associationEvidenceEdges(m) {
			if r.meta[e.Source] != nil {
				r.addEdge(e.Source, e.Target, e.Relation)
				r.recordAssociationEdge(e, m.Key)
			}
		}
	}
}

func (r *run) recordAssociationEdge(e Edge, key string) {
	if r.associationEdges == nil {
		r.associationEdges = map[Edge]string{}
	}
	if previous := r.associationEdges[e]; previous == "" || key < previous {
		r.associationEdges[e] = key
	}
	// A canonical current pair is stronger evidence than a cached attachment.
	// Do not serialize contradictory cache and association proof metadata.
	delete(r.cachedEdges, e)
	delete(r.derivedEdges, e)
}

// Retained source association records establish explicit endpoint edges,
// even when ordinary endpoint snapshots omit their projections. Their proof
// remains a canonical association record; never label this as cached evidence.
func (r *run) reconcileAssociationEdges() {
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
		for _, e := range associationEvidenceEdges(m) {
			source, target := r.meta[e.Source], r.meta[e.Target]
			if source != nil && source.State == "included" && target != nil && target.State == "included" {
				r.addEdge(e.Source, e.Target, e.Relation)
				r.recordAssociationEdge(e, m.Key)
			}
		}
	}
}
