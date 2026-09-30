package exporter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	markdown "github.com/teekennedy/goldmark-markdown"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var piecesPattern = regexp.MustCompile(`(?i)pieces://(persons|tags|anchors)/([^\s/\)\]<>"?#]+)`)
var piecesTypes = map[string]string{"persons": "PERSONS", "tags": "TAGS", "anchors": "ANCHORS"}

func md(s string) string {
	return strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "`", "\\`", "*", "\\*", "_", "\\_", "<", "&lt;", ">", "&gt;", "#", "\\#", "\n", " ", "\r", " ", "|", "\\|").Replace(s)
}
func relative(from, to string) string {
	p, err := filepath.Rel(filepath.Dir(filepath.FromSlash(from)), filepath.FromSlash(to))
	if err != nil {
		return ""
	}
	return uriPath(filepath.ToSlash(p))
}

// Normalize the Markdown AST so reference-style and nested-label links follow
// the same policy. Unknown local/custom links retain their labels without a
// destination. Code samples remain inert and unchanged in meaning.
func rewriteMarkdown(body string, sourcePath string, records map[string]*Meta) string {
	data := []byte(body)
	root := goldmark.DefaultParser().Parse(text.NewReader(data))
	destination := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || raw == "" {
			return ""
		}
		switch strings.ToLower(u.Scheme) {
		case "pieces":
			targetType := piecesTypes[strings.ToLower(u.Host)]
			id := strings.TrimPrefix(u.Path, "/")
			if targetType == "" || id == "" || strings.Contains(id, "/") {
				return ""
			}
			target := records[targetType+"\x00"+id]
			if target == nil || target.State != "included" || target.Path == "" {
				return ""
			}
			// Original fragments are not guaranteed to exist in the export.
			return relative(sourcePath, target.Path)
		case "http", "https":
			if u.Hostname() == "" {
				return ""
			}
		case "mailto":
			if u.Opaque == "" {
				return ""
			}
		default:
			// Original relative/file links are not rooted in this export.
			return ""
		}
		return strings.NewReplacer("(", "%28", ")", "%29").Replace(u.String())
	}
	var transform func(ast.Node)
	flatten := func(n ast.Node) {
		parent := n.Parent()
		for child := n.FirstChild(); child != nil; {
			next := child.NextSibling()
			parent.InsertBefore(parent, n, child)
			child = next
		}
		parent.RemoveChild(parent, n)
	}
	transform = func(parent ast.Node) {
		for n := parent.FirstChild(); n != nil; {
			next := n.NextSibling()
			transform(n)
			switch node := n.(type) {
			case *ast.Link:
				target := destination(string(node.Destination))
				if target == "" {
					flatten(n)
				} else {
					node.Destination = []byte(target)
					node.Title = nil
				}
			case *ast.Image:
				// No image bytes are fetched or copied by the Markdown renderer.
				flatten(n)
			case *ast.AutoLink:
				label := ast.NewString([]byte(md(string(node.Label(data)))))
				target := destination(string(node.URL(data)))
				if target == "" {
					parent.ReplaceChild(parent, n, label)
				} else {
					link := ast.NewLink()
					link.Destination = []byte(target)
					link.AppendChild(link, label)
					parent.ReplaceChild(parent, n, link)
				}
			case *ast.RawHTML:
				var raw strings.Builder
				for i := 0; i < node.Segments.Len(); i++ {
					segment := node.Segments.At(i)
					raw.Write(segment.Value(data))
				}
				parent.ReplaceChild(parent, n, ast.NewString([]byte(md(raw.String()))))
			case *ast.HTMLBlock:
				// Display source HTML as a fenced sample, never active markup.
				block := ast.NewFencedCodeBlock(nil)
				for i := 0; i < node.Lines().Len(); i++ {
					block.Lines().Append(node.Lines().At(i))
				}
				if node.HasClosure() {
					block.Lines().Append(node.ClosureLine)
				}
				block.SetBlankPreviousLines(node.HasBlankPreviousLines())
				parent.ReplaceChild(parent, n, block)
			}
			n = next
		}
	}
	transform(root)
	var output bytes.Buffer
	if err := markdown.NewRenderer().Render(&output, data, root); err != nil {
		// bytes.Buffer cannot fail, but never return active unvalidated links.
		return md(body)
	}
	return output.String()
}

type TimelineEntry struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Path      string `json:"path"`
	Title     string `json:"title"`
	Created   string `json:"created,omitempty"`
	Updated   string `json:"updated,omitempty"`
	TimeBasis string `json:"time_basis"`
}
type PublicEdge struct {
	AssociationRef           string `json:"association_ref,omitempty"`
	Source, Target, Relation string
	Provenance               string         `json:"provenance,omitempty"`
	CacheEvidence            *CacheEvidence `json:"cache_evidence,omitempty"`
}

func (r *run) render() error {
	r.assignPaths()
	graph, err := r.buildSummaryGraph()
	if err != nil {
		return err
	}
	r.documentMetadata = map[string]*DocumentMetadata{}
	backlinks := map[string][]Edge{}
	edges := []PublicEdge{}
	entries := []TimelineEntry{}
	undated := []*Meta{}
	r.progress.Stage("Index graph and chronology", len(r.meta))
	for _, m := range r.sortedMeta() {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		r.progress.Add(1)
		if m.State != "included" {
			continue
		}
		for _, e := range m.Edges {
			target := r.meta[e.Target]
			if target == nil || target.State != "included" {
				if typ, _, ok := splitRef(e.Target); ok && r.coverage[typ] != nil && (target == nil || target.State == "missing") {
					r.issue(m.Type, m.ID, "unresolved_reference")
				}
				continue
			}
			backlinks[e.Target] = append(backlinks[e.Target], e)
			provenance := ""
			if r.derivedEdges[e] {
				provenance = "derived_inverse"
			}
			var cached *CacheEvidence
			if evidence, ok := r.cachedEdges[e]; ok {
				cached = &evidence
				provenance = "historical_client_cache"
				if r.derivedEdges[e] {
					provenance += "_derived_inverse"
				}
				r.manifest.SDKCache.RetainedEdges++
			}
			associationRef := ""
			if proof := r.meta[r.associationEdges[e]]; proof != nil && proof.State == "included" {
				provenance, associationRef = "association_record", opaque(proof.Type, proof.ID)
			}
			edges = append(edges, PublicEdge{Source: m.Path, Target: target.Path, Relation: e.Relation, Provenance: provenance, CacheEvidence: cached, AssociationRef: associationRef})
		}
		if created, err := time.Parse(time.RFC3339Nano, m.Created); err == nil {
			entries = append(entries, TimelineEntry{m.Type, m.ID, m.Path, m.Title, created.UTC().Format(time.RFC3339Nano), m.Updated, "record_created"})
		} else {
			undated = append(undated, m)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, entries[i].Created)
		b, _ := time.Parse(time.RFC3339Nano, entries[j].Created)
		if !a.Equal(b) {
			return a.Before(b)
		}
		if entries[i].Type != entries[j].Type {
			return entries[i].Type < entries[j].Type
		}
		return entries[i].ID < entries[j].ID
	})
	r.progress.Stage("Render Markdown", len(r.meta))
	for _, m := range r.sortedMeta() {
		r.progress.Add(1)
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if m.State != "included" {
			continue
		}
		v, err := readRecord(filepath.Join(r.stage, m.DataPath))
		if err != nil {
			return err
		}
		if r.opts.Mode == "filtered" {
			if pruneReferences(v, r.meta) {
				if err = rewriteJSON(filepath.Join(r.stage, m.DataPath), v); err != nil {
					return err
				}
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\nType: `%s`\n\n", md(m.Title), m.Type)
		if m.Created != "" {
			fmt.Fprintf(&b, "Created: %s\n\n", md(m.Created))
		}
		if m.Updated != "" {
			fmt.Fprintf(&b, "Updated: %s\n\n", md(m.Updated))
		}
		fmt.Fprintf(&b, "[Record JSON](%s) · [Export index](%s)\n\n", relative(m.Path, m.DataPath), relative(m.Path, "index.md"))
		for _, e := range m.Edges {
			if _, ok := r.cachedEdges[e]; ok {
				fmt.Fprintf(&b, "Some relationships were recovered from an explicitly selected historical client cache and may be stale. Current record JSON is unchanged. [Recovery coverage](%s).\n\n", relative(m.Path, "coverage.md"))
				break
			}
		}
		if m.Redactions > 0 {
			b.WriteString("Some fields were redacted by the export policy.\n\n")
		}
		if meta := graph.Metadata[m.Key]; meta != nil {
			b.WriteString(metadataMarkdown(meta))
		}
		for _, block := range content(v) {
			fmt.Fprintf(&b, "## %s\n\n%s\n\n", md(block.Path), rewriteMarkdown(block.Text, m.Path, r.meta))
		}
		renderAssociationMetadata(&b, m, v, r.meta)
		// Summaries expose their narrative through annotation records, not a body field.
		if m.Type == "WORKSTREAM_SUMMARIES" {
			for _, e := range m.Edges {
				a := r.meta[e.Target]
				if e.Relation != "annotations" || a == nil || a.State != "included" {
					continue
				}
				av, err := readRecord(filepath.Join(r.stage, a.DataPath))
				if err != nil {
					return err
				}
				if text := fieldString(av, "text"); text != "" {
					if evidence, ok := r.cachedEdges[e]; ok {
						fmt.Fprintf(&b, "%s\n\n", evidence.description())
					}
					fmt.Fprintf(&b, "## Annotation: %s\n\n[Canonical annotation](%s)\n\n%s\n\n", md(fieldString(av, "type")), relative(m.Path, a.Path), rewriteMarkdown(text, m.Path, r.meta))
				}
			}
		}
		if m.Type == "CONVERSATIONS" {
			if err := r.transcript(&b, m); err != nil {
				return err
			}
		}
		b.WriteString("## Related records\n\n")
		for _, e := range m.Edges {
			if target := r.meta[e.Target]; target != nil && target.State == "included" {
				label := e.Relation
				if r.derivedEdges[e] {
					label += " (derived inverse)"
				}
				if r.associationEdges[e] != "" {
					label += " (source association record)"
				}
				if _, ok := r.cachedEdges[e]; ok {
					label += " (historical client cache)"
				}
				fmt.Fprintf(&b, "- %s: [%s](%s)\n", md(label), md(target.Title), relative(m.Path, target.Path))
			}
		}
		b.WriteString("\n## Referenced by\n\n")
		for _, e := range backlinks[m.Key] {
			source := r.meta[e.Source]
			label := e.Relation
			if _, ok := r.cachedEdges[e]; ok {
				label += " (historical client cache)"
			}
			fmt.Fprintf(&b, "- [%s](%s) — %s (derived backlink)\n", md(source.Title), relative(m.Path, source.Path), md(label))
		}
		if meta := graph.Metadata[m.Key]; meta != nil {
			// Both documents share a directory, so all relative destinations are
			// identical. Rank and render the related sections only once.
			related, err := graph.render(r, m, m.Path)
			if err != nil {
				return err
			}
			if r.opts.Relationships == "inline" || r.opts.Relationships == "both" {
				b.WriteString(related)
			}
			if r.opts.Relationships == "sidecar" || r.opts.Relationships == "both" {
				sibling := relationshipPath(m.Path)
				body := fmt.Sprintf("# Relationships: %s\n\n[Summary](%s)\n\n", md(m.Title), relative(sibling, m.Path)) + related
				if len(r.opts.SDKCaches) > 0 {
					body += "\nHistorical client-cache relationships may contribute to these suggestions. See the summary and export coverage for provenance.\n"
				}
				if err := writeFile(filepath.Join(r.stage, sibling), []byte(body)); err != nil {
					return err
				}
				fmt.Fprintf(&b, "\n[Relationship graph](%s)\n", relative(m.Path, sibling))
				r.documentMetadata[sibling] = meta
			}
			r.documentMetadata[m.Path] = meta
		}

		if err = writeFile(filepath.Join(r.stage, m.Path), []byte(b.String())); err != nil {
			return err
		}
	}
	if err := writeJSONL(filepath.Join(r.stage, "relationships.jsonl"), edges); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(r.stage, "timeline", "records.jsonl"), entries); err != nil {
		return err
	}
	zone, _ := time.LoadLocation(r.opts.Timezone)
	days := map[string][]TimelineEntry{}
	for _, e := range entries {
		t, _ := time.Parse(time.RFC3339Nano, e.Created)
		day := t.In(zone).Format("2006-01-02")
		days[day] = append(days[day], e)
	}
	dayNames := []string{}
	for day := range days {
		dayNames = append(dayNames, day)
	}
	sort.Strings(dayNames)
	var chronology strings.Builder
	chronology.WriteString("# Chronological record index\n\nThis index covers every included dated material record, ordered by its record creation timestamp. It is separate from the workstream-summary timeline, which organizes summary documents.\n\n[Chronological JSONL](records.jsonl) · [Export index](../index.md)\n\n## Days\n\n")
	var index strings.Builder
	index.WriteString("# Pieces export\n\nChronology uses record creation time, not necessarily activity time. Summary ranges and calendar context remain in record JSON.\n\n[Manifest](manifest.json) · [Chronological JSONL](timeline/records.jsonl) · [Undated records](markdown/undated.md)\n\n## Days\n\n")
	fmt.Fprintf(&index, "Selected scope: **%s**. Read [coverage and intentional omissions](coverage.md) before treating this as a full migration.\n\n", md(r.manifest.Scope.Name))
	for _, day := range dayNames {
		path := "markdown/days/" + day + ".md"
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\nTimezone: %s\n\n", day, md(r.opts.Timezone))
		for _, e := range days[day] {
			t, _ := time.Parse(time.RFC3339Nano, e.Created)
			fmt.Fprintf(&b, "- %s — [%s](%s) (%s)\n", t.In(zone).Format("15:04:05 -07:00"), md(e.Title), relative(path, e.Path), e.Type)
		}
		if err := writeFile(filepath.Join(r.stage, path), []byte(b.String())); err != nil {
			return err
		}
		fmt.Fprintf(&index, "- [%s](%s) (%d records)\n", day, path, len(days[day]))
		fmt.Fprintf(&chronology, "- [%s](%s) (%d records)\n", day, relative("timeline/index.md", path), len(days[day]))
	}
	if len(dayNames) == 0 {
		chronology.WriteString("No included records have a valid creation timestamp.\n")
	}
	if err := writeFile(filepath.Join(r.stage, "timeline/index.md"), []byte(chronology.String())); err != nil {
		return err
	}
	if r.opts.Format != "markdown" {
		index.WriteString("\n[Open PDF index](index.pdf) - PDFs are in the pdf folder; Markdown companions are retained.\n")
	}
	if err := r.renderPersonas(); err != nil {
		return err
	}
	if err := r.renderOrganization(); err != nil {
		return err
	}
	if err := r.renderSignalDigest(); err != nil {
		return err
	}
	if r.manifest.SignalDigest.Index != "" {
		index.WriteString("\n[Consolidated signals](signals/index.md)\n")
	}
	if err := r.renderCoverage(); err != nil {
		return err
	}
	index.WriteString("\n[Export coverage and relationship gaps](coverage.md)\n")
	index.WriteString("\n[Chronological record index](timeline/index.md) · [All workstream summaries](workstream_summaries/index.md) · [Single-click summaries](workstream_summaries/single_click_summaries/index.md)\n")
	index.WriteString("\n[Personas, profiles, and people](workstream_summaries/personas/index.md) · [Known pipeline associations](workstream_summaries/pipeline_associations/index.md)\n")
	index.WriteString("\n## All included records\n\n")
	links := map[string]string{}
	for _, m := range r.sortedMeta() {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if m.State == "included" {
			fmt.Fprintf(&index, "- [%s](%s) (%s)\n", md(m.Title), uriPath(m.Path), m.Type)
			links[opaque(m.Type, m.ID)] = m.Path
		}
	}
	if err := writeJSON(filepath.Join(r.stage, "link-map.json"), links); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(r.stage, "index.md"), []byte(index.String())); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Undated records\n\nThese records have missing or invalid creation timestamps.\n\n")
	for _, m := range undated {
		fmt.Fprintf(&b, "- [%s](%s) (%s)\n", md(m.Title), relative("markdown/undated.md", m.Path), m.Type)
	}
	return writeFile(filepath.Join(r.stage, "markdown", "undated.md"), []byte(b.String()))
}

func writeJSONL[T any](path string, items []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, item := range items {
		if err = enc.Encode(item); err != nil {
			f.Close()
			return err
		}
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
func rewriteJSON(path string, v any) error {
	if err := writeJSON(path+".tmp", v); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

type textBlock struct{ Path, Text string }

func content(v map[string]any) []textBlock {
	result := []textBlock{}
	var walk func(string, string, any)
	walk = func(path, key string, value any) {
		switch x := value.(type) {
		case map[string]any:
			keys := []string{}
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if _, isRef := referenceTypes[k]; isRef || k == "reference" {
					continue
				}
				p := k
				if path != "" {
					p = path + "." + k
				}
				walk(p, k, x[k])
			}
		case []any:
			for i, a := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), key, a)
			}
		case string:
			switch key {
			case "text", "readable", "description", "ocrText", "raw", "url", "browserUrl":
				if x != "" {
					result = append(result, textBlock{path, x})
				}
			}
		}
	}
	walk("", "", v)
	return result
}
func pruneReferences(v map[string]any, metas map[string]*Meta) bool {
	changed := false
	for field, t := range referenceTypes {
		value, ok := v[field].(map[string]any)
		if !ok {
			continue
		}
		denied := func(id string) bool { m := metas[t+"\x00"+id]; return m != nil && m.State != "included" }
		if denied(fieldString(value, "id")) {
			delete(v, field)
			changed = true
			continue
		}
		if indices, ok := value["indices"].(map[string]any); ok {
			for id := range indices {
				if denied(id) {
					delete(indices, id)
					changed = true
				}
			}
		}
		if items, ok := value["iterable"].([]any); ok {
			kept := []any{}
			for _, item := range items {
				id := ""
				switch x := item.(type) {
				case string:
					id = x
				case map[string]any:
					id = fieldString(x, "id")
				}
				if !denied(id) {
					kept = append(kept, item)
				}
			}
			if len(kept) != len(items) {
				value["iterable"] = kept
				changed = true
			}
		}
	}
	return changed
}
func (r *run) transcript(b *strings.Builder, m *Meta) error {
	type message struct {
		meta        *Meta
		record      map[string]any
		sequence    float64
		hasSequence bool
	}
	messages := []message{}
	allHaveSequence := true
	for _, e := range m.Edges {
		n := r.meta[e.Target]
		if e.Relation != "messages" || n == nil || n.State != "included" {
			continue
		}
		v, err := readRecord(filepath.Join(r.stage, n.DataPath))
		if err != nil {
			return err
		}
		seq, ok := v["sequence"].(json.Number)
		num, seqErr := seq.Float64()
		ok = ok && seqErr == nil
		allHaveSequence = allHaveSequence && ok
		messages = append(messages, message{n, v, num, ok})
	}
	sort.SliceStable(messages, func(i, j int) bool {
		a, b := messages[i], messages[j]
		if allHaveSequence && a.sequence != b.sequence {
			return a.sequence < b.sequence
		}
		ta, ea := time.Parse(time.RFC3339Nano, a.meta.Created)
		tb, eb := time.Parse(time.RFC3339Nano, b.meta.Created)
		if (ea == nil) != (eb == nil) {
			return ea == nil
		}
		if ea == nil && eb == nil && !ta.Equal(tb) {
			return ta.Before(tb)
		}
		return a.meta.ID < b.meta.ID
	})
	b.WriteString("## Transcript\n\n")
	for _, message := range messages {
		fmt.Fprintf(b, "### %s\n\n[Message record](%s)\n\n", md(message.meta.Created), relative(m.Path, message.meta.Path))
		for _, block := range content(message.record) {
			b.WriteString(rewriteMarkdown(block.Text, m.Path, r.meta) + "\n\n")
		}
	}
	return nil
}
