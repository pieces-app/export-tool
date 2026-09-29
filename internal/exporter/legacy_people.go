package exporter

// In archive format 4, all-people mode's Unknown statistic counts only people
// with unknown annotation-selection evidence. Each failed projected-person
// history query also emits a keyed persona_history_unresolved issue. Reconcile
// those two explicit reports; absence of an issue alone proves nothing.
func (r *run) restoreLegacyPersonEvidence(source Manifest, raw []byte, byRef map[string]*Meta) {
	if source.ArchiveState != nil || source.People.Mode != "all" || r.coverage["PERSONS"] == nil || r.coverage["ANNOTATIONS"] == nil {
		return
	}
	var fields map[string]any
	if decodeArchiveJSON(raw, &fields) != nil {
		return
	}
	people := object(fields, "people")
	for _, key := range []string{"mode", "total_persons", "selected_persons", "intentionally_omitted_persons", "persons_with_incomplete_selection_evidence"} {
		if value, present := people[key]; !present || value == nil {
			return // An omitted numeric field must not become an inferred zero.
		}
	}
	if _, ok := fields["issues"].([]any); !ok {
		return
	}
	count := 0
	for _, m := range byRef {
		if m.Type == "PERSONS" {
			count++
		}
	}
	stats := source.People
	if stats.Total != count || stats.Selected != count || stats.Omitted != 0 || stats.Unknown < 0 || stats.Unknown > count {
		return
	}
	unknown := map[string]bool{}
	for _, issue := range source.Issues {
		if issue.Material != "PERSONS" || issue.Code != "persona_history_unresolved" {
			continue
		}
		if !validDigest(issue.Key) {
			return
		}
		// Historical issues for privacy-excluded persons do not contribute to
		// the original included-people report; only retained identities count.
		if m := byRef[issue.Key]; m != nil {
			if m.Type != "PERSONS" {
				return
			}
			unknown[issue.Key] = true
		}
	}
	if len(unknown) != stats.Unknown {
		return // Other missing annotation evidence cannot be assigned safely.
	}
	for ref, m := range byRef {
		if m.Type == "PERSONS" {
			m.PersonEvidence = &PersonFacts{UnknownAnnotations: unknown[ref], UnknownConnections: true}
		}
	}
	r.manifest.Rebuild.LegacyPersonEvidence = true
	r.manifest.Rebuild.LegacyPersonsReconciled = count
	r.manifest.Rebuild.LegacyPersonsUnknown = len(unknown)
	r.manifest.Warnings = append(r.manifest.Warnings, "Legacy person annotation-selection evidence was reconciled from the explicit all-people report and matching per-person history issues. Unresolved people stay unknown. Event-connectivity totals and absent summary relationships were not reconstructed; the archive remains partial.")
}
