package exporter

// Either the annotation or its summary/signal can expose an attachment. Materialize its
// inverse for body rendering and privacy propagation, recording provenance.
// This never attaches records by matching title, text, timestamp, or enum alone.
func (r *run) reconcileAnnotationAttachments() {
	if r.derivedEdges == nil {
		r.derivedEdges = map[Edge]bool{}
	}
	for _, m := range r.sortedMeta() {
		for _, e := range append([]Edge(nil), m.Edges...) {
			target := r.meta[e.Target]
			if target == nil {
				continue
			}
			inverse := ""
			if m.Type == "ANNOTATIONS" && target.Type == "WORKSTREAM_SUMMARIES" && e.Relation == "summaries" {
				inverse = "annotations"
			} else if m.Type == "WORKSTREAM_SUMMARIES" && target.Type == "ANNOTATIONS" && e.Relation == "annotations" {
				inverse = "summaries"
			} else if m.Type == "ANNOTATIONS" && target.Type == "SIGNALS" && e.Relation == "signals" {
				inverse = "annotations"
			} else if m.Type == "SIGNALS" && target.Type == "ANNOTATIONS" && e.Relation == "annotations" {
				inverse = "signals"
			}
			if inverse != "" && r.addEdge(target.Key, m.Key, inverse) {
				r.derivedEdges[Edge{target.Key, m.Key, inverse}] = true
				if evidence, ok := r.cachedEdges[e]; ok {
					r.cachedEdges[Edge{target.Key, m.Key, inverse}] = evidence
					r.manifest.SDKCache.AddedEdges++
				}
			}
		}
	}
}
