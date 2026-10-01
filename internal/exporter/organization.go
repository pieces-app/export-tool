package exporter

import (
	"fmt"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const workstreamRoot = "workstream_summaries"

func stableFileID(typ, id string) string {
	if uuidName.MatchString(id) {
		return id
	}
	return opaque(typ, id)
}

func (r *run) pipelineAssociationPath(m *Meta) string {
	name := "pipeline"
	if r.opts.Naming != "opaque" {
		name = safeTitle(m.Title, 48)
	}
	return workstreamRoot + "/pipeline_associations/" + name + "." + stableFileID(m.Type, m.ID) + ".md"
}

// Only explicit typed associations contribute to this secondary navigation.
// A descriptor classifies a summary; it does not invent a pipeline association.
func (r *run) pipelineMemberships() (map[string]*Meta, map[string][]string) {
	groups, members := map[string]*Meta{}, map[string][]string{}
	for key, m := range r.meta {
		if m.State == "included" && m.Type == "PIPELINES" {
			groups[key] = m
		}
	}
	for _, m := range r.meta {
		if m.State != "included" {
			continue
		}
		for _, e := range m.Edges {
			target := r.meta[e.Target]
			if target == nil || target.State != "included" {
				continue
			}
			if m.Type == "WORKSTREAM_SUMMARIES" && target.Type == "PIPELINES" && e.Relation == "pipelines" {
				members[m.Key] = append(members[m.Key], target.Key)
			}
			if m.Type == "PIPELINES" && target.Type == "WORKSTREAM_SUMMARIES" && e.Relation == "summaries" {
				members[target.Key] = append(members[target.Key], m.Key)
			}
		}
	}
	for key, pipelines := range members {
		members[key] = unique(pipelines)
	}
	return groups, members
}

func (r *run) personaGroups() map[string][]*PersonFacts {
	groups := map[string][]*PersonFacts{}
	for id, p := range r.people {
		if m := r.meta["PERSONS\x00"+id]; m == nil || m.State != "included" {
			continue
		}
		// A verified user-to-person mapping is the only way into users/. Other
		// explicit platform identities remain useful navigation groups, but never
		// become a user folder just because their fields look similar.
		key := "person:" + id
		if p.User {
			key = "user:" + id
		} else if p.PlatformID != "" {
			key = "platform:" + p.PlatformID
		}
		groups[key] = append(groups[key], p)
	}
	for _, people := range groups {
		sort.Slice(people, func(i, j int) bool { return people[i].ID < people[j].ID })
	}
	return groups
}

func personaDisplayName(p *PersonFacts) string {
	if p.Name != "" && p.Name != "Unnamed person" {
		return p.Name
	}
	if strings.Contains(p.Email, "@") && !strings.ContainsAny(p.Email, "[] ") {
		return p.Email
	}
	return "person"
}

func (r *run) personaFolder(key string, people []*PersonFacts) string {
	category := "related_persons"
	if len(people) > 0 && people[0].User {
		category = "users"
	}
	name := "person"
	if r.opts.Naming != "opaque" {
		name = safeTitle(personaDisplayName(people[0]), 48)
	}
	return workstreamRoot + "/personas/" + category + "/" + name + "." + opaque("PERSONA_GROUP", key)
}

// Histories with one included owner live in that person's folder. Shared
// history retains one canonical annotation file and every owner index links it.
func (r *run) assignPersonaHistoryPaths() {
	groups := r.personaGroups()
	owners := map[string][]string{}
	for key, people := range groups {
		for _, p := range people {
			for _, id := range append(append([]string{}, p.Personas...), p.Profiles...) {
				owners[id] = append(owners[id], key)
			}
		}
	}
	history := map[string][]*Meta{}
	for id, keys := range owners {
		keys = unique(keys)
		m := r.meta["ANNOTATIONS\x00"+id]
		if len(keys) == 1 && m != nil && m.State == "included" {
			history[keys[0]] = append(history[keys[0]], m)
		}
	}
	zone, _ := time.LoadLocation(r.opts.Timezone)
	for key, items := range history {
		sort.Slice(items, func(i, j int) bool { return newer(items[i], items[j]) })
		folder := r.personaFolder(key, groups[key]) + "/profile_summaries"
		for i, m := range items {
			name := opaque(m.Type, m.ID)
			if r.opts.Naming != "opaque" {
				date := "undated"
				if t, err := time.Parse(time.RFC3339Nano, m.Created); err == nil {
					date = t.In(zone).Format("2006-01-02")
				}
				name = fmt.Sprintf("%06d.%s.%s.%s", i, safeTitle(strings.ToLower(m.AnnotationType), 48), date, stableFileID(m.Type, m.ID))
			}
			m.Path = folder + "/" + name + ".md"
		}
	}
}

func (r *run) writeSummaryIndex(path, heading, intro string, items []*Meta) error {
	sort.Slice(items, func(i, j int) bool { return newer(items[i], items[j]) })
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\n[Export index](%s)\n\n", md(heading), intro, relative(path, "index.md"))
	entries := make([]navigationEntry, 0, len(items))
	for _, m := range items {
		entries = append(entries, navigationEntry{label: m.Title, path: m.Path, detail: m.Created})
	}
	if len(items) == 0 {
		b.WriteString("No included records in this folder. Missing association coverage is not evidence that no relationships existed.\n")
	}
	return r.writeNavigationIndex(path, b.String(), entries)
}

func (r *run) writeGroupedSummaryIndexes(indexPath, heading, intro string, groups map[string][]*Meta, labels map[string]string) error {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\n[All workstream summaries](%s)\n\n", md(heading), intro, relative(indexPath, workstreamRoot+"/index.md"))
	for _, folder := range keys {
		label := labels[folder]
		if label == "" {
			label = pathpkg.Base(folder)
		}
		if err := r.writeSummaryIndex(folder+"/index.md", label, "Canonical summaries in this group, newest created first. Global rank gaps preserve ordering across the complete workstream-summary archive.", groups[folder]); err != nil {
			return err
		}
		fmt.Fprintf(&b, "- [%s](%s) — %d summaries\n", md(label), relative(indexPath, folder+"/index.md"), len(groups[folder]))
	}
	if len(keys) == 0 {
		b.WriteString("No included summaries in this category.\n")
	}
	return r.writeFile(filepath.Join(r.stage, indexPath), []byte(b.String()))
}

func (r *run) renderPipelineAssociations(groups map[string]*Meta, memberships map[string][]string) error {
	byPipeline := map[string][]*Meta{}
	for summaryKey, pipelineKeys := range memberships {
		if summary := r.meta[summaryKey]; summary != nil && summary.State == "included" {
			for _, key := range pipelineKeys {
				byPipeline[key] = append(byPipeline[key], summary)
			}
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	path := workstreamRoot + "/pipeline_associations/index.md"
	var b strings.Builder
	b.WriteString("# Known pipeline associations\n\nThis is secondary navigation only. Canonical summary placement comes from `parentHierarchicalType` and `parentHierarchicalTypeDescriptor`, because that is the classification used by the OS client. This index contains only explicit pipeline associations exposed by the selected record graph; omitted projections are not interpreted as no association.\n\n")
	b.WriteString("[All workstream summaries](../index.md)\n\n")
	for _, key := range keys {
		pipeline := groups[key]
		pipelinePath := r.pipelineAssociationPath(pipeline)
		intro := fmt.Sprintf("[Pipeline record](%s). Known explicitly associated summaries, newest created first. A relationship can be absent from this list when the OS omits its association projection.", relative(pipelinePath, pipeline.Path))
		if err := r.writeSummaryIndex(pipelinePath, pipeline.Title, intro, byPipeline[key]); err != nil {
			return err
		}
		fmt.Fprintf(&b, "- [%s](%s) — %d known summaries\n", md(pipeline.Title), relative(path, pipelinePath), len(byPipeline[key]))
	}
	if len(keys) == 0 {
		b.WriteString("No included pipeline records.\n")
	}
	return r.writeFile(filepath.Join(r.stage, path), []byte(b.String()))
}

func (r *run) renderOrganization() error {
	groups, memberships := r.pipelineMemberships()
	unknown := map[string]int{}
	for _, m := range r.meta {
		if m.State == "included" && m.RelationshipProjectionUnknown {
			unknown[m.Type]++
		}
	}
	if n := unknown["WORKSTREAM_SUMMARIES"]; n > 0 {
		message := fmt.Sprintf("WORKSTREAM_SUMMARIES: %d retained snapshots omit one or more direct relationship fields. Summary annotation bodies and person/pipeline membership may be incomplete; omitted fields are not evidence of no relationship.", n)
		if r.manifest.SummaryHierarchy.InventoriesMatched {
			message += " The parent/child endpoint traversal matched its returned inventories; this does not verify internal database completeness."
		}
		r.manifest.Warnings = append(r.manifest.Warnings, message)
	}
	if n := unknown["PIPELINES"]; n > 0 {
		r.manifest.Warnings = append(r.manifest.Warnings, fmt.Sprintf("PIPELINES: %d retained snapshots omit direct summary relationships. Pipeline association navigation may be incomplete; omitted fields are not evidence of no relationship.", n))
	}

	all, timeline := []*Meta{}, []*Meta{}
	single, hierarchical := map[string][]*Meta{}, map[string][]*Meta{}
	singleLabels, hierarchyLabels := map[string]string{}, map[string]string{}
	for _, m := range r.meta {
		if m.State != "included" || m.Type != "WORKSTREAM_SUMMARIES" {
			continue
		}
		all = append(all, m)
		folder := pathpkg.Dir(m.Path)
		switch {
		case strings.HasPrefix(m.Path, workstreamRoot+"/timeline/"):
			timeline = append(timeline, m)
		case strings.HasPrefix(m.Path, workstreamRoot+"/single_click_summaries/"):
			single[folder] = append(single[folder], m)
			singleLabels[folder] = r.summaryDescriptorLabel(m)
		case strings.HasPrefix(m.Path, workstreamRoot+"/hierarchical_summaries/"):
			hierarchical[folder] = append(hierarchical[folder], m)
			hierarchyLabels[folder] = strings.ReplaceAll(strings.ToLower(m.SummaryKind), "_", " ")
		}
	}
	if err := r.writeSummaryIndex(workstreamRoot+"/index.md", "All workstream summaries", "Every included workstream summary, newest created first. Rank is global across timeline, single-click, and hierarchical folders; gaps within a folder are expected.", all); err != nil {
		return err
	}
	if err := r.writeSummaryIndex(workstreamRoot+"/timeline/index.md", "Workstream summary timeline", "Temporal, legacy, and unknown workstream summaries. Their physical location expresses creation-oriented chronology; the summary record retains its exact hierarchy enum.", timeline); err != nil {
		return err
	}
	if err := r.writeGroupedSummaryIndexes(workstreamRoot+"/single_click_summaries/index.md", "Single-click summaries", "Folders use the OS summary descriptor, not a title heuristic. Familiar descriptors use stable names such as daily_standups; custom descriptors receive a deterministic suffix.", single, singleLabels); err != nil {
		return err
	}
	if err := r.writeGroupedSummaryIndexes(workstreamRoot+"/hierarchical_summaries/index.md", "Hierarchical summaries", "Non-temporal hierarchy types such as deep study, query-driven, generic, and conversational summaries.", hierarchical, hierarchyLabels); err != nil {
		return err
	}
	return r.renderPipelineAssociations(groups, memberships)
}
