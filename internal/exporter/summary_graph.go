package exporter

import (
	"context"
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
	Written    map[string]bool
	Metadata   map[string]*DocumentMetadata
	Keys       map[string]map[string]map[string]string // summary -> dimension -> key -> label
	Index      map[string]map[string][]*Meta
	chronology []summaryChronology
	ranks      map[string]int
}

var dimensions = []string{"Tags", "Source", "Person", "Website"}

func (r *run) buildSummaryGraph() (*summaryGraph, error) {
	g := &summaryGraph{Written: map[string]bool{}, Metadata: map[string]*DocumentMetadata{}, Keys: map[string]map[string]map[string]string{}, Index: map[string]map[string][]*Meta{}}
	for _, dimension := range dimensions {
		g.Index[dimension] = map[string][]*Meta{}
	}
	// Sort only summary identities. Supporting canonical records stay on disk;
	// keep extracted descriptions/hosts only when a summary needs them.
	summaries := []*Meta{}
	for _, m := range r.meta {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		if m.State == "included" && m.Type == "WORKSTREAM_SUMMARIES" {
			summaries = append(summaries, m)
		}
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Key < summaries[j].Key })
	descriptions, hosts := map[string]string{}, map[string]string{}
	r.progress.Stage("Build summary relationships", len(summaries))
	for _, m := range summaries {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		keys := map[string]map[string]string{}
		for _, d := range dimensions {
			keys[d] = map[string]string{}
		}
		v, err := r.readCanonical(m)
		if err != nil {
			return nil, err
		}
		description := fieldString(v, "description")
		for _, e := range m.Edges {
			if description != "" {
				break
			}
			a := r.meta[e.Target]
			if a != nil && a.State == "included" && a.Type == "ANNOTATIONS" && strings.Contains(a.AnnotationType, "DESCRIPTION") {
				text, loaded := descriptions[a.Key]
				if !loaded {
					value, err := r.readCanonical(a)
					if err != nil {
						return nil, err
					}
					text = fieldString(value, "text")
					descriptions[a.Key] = text
				}
				description = text
			}
		}
		visited := map[string]bool{}
		var visit func(*Meta, int) error
		visit = func(node *Meta, depth int) error {
			if err := r.ctx.Err(); err != nil {
				return err
			}
			if depth > 4 || node == nil || node.State != "included" || visited[node.Key] {
				return nil
			}
			visited[node.Key] = true
			switch node.Type {
			case "TAGS":
				keys["Tags"][node.Key] = node.Title
				return nil
			case "PERSONS":
				keys["Person"][node.Key] = node.Title
				return nil
			case "WORKSTREAM_PATTERN_ENGINE_SOURCES", "APPLICATIONS":
				keys["Source"][node.Key] = node.Title
				return nil
			case "WEBSITES":
				host, loaded := hosts[node.Key]
				if !loaded {
					website, err := r.readCanonical(node)
					if err != nil {
						return err
					}
					raw := fieldString(website, "url")
					if raw == "" {
						raw = fieldString(website, "name")
					}
					if !strings.Contains(raw, "://") {
						raw = "https://" + raw
					}
					if u, err := url.Parse(raw); err == nil {
						host, _ = normalizeHost(u.Hostname())
					}
					hosts[node.Key] = host
				}
				if host != "" {
					keys["Website"][host] = host
				}
				return nil
			}
			for _, e := range node.Edges {
				target := r.meta[e.Target]
				if target == nil {
					continue
				}
				switch e.Relation {
				case "tags", "persons", "person", "sources", "websites", "applications", "events", "workstream_events", "source_windows":
					if err := visit(target, depth+1); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := visit(m, 0); err != nil {
			return nil, err
		}
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
		r.progress.Add(1)
	}
	if err := g.prepareRanking(r.ctx); err != nil {
		return nil, err
	}
	for _, groups := range g.Index {
		for _, items := range groups {
			sort.Slice(items, func(i, j int) bool { return g.ranks[items[i].Key] < g.ranks[items[j].Key] })
		}
	}
	return g, nil
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
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	selection, err := g.selectRelated(ctx, m, r.opts.RelatedSince, order, limit)
	if err != nil {
		return "", err
	}
	for dimension, d := range dimensions {
		fmt.Fprintf(&b, "#### Related Summaries by %s\n\n", d)
		for _, match := range selection.lists[dimension] {
			other, mask := g.chronology[match.rank].meta, match.mask
			evidence := []string{}
			for key, label := range g.Keys[m.Key][d] {
				if _, shared := g.Keys[other.Key][d][key]; shared {
					evidence = append(evidence, label)
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
		if len(selection.lists[dimension]) == 0 {
			b.WriteString("No included related summaries match these list settings.\n")
		}
		if selection.totals[dimension] > limit || selection.omitted[dimension] {
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
					if err := r.writeFile(filepath.Join(r.stage, path), []byte(group.String())); err != nil {
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
