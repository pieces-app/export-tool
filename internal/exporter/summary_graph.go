package exporter

import (
	"fmt"
	"math/bits"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type DocumentMetadata struct {
	ID           string   `json:"id"`
	Path         string   `json:"document_path"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Tags         []string `json:"tags"`
	Sources      []string `json:"sources"`
	Persons      []string `json:"persons"`
	Websites     []string `json:"websites"`
	NativeTags   []string `json:"native_tags"`
	NativeStatus string   `json:"native_status"`
	DateBasis    string   `json:"date_basis"`
}
type summaryGraph struct {
	Written  map[string]bool
	Metadata map[string]*DocumentMetadata
	Keys     map[string]map[string]map[string]string // summary -> dimension -> key -> label
	Index    map[string]map[string][]*Meta
}

var dimensions = []string{"Tags", "Source", "Person", "Website"}

func (r *run) buildSummaryGraph() (*summaryGraph, error) {
	g := &summaryGraph{Written: map[string]bool{}, Metadata: map[string]*DocumentMetadata{}, Keys: map[string]map[string]map[string]string{}, Index: map[string]map[string][]*Meta{}}
	for _, dimension := range dimensions {
		g.Index[dimension] = map[string][]*Meta{}
	}
	// Only these nodes provide labels/URL/description. Large event bodies stay on disk.
	values := map[string]map[string]any{}
	for _, m := range r.sortedMeta() {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		if m.State != "included" {
			continue
		}
		switch m.Type {
		case "WORKSTREAM_SUMMARIES", "ANNOTATIONS", "WEBSITES", "WORKSTREAM_PATTERN_ENGINE_SOURCES":
			v, err := readRecord(filepath.Join(r.stage, m.DataPath))
			if err != nil {
				return nil, err
			}
			values[m.Key] = v
		}
	}
	for _, m := range r.sortedMeta() {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		if m.State != "included" || m.Type != "WORKSTREAM_SUMMARIES" {
			continue
		}
		keys := map[string]map[string]string{}
		for _, d := range dimensions {
			keys[d] = map[string]string{}
		}
		v := values[m.Key]
		description := fieldString(v, "description")
		for _, e := range m.Edges {
			a := r.meta[e.Target]
			if a != nil && a.State == "included" && a.Type == "ANNOTATIONS" && strings.Contains(a.AnnotationType, "DESCRIPTION") {
				if description == "" {
					description = fieldString(values[a.Key], "text")
				}
			}
		}
		visited := map[string]bool{}
		var visit func(*Meta, int)
		visit = func(node *Meta, depth int) {
			if depth > 4 || node == nil || node.State != "included" || visited[node.Key] {
				return
			}
			visited[node.Key] = true
			switch node.Type {
			case "TAGS":
				keys["Tags"][node.Key] = node.Title
				return
			case "PERSONS":
				keys["Person"][node.Key] = node.Title
				return
			case "WORKSTREAM_PATTERN_ENGINE_SOURCES", "APPLICATIONS":
				keys["Source"][node.Key] = node.Title
				return
			case "WEBSITES":
				website := values[node.Key]
				raw := fieldString(website, "url")
				if raw == "" {
					raw = fieldString(website, "name")
				}
				if !strings.Contains(raw, "://") {
					raw = "https://" + raw
				}
				u, err := url.Parse(raw)
				if err == nil {
					if host, err := normalizeHost(u.Hostname()); err == nil {
						keys["Website"][host] = host
					}
				}
				return
			}
			for _, e := range node.Edges {
				target := r.meta[e.Target]
				if target == nil {
					continue
				}
				switch e.Relation {
				case "tags", "persons", "person", "sources", "websites", "applications", "events", "workstream_events", "source_windows":
					visit(target, depth+1)
				}
			}
		}
		visit(m, 0)
		meta := &DocumentMetadata{ID: m.ID, Path: m.Path, Title: m.Title, Description: description, Tags: []string{}, Sources: []string{}, Persons: []string{}, Websites: []string{}, NativeTags: []string{}, NativeStatus: "off", DateBasis: "record_created"}
		for _, d := range dimensions {
			labels := []string{}
			for key, label := range keys[d] {
				labels = append(labels, label)
				g.Index[d][key] = append(g.Index[d][key], m)
			}
			labels = unique(labels)
			switch d {
			case "Tags":
				meta.Tags = labels
				meta.NativeTags = append(meta.NativeTags, labels...)
			case "Source":
				meta.Sources = labels
				for _, label := range labels {
					meta.NativeTags = append(meta.NativeTags, "source:"+strings.ToLower(safeTitle(label, 80)))
				}
			case "Person":
				meta.Persons = labels
			case "Website":
				meta.Websites = labels
				for _, label := range labels {
					meta.NativeTags = append(meta.NativeTags, "website:"+label)
				}
			}
		}
		meta.NativeTags = unique(meta.NativeTags)
		g.Metadata[m.Key] = meta
		g.Keys[m.Key] = keys
	}
	for _, groups := range g.Index {
		for _, items := range groups {
			sort.Slice(items, func(i, j int) bool { return newer(items[i], items[j]) })
		}
	}
	return g, nil
}

// Score counts distinct shared dimensions, never the number of matching tags.
// Evaluate the complete candidate union before limiting the visible list.
func (g *summaryGraph) candidates(m *Meta, since time.Time, order string) ([]*Meta, map[string]uint8, map[string]bool) {
	masks := map[string]uint8{}
	candidates := map[string]*Meta{}
	omitted := map[string]bool{}
	for dimension, d := range dimensions {
		for key := range g.Keys[m.Key][d] {
			for _, other := range g.Index[d][key] {
				if other.Key == m.Key {
					continue
				}
				if !since.IsZero() {
					created, err := time.Parse(time.RFC3339Nano, other.Created)
					if err != nil || created.Before(since) {
						omitted[d] = true
						continue
					}
				}
				masks[other.Key] |= 1 << dimension
				candidates[other.Key] = other
			}
		}
	}
	ordered := make([]*Meta, 0, len(candidates))
	for _, candidate := range candidates {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if order != "recent" {
			a, b := bits.OnesCount8(masks[ordered[i].Key]), bits.OnesCount8(masks[ordered[j].Key])
			if a != b {
				return a > b
			}
		}
		return newer(ordered[i], ordered[j])
	})
	return ordered, masks, omitted
}

func (g *summaryGraph) render(r *run, m *Meta, from string) (string, error) {
	var b strings.Builder
	order, limit := r.opts.RelatedOrder, r.opts.RelatedLimit
	if order == "" {
		order = "relevance"
	}
	if limit == 0 {
		limit = 50
	}
	b.WriteString("\n## Related summaries\n\nThese links are derived from shared included graph relationships.\n\n")
	if order == "relevance" {
		b.WriteString("Ranked by distinct shared dimensions (Tags, Source, Person, Website), then newest timestamp and ID. Multiple shared tags count as one dimension.\n\n")
	} else {
		b.WriteString("Ranked by newest timestamp, then ID.\n\n")
	}
	fmt.Fprintf(&b, "Up to %d matches per section. ", limit)
	if !r.opts.RelatedSince.IsZero() {
		fmt.Fprintf(&b, "Related-list cutoff: %s (record creation). Undated matches are omitted from these lists. ", r.opts.RelatedSince.UTC().Format(time.RFC3339Nano))
	}
	b.WriteString("Complete shared-group indexes retain older and overflow matches.\n\n")
	ordered, masks, omitted := g.candidates(m, r.opts.RelatedSince, order)
	for dimension, d := range dimensions {
		fmt.Fprintf(&b, "#### Related Summaries by %s\n\n", d)
		shown, total := 0, 0
		for _, other := range ordered {
			mask := masks[other.Key]
			if mask&(1<<dimension) == 0 {
				continue
			}
			total++
			if shown >= limit {
				continue
			}
			shown++
			evidence := []string{}
			for key, label := range g.Keys[m.Key][d] {
				// Membership via the inverted index also supports legacy fixtures.
				for _, member := range g.Index[d][key] {
					if member.Key == other.Key {
						evidence = append(evidence, label)
						break
					}
				}
			}
			shared := []string{}
			for i, name := range dimensions {
				if mask&(1<<i) != 0 {
					shared = append(shared, name)
				}
			}
			date := other.Created
			if date == "" {
				date = "undated"
			}
			fmt.Fprintf(&b, "- [%s](%s) — shared: %s; %d/4 dimensions (%s); %s\n", md(other.Title), relative(from, other.Path), md(strings.Join(unique(evidence), ", ")), bits.OnesCount8(mask), strings.Join(shared, ", "), md(date))
		}
		if shown == 0 {
			b.WriteString("No included related summaries match these list settings.\n")
		}
		if total > limit || omitted[d] {
			b.WriteString("\nBrowse complete shared-group indexes (all exported dates; includes this summary):\n\n")
			groupKeys := []string{}
			for key := range g.Keys[m.Key][d] {
				groupKeys = append(groupKeys, key)
			}
			sort.Strings(groupKeys)
			for _, key := range groupKeys {
				members := g.Index[d][key]
				if len(members) < 2 {
					continue
				}
				path := "markdown/relationships/" + strings.ToLower(d) + "/" + opaque(d, key) + ".md"
				label := g.Keys[m.Key][d][key]
				if !g.Written[path] {
					var group strings.Builder
					fmt.Fprintf(&group, "# Summaries sharing %s: %s\n\nAll %d included summaries, newest first. Related-list cutoffs and limits do not apply here.\n\n", d, md(label), len(members))
					for _, member := range members {
						fmt.Fprintf(&group, "- [%s](%s)\n", md(member.Title), relative(path, member.Path))
					}
					if err := writeFile(filepath.Join(r.stage, path), []byte(group.String())); err != nil {
						return "", err
					}
					g.Written[path] = true
				}
				fmt.Fprintf(&b, "- [%s - all %d summaries](%s)\n", md(label), len(members), relative(from, path))
			}
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}
func metadataMarkdown(meta *DocumentMetadata) string {
	var b strings.Builder
	b.WriteString("## Summary metadata\n\n")
	if meta.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n\n", md(meta.Description))
	}
	for _, item := range []struct {
		label  string
		values []string
	}{{"Tags", meta.Tags}, {"Sources", meta.Sources}, {"Persons", meta.Persons}, {"Websites", meta.Websites}, {"Normalized file tags", meta.NativeTags}} {
		fmt.Fprintf(&b, "%s: %s\n\n", item.label, md(strings.Join(item.values, ", ")))
	}
	return b.String()
}
