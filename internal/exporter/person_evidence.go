package exporter

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"time"
)

// Query the actual persona/profile store; absent embedded fields are not evidence
// of absence. Preview needs one newest record per type. Export reads overlapping
// created-time windows, checking that each full page makes progress.
func personAnnotations(ctx context.Context, c *Client, id, typ string, history bool) ([]map[string]any, bool, error) {
	return personAnnotationTypes(ctx, c, id, []string{typ}, history)
}

func personAnnotationTypes(ctx context.Context, c *Client, id string, types []string, history bool) ([]map[string]any, bool, error) {
	allowed := map[string]bool{}
	for _, typ := range types {
		allowed[typ] = true
	}
	limit := 1
	if history {
		limit = 50
	}
	found := map[string]map[string]any{}
	var upper *time.Time
	for page := 0; page < 2000; page++ {
		filter := map[string]any{"types": types}
		if upper != nil {
			filter["created"] = map[string]any{"to": map[string]any{"value": upper.UTC().Format(time.RFC3339Nano)}}
		}
		var out map[string]any
		err := c.JSON(ctx, "POST", "/person/"+url.PathEscape(id)+"/annotations", map[string]any{"filter": filter, "limit": limit}, &out)
		if err != nil {
			return nil, false, err
		}
		person := object(out, "person")
		if fieldString(person, "id") != id {
			return nil, false, errConfig("person annotation query returned a different identity")
		}
		list, ok := object(out, "annotations")["iterable"].([]any)
		if !ok || len(list) > limit {
			return nil, false, errConfig("invalid bounded person annotation response")
		}
		added := 0
		var oldest time.Time
		for _, item := range list {
			v, ok := item.(map[string]any)
			if !ok || fieldString(v, "id") == "" || !allowed[fieldString(v, "type")] {
				return nil, false, errConfig("person annotation response type/identity mismatch")
			}
			key := fieldString(v, "id")
			if _, exists := found[key]; !exists {
				found[key] = v
				added++
			}
			t, e := time.Parse(time.RFC3339Nano, timestamp(v, "created"))
			if e == nil && (oldest.IsZero() || t.Before(oldest)) {
				oldest = t
			}
		}
		result := func() []map[string]any {
			ids := []string{}
			for id := range found {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			v := []map[string]any{}
			for _, id := range ids {
				v = append(v, found[id])
			}
			return v
		}
		if !history || len(list) < limit {
			return result(), true, nil
		}
		if added == 0 || oldest.IsZero() || (upper != nil && !oldest.Before(*upper)) {
			return result(), false, nil
		}
		upper = &oldest // overlap boundary, deduplicate; never skip tied timestamps.
	}
	return nil, false, errConfig("person annotation history exceeded the page safety bound")
}
func personEventCount(ctx context.Context, c *Client, id string) (int, error) {
	var out struct {
		Total *int `json:"total"`
	}
	err := c.JSON(ctx, "GET", "/workstream_event_to_person_associations/person/"+url.PathEscape(id)+"?limit=1&offset=0&transferables=false", nil, &out)
	if err != nil {
		return 0, err
	}
	if out.Total == nil || *out.Total < 0 {
		return 0, errConfig("person association endpoint omitted its total")
	}
	return *out.Total, nil
}
func personEvidence(ctx context.Context, c *Client, p *PersonFacts, history, eventCounts bool) ([]map[string]any, error) {
	records := []map[string]any{}
	seen := map[string]bool{}
	p.UnknownAnnotations = false
	read := func(types []string) (bool, error) {
		values, complete, err := personAnnotationTypes(ctx, c, p.ID, types, history)
		if errors.Is(err, ErrOSBusy) || ctx.Err() != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, err
		}
		for _, v := range values {
			id := fieldString(v, "id")
			if !seen[id] {
				seen[id] = true
				records = append(records, v)
			}
		}
		return complete && err == nil, nil
	}
	types := []string{"HIERARCHICAL_PROFILE_SUMMARY", "PROFILE_DESCRIPTION"}
	complete := false
	if history {
		var err error
		complete, err = read(types)
		if err != nil {
			return nil, err
		}
	}
	if !complete {
		// Preview needs one result per type. For history, separate streams also
		// recover mixed-type timestamp ties and older multi-type incompatibility.
		for _, typ := range types {
			ok, err := read([]string{typ})
			if err != nil {
				return nil, err
			}
			p.UnknownAnnotations = p.UnknownAnnotations || !ok
		}
	}
	p.SourceEventConnections = 0
	p.UnknownConnections = true
	if !eventCounts {
		return records, nil
	}
	count, err := personEventCount(ctx, c, p.ID)
	if errors.Is(err, ErrOSBusy) || ctx.Err() != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if err != nil {
		p.UnknownConnections = true
	} else {
		p.UnknownConnections = false
		p.SourceEventConnections = count
	}
	return records, nil
}

// Enrich only projected person records. Old-format embedded graphs continue to
// work without requiring newer endpoints. No generate/regenerate/update route.
func (r *run) loadPersonEvidence() error {
	if _, ok := r.coverage["ANNOTATIONS"]; !ok {
		return nil
	}
	projected := []*Meta{}
	for _, m := range r.sortedMeta() {
		if m.Type == "PERSONS" && m.State == "included" && m.PersonProjection {
			projected = append(projected, m)
		}
	}
	r.progress.Stage("Resolve projected person profiles", len(projected))
	annotationMaterial, _ := materialByType("ANNOTATIONS")
	for _, m := range projected {
		p := &PersonFacts{ID: m.ID, UnknownSummaries: true}
		records, err := personEvidence(r.ctx, r.client, p, true, r.opts.PeopleMode != "profiles")
		if err != nil {
			return err
		}
		m.PersonEvidence = p
		if p.UnknownAnnotations {
			r.issue("PERSONS", m.ID, "persona_history_unresolved")
		}
		for _, v := range records {
			id := fieldString(v, "id")
			key := "ANNOTATIONS\x00" + id
			if existing := r.meta[key]; existing == nil {
				if err := r.store(annotationMaterial, v, false); err != nil {
					return err
				}
			}
			m.Edges = append(m.Edges, Edge{m.Key, key, "annotations"})
			if annotation := r.meta[key]; annotation != nil {
				annotation.Edges = append(annotation.Edges, Edge{key, m.Key, "persons"})
			}
		}
		r.progress.Add(1)
	}
	if len(projected) > 0 {
		r.manifest.Warnings = append(r.manifest.Warnings, "Person snapshots omit embedded relationships on this OS. Persona histories were queried directly. Person-to-summary associations remain incomplete; connected mode conservatively retains people with unknown summary connectivity. Profiles mode selects a narrower people export.")
	}
	return nil
}
