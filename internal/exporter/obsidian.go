package exporter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

const obsidianHome = "Start Here.md"
const obsidianGraphSearch = "tag:pieces/connection OR tag:pieces/summary OR tag:pieces/profile OR tag:pieces/signal"

// Obsidian is a presentation layer. Canonical JSON, graph evidence, privacy
// decisions and coverage remain in the archive and are never inferred anew.
type ObsidianInfo struct {
	Layout               string    `json:"layout,omitempty"`
	VaultPath            string    `json:"vault_path,omitempty"`
	ArchivePresentation  string    `json:"archive_presentation,omitempty"`
	VaultBytes           int64     `json:"vault_markdown_bytes,omitempty"`
	Version              int       `json:"version"`
	Notes                int       `json:"notes"`
	TaggedNotes          int       `json:"tagged_notes"`
	SourceNotes          int       `json:"source_notes"`
	ConnectionNotes      int       `json:"connection_notes"`
	ConnectionLinks      int       `json:"connection_links"`
	DirectRelatedLinks   int       `json:"direct_related_summary_links"`
	StartPage            string    `json:"start_page"`
	ConvertedAt          time.Time `json:"converted_at"`
	SourceManifestSHA256 string    `json:"source_manifest_sha256,omitempty"`
	SourceFormat         string    `json:"source_format,omitempty"`
}

// displayFunc returns the text that generated documents show for a value
// after display formatting changed it; see run.rescanDisplay. The vault builder
// applies it to titles, previews and readable note names.
type displayFunc func(original, shown string) (string, error)

func showAsIs(_, shown string) (string, error) { return shown, nil }

// name sanitizes value for a note name and scans the result again, as
// run.safeName does for archive paths. A nil display shows names as is.
func (display displayFunc) name(value string, maxBytes int) (string, error) {
	if display == nil {
		display = showAsIs
	}
	name := safeTitle(value, maxBytes)
	shown, err := display(value, name)
	if err != nil {
		return "", err
	}
	if shown == name {
		return name, nil
	}
	return safeTitle(shown, maxBytes), nil
}

type obsidianPlan struct {
	display      displayFunc
	compactKinds map[string]string
	ctx          context.Context
	relatedOrder string
	connections  map[string]*obsidianConnection
	entries      map[string]TimelineEntry
	shared       map[string]bool
	zone         *time.Location
	info         ObsidianInfo
}

func newObsidianPlan(ctx context.Context, root *os.Root, timezone, naming, relatedOrder string, display displayFunc) (*obsidianPlan, error) {
	if display == nil {
		display = showAsIs
	}
	zone, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, errConfig("Obsidian requires a valid archive timezone")
	}
	p := &obsidianPlan{display: display, ctx: ctx, relatedOrder: relatedOrder, entries: map[string]TimelineEntry{}, shared: map[string]bool{}, zone: zone,
		info: ObsidianInfo{Version: 2, StartPage: obsidianHome, ConvertedAt: time.Now().UTC()}}
	f, err := archiveOpen(root, "timeline/records.jsonl")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64<<10), 4<<20)
	for s.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var e TimelineEntry
		if json.Unmarshal(s.Bytes(), &e) != nil || !safeArchivePath(e.Path) {
			return nil, errConfig("invalid timeline entry in Obsidian source")
		}
		if p.shared[e.Path] {
			continue
		}
		if _, exists := p.entries[e.Path]; exists {
			delete(p.entries, e.Path)
			p.shared[e.Path] = true // A grouped document is not one record.
		} else {
			p.entries[e.Path] = e
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if err := p.undatedEntries(root); err != nil {
		return nil, err
	}
	if err := p.planConnections(naming); err != nil {
		return nil, err
	}
	return p, nil
}

var obsidianType = regexp.MustCompile("(?m)^Type: `([A-Z_]+)`$")

func obsidianKind(name, typ string) string {
	switch {
	case strings.HasSuffix(name, ".relationships_graph.md"):
		return "relationships"
	case name == obsidianHome || path.Base(name) == "index.md" || strings.Contains(name, ".pages/") || strings.Contains(name, "/indexes/") || strings.HasPrefix(name, "markdown/days/") || strings.Contains(name, "/related_workstream_summaries/"):
		return "navigation"
	case strings.HasPrefix(name, "connections/"):
		return "connection"
	case strings.Contains(name, "/profile_summaries/"):
		return "profile"
	case path.Base(name) == "profile.md" || typ == "PERSONS":
		return "person"
	case typ == "WORKSTREAM_SUMMARIES":
		return "summary"
	case typ == "SIGNALS":
		return "signal"
	case typ == "TAGS":
		return "tag"
	case strings.HasPrefix(name, "signals/"):
		return "signal-digest"
	default:
		return "support"
	}
}

// Plain property values must not introduce Obsidian-only wiki links.
func obsidianLabel(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer("[[", "(", "]]", ")").Replace(s)
}

func obsidianTag(namespace, label string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(label)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-_")
	if s == "" {
		return ""
	}
	// Bound derived property size without silently merging long labels.
	if len([]rune(s)) > 100 {
		h := sha256.Sum256([]byte(s))
		s = string([]rune(s)[:80]) + "-" + hex.EncodeToString(h[:6])
	}
	return namespace + "/" + s // Namespace makes numeric labels valid tags.
}

// Standard Markdown links already create Obsidian backlinks. Neutralize only
// literal, unescaped wiki openers so source text cannot become a phantom link
// or transclusion. Code examples remain byte-for-byte unchanged.
func escapeObsidianWikis(data []byte) []byte {
	if !bytes.Contains(data, []byte("[[")) {
		return data
	}
	type span struct{ start, end int }
	var protected []span
	tree := goldmark.DefaultParser().Parse(text.NewReader(data))
	_ = ast.Walk(tree, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			for i := 0; i < n.Lines().Len(); i++ {
				s := n.Lines().At(i)
				protected = append(protected, span{s.Start, s.Stop})
			}
		case *ast.CodeSpan:
			for c := n.FirstChild(); c != nil; c = c.NextSibling() {
				if t, ok := c.(*ast.Text); ok {
					protected = append(protected, span{t.Segment.Start, t.Segment.Stop})
				}
			}
		}
		return ast.WalkContinue, nil
	})
	sort.Slice(protected, func(i, j int) bool { return protected[i].start < protected[j].start })
	var out bytes.Buffer
	region := 0
	for i := 0; i < len(data); i++ {
		for region < len(protected) && i >= protected[region].end {
			region++
		}
		inCode := region < len(protected) && i >= protected[region].start
		if !inCode && i+1 < len(data) && data[i] == '[' && data[i+1] == '[' {
			backslashes := 0
			for j := i - 1; j >= 0 && data[j] == '\\'; j-- {
				backslashes++
			}
			if backslashes%2 == 0 {
				out.WriteString(`\[\[`)
				i++
				continue
			}
		}
		out.WriteByte(data[i])
	}
	return out.Bytes()
}

func (p *obsidianPlan) note(root *os.Root, name string, body []byte) ([]byte, error) {
	// Only replace frontmatter written by this module. Never silently discard
	// arbitrary user properties when converting a manually edited archive.
	if bytes.HasPrefix(body, []byte("---\n")) {
		end := bytes.Index(body[4:], []byte("\n---\n"))
		if end < 0 || !bytes.Contains(body[4:4+end], []byte("pieces_export: 1\n")) {
			return nil, errConfig("unexpected existing frontmatter; use an untouched export")
		}
		body = body[4+end+5:]
		body = bytes.TrimPrefix(body, []byte("\n"))
		var err error
		body, err = removeObsidianNavigation(body)
		if err != nil {
			return nil, err
		}
	}
	e := p.entries[name]
	if e.Type == "" {
		if m := obsidianType.FindSubmatch(body); len(m) == 2 {
			e.Type = string(m[1])
		}
	}
	if e.Title == "" && bytes.HasPrefix(body, []byte("# ")) {
		e.Title = strings.SplitN(string(body), "\n", 2)[0][2:]
	}
	meta := DocumentMetadata{}
	sidecar := sidecarPath(name)
	if _, err := root.Lstat(sidecar); err == nil {
		b, err := archiveRead(root, sidecar, 16<<20)
		if err != nil || json.Unmarshal(b, &meta) != nil || meta.Path != name {
			return nil, errConfig("invalid document metadata in Obsidian source")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	kind := obsidianKind(name, e.Type)
	if override := p.compactKinds[name]; override != "" {
		kind = override
	}
	tags := []string{"pieces/" + kind}
	if kind == "connection" {
		_, entity := obsidianEntity(e.Type)
		tags = append(tags, "entity/"+entity)
	}
	if e.Type == "TAGS" {
		if tag := obsidianTag("topic", e.Title); tag != "" {
			tags = append(tags, tag)
		}
	}
	for namespace, labels := range map[string][]string{"topic": meta.Tags, "source": meta.Sources, "person": meta.Persons, "website": meta.Websites} {
		for _, label := range labels {
			if tag := obsidianTag(namespace, label); tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	tags = unique(tags)
	var header strings.Builder
	header.WriteString("---\npieces_export: 1\n")
	property := func(key string, value any) {
		b, _ := json.Marshal(value) // JSON scalars and arrays are valid YAML.
		fmt.Fprintf(&header, "%s: %s\n", key, b)
	}
	if e.Title != "" {
		property("aliases", []string{obsidianLabel(e.Title)})
	}
	property("pieces_kind", kind)
	if e.Type != "" {
		property("pieces_type", e.Type)
	}
	if e.ID != "" {
		property("pieces_id", e.ID)
	}
	for _, date := range []struct{ key, raw string }{{"created", e.Created}, {"updated", e.Updated}} {
		if t, err := time.Parse(time.RFC3339Nano, date.raw); err == nil {
			property(date.key, t.In(p.zone).Format(time.RFC3339Nano))
		}
	}
	if len(meta.Tags) > 0 {
		labels := make([]string, 0, len(meta.Tags))
		for _, label := range meta.Tags {
			labels = append(labels, obsidianLabel(label))
		}
		property("pieces_tags", labels)
	}
	property("tags", tags)
	header.WriteString("---\n\n")
	p.info.Notes++
	if !strings.HasPrefix(name, "connections/") {
		p.info.SourceNotes++
	}
	if len(tags) > 1 {
		p.info.TaggedNotes++
	}
	return append([]byte(header.String()), escapeObsidianWikis(body)...), nil
}

// Generated YAML properties are literals, not Markdown document links.
func obsidianBody(data []byte) []byte {
	if bytes.HasPrefix(data, []byte("---\n")) {
		end := bytes.Index(data[4:], []byte("\n---\n"))
		if end >= 0 && bytes.Contains(data[4:4+end], []byte("pieces_export: 1\n")) {
			return data[4+end+5:]
		}
	}
	return data
}

func (r *run) prepareObsidian() error {
	info, err := buildCompactObsidian(r.ctx, r.stage, r.opts.Timezone, r.opts.Naming, r.opts.RelatedOrder, r.opts.Progress, r.rescanDisplay)
	if err != nil {
		return err
	}
	info.ArchivePresentation = "markdown"
	r.manifest.Obsidian = info
	return nil
}

// ConvertObsidian copies a finalized archive without OS calls or source edits.
// It trusts the source archive's existing privacy decisions; it does not claim
// to re-filter manually edited input. Use Rebuild for a complete re-audit.
func ConvertObsidian(ctx context.Context, source, output string, progress io.Writer) (Manifest, error) {
	m, err := InspectArchive(source)
	if err != nil {
		return Manifest{}, err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return Manifest{}, err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return Manifest{}, err
	}
	destination, err := rebuildDestination(source, output)
	if err != nil {
		return Manifest{}, err
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return Manifest{}, err
	}
	defer root.Close()
	original, err := archiveRead(root, "manifest.json", 16<<20)
	if err != nil {
		return Manifest{}, err
	}
	stage := destination + ".partial"
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return Manifest{}, err
	}
	if err := os.Mkdir(stage, 0700); err != nil {
		return Manifest{}, err
	}
	pr := startProgress(progress, nil)
	defer func() { pr.Close() }()
	pr.Stage("Copy complete archive", 0)
	buffer := make([]byte, 256<<10)
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errConfig("Obsidian source must not contain symlinks")
		}
		if d.IsDir() {
			if name == "vault" {
				if m.Obsidian != nil && m.Obsidian.Layout == "compact" && m.Obsidian.VaultPath == "vault" {
					return fs.SkipDir
				}
				return errConfig("source contains a reserved vault directory")
			}
			if strings.HasPrefix(path.Base(name), ".") {
				return fs.SkipDir
			}
			return os.MkdirAll(filepath.Join(stage, filepath.FromSlash(name)), 0700)
		}
		if strings.HasPrefix(path.Base(name), ".") || name == "manifest.json" {
			return nil
		}
		in, err := archiveOpen(root, name)
		if err != nil {
			return err
		}
		defer in.Close()
		target := filepath.Join(stage, filepath.FromSlash(name))
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".metadata.json") {
			// This portable copy does not claim to preserve native xattrs/ADS.
			var meta DocumentMetadata
			if err = json.NewDecoder(io.LimitReader(in, 16<<20)).Decode(&meta); err == nil {
				meta.NativeStatus = "off"
				err = json.NewEncoder(out).Encode(meta)
			}
		} else {
			_, err = io.CopyBuffer(out, in, buffer)
		}
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		pr.Add(1)
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	pr.Close()
	pr = nil
	info, err := buildCompactObsidian(ctx, stage, m.Timezone, m.Naming, m.RelatedOrder, progress, nil)
	if err != nil {
		return Manifest{}, err
	}
	hash := sha256.Sum256(original)
	info.SourceManifestSHA256, info.SourceFormat = hex.EncodeToString(hash[:]), m.Format
	info.ArchivePresentation = "markdown"
	if m.Format == "obsidian" && (m.Obsidian == nil || m.Obsidian.Layout != "compact" || m.Obsidian.ArchivePresentation == "obsidian") {
		info.ArchivePresentation = "obsidian"
	}
	m.Format, m.Obsidian = "obsidian", info
	m.Metadata = "off"
	if err := writeJSON(filepath.Join(stage, "manifest.json"), m); err != nil {
		return Manifest{}, err
	}
	pr = startProgress(progress, nil)
	pr.Stage("Validate Obsidian links", 0)
	if err := (&run{ctx: ctx, stage: stage}).validateMarkdownLinks(); err != nil {
		return Manifest{}, err
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	latest, err := archiveRead(root, "manifest.json", 16<<20)
	if err != nil || !bytes.Equal(latest, original) {
		return Manifest{}, errConfig("source manifest changed during conversion; retry from a finalized archive")
	}
	if err := commitDirectory(stage, destination); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func writeObsidianSettings(root string) error {
	files := map[string]any{
		".obsidian/app.json":               map[string]any{"useMarkdownLinks": true, "newLinkFormat": "relative", "alwaysUpdateLinks": true, "showUnsupportedFiles": false, "showInlineTitle": false, "propertiesInDocument": "hidden", "defaultViewMode": "preview", "readableLineLength": true},
		".obsidian/community-plugins.json": []string{},
		".obsidian/core-plugins.json":      map[string]bool{"file-explorer": true, "global-search": true, "switcher": true, "graph": true, "backlink": true, "outgoing-link": true, "tag-pane": true, "page-preview": true, "outline": true, "properties": true, "command-palette": true, "sync": false, "publish": false},
		".obsidian/graph.json": map[string]any{"search": obsidianGraphSearch, "hideUnresolved": true, "showTags": false, "showAttachments": false, "showOrphans": true, "showArrow": false,
			"colorGroups": []any{map[string]any{"query": "tag:entity/person", "color": map[string]any{"a": 1, "rgb": 0x53b9a3}}, map[string]any{"query": "tag:entity/tag", "color": map[string]any{"a": 1, "rgb": 0xe7b65d}}, map[string]any{"query": "tag:entity/source OR tag:entity/website", "color": map[string]any{"a": 1, "rgb": 0xc78369}}, map[string]any{"query": "tag:pieces/profile", "color": map[string]any{"a": 1, "rgb": 0xb185db}}, map[string]any{"query": "tag:pieces/summary", "color": map[string]any{"a": 1, "rgb": 0x648bdb}}}},
	}
	for name, v := range files {
		if err := writeJSON(filepath.Join(root, filepath.FromSlash(name)), v); err != nil {
			return err
		}
	}
	return nil
}
