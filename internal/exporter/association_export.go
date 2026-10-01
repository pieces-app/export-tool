package exporter

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Each family is processed independently. Exceeding either bound stops the
// export rather than silently accepting a truncated metadata collection.
const maxAssociationPairs = 2000000
const maxAssociationPairBytes = 256 << 20

type AssociationCoverage struct {
	Mode        string                      `json:"mode"`
	Enumeration string                      `json:"enumeration"`
	Families    []AssociationFamilyCoverage `json:"families"`
}

type AssociationFamilyCoverage struct {
	Pagination  *AssociationPageCoverage `json:"person_pagination,omitempty"`
	Family      string                   `json:"family"`
	Pairs       int                      `json:"observed_pairs"`
	Lookups     int                      `json:"lookups"`
	Unavailable int                      `json:"unavailable_or_unsupported"`
	Unsupported int                      `json:"unsupported"`
	Failed      int                      `json:"failed"`
	Skipped     int                      `json:"not_attempted"`
}

func ValidateAssociations(mode string) error {
	if mode != "" && mode != "linked" && mode != "off" {
		return errConfig("associations must be linked or off")
	}
	return nil
}

func (f associationFamily) material() Material {
	return Material{Type: strings.ToUpper(f.name), Folder: "associations/" + f.name}
}

var associationTypes = func() map[string]associationFamily {
	out := map[string]associationFamily{}
	for _, family := range junctionFamilies {
		out[family.material().Type] = family
	}
	return out
}()

func associationFamilyByType(typ string) (associationFamily, bool) {
	family, ok := associationTypes[typ]
	return family, ok
}

func associationRelation(typ string) string {
	// These existing graph labels work with ordinary rendering and rebuilding.
	for _, m := range Materials {
		if m.Type == typ {
			if typ == "WORKSTREAM_SUMMARIES" {
				return "summaries"
			}
			if typ == "WORKSTREAM_EVENTS" {
				return "events"
			}
			if typ == "WORKSTREAM_PATTERN_ENGINE_SOURCES" {
				return "sources"
			}
			if typ == "CONVERSATION_MESSAGES" {
				return "messages"
			}
			return strings.ToLower(typ)
		}
	}
	return ""
}

func associationEndpoints(f associationFamily, v map[string]any) []Edge {
	key := f.material().Type + "\x00" + fieldString(v, "id")
	return []Edge{
		{key, f.leftType + "\x00" + fieldString(v, f.leftField), associationRelation(f.leftType)},
		{key, f.rightType + "\x00" + fieldString(v, f.rightField), associationRelation(f.rightType)},
	}
}

func (r *run) associationPairs(f associationFamily) ([]associationReference, error) {
	return r.associationPairsBounded(f, maxAssociationPairs, maxAssociationPairBytes)
}

func (r *run) associationPairsBounded(f associationFamily, maxPairs, maxBytes int) ([]associationReference, error) {
	seen := map[associationReference]bool{}
	bytes := 0
	for _, m := range r.meta {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		if m.State != "included" || m.Type != f.leftType && m.Type != f.rightType {
			continue
		}
		for _, e := range m.Edges {
			if err := r.ctx.Err(); err != nil {
				return nil, err
			}
			// A prose hyperlink alone is not a typed association projection.
			if e.Relation == "embedded_markdown" || referenceTypes[e.Relation] == "" {
				continue
			}
			target := r.meta[e.Target]
			if target == nil || target.State != "included" || referenceTypes[e.Relation] != target.Type {
				continue
			}
			ref := associationReference{}
			if m.Type == f.leftType && target.Type == f.rightType {
				ref.left, ref.right = m.ID, target.ID
			} else if m.Type == f.rightType && target.Type == f.leftType {
				ref.left, ref.right = target.ID, m.ID
			} else {
				continue
			}
			if !validAssociationIdentity(ref.left) || !validAssociationIdentity(ref.right) {
				return nil, errConfig("observed association has an invalid endpoint identity")
			}
			if !seen[ref] {
				seen[ref] = true
				bytes += len(ref.left) + len(ref.right)
				if len(seen) > maxPairs || bytes > maxBytes {
					return nil, errConfig("observed associations exceed the per-family read bound; export was not finalized")
				}
			}
		}
	}
	pairs := make([]associationReference, 0, len(seen))
	for pair := range seen {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].left != pairs[j].left {
			return pairs[i].left < pairs[j].left
		}
		return pairs[i].right < pairs[j].right
	})
	return pairs, nil
}

func (r *run) exportAssociations() error {
	mode := r.opts.Associations
	if mode == "" {
		mode = "linked"
	}
	r.manifest.Associations = &AssociationCoverage{Mode: mode, Enumeration: "observed_pairs_only", Families: []AssociationFamilyCoverage{}}
	if mode == "off" {
		return nil
	}
	// Page event/person evidence first so newly reached event references can
	// participate in the other families' observed-pair lookups.
	eventPerson, _ := associationFamilyByName(eventPersonFamily)
	families := []associationFamily{eventPerson}
	for _, family := range associationFamilies {
		if family.name != eventPersonFamily {
			families = append(families, family)
		}
	}
	for _, family := range families {
		// Scope never expands to obtain metadata for an unselected endpoint.
		if r.coverage[family.leftType] == nil || r.coverage[family.rightType] == nil {
			continue
		}
		// A successful current-side enumeration already retained these records.
		// Re-reading observed pairs would duplicate them and lose provenance.
		if r.junctionFamiliesRead[family.name] {
			continue
		}
		pairs, err := r.associationPairs(family)
		if err != nil {
			return err
		}
		row := AssociationFamilyCoverage{Family: family.name, Pairs: len(pairs)}
		material := family.material()
		cov := &Coverage{Material: material.Type, InventoryMode: "observed_pairs", InitialCount: -1, FinalCount: -1, Inventoried: len(pairs)}
		if family.name == eventPersonFamily {
			r.manifest.Associations.Enumeration = "observed_pairs_and_person_pages"
			cov.InventoryMode = "person_pages_and_observed_pairs"
			var err error
			pairs, err = r.exportPersonAssociationPages(family, pairs, cov, &row)
			if err != nil {
				return err
			}
			if err := r.resolveReferences(); err != nil {
				return err
			}
			r.reconcileAnnotationAttachments()
		}
		if len(pairs) == 0 {
			r.manifest.Associations.Families = append(r.manifest.Associations.Families, row)
			continue
		}
		r.progress.Stage("Read association metadata "+family.name, len(pairs))
		for i, pair := range pairs {
			if err := r.ctx.Err(); err != nil {
				return err
			}
			v, err := r.client.readAssociationPair(r.ctx, family.name, pair.left, pair.right)
			row.Lookups++
			r.progress.Add(1)
			if err != nil {
				if errors.Is(err, ErrOSBusy) || r.ctx.Err() != nil {
					return err
				}
				var api *APIError
				if errors.As(err, &api) && (api.Status == 405 || api.Status == 501) {
					row.Unsupported++
					row.Skipped = len(pairs) - i - 1
					break
				}
				if errors.As(err, &api) && api.Status == 404 {
					row.Unavailable++
				} else {
					row.Failed++
				}
				continue
			}
			stored, err := r.storeAssociationValue(family, v, cov)
			if err != nil {
				return err
			}
			if !stored {
				row.Failed++
			}
		}
		if row.Unavailable > 0 {
			r.issue(material.Type, "", "association_unavailable_or_unsupported")
		}
		if row.Unsupported > 0 {
			r.issue(material.Type, "", "association_route_unsupported")
		}
		if row.Failed > 0 {
			r.issue(material.Type, "", "association_read_failed")
		}
		r.manifest.Associations.Families = append(r.manifest.Associations.Families, row)
	}
	return nil
}

func (r *run) storeAssociationValue(family associationFamily, v map[string]any, cov *Coverage) (bool, error) {
	material := family.material()
	key := material.Type + "\x00" + fieldString(v, "id")
	if r.meta[key] != nil {
		if err := r.withholdAssociation(key); err != nil {
			return false, err
		}
		return false, nil
	}
	if r.coverage[material.Type] == nil {
		r.coverage[material.Type] = cov
		r.manifest.Coverage = append(r.manifest.Coverage, cov)
	}
	return true, r.store(material, v, false)
}

func (r *run) withholdAssociation(key string) error {
	if prior := r.meta[key]; prior != nil {
		prior.State = "withheld"
		if prior.DataPath != "" {
			if err := r.removeCanonical(prior); err != nil {
				return err
			}
		}
	}
	return nil
}

// Re-run after person selection, including in preserve mode. A metadata record
// is useful only with both canonical endpoints; omitted IDs must not remain in
// its raw string binding fields. Never reclassify an already denied record.
func (r *run) filterAssociationRecords() error {
	for _, m := range r.meta {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if _, ok := associationFamilyByType(m.Type); !ok || m.State != "included" {
			continue
		}
		for _, e := range m.AssociationEndpoints {
			target := r.meta[e.Target]
			if target == nil || target.State != "included" {
				m.State = "withheld"
				if target != nil && target.State == "omitted" {
					m.State = "omitted"
				}
				if err := r.removeCanonical(m); err != nil {
					return errConfig("cannot remove association with unavailable endpoint; export was not finalized")
				}
				break
			}
		}
	}
	return nil
}

func renderAssociationMetadata(b *strings.Builder, m *Meta, v map[string]any, records map[string]*Meta) {
	if _, ok := associationFamilyByType(m.Type); !ok {
		return
	}
	b.WriteString("## Association metadata\n\n")
	for _, field := range []string{"role", "confidence", "evidence_type", "source_type", "explanation"} {
		if value, exists := v[field]; exists {
			switch value.(type) {
			case string, json.Number, float64, bool:
				fmt.Fprintf(b, "### %s\n\n%s\n\n", md(field), rewriteMarkdown(fmt.Sprint(value), m.Path, records))
			default:
				// Complete metadata, including structured/future fields, remains
				// in the canonical JSON linked above.
			}
		}
	}
	b.WriteString("These are source association values, not independently verified identity or confidence claims. Additional fields are retained in Record JSON.\n\n")
}
