package exporter

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	scope := r.manifest.Scope
	b.WriteString("# Export coverage\n\n[Export index](index.md) · [Manifest](manifest.json)\n\n## Selected export scope\n\n")
	if info := r.manifest.Rebuild; info != nil {
		fmt.Fprintf(&b, "This archive was rebuilt offline. No OS connection or new source reconciliation occurred. The original read interval was %s through %s. Initial, inventoried, fetched, and final counts describe that source read; included/excluded/withheld/omitted counts include subsequent rebuild decisions. Original coverage and people statistics remain in the manifest's rebuild section.\n\n", info.OriginalReadStarted.UTC().Format(time.RFC3339), info.OriginalReadFinished.UTC().Format(time.RFC3339))
		if info.LegacyEvidence {
			b.WriteString("The source uses the legacy archive format without reconstruction evidence. Unknown original projections remain unknown, redacted counts can be unavailable (-1), and verified-user labels are replayed from existing user-profile navigation. This archive remains partial.\n\n")
		}
		fmt.Fprintf(&b, "Unavailable selected dependencies encountered during cache recovery: %d. Included annotation bodies withheld because a historical cached summary was unavailable: %d. Missing records are never recreated from cached text. Conservative withholding can remove additional content.\n\n", info.UnavailableTargets, info.BlockedCacheBodies)
	}
	fmt.Fprintf(&b, "Scope: **%s**.\n\n", md(scope.Name))
	fmt.Fprintf(&b, "Fully inventoried collections: %s.\n\nReferenced supporting collections only: %s.\n\nIntentionally skipped collections: %s.\n\n", strings.Join(scope.Inventoried, ", "), strings.Join(scope.ReferenceOnly, ", "), strings.Join(scope.Omitted, ", "))
	if len(scope.ReferenceOnly) > 0 {
		b.WriteString("Supporting collections are not enumerated or reconciled as complete inventories. Initial/final counts are unavailable; fetched/included counts cover only reached records.\n\n")
	}
	if len(scope.Omitted) > 0 {
		b.WriteString("Skipped relationships are intentionally unlinked. Without event history, some source, website, and person connections are unavailable. Secret scanning and URL rules still apply to exported content; they cannot identify a website origin present only in skipped activity. Strict derived-content filtering remains available in the privacy policy.\n\n")
	}
	keys := []string{}
	for typ := range scope.OmittedReferences {
		keys = append(keys, typ)
	}
	sort.Strings(keys)
	for _, typ := range keys {
		fmt.Fprintf(&b, "- %s: %d intentionally skipped reference occurrences (not a count of distinct records).\n", typ, scope.OmittedReferences[typ])
	}
	b.WriteByte('\n')
	b.WriteString("Counts describe the original HTTP source read and the retained output. This is not an atomic database backup. Exclusions and selection omissions are intentional; unknown relationship projections remain a coverage gap.\n\n## Material records\n\n| Material | Initial | Inventoried | Fetched | Included | Excluded | Withheld | Omitted | Final |\n| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
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
	if c := r.manifest.SDKCache; c.Selected > 0 {
		fmt.Fprintf(&b, "\n## Historical SDK-cache recovery\n\nExplicitly selected caches: %d. Rows read: %d; matching summary rows: %d; expired rows: %d; invalid/oversized rows: %d; creation-time mismatches: %d; invalid/future update times: %d.\n\nCandidate fields: %d; conflicting fields skipped: %d; unavailable target references skipped: %d. Edges added before privacy (including derived inverses): %d; retained historical edges after filtering/selection: %d.\n\nCache evidence is historical and may be stale. Current OS records were not replaced. Present OS collections, including explicit empties, take precedence. A newer valid cache field supersedes an older field, including empties and tombstones; equal-time conflicts are not merged. Absent cache fields do not prove deletion. Only exact current record IDs with matching summary creation times and non-future cache update times qualify. Missing targets produce no links. The graph JSONL records cache ordinal and summary update times for each retained historical edge; private cache paths and cached prose are not exported. This does not close the current relationship-projection gap.\n", c.Selected, c.Rows, c.Matching, c.Expired, c.Invalid, c.IdentityMismatch, c.UnusableTime, c.Fields, c.Conflicts, c.MissingTargets, c.AddedEdges, c.RetainedEdges)
	}
	return writeFile(filepath.Join(r.stage, "coverage.md"), []byte(b.String()))
}
