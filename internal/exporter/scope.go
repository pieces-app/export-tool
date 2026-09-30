package exporter

import (
	"fmt"
	"io"
	"sort"
)

// Scope separates complete collection inventories from supporting records
// reached through explicit relationships. Neither path implicitly selects events.
type Scope struct {
	Name              string         `json:"name"`
	Inventoried       []string       `json:"inventoried_materials"`
	ReferenceOnly     []string       `json:"referenced_materials"`
	Omitted           []string       `json:"omitted_materials"`
	OmittedReferences map[string]int `json:"omitted_reference_occurrences,omitempty"`
}

type Selection struct {
	Name          string
	Materials     []Material
	ReferenceOnly map[string]bool
}

func SelectScope(scope, materials string) (Selection, error) {
	if scope != "" && materials != "" {
		return Selection{}, errConfig("choose --scope or --materials, not both")
	}
	s := Selection{ReferenceOnly: map[string]bool{}}
	if materials != "" {
		s.Name = "custom"
		var err error
		s.Materials, err = SelectMaterials(materials)
		return s, err
	}
	if scope == "" {
		scope = "summaries"
	}
	s.Name = scope
	switch scope {
	case "all":
		s.Materials = append([]Material(nil), Materials...)
	case "summaries":
		roots := map[string]bool{"WORKSTREAM_SUMMARIES": true, "ANNOTATIONS": true, "PERSONS": true, "PIPELINES": true}
		for _, typ := range []string{"TAGS", "WEBSITES", "WORKSTREAM_PATTERN_ENGINE_SOURCES", "APPLICATIONS", "RANGES", "ANCHORS"} {
			s.ReferenceOnly[typ] = true
		}
		for _, m := range Materials {
			if roots[m.Type] || s.ReferenceOnly[m.Type] {
				s.Materials = append(s.Materials, m)
			}
		}
	default:
		return Selection{}, errConfig("scope must be all or summaries")
	}
	return s, nil
}

func (s Selection) InventoryMaterials() []Material {
	var out []Material
	for _, m := range s.Materials {
		if !s.ReferenceOnly[m.Type] {
			out = append(out, m)
		}
	}
	return out
}

func (s Selection) Print(w io.Writer) {
	fmt.Fprintf(w, "Export scope: %s\n", s.Name)
	if s.Name == "summaries" {
		fmt.Fprintln(w, "Inventory summaries, annotations, persons, and pipelines. Fetch tags, websites, sources, applications, ranges, and anchors only when referenced.")
		fmt.Fprintln(w, "Event bodies, signals, hints, source-window history, and other unselected collections are skipped. Graph and website-origin coverage are reduced; secret and visible-URL filtering still apply.")
		fmt.Fprintln(w, "Estimates cover the four inventoried collections; referenced supporting records and direct persona-history reads add work. Profiles mode skips event-count queries; all/connected people modes can query association totals without reading event bodies.")
	}
}

func scopeFor(o Options) Scope {
	s := Scope{Name: o.Scope, Inventoried: []string{}, ReferenceOnly: []string{}, Omitted: []string{}}
	if s.Name == "" {
		s.Name = "custom"
	}
	selected := map[string]bool{}
	for _, m := range o.Materials {
		selected[m.Type] = true
		if o.ReferenceOnly[m.Type] {
			s.ReferenceOnly = append(s.ReferenceOnly, m.Type)
		} else {
			s.Inventoried = append(s.Inventoried, m.Type)
		}
	}
	for _, m := range Materials {
		if !selected[m.Type] {
			s.Omitted = append(s.Omitted, m.Type)
		}
	}
	return s
}

func validateScope(o Options) error {
	selected := map[string]bool{}
	for _, m := range o.Materials {
		if selected[m.Type] {
			return errConfig("duplicate material in export selection")
		}
		selected[m.Type] = true
	}
	for typ, enabled := range o.ReferenceOnly {
		if enabled && !selected[typ] {
			return errConfig("reference-only material must be part of the selection")
		}
	}
	if o.Scope == "" || o.Scope == "custom" {
		return nil
	}
	plan, err := SelectScope(o.Scope, "")
	if err != nil {
		return err
	}
	if len(plan.Materials) != len(o.Materials) {
		return errConfig("materials do not match the selected scope")
	}
	for _, m := range plan.Materials {
		if !selected[m.Type] || o.ReferenceOnly[m.Type] != plan.ReferenceOnly[m.Type] {
			return errConfig("materials do not match the selected scope")
		}
	}
	return nil
}

func (r *run) collectScopeOmissions() {
	if r.rebuilding {
		return // Original scope omissions describe the source read interval.
	}
	r.manifest.Scope.OmittedReferences = map[string]int{}
	for _, m := range r.meta {
		if m.State != "included" {
			continue
		}
		for _, e := range m.Edges {
			if typ, _, ok := splitRef(e.Target); ok && r.coverage[typ] == nil {
				r.manifest.Scope.OmittedReferences[typ]++
			}
		}
	}
}

// Resolve only selected types, batching the frontier instead of issuing a
// singular request for every related label. No unselected collection is read.
func (r *run) resolveReferences() error {
	for pass := 0; pass < 8; pass++ {
		pending := map[string]map[string]bool{}
		for _, m := range r.meta {
			for _, e := range m.Edges {
				if r.meta[e.Target] != nil {
					continue
				}
				typ, id, ok := splitRef(e.Target)
				material, supported := materialByType(typ)
				if !ok || !supported || material.Collection == "" || r.coverage[typ] == nil {
					continue
				}
				if pending[typ] == nil {
					pending[typ] = map[string]bool{}
				}
				pending[typ][id] = true
			}
		}
		if len(pending) == 0 {
			return nil
		}
		for _, material := range r.opts.Materials {
			if len(pending[material.Type]) == 0 {
				continue
			}
			ids := make([]string, 0, len(pending[material.Type]))
			for id := range pending[material.Type] {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			r.progress.Stage("Resolve referenced "+material.Type, len(ids))
			for start := 0; start < len(ids); {
				if err := r.ctx.Err(); err != nil {
					return err
				}
				end := min(start+r.client.BatchSize(material, r.opts.BatchSize), len(ids))
				if err := r.fetch(material, ids[start:end]); err != nil {
					return err
				}
				r.progress.Add(end - start)
				start = end
			}
		}
	}
	r.issue("GRAPH", "", "reference_traversal_limit")
	return nil
}
