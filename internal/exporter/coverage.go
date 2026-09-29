package exporter

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
)

// These are core navigation/body projections, not all optional model fields.
// An absent projection cannot be interpreted as an authoritative empty set.
func projectionFields(material string) []string {
	switch material {
	case "WORKSTREAM_SUMMARIES":
		return []string{"annotations", "persons", "pipelines"}
	case "PERSONS", "PIPELINES":
		return []string{"summaries"}
	}
	return nil
}

type RelationshipCoverage struct {
	Material string `json:"material"`
	Field    string `json:"field"`
	Included int    `json:"included_records"`
	Absent   int    `json:"absent"`
	Invalid  int    `json:"invalid"`
	Empty    int    `json:"explicit_empty"`
	Linked   int    `json:"with_active_references"`
}

func projectionState(value any) string {
	if value == nil {
		return "absent"
	}
	m, ok := value.(map[string]any)
	if !ok {
		return "invalid"
	}
	valid := false
	if raw, exists := m["indices"]; exists {
		indices, ok := raw.(map[string]any)
		if !ok {
			return "invalid"
		}
		valid = true
		for id, raw := range indices {
			if id == "" {
				return "invalid"
			}
			var n float64
			switch x := raw.(type) {
			case int:
				n = float64(x)
			case float64:
				n = x
			case json.Number:
				var err error
				n, err = x.Float64()
				if err != nil {
					return "invalid"
				}
			default:
				return "invalid"
			}
			if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n {
				return "invalid"
			}
		}
	}
	if raw, exists := m["iterable"]; exists {
		items, ok := raw.([]any)
		if !ok {
			return "invalid"
		}
		valid = true
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok || fieldString(item, "id") == "" {
				return "invalid"
			}
		}
	}
	if !valid {
		return "invalid"
	}
	if len(references(m)) == 0 {
		return "empty"
	}
	return "linked"
}

func projectionStates(material string, v map[string]any) map[string]string {
	fields := projectionFields(material)
	if len(fields) == 0 {
		return nil
	}
	states := make(map[string]string, len(fields))
	for _, field := range fields {
		states[field] = projectionState(v[field])
	}
	return states
}

func (r *run) collectRelationshipCoverage() {
	r.manifest.RelationshipCoverage = []RelationshipCoverage{}
	for _, material := range []string{"WORKSTREAM_SUMMARIES", "PERSONS", "PIPELINES"} {
		for _, field := range projectionFields(material) {
			row := RelationshipCoverage{Material: material, Field: field}
			for _, m := range r.meta {
				if m.Type != material || m.State != "included" {
					continue
				}
				row.Included++
				switch m.ProjectionStates[field] {
				case "empty":
					row.Empty++
				case "linked":
					row.Linked++
				case "invalid":
					row.Invalid++
				default:
					row.Absent++
				}
			}
			if row.Included == 0 {
				continue
			}
			r.manifest.RelationshipCoverage = append(r.manifest.RelationshipCoverage, row)
			if row.Absent+row.Invalid > 0 {
				r.issue(material, "", "unverified_"+field+"_projection")
			}
		}
	}
}

func (r *run) renderCoverage() error {
	var b strings.Builder
	b.WriteString("# Export coverage\n\n[Export index](index.md) · [Manifest](manifest.json)\n\nCounts describe the HTTP data returned during this run. This is not an atomic database backup. Exclusions and selection omissions are intentional; unknown relationship projections remain a coverage gap.\n\n## Material records\n\n| Material | Initial | Inventoried | Fetched | Included | Excluded | Withheld | Omitted | Final |\n| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, c := range r.manifest.Coverage {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %d | %d |\n", c.Material, c.InitialCount, c.Inventoried, c.Fetched, c.Included, c.Excluded, c.Withheld, c.Omitted, c.FinalCount)
	}
	b.WriteString("\nA count of -1 means unavailable. Fetched can exceed the initial inventory when reference traversal discovers additional records.\n\n## Core relationship projections\n\nThese counts examine the original HTTP field shapes of included records, before pruning private references. An explicit empty collection differs from an absent/null or malformed collection. Present projections still do not prove internal database completeness. Derived reverse edges can recover individual links without proving that every link was returned.\n\n| Material | Field | Included | Absent/null | Invalid | Explicit empty | Active references |\n| --- | --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, c := range r.manifest.RelationshipCoverage {
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %d | %d |\n", c.Material, c.Field, c.Included, c.Absent, c.Invalid, c.Empty, c.Linked)
	}
	if len(r.manifest.RelationshipCoverage) == 0 {
		b.WriteString("\nNo included records use the core summary/person/pipeline projections checked here.\n")
	}
	b.WriteString("\nAny absent or invalid core projection makes this export partial. Unknown body attachments cannot be reconstructed from titles, timestamps, or hierarchy enums. Persona history and hierarchy reads are separate evidence; they do not fill every body/person/pipeline projection. See the manifest for all issues, hierarchy coverage, warnings, privacy settings, and limitations.\n")
	return writeFile(filepath.Join(r.stage, "coverage.md"), []byte(b.String()))
}
