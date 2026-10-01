package exporter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type PersonFacts struct {
	Projected, UnknownConnections, UnknownSummaries bool
	SourceEventConnections                          int
	ID, Name, PlatformID, Email                     string
	Ghost, User, UnknownAnnotations                 bool
	Annotations, Personas, Profiles, Summaries      []string
	ProfileSummaries                                []string
	Connections                                     int
}
type PeopleStats struct {
	NameReviewGroups        int    `json:"shared_name_review_groups"`
	Total                   int    `json:"total_persons"`
	Personas                int    `json:"persons_with_personas"`
	Profiles                int    `json:"persons_with_profiles"`
	Summaries               int    `json:"persons_with_summaries"`
	Connected               int    `json:"persons_above_connection_threshold"`
	Account                 int    `json:"account_persons"`
	Unknown                 int    `json:"persons_with_incomplete_selection_evidence"`
	UnknownSummaries        int    `json:"persons_with_unknown_summary_connectivity"`
	UnknownEventConnections int    `json:"persons_with_unknown_event_connectivity"`
	Selected                int    `json:"selected_persons"`
	Omitted                 int    `json:"intentionally_omitted_persons"`
	PlatformGroups          int    `json:"shared_platform_identity_groups"`
	ReviewGroups            int    `json:"shared_email_review_groups"`
	Mode                    string `json:"mode"`
	MinConnections          int    `json:"min_connections"`
}

func object(v map[string]any, key string) map[string]any { m, _ := v[key].(map[string]any); return m }
func personFacts(v map[string]any) *PersonFacts {
	p := &PersonFacts{ID: fieldString(v, "id"), Annotations: references(v["annotations"]), Summaries: references(v["summaries"])}
	typ := object(v, "type")
	platform, basic := object(typ, "platform"), object(typ, "basic")
	p.PlatformID = fieldString(platform, "id")
	// A User ID and a platform identity are distinct identifiers. The caller can
	// set User only after /user/<user>/person has confirmed this Person record.
	p.Name = fieldString(basic, "name")
	if p.Name == "" {
		p.Name = fieldString(platform, "name")
	}
	if p.Name == "" {
		p.Name = fieldString(v, "name")
	}
	if p.Name == "" {
		p.Name = "Unnamed person"
	}
	p.Email = strings.ToLower(strings.TrimSpace(fieldString(basic, "email")))
	if p.Email == "" {
		p.Email = strings.ToLower(strings.TrimSpace(fieldString(platform, "email")))
	}
	p.Projected = object(v, "annotations") == nil && object(v, "summaries") == nil && object(v, "workstream_events") == nil
	p.UnknownSummaries = p.Projected
	p.Ghost, _ = v["ghost"].(bool)
	for _, field := range []string{"summaries", "workstream_events", "messages", "assets"} {
		p.Connections += len(references(v[field]))
	}
	return p
}
func classifyPerson(p *PersonFacts, types map[string]string) {
	p.Personas = nil
	p.Profiles = nil
	p.UnknownAnnotations = false
	for _, id := range unique(p.Annotations) {
		typ, ok := types[id]
		if !ok {
			p.UnknownAnnotations = true
			continue
		}
		switch typ {
		case "HIERARCHICAL_PROFILE_SUMMARY":
			p.Personas = append(p.Personas, id)
		case "PROFILE_DESCRIPTION":
			p.Profiles = append(p.Profiles, id)
		}
	}
}
func selectedPerson(p *PersonFacts, mode string, minConnections int) bool {
	// Unknown coverage is kept, never silently treated as proof of irrelevance.
	if mode == "all" || p.PlatformID != "" || p.User || p.UnknownAnnotations || len(p.Personas) > 0 || len(p.Profiles) > 0 {
		return true
	}
	return mode == "connected" && (p.UnknownConnections || p.UnknownSummaries || len(p.Summaries) > 0 || !p.Ghost && max(p.Connections, p.SourceEventConnections) >= minConnections)
}
func peopleStats(facts map[string]*PersonFacts, mode string, minConnections int) PeopleStats {
	s := PeopleStats{Mode: mode, MinConnections: minConnections}
	platform, email, names := map[string]int{}, map[string]int{}, map[string]int{}
	for _, p := range facts {
		s.Total++
		if len(p.Personas) > 0 {
			s.Personas++
		}
		if len(p.Profiles) > 0 {
			s.Profiles++
		}
		if len(p.Summaries) > 0 {
			s.Summaries++
		}
		if max(p.Connections, p.SourceEventConnections) >= minConnections {
			s.Connected++
		}
		if p.PlatformID != "" {
			s.Account++
		}
		if p.UnknownAnnotations || mode == "connected" && (p.UnknownConnections || p.UnknownSummaries) {
			s.Unknown++
		}
		if p.UnknownSummaries {
			s.UnknownSummaries++
		}
		if p.UnknownConnections {
			s.UnknownEventConnections++
		}
		if selectedPerson(p, mode, minConnections) {
			s.Selected++
		} else {
			s.Omitted++
		}
		if key := nameCandidate(p.Name); key != "" {
			names[key]++
		}
		if p.PlatformID != "" {
			platform[p.PlatformID]++
		}
		if strings.Contains(p.Email, "@") && !strings.ContainsAny(p.Email, "[] ") {
			email[p.Email]++
		}
	}
	for _, n := range platform {
		if n > 1 {
			s.PlatformGroups++
		}
	}
	for _, n := range email {
		if n > 1 {
			s.ReviewGroups++
		}
	}
	for _, n := range names {
		if n > 1 {
			s.NameReviewGroups++
		}
	}
	return s
}
func nameCandidate(name string) string {
	parts := strings.Fields(strings.ToLower(name))
	if len(parts) < 2 || name == "Unnamed person" {
		return ""
	}
	return strings.Join(parts, " ")
}
func (s PeopleStats) Print(w io.Writer) {
	fmt.Fprintf(w, "People: %d read | persona %d | profile %d | known summary links %d (%d unknown) | ≥%d known content connections %d | source event counts unavailable/not queried %d | account identities %d\n", s.Total, s.Personas, s.Profiles, s.Summaries, s.UnknownSummaries, s.MinConnections, s.Connected, s.UnknownEventConnections, s.Account)
	fmt.Fprintf(w, "People mode %s: retain %d, omit %d; incomplete selection evidence on %d people is retained conservatively.\n", s.Mode, s.Selected, s.Omitted, s.Unknown)
	fmt.Fprintf(w, "Identity grouping across evaluated people: %d shared platform-ID groups; %d shared-email groups and %d shared-name groups need review (no automatic email/name merges).\n", s.PlatformGroups, s.ReviewGroups, s.NameReviewGroups)
}

// PeopleReport uses person-side edges and referenced annotation types. It is a
// pre-filter planning view, not a full-graph or privacy-qualified selection.
func PeopleReport(ctx context.Context, c *Client, mode string, threshold int, w io.Writer) (PeopleStats, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	material, _ := materialByType("PERSONS")
	count, err := c.Count(ctx, material, Window{})
	if err != nil {
		return PeopleStats{}, err
	}
	if count > 20000 {
		return PeopleStats{}, errConfig("people preview is bounded to 20,000 persons; use an export for larger inventories")
	}
	ids, err := c.IDs(ctx, material, Window{})
	if err != nil {
		return PeopleStats{}, err
	}
	if len(ids) > 20000 {
		return PeopleStats{}, errConfig("people preview inventory grew beyond its bound")
	}
	facts := map[string]*PersonFacts{}
	annotationIDs := []string{}
	userPerson, err := currentUserID(ctx, c)
	if err == nil && userPerson != "" {
		userPerson, err = userPersonID(ctx, c, userPerson)
	}
	if err != nil {
		if errors.Is(err, ErrOSBusy) || ctx.Err() != nil {
			return PeopleStats{}, err
		}
		userPerson = ""
	}
	progress := startProgress(w, c)
	defer progress.Close()
	if progress != nil {
		w = progress.out
	}
	progress.Stage("Read people for preview", len(ids))
	for start := 0; start < len(ids); {
		end := min(start+c.BatchSize(material, 50), len(ids))
		records, e := FetchBatch(ctx, c, material, ids[start:end])
		if e != nil {
			return PeopleStats{}, e
		}
		if len(records) != end-start {
			return PeopleStats{}, errConfig("people preview has missing records; no complete selection estimate available")
		}
		for _, v := range records {
			p := personFacts(v)
			p.User = p.ID == userPerson && userPerson != ""
			facts[p.ID] = p
			annotationIDs = append(annotationIDs, p.Annotations...)
		}
		progress.Add(end - start)
		start = end
	}
	annotationIDs = unique(annotationIDs)
	// Preserve unknown annotations if the preview budget cannot read every type.
	if len(annotationIDs) > 10000 {
		annotationIDs = annotationIDs[:10000]
	}
	annotationMaterial, _ := materialByType("ANNOTATIONS")
	types := map[string]string{}
	progress.Stage("Read referenced annotation types", len(annotationIDs))
	for start := 0; start < len(annotationIDs); {
		end := min(start+c.BatchSize(annotationMaterial, 50), len(annotationIDs))
		records, e := FetchBatch(ctx, c, annotationMaterial, annotationIDs[start:end])
		if e != nil {
			return PeopleStats{}, e
		}
		for _, v := range records {
			types[fieldString(v, "id")] = fieldString(v, "type")
		}
		progress.Add(end - start)
		start = end
	}
	progress.Stage("Query persona/profile evidence", len(facts))
	ordered := []string{}
	for id := range facts {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		p := facts[id]
		classifyPerson(p, types)
		if p.Projected {
			records, e := personEvidence(ctx, c, p, false, mode != "profiles")
			if e != nil {
				return PeopleStats{}, e
			}
			unknown := p.UnknownAnnotations
			for _, v := range records {
				key := fieldString(v, "id")
				p.Annotations = append(p.Annotations, key)
				types[key] = fieldString(v, "type")
			}
			classifyPerson(p, types)
			p.UnknownAnnotations = p.UnknownAnnotations || unknown
		}
		progress.Add(1)
	}
	fmt.Fprintln(w, "Missing person-to-summary projections remain unknown. Connected mode retains those people; profiles mode can explicitly select only persona/profile/account evidence.")
	stats := peopleStats(facts, mode, threshold)
	fmt.Fprintln(w, "People preview uses person-side retained references before privacy filtering; reverse-only links are reconciled during export. Direct persona/profile queries supplement projected records; profiles mode skips source event-count queries. No person names or bodies are printed.")
	stats.Print(w)
	if mode != "profiles" {
		fmt.Fprintln(w, "Alternative explicit profile-only selection:")
		peopleStats(facts, "profiles", threshold).Print(w)
	}
	if len(ids) != count {
		return stats, errConfig("people inventory changed during preview")
	}
	return stats, nil
}

func (r *run) preparePeople() error {
	r.people = map[string]*PersonFacts{}
	types := map[string]string{}
	for _, m := range r.meta {
		if m.Type == "ANNOTATIONS" {
			if m.State == "included" {
				types[m.ID] = m.AnnotationType
			} else if m.State == "excluded" {
				types[m.ID] = ""
			}
		}
	}
	for _, m := range r.sortedMeta() {
		if m.Type != "PERSONS" || m.State != "included" {
			continue
		}
		v, err := r.readRecord(filepath.Join(r.stage, m.DataPath))
		if err != nil {
			return err
		}
		p := personFacts(v)
		p.User = r.userPersonIDs[m.ID]
		if m.JunctionFields["summaries"] {
			p.UnknownSummaries = false
		}
		if evidence := m.PersonEvidence; evidence != nil {
			p.UnknownConnections = evidence.UnknownConnections
			p.SourceEventConnections = evidence.SourceEventConnections
		} else if p.Projected {
			p.UnknownConnections = true
		}
		// Selection uses only included exported content, after privacy processing.
		p.Summaries = nil
		p.Connections = 0
		content := map[string]bool{}
		for _, e := range m.Edges {
			if typ, id, ok := splitRef(e.Target); ok && typ == "ANNOTATIONS" {
				p.Annotations = append(p.Annotations, id)
			}
			target := r.meta[e.Target]
			if target == nil || target.State != "included" {
				continue
			}
			switch target.Type {
			case "WORKSTREAM_SUMMARIES":
				p.Summaries = append(p.Summaries, target.ID)
				content[target.Key] = true
			case "WORKSTREAM_EVENTS", "CONVERSATION_MESSAGES", "ASSETS":
				content[target.Key] = true
			}
		}
		p.Connections = len(content)
		r.people[m.ID] = p
	}
	// Reconcile reverse-only links without walking the graph transitively.
	connections := map[string]map[string]bool{}
	for _, m := range r.meta {
		if m.State != "included" {
			continue
		}
		for _, e := range m.Edges {
			typ, id, ok := splitRef(e.Target)
			if !ok || typ != "PERSONS" {
				continue
			}
			p := r.people[id]
			if p == nil {
				continue
			}
			switch m.Type {
			case "ANNOTATIONS":
				p.Annotations = append(p.Annotations, m.ID)
			case "WORKSTREAM_SUMMARIES":
				if e.Relation == "persons" || e.Relation == "person" {
					p.Summaries = append(p.Summaries, m.ID)
				}
				fallthrough
			case "WORKSTREAM_EVENTS", "CONVERSATION_MESSAGES", "ASSETS":
				if connections[id] == nil {
					connections[id] = map[string]bool{}
				}
				connections[id][m.Key] = true
			}
		}
	}
	for id, p := range r.people {
		// Union outgoing and incoming IDs rather than double-counting backlinks.
		if connections[id] == nil {
			connections[id] = map[string]bool{}
		}
		for _, e := range r.meta["PERSONS\x00"+id].Edges {
			if target := r.meta[e.Target]; target != nil && target.State == "included" {
				switch target.Type {
				case "WORKSTREAM_SUMMARIES", "WORKSTREAM_EVENTS", "CONVERSATION_MESSAGES", "ASSETS":
					connections[id][target.Key] = true
				}
			}
		}
		p.Connections = len(connections[id])
		p.Summaries = unique(p.Summaries)
		classifyPerson(p, types)
		m := r.meta["PERSONS\x00"+id]
		for _, annotationID := range append(append([]string{}, p.Personas...), p.Profiles...) {
			annotation := r.meta["ANNOTATIONS\x00"+annotationID]
			if annotation == nil || annotation.State != "included" {
				continue
			}
			for _, e := range annotation.Edges {
				if target := r.meta[e.Target]; e.Relation == "summaries" && target != nil && target.State == "included" && target.Type == "WORKSTREAM_SUMMARIES" {
					p.ProfileSummaries = append(p.ProfileSummaries, target.ID)
				}
			}
		}
		p.ProfileSummaries = unique(p.ProfileSummaries)
		if p.Projected && !m.JunctionFields["annotations"] && (m.PersonEvidence == nil || m.PersonEvidence.UnknownAnnotations) {
			p.UnknownAnnotations = true
		}
	}
	r.manifest.People = peopleStats(r.people, r.opts.PeopleMode, r.opts.MinPersonConnections)
	if r.opts.Progress != nil {
		r.manifest.People.Print(r.opts.Progress)
	}
	for id, p := range r.people {
		if selectedPerson(p, r.opts.PeopleMode, r.opts.MinPersonConnections) {
			continue
		}
		m := r.meta["PERSONS\x00"+id]
		m.State = "omitted"
		if err := os.Remove(filepath.Join(r.stage, m.DataPath)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (r *run) renderPersonas() error {
	groups := r.personaGroups()
	review := map[string][]*PersonFacts{}
	for id, p := range r.people {
		m := r.meta["PERSONS\x00"+id]
		if m.State != "included" {
			continue
		}
		if strings.Contains(p.Email, "@") && !strings.ContainsAny(p.Email, "[] ") {
			review["email:"+p.Email] = append(review["email:"+p.Email], p)
		}
		if key := nameCandidate(p.Name); key != "" {
			review["name:"+key] = append(review["name:"+key], p)
		}
	}
	indexPath := workstreamRoot + "/personas/index.md"
	var index strings.Builder
	index.WriteString("# Personas\n\nPersonas and profile descriptions are retained as their own annotation types. `users/` contains only persons verified through `GET /user/<user>/person`; `related_persons/` contains every other included person. A matching name, email, or platform value never changes that category.\n\n")
	for _, category := range []struct {
		name, heading, intro string
		user                 bool
	}{{"users", "Verified user personas", "Each folder is backed by the explicit user-to-person mapping endpoint.", true}, {"related_persons", "Related persons", "These records are retained because of the selected people policy, not because they were inferred to be the current user.", false}} {
		keys := []string{}
		for key, people := range groups {
			if len(people) > 0 && people[0].User == category.user {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		categoryPath := workstreamRoot + "/personas/" + category.name + "/index.md"
		var categoryIndex strings.Builder
		fmt.Fprintf(&categoryIndex, "# %s\n\n%s\n\n[Personas index](%s)\n\n", category.heading, category.intro, relative(categoryPath, indexPath))
		for _, key := range keys {
			people := groups[key]
			sort.Slice(people, func(i, j int) bool { return people[i].ID < people[j].ID })
			folder := r.personaFolder(key, people)
			profilePath := folder + "/profile.md"
			var body strings.Builder
			fmt.Fprintf(&body, "# %s\n\n[Personas index](%s) · [Category index](%s) · [Profile summaries](profile_summaries/index.md) · [Related workstream summaries](related_workstream_summaries/index.md)\n\n## Identity records\n\n", md(personaDisplayName(people[0])), relative(profilePath, indexPath), relative(profilePath, categoryPath))
			if len(r.cachedEdges) > 0 {
				fmt.Fprintf(&body, "Historical cache links may contribute to the associations below. Text comes from retained OS annotations; a recent text date does not prove that a historical attachment is current. See [coverage](%s) and the [relationship provenance](%s) for source evidence.\n\n", relative(profilePath, "coverage.md"), relative(profilePath, "relationships.jsonl"))
			}
			persona, profile, summaries, profileSummaries := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
			for _, p := range people {
				m := r.meta["PERSONS\x00"+p.ID]
				fmt.Fprintf(&body, "- [%s](%s) — %d distinct included content connections\n", md(p.Name), relative(profilePath, m.Path), p.Connections)
				for _, id := range p.Personas {
					persona["ANNOTATIONS\x00"+id] = true
				}
				for _, id := range p.Profiles {
					profile["ANNOTATIONS\x00"+id] = true
				}
				for _, id := range p.Summaries {
					summaries["WORKSTREAM_SUMMARIES\x00"+id] = true
				}
				for _, id := range p.ProfileSummaries {
					profileSummaries["WORKSTREAM_SUMMARIES\x00"+id] = true
				}
			}
			for _, section := range []struct {
				title string
				keys  map[string]bool
			}{{"Persona summaries", persona}, {"Profile descriptions", profile}, {"Related summaries and hierarchies", summaries}, {"Workstream summaries linked to profile annotations", profileSummaries}} {
				fmt.Fprintf(&body, "\n## %s\n\n", section.title)
				items := []*Meta{}
				for key := range section.keys {
					if m := r.meta[key]; m != nil && m.State == "included" {
						items = append(items, m)
					}
				}
				sort.Slice(items, func(i, j int) bool { return newer(items[i], items[j]) })
				for i, m := range items {
					fmt.Fprintf(&body, "- [%s](%s) — %s", md(m.Title), relative(profilePath, m.Path), md(m.Created))
					if i == 0 {
						body.WriteString(" (newest retained)")
					}
					body.WriteByte('\n')
					if m.Type == "WORKSTREAM_SUMMARIES" {
						for _, e := range m.Edges {
							if e.Relation != "parents" && e.Relation != "children" {
								continue
							}
							if target := r.meta[e.Target]; target != nil && target.State == "included" {
								fmt.Fprintf(&body, "  - %s: [%s](%s)\n", e.Relation, md(target.Title), relative(profilePath, target.Path))
							}
						}
					}
				}
				if len(items) == 0 {
					body.WriteString("No included records.\n")
				}
				if len(items) > 0 && items[0].Type == "ANNOTATIONS" {
					v, err := r.readRecord(filepath.Join(r.stage, items[0].DataPath))
					if err != nil {
						return err
					}
					body.WriteString("\n### Latest retained text\n\n" + rewriteMarkdown(fieldString(v, "text"), profilePath, r.meta) + "\n")
				}
			}
			if err := r.writeFile(filepath.Join(r.stage, profilePath), []byte(body.String())); err != nil {
				return err
			}
			history, associated := []*Meta{}, []*Meta{}
			seen := map[string]bool{}
			for _, keys := range []map[string]bool{persona, profile} {
				for key := range keys {
					if m := r.meta[key]; m != nil && m.State == "included" && !seen[key] {
						history = append(history, m)
						seen[key] = true
					}
				}
			}
			for key := range profileSummaries {
				summaries[key] = true
			}
			for key := range summaries {
				if m := r.meta[key]; m != nil && m.State == "included" {
					associated = append(associated, m)
				}
			}
			if err := r.writeSummaryIndex(folder+"/profile_summaries/index.md", "Persona and profile summaries", "[Profile](../profile.md). All retained approved persona and profile versions, newest first. Shared annotations can live outside this folder and are linked here.", history); err != nil {
				return err
			}
			intro := "[Profile](../profile.md). Known direct person-to-summary associations and workstream summaries linked to this person's persona/profile annotations. The profile page distinguishes those paths. An association can indicate authorship, involvement, or profile context; it does not prove the whole summary describes this person. Each canonical summary links to its known parent/child hierarchy. Narrative mentions and co-occurrence are not treated as direct associations."
			for _, p := range people {
				if p.UnknownSummaries {
					intro += " This OS omits person-side summary relationships; this list has incomplete association coverage."
					break
				}
			}
			if err := r.writeSummaryIndex(folder+"/related_workstream_summaries/index.md", "Related workstream summaries", intro, associated); err != nil {
				return err
			}
			fmt.Fprintf(&categoryIndex, "- [%s](%s) — %d identity record(s)\n", md(personaDisplayName(people[0])), relative(categoryPath, profilePath), len(people))
		}
		if len(keys) == 0 {
			categoryIndex.WriteString("No included people in this category.\n")
		}
		if err := r.writeFile(filepath.Join(r.stage, categoryPath), []byte(categoryIndex.String())); err != nil {
			return err
		}
		fmt.Fprintf(&index, "## %s\n\n%s\n\n[%s](%s) — %d folder(s)\n\n", category.heading, category.intro, category.heading, relative(indexPath, categoryPath), len(keys))
	}
	index.WriteString("## Potential duplicates for review\n\nShared email or full display name is only a candidate signal, not evidence of identity. No records are merged by email, extracted aliases, or display name.\n\n")
	keys := []string{}
	for key, people := range review {
		if len(people) > 1 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for n, key := range keys {
		sort.Slice(review[key], func(i, j int) bool { return review[key][i].ID < review[key][j].ID })
		reason := "shared email"
		if strings.HasPrefix(key, "name:") {
			reason = "shared display name; low confidence"
		}
		fmt.Fprintf(&index, "Candidate group %d (%s):\n\n", n+1, reason)
		for _, p := range review[key] {
			m := r.meta["PERSONS\x00"+p.ID]
			fmt.Fprintf(&index, "- [%s](%s)\n", md(p.Name), relative(indexPath, m.Path))
		}
		index.WriteByte('\n')
	}
	return r.writeFile(filepath.Join(r.stage, indexPath), []byte(index.String()))
}
