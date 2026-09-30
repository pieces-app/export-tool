package exporter

import (
	"errors"
	"sort"
)

// Pagination is restricted to selected persons and events. It can discover a
// selected event whose inventory changed during the read interval, but never
// reads a collection omitted by summaries/custom scope.
func (r *run) exportPersonAssociationPages(family associationFamily, pairs []associationReference, cov *Coverage, row *AssociationFamilyCoverage) ([]associationReference, error) {
	stats := &AssociationPageCoverage{}
	row.Pagination = stats
	persons := []string{}
	for _, m := range r.meta {
		if m.Type == "PERSONS" && m.State == "included" {
			persons = append(persons, m.ID)
		}
	}
	// Prefer a previously measured small collection to discover old/unbounded
	// response schemas before asking for a heavily connected person's history.
	count := func(id string) int {
		if evidence := r.meta["PERSONS\x00"+id].PersonEvidence; evidence != nil && !evidence.UnknownConnections {
			return evidence.SourceEventConnections
		}
		return int(^uint(0) >> 1)
	}
	sort.Slice(persons, func(i, j int) bool {
		a, b := count(persons[i]), count(persons[j])
		if a != b {
			return a < b
		}
		return persons[i] < persons[j]
	})
	stats.Persons = len(persons)
	wanted := map[associationReference]bool{}
	for _, pair := range pairs {
		wanted[pair] = true
	}
	r.progress.Stage("Page event/person association metadata", len(persons))
	for i, id := range persons {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		stats.Attempted++
		unsupported, err := r.exportOnePersonAssociations(family, id, cov, stats, wanted)
		if err != nil {
			return nil, err
		}
		r.progress.Add(1)
		if unsupported {
			stats.NotAttempted = len(persons) - i - 1
			break
		}
	}
	for code, n := range map[string]int{
		"association_pagination_unavailable_or_unsupported": stats.Unavailable,
		"association_pagination_unsupported":                stats.Unsupported,
		"association_pagination_schema_unsupported":         stats.SchemaUnsupported,
		"association_pagination_failed":                     stats.Failed,
		"association_pagination_changed_or_incomplete":      stats.Drift,
		"association_endpoint_unavailable":                  stats.UnavailableEndpoints,
	} {
		if n > 0 {
			r.issue(family.material().Type, "", code)
		}
	}
	remaining := pairs[:0]
	for _, pair := range pairs {
		if wanted[pair] {
			remaining = append(remaining, pair)
		}
	}
	return remaining, nil
}

func (r *run) exportOnePersonAssociations(family associationFamily, person string, cov *Coverage, stats *AssociationPageCoverage, wanted map[associationReference]bool) (bool, error) {
	seen := map[string]associationReference{}
	seenPairs := map[associationReference]string{}
	identityBytes := 0
	offset := 0
	var first associationPage
	read := func(offset int) (associationPage, error) {
		stats.Pages++
		return r.client.readEventPersonPage(r.ctx, "person", person, associationPageSize, offset)
	}
	readFailure := func(err error) (bool, error) {
		if r.ctx.Err() != nil {
			return false, r.ctx.Err()
		}
		if errors.Is(err, ErrOSBusy) {
			return false, err
		}
		if errors.Is(err, errAssociationPaginationSchema) {
			stats.SchemaUnsupported++
			return true, nil
		}
		var api *APIError
		if errors.As(err, &api) && (api.Status == 405 || api.Status == 501) {
			stats.Unsupported++
			return true, nil
		}
		if errors.As(err, &api) && api.Status == 404 {
			stats.Unavailable++
		} else {
			stats.Failed++
		}
		return false, nil
	}
	for pageNumber := 0; pageNumber < maxAssociationPagesPerPerson; pageNumber++ {
		page, err := read(offset)
		if err != nil {
			return readFailure(err)
		}
		if pageNumber == 0 {
			first = page
		} else if page.total != first.total {
			stats.Drift++
			return false, nil
		}
		stats.Rows += len(page.records)
		if stats.Rows > maxAssociationPairs {
			return false, errConfig("event/person association enumeration exceeded its record bound; export was not finalized")
		}
		// Validate progress before committing any record from this page.
		for _, v := range page.records {
			id := fieldString(v, "id")
			pair := associationReference{left: fieldString(v, "workstream_event"), right: person}
			if prior, exists := seen[id]; exists {
				if prior != pair {
					if err := r.withholdAssociation(family.material().Type + "\x00" + id); err != nil {
						return false, err
					}
				}
				stats.Drift++
				return false, nil
			}
			if priorID := seenPairs[pair]; priorID != "" {
				if err := r.withholdAssociation(family.material().Type + "\x00" + priorID); err != nil {
					return false, err
				}
				stats.Drift++
				return false, nil
			}
			seen[id], seenPairs[pair] = pair, id
			identityBytes += len(id) + len(pair.left) + len(pair.right)
			if identityBytes > maxAssociationPairBytes {
				return false, errConfig("event/person association identity bound exceeded; export was not finalized")
			}
		}
		missingEvents := []string{}
		for _, v := range page.records {
			id := fieldString(v, "workstream_event")
			if r.meta["WORKSTREAM_EVENTS\x00"+id] == nil {
				missingEvents = append(missingEvents, id)
			}
		}
		material, _ := materialByType("WORKSTREAM_EVENTS")
		missingEvents = unique(missingEvents)
		for start := 0; start < len(missingEvents); {
			end := min(start+r.client.BatchSize(material, r.opts.BatchSize), len(missingEvents))
			if err := r.fetch(material, missingEvents[start:end]); err != nil {
				return false, err
			}
			start = end
		}
		for _, v := range page.records {
			pair := associationReference{left: fieldString(v, "workstream_event"), right: person}
			target := r.meta["WORKSTREAM_EVENTS\x00"+pair.left]
			if target == nil || target.State == "missing" {
				stats.UnavailableEndpoints++
			}
			stored, err := r.storeAssociationValue(family, v, cov)
			if err != nil {
				return false, err
			}
			if !stored {
				stats.Drift++
				return false, nil
			}
			if !wanted[pair] {
				cov.Inventoried++
			}
			delete(wanted, pair)
		}
		offset += len(page.records)
		if len(page.records) < associationPageSize && offset != first.total {
			stats.Drift++
			return false, nil
		}
		if offset == first.total {
			// A terminal boundary read catches additions/shifts past the original
			// end. Re-reading the head catches changed top rows or totals. Neither
			// proves an atomic snapshot or every in-place change in middle pages.
			if offset > 0 {
				end, err := read(offset)
				if err != nil {
					return readFailure(err)
				}
				if end.total != first.total || len(end.records) != 0 {
					stats.Drift++
					return false, nil
				}
			}
			head, err := read(0)
			if err != nil {
				return readFailure(err)
			}
			if head.total != first.total || head.fingerprint != first.fingerprint {
				stats.Drift++
				return false, nil
			}
			stats.Reconciled++
			return false, nil
		}
	}
	return false, errConfig("event/person association enumeration exceeded its page bound; export was not finalized")
}
