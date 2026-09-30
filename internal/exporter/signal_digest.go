package exporter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SignalDigestOptions struct {
	Mode           string `json:"mode"`
	RecordsPerPart int    `json:"records_per_part"`
	MaxPartMiB     int    `json:"max_part_mib"`
}

func DefaultSignalDigestOptions() SignalDigestOptions { return SignalDigestOptions{"split", 1000, 8} }

func (o SignalDigestOptions) defaults() SignalDigestOptions {
	d := DefaultSignalDigestOptions()
	if o.Mode == "" {
		o.Mode = d.Mode
	}
	if o.RecordsPerPart == 0 {
		o.RecordsPerPart = d.RecordsPerPart
	}
	if o.MaxPartMiB == 0 {
		o.MaxPartMiB = d.MaxPartMiB
	}
	return o
}

func (o SignalDigestOptions) Validate() error {
	if o.Mode != "" && o.Mode != "split" && o.Mode != "single" && o.Mode != "off" {
		return errConfig("signals-digest must be split, single, or off")
	}
	if o.RecordsPerPart < 1 || o.RecordsPerPart > 10000 || o.MaxPartMiB < 1 || o.MaxPartMiB > 128 {
		return errConfig("signals-per-part must be 1–10000 and signals-max-part-mib must be 1–128")
	}
	return nil
}

type SignalDigestPart struct {
	Path      string `json:"path"`
	Entries   int    `json:"entries"`
	Bytes     int    `json:"bytes"`
	FirstRank int    `json:"first_rank"`
	LastRank  int    `json:"last_rank"`
}

type SignalDigestCoverage struct {
	Options                SignalDigestOptions `json:"options"`
	Selected               bool                `json:"signals_selected"`
	Index                  string              `json:"index,omitempty"`
	Included               int                 `json:"included_signals"`
	Entries                int                 `json:"digest_entries"`
	WithDescription        int                 `json:"signals_with_description"`
	WithoutDescription     int                 `json:"signals_without_description"`
	MultipleDescriptions   int                 `json:"signals_with_multiple_descriptions"`
	DescriptionAttachments int                 `json:"description_attachments"`
	UnknownProjections     int                 `json:"signals_with_unknown_projections"`
	UnavailableReferences  int                 `json:"unavailable_reference_occurrences"`
	Undated                int                 `json:"undated_signals"`
	TotalBytes             int64               `json:"document_bytes"`
	Parts                  []SignalDigestPart  `json:"parts"`
}

type signalDigestEntry struct {
	meta    *Meta
	created time.Time
	dated   bool
	hash    [32]byte
	size    int
}

type signalEntryCoverage struct {
	descriptions, unavailable int
	unknown                   bool
}

// Each entry and output document has a byte budget. This is not a whole-run
// memory ceiling: the export graph and bounded decoded records also use memory.
type signalTextBuffer struct {
	bytes.Buffer
	ctx   context.Context
	limit int
	err   error
}

func signalDigestLimitError() error {
	return errConfig("signals digest exceeds --signals-max-part-mib; use --signals-digest split, raise the size limit, or choose off to retain canonical signal files only. No archive was finalized; retry with a new output folder")
}

func (b *signalTextBuffer) Write(p []byte) (int, error) {
	if b.err == nil {
		b.err = b.ctx.Err()
	}
	if b.err == nil && len(p) > b.limit-b.Len() {
		b.err = signalDigestLimitError()
	}
	if b.err != nil {
		return 0, b.err
	}
	return b.Buffer.Write(p)
}
func (b *signalTextBuffer) WriteString(s string) (int, error) { return b.Write([]byte(s)) }

func (r *run) readSignalRecord(root *os.Root, m *Meta) (map[string]any, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	data, err := archiveRead(root, m.DataPath, 128<<20)
	if err != nil {
		return nil, err
	}
	var v map[string]any
	if err := decodeArchiveJSON(data, &v); err != nil {
		return nil, errConfig("signal digest record could not be decoded")
	}
	if fieldString(v, "id") != m.ID {
		return nil, errConfig("signal digest record identity changed")
	}
	return v, r.ctx.Err()
}

func (r *run) signalDigestEntry(root *os.Root, m *Meta, rank int, path string, limit int) ([]byte, signalEntryCoverage, error) {
	cov := signalEntryCoverage{}
	v, err := r.readSignalRecord(root, m)
	if err != nil {
		return nil, cov, err
	}
	b := &signalTextBuffer{ctx: r.ctx, limit: limit}
	name := fieldString(v, "name")
	if name == "" {
		name = m.Title
	}
	if len(name)+len(m.ID)+len(fieldString(v, "origin"))+len(fieldString(v, "category"))+len(m.Created)+len(m.Updated) > limit {
		return nil, cov, signalDigestLimitError()
	}
	fmt.Fprintf(b, "## %06d. %s\n\n[Canonical signal](%s)\n\nID: %s\n\nOrigin: %s\n\nCategory: %s\n\nCreated: %s\n\nUpdated: %s\n\n", rank, md(name), relative(path, m.Path), md(m.ID), md(fieldString(v, "origin")), md(fieldString(v, "category")), md(m.Created), md(m.Updated))
	if b.err != nil {
		return nil, cov, b.err
	}
	unknown := []string{}
	for _, field := range projectionFields("SIGNALS") {
		if state := m.ProjectionStates[field]; state != "linked" && state != "empty" {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) > 0 {
		cov.unknown = true
		fmt.Fprintf(b, "Unknown or malformed relationship projections: %s. This is not evidence that these relationships are empty.\n\n", strings.Join(unknown, ", "))
	}
	targets := map[string][]*Meta{}
	seen := map[Edge]bool{}
	for _, e := range m.Edges {
		if err := r.ctx.Err(); err != nil {
			return nil, cov, err
		}
		if seen[e] {
			continue
		}
		seen[e] = true
		target := r.meta[e.Target]
		if target == nil || target.State != "included" || target.Path == "" {
			cov.unavailable++
			continue
		}
		targets[e.Relation] = append(targets[e.Relation], target)
	}
	for relation := range targets {
		sort.Slice(targets[relation], func(i, j int) bool {
			a, b := targets[relation][i], targets[relation][j]
			at, ae := time.Parse(time.RFC3339Nano, a.Created)
			bt, be := time.Parse(time.RFC3339Nano, b.Created)
			if (ae == nil) != (be == nil) {
				return ae == nil
			}
			if ae == nil && !at.Equal(bt) {
				return at.After(bt)
			}
			return a.Key < b.Key
		})
	}
	b.WriteString("### Retained descriptions\n\n")
	for _, a := range targets["annotations"] {
		if a.Type != "ANNOTATIONS" {
			continue
		}
		av, err := r.readSignalRecord(root, a)
		if err != nil {
			return nil, cov, err
		}
		text := fieldString(av, "text")
		if fieldString(av, "type") != "SIGNAL_DESCRIPTION" || strings.TrimSpace(text) == "" {
			continue
		}
		cov.descriptions++
		provenance := "explicit signal attachment"
		if r.derivedEdges[Edge{m.Key, a.Key, "annotations"}] {
			provenance = "derived inverse of annotation.signals"
		}
		fmt.Fprintf(b, "[Description annotation](%s); created %s; updated %s; %s.\n\n", relative(path, a.Path), md(a.Created), md(a.Updated), provenance)
		// Do not parse a huge body after the document budget has already failed.
		if b.err != nil {
			return nil, cov, b.err
		}
		if len(text) > b.limit-b.Len() {
			return nil, cov, signalDigestLimitError()
		}
		b.WriteString(rewriteMarkdown(text, path, r.meta))
		b.WriteString("\n\n")
		if b.err != nil {
			return nil, cov, b.err
		}
	}
	if cov.descriptions == 0 {
		b.WriteString("No retained SIGNAL_DESCRIPTION text could be attached. Missing projections, omitted/filtered targets, and an explicitly empty attachment list are different cases; consult the coverage report and canonical record.\n\n")
	}
	if cov.descriptions > 1 {
		b.WriteString("Multiple descriptions are retained in newest-created order. No authoritative current description was selected.\n\n")
	}
	b.WriteString("### Occurrence ranges\n\n")
	for _, a := range targets["ranges"] {
		if a.Type != "RANGES" {
			continue
		}
		av, err := r.readSignalRecord(root, a)
		if err != nil {
			return nil, cov, err
		}
		between := "unspecified or invalid"
		if v, ok := av["between"].(bool); ok {
			between = fmt.Sprint(v)
		}
		fmt.Fprintf(b, "- [Range](%s): from %s to %s; between: %s.\n", relative(path, a.Path), md(timestamp(av, "from")), md(timestamp(av, "to")), between)
		if b.err != nil {
			return nil, cov, b.err
		}
	}
	if len(targets["ranges"]) == 0 {
		b.WriteString("No included occurrence range could be attached. Creation time is not a substitute for occurrence time.\n")
	}
	b.WriteString("\n### Related records\n\n")
	for _, relation := range []string{"annotations", "persons", "pipelines", "summaries", "workstream_events", "websites", "ranges"} {
		for _, a := range targets[relation] {
			provenance := ""
			if r.derivedEdges[Edge{m.Key, a.Key, relation}] {
				provenance = " (derived inverse)"
			}
			fmt.Fprintf(b, "- %s%s: [%s](%s)\n", relation, provenance, md(a.Title), relative(path, a.Path))
			if b.err != nil {
				return nil, cov, b.err
			}
		}
	}
	if cov.unavailable > 0 {
		fmt.Fprintf(b, "\n%d unavailable relationship target(s) have no local link.\n", cov.unavailable)
	}
	b.WriteString("\n---\n\n")
	if b.err != nil {
		return nil, cov, b.err
	}
	return b.Bytes(), cov, r.ctx.Err()
}

func (r *run) renderSignalDigest() error {
	o := r.opts.SignalDigest.defaults()
	if err := o.Validate(); err != nil {
		return err
	}
	stats := &SignalDigestCoverage{Options: o, Parts: []SignalDigestPart{}}
	r.manifest.SignalDigest = stats
	cov := r.coverage["SIGNALS"]
	stats.Selected = cov != nil
	if cov == nil {
		return nil
	}
	if o.Mode == "off" {
		stats.Included = cov.Included
		return nil
	}
	entries := []signalDigestEntry{}
	for _, m := range r.meta {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if m.Type == "SIGNALS" && m.State == "included" {
			t, err := time.Parse(time.RFC3339Nano, m.Created)
			entries = append(entries, signalDigestEntry{meta: m, created: t, dated: err == nil})
		}
	}
	stats.Included = len(entries)
	if cov.Included != len(entries) {
		return errConfig("signal digest count does not match included inventory")
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.dated != b.dated {
			return a.dated
		}
		if !a.created.Equal(b.created) {
			return a.created.After(b.created)
		}
		return a.meta.ID < b.meta.ID
	})
	root, err := os.OpenRoot(r.stage)
	if err != nil {
		return err
	}
	defer root.Close()
	limit := o.MaxPartMiB << 20
	// Reserve space for document headings and previous/next navigation.
	budget := limit - (16 << 10)
	entryPath := "signals/parts/part.md"
	if o.Mode == "single" {
		entryPath = "signals/all-signals.md"
	}
	r.progress.Stage("Plan signals digest", len(entries))
	for i := range entries {
		data, c, err := r.signalDigestEntry(root, entries[i].meta, i, entryPath, budget)
		if err != nil {
			return err
		}
		entries[i].size = len(data)
		entries[i].hash = sha256.Sum256(data)
		stats.DescriptionAttachments += c.descriptions
		stats.UnavailableReferences += c.unavailable
		if c.descriptions > 0 {
			stats.WithDescription++
		} else {
			stats.WithoutDescription++
		}
		if c.descriptions > 1 {
			stats.MultipleDescriptions++
		}
		if c.unknown {
			stats.UnknownProjections++
		}
		if !entries[i].dated {
			stats.Undated++
		}
		r.progress.Add(1)
	}
	type partPlan struct {
		first, last int
		path        string
		size        int
	}
	plans := []partPlan{}
	zone, err := time.LoadLocation(r.opts.Timezone)
	if err != nil {
		return errConfig("invalid digest timezone")
	}
	date := func(e signalDigestEntry) string {
		if !e.dated {
			return "undated"
		}
		return e.created.In(zone).Format("2006-01-02")
	}
	for i, e := range entries {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if len(plans) == 0 || o.Mode == "split" && (plans[len(plans)-1].last-plans[len(plans)-1].first+1 >= o.RecordsPerPart || plans[len(plans)-1].size+e.size > budget) {
			plans = append(plans, partPlan{first: i, last: i - 1})
		}
		p := &plans[len(plans)-1]
		p.last = i
		p.size += e.size
		if p.size > budget {
			return signalDigestLimitError()
		}
	}
	for i := range plans {
		p := &plans[i]
		p.path = fmt.Sprintf("signals/parts/%06d.signals.%s.%s.md", i, date(entries[p.first]), date(entries[p.last]))
		if o.Mode == "single" {
			p.path = "signals/all-signals.md"
		}
	}
	stats.Index = "signals/index.md"
	if r.opts.Progress != nil {
		total := int64(0)
		for _, p := range plans {
			total += int64(p.size)
		}
		fmt.Fprintf(r.opts.Progress, "Signals digest plan: %d entries, %d documents, %d bytes of approved entries before document headers.\n", len(entries), len(plans), total)
	}
	// Empty selections get an index explaining zero entries, not a dangling
	// link to a nonexistent part/all-signals document.
	r.progress.Stage("Write signals digest", len(entries))
	for i, p := range plans {
		b := &signalTextBuffer{ctx: r.ctx, limit: limit}
		fmt.Fprintf(b, "# Signals %06d-%06d\n\n[Signals index](%s) · [Export coverage](%s)\n\nNewest-created first; occurrence ranges are reported separately. All approved description attachments are retained, not a chosen current version.\n\n", p.first, p.last, relative(p.path, stats.Index), relative(p.path, "coverage.md"))
		for j := p.first; j <= p.last; j++ {
			data, _, err := r.signalDigestEntry(root, entries[j].meta, j, p.path, budget)
			if err != nil {
				return err
			}
			if sha256.Sum256(data) != entries[j].hash {
				return errConfig("staged signal digest content changed after planning")
			}
			b.Write(data)
			if b.err != nil {
				return b.err
			}
		}
		if i > 0 {
			fmt.Fprintf(b, "[Previous part](%s)\n\n", relative(p.path, plans[i-1].path))
		}
		if i+1 < len(plans) {
			fmt.Fprintf(b, "[Next part](%s)\n\n", relative(p.path, plans[i+1].path))
		}
		if b.err != nil {
			return b.err
		}
		if err := writeFile(filepath.Join(r.stage, p.path), b.Bytes()); err != nil {
			return err
		}
		stats.Entries += p.last - p.first + 1
		r.progress.Add(p.last - p.first + 1)
		stats.TotalBytes += int64(b.Len())
		stats.Parts = append(stats.Parts, SignalDigestPart{Path: p.path, Entries: p.last - p.first + 1, Bytes: b.Len(), FirstRank: p.first, LastRank: p.last})
		r.documentMetadata[p.path] = &DocumentMetadata{Title: fmt.Sprintf("Signals %06d-%06d", p.first, p.last), Description: "Approved signal records and their available description attachments.", NativeTags: []string{"signals"}, DateBasis: "record_created"}
	}
	if stats.Entries != stats.Included {
		return errConfig("signal digest did not represent every included signal")
	}
	b := &signalTextBuffer{ctx: r.ctx, limit: limit}
	fmt.Fprintf(b, "# Signals\n\n[Export index](../index.md) · [Full coverage](../coverage.md)\n\n%d of %d included signals represented; %d undated. Ordering uses creation time descending, then source ID ascending; missing/invalid creation times follow dated records.\n\nDescriptions: %d signals with retained text; %d without attached text; %d with multiple descriptions; %d attachment occurrences. Unknown relationship projections affect %d included signals. Unavailable relationship target occurrences: %d. These figures describe exposed, approved records and do not certify complete source history or embeddings.\n\nInitial inventory: %d; fetched: %d; excluded: %d; withheld: %d; intentionally omitted: %d. Those records are not digest entries.\n\n## Documents\n\n", stats.Entries, stats.Included, stats.Undated, stats.WithDescription, stats.WithoutDescription, stats.MultipleDescriptions, stats.DescriptionAttachments, stats.UnknownProjections, stats.UnavailableReferences, cov.InitialCount, cov.Fetched, cov.Excluded, cov.Withheld, cov.Omitted)
	for _, p := range stats.Parts {
		fmt.Fprintf(b, "- [Signals %06d-%06d](%s): %d entries, %d Markdown bytes.\n", p.FirstRank, p.LastRank, relative(stats.Index, p.Path), p.Entries, p.Bytes)
		if b.err != nil {
			return b.err
		}
	}
	if len(stats.Parts) == 0 {
		b.WriteString("No included signals to display.\n")
	}
	if b.err != nil {
		return b.err
	}
	if err := writeFile(filepath.Join(r.stage, stats.Index), b.Bytes()); err != nil {
		return err
	}
	stats.TotalBytes += int64(b.Len())
	r.documentMetadata[stats.Index] = &DocumentMetadata{Title: "Signals", Description: fmt.Sprintf("%d included signals; %d have an attached description.", stats.Included, stats.WithDescription), NativeTags: []string{"signals"}, DateBasis: "record_created"}
	if r.opts.Progress != nil {
		fmt.Fprintf(r.opts.Progress, "Signals digest: %d entries, %d documents, %d Markdown bytes; %d signals without attached descriptions.\n", stats.Entries, len(stats.Parts), stats.TotalBytes, stats.WithoutDescription)
	}
	return nil
}
