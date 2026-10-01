package exporter

import "errors"

// Only summaries scope may omit unrelated annotations. Both summary bodies and
// person histories need fully enumerated current owner-side associations, even
// for excluded owners. Merely finding some bodies is insufficient.
func (r *run) finishSummaryAnnotations() error {
	if r.opts.Scope != "summaries" || r.opts.Associations == "off" {
		return nil
	}
	complete := true
	for _, typ := range []string{"WORKSTREAM_SUMMARIES", "PERSONS"} {
		cov := r.coverage[typ]
		if cov == nil || cov.InitialCount < 0 || cov.Inventoried != cov.InitialCount {
			complete = false
		}
		for _, m := range r.meta {
			if m.Type == typ && !m.JunctionFields["annotations"] {
				complete = false
			}
		}
	}
	cov := r.coverage["ANNOTATIONS"]
	if complete {
		// Copy options: the caller's selected scope must not be mutated.
		refOnly := map[string]bool{}
		for typ, v := range r.opts.ReferenceOnly {
			refOnly[typ] = v
		}
		refOnly["ANNOTATIONS"] = true
		r.opts.ReferenceOnly = refOnly
		cov.InventoryMode = "references"
		r.manifest.Scope = scopeFor(r.opts)
		return nil
	}
	// This also restores full coverage if a programmatic caller supplied the
	// referenced-only shape from a prior capture to an older server.
	if r.opts.ReferenceOnly["ANNOTATIONS"] {
		refOnly := map[string]bool{}
		for typ, v := range r.opts.ReferenceOnly {
			if typ != "ANNOTATIONS" {
				refOnly[typ] = v
			}
		}
		r.opts.ReferenceOnly = refOnly
		r.manifest.Scope = scopeFor(r.opts)
	}
	cov.InventoryMode = "full"
	material, _ := materialByType("ANNOTATIONS")
	r.progress.Stage("Inventory ANNOTATIONS (incomplete relationship coverage)", 0)
	ids, err := r.inventoryMaterial(material, cov)
	if err != nil {
		if errors.Is(err, ErrOSBusy) || r.ctx.Err() != nil {
			return err
		}
		r.issue(material.Type, "", "inventory_failed")
		return nil
	}
	r.inventory[material.Type] = ids
	cov.Inventoried = len(ids)
	r.progress.Stage("Fetch ANNOTATIONS (incomplete relationship coverage)", len(ids))
	for start := 0; start < len(ids); {
		end := min(start+r.client.BatchSize(material, r.opts.BatchSize), len(ids))
		if err := r.fetch(material, ids[start:end]); err != nil {
			return err
		}
		r.progress.Add(end - start)
		start = end
	}
	return nil
}
