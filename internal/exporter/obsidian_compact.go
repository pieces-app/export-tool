package exporter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

const obsidianVault = "vault"
const compactRelatedLimit = 4

type compactRecord struct {
	entry                         TimelineEntry
	kind, annotation, destination string
	edges                         []compactEdge
	mentions                      []*compactRecord
	description                   string
	rangeFrom, rangeTo            time.Time
}
type compactEdge struct {
	target               *compactRecord
	relation, provenance string
}

func compactCanonical(root *os.Root, e TimelineEntry) (map[string]any, string, error) {
	m, ok := materialByType(e.Type)
	if !ok {
		return nil, "", errConfig("unknown compact record type")
	}
	file := "data/" + m.Folder + "/" + opaque(e.Type, e.ID) + ".json"
	if _, err := root.Lstat(file); os.IsNotExist(err) {
		file = "raw/" + m.Folder + "/" + opaque(e.Type, e.ID) + ".json"
	}
	b, err := archiveRead(root, file, 64<<20)
	if err != nil {
		return nil, "", err
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil || fieldString(v, "id") != e.ID {
		return nil, "", errConfig("compact record identity mismatch")
	}
	return v, file, nil
}

// Keep the full export at the bundle root. Only this child folder is a vault:
// no copied raw indexes, exhaustive technical inverse lists or relationship
// sidecars to index. Connection notes have bounded lists of readable memories.
//
// display receives each title or preview before and after the vault's display
// formatting (Markdown stripped, lines joined, text shortened) and returns the
// text to show. A filtered export scans changed text again; conversions of a
// finalized archive pass nil and show the formatted text as is.
func buildCompactObsidian(ctx context.Context, archive, timezone, naming, order string, progress io.Writer, display displayFunc) (*ObsidianInfo, error) {
	if display == nil {
		display = showAsIs
	}
	root, err := os.OpenRoot(archive)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	p, err := newObsidianPlan(ctx, root, timezone, naming, order, display)
	if err != nil {
		return nil, err
	}
	p.compactKinds = map[string]string{}
	p.info.Version = 3
	p.info.Layout = "compact"
	p.info.VaultPath = obsidianVault
	pr := startProgress(progress, nil)
	defer pr.Close()
	pr.Stage("Plan compact Obsidian view", 0)
	records := map[string]*compactRecord{}
	byPath := map[string]*compactRecord{}
	paths := []string{}
	for name := range p.entries {
		if !strings.HasPrefix(name, "connections/") {
			paths = append(paths, name)
		}
	}
	sort.Strings(paths)
	for _, name := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e := p.entries[name]
		shown, err := display(e.Title, compactDisplayTitle(e.Title))
		if err != nil {
			return nil, err
		}
		e.Title = shown
		p.entries[name] = e
		kind := ""
		dest := name
		switch e.Type {
		case "WORKSTREAM_SUMMARIES":
			kind = "summary"
		case "ANNOTATIONS":
			kind = "annotation"
		case "RANGES":
			kind, dest = "range", ""
		case "SIGNALS":
			kind = "signal"
			label, err := display.name(e.Title, 72)
			if err != nil {
				return nil, err
			}
			dest = "signals/" + label + "." + opaque(e.Type, e.ID)[:12] + ".md"
		default:
			if c := p.connections[name]; c != nil {
				kind = "connection"
				dest = c.Path
			}
		}
		if kind == "" {
			continue
		}
		v, _, err := compactCanonical(root, e)
		if err != nil {
			return nil, err
		}
		r := &compactRecord{entry: e, kind: kind, destination: dest}
		if kind == "summary" {
			if err := compactSummaryDescription(root, r, v, display); err != nil {
				return nil, err
			}
		}
		if kind == "range" {
			r.rangeFrom, _ = time.Parse(time.RFC3339Nano, timestamp(v, "from"))
			r.rangeTo, _ = time.Parse(time.RFC3339Nano, timestamp(v, "to"))
		}
		if e.Type == "ANNOTATIONS" {
			r.annotation = fieldString(v, "type")
			if profileAnnotation(r.annotation) {
				r.kind = "profile"
			} else if r.annotation == "SIGNAL_DESCRIPTION" {
				r.kind = "signal-description"
				label, err := display.name(e.Title, 72)
				if err != nil {
					return nil, err
				}
				r.destination = "signals/descriptions/" + label + "." + opaque(e.Type, e.ID)[:12] + ".md"
			} else {
				r.destination = ""
			}
		}
		if naming == "opaque" && (r.kind == "signal" || r.kind == "signal-description") {
			r.destination = "signals/" + opaque(e.Type, e.ID) + ".md"
		}
		records[opaque(e.Type, e.ID)] = r
		byPath[name] = r
		pr.Add(1)
	}
	// Resolve only identities already retained by the archive. Large association
	// inventories stream past without creating an Obsidian note for every row.
	f, err := archiveOpen(root, "relationships.jsonl")
	if err != nil {
		return nil, err
	}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64<<10), 4<<20)
	for s.Scan() {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		var e PublicEdge
		if json.Unmarshal(s.Bytes(), &e) != nil {
			f.Close()
			return nil, errConfig("invalid compact graph record")
		}
		a, b := records[e.SourceRef], records[e.TargetRef]
		if e.SourceRef == "" {
			a = byPath[e.Source]
		}
		if e.TargetRef == "" {
			b = byPath[e.Target]
		}
		if a != nil && b != nil {
			a.edges = append(a.edges, compactEdge{b, e.Relation, e.Provenance})
		}
	}
	scanErr := s.Err()
	f.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	if err := compactNotePaths(records, naming, p.zone, display); err != nil {
		return nil, err
	}
	metas := map[string]*Meta{}
	mapping := map[string]string{}
	byDestination := map[string]*compactRecord{}
	for _, r := range records {
		if r.destination != "" {
			metas[r.entry.Type+"\x00"+r.entry.ID] = &Meta{Type: r.entry.Type, ID: r.entry.ID, State: "included", Path: r.destination}
			mapping[opaque(r.entry.Type, r.entry.ID)] = r.destination
			byDestination[r.destination] = r
		}
	}
	vault := filepath.Join(archive, obsidianVault)
	if err := os.Mkdir(vault, 0700); err != nil {
		return nil, err
	}
	write := func(name string, body []byte) error {
		if !safeArchivePath(name) {
			return errConfig("invalid compact output path")
		}
		file := filepath.Join(vault, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			return err
		}
		return os.WriteFile(file, body, 0600)
	}
	pr.Stage("Write compact memories and connections", len(mapping))
	// Render memories first so connection notes can link back to their actual
	// incoming references, including retained links embedded inside prose.
	sort.SliceStable(paths, func(i, j int) bool {
		a, b := byPath[paths[i]], byPath[paths[j]]
		return (a == nil || a.kind != "connection") && b != nil && b.kind == "connection"
	})
	for _, name := range paths {
		r := byPath[name]
		if r == nil || r.destination == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		body, err := compactDocument(root, p, r, metas, byPath)
		if err != nil {
			return nil, err
		}
		// Read metadata at the original archive path; destinations can differ.
		p.compactKinds[name] = r.kind
		if r.kind == "signal-description" {
			p.compactKinds[name] = "signal"
		}
		decorated, err := p.note(root, name, body)
		if err != nil {
			return nil, err
		}
		compactCollectMentions(r, decorated, byDestination)
		if err := write(r.destination, decorated); err != nil {
			return nil, err
		}
		if r.kind == "connection" {
			p.info.ConnectionNotes++
		}
		pr.Add(1)
	}
	if err := compactIndexes(ctx, vault, records, p.zone); err != nil {
		return nil, err
	}
	if err := writeObsidianSettings(vault); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(vault, "record-map.json"), mapping); err != nil {
		return nil, err
	}
	home := fmt.Sprintf("---\npieces_export: 1\npieces_kind: navigation\ntags: [pieces/navigation]\n---\n\n# Your Pieces memories\n\nThis is the compact browsing vault. Your full export is preserved in the parent folder, outside Obsidian's index.\n\n[Browse folders](index.md) · [Coverage and archive](Archive.md)\n\n## Explore\n\n")
	for _, item := range []struct{ label, link string }{{"Timeline", "workstream_summaries/timeline/index.md"}, {"Personas and profile histories", "workstream_summaries/personas/index.md"}, {"Single-click summaries", "workstream_summaries/single_click_summaries/index.md"}, {"People", "connections/people/index.md"}, {"Topics", "connections/topics/index.md"}, {"Sources", "connections/sources/index.md"}, {"Websites", "connections/websites/index.md"}, {"Pipelines", "connections/pipelines/index.md"}, {"Signals", "signals/index.md"}} {
		if _, err := os.Stat(filepath.Join(vault, item.link)); err == nil {
			home += fmt.Sprintf("- [%s](%s)\n", item.label, item.link)
		}
	}
	home += "\n## Navigate\n\nOpen a memory, then follow its people, topics, sources or nearby summaries. Connection notes have clickable **Connected memories** and a complete paginated list when needed. The **Backlinks** panel also shows incoming references, including navigation pages. **Open local graph** starts with the current note; use depth 1 before expanding it. The global graph is an overview and can still be dense.\n\nExisting Pieces tags appear under `topic/`. Search `tag:pieces/summary`, `tag:pieces/profile`, or a topic from the Tags panel. No topics or identities are invented or merged. Nearby summaries are suggestions ranked from the archive's existing related lists, with up to four links per memory.\n\nAll retained profile versions have their own notes. A person's page links their history without copying the reports into every summary. Signals appear only if the source archive included them.\n\n## Keep the archive\n\nThe parent folder contains the complete source records, relationship evidence, full lists and coverage reports. Open its `index.md` with a Markdown viewer or Finder/Explorer. Obsidian links stay inside this vault; technical references without a browsing note are shown as plain text. No hidden external-file links or plugins are needed. Open **this folder**, not its parent, as the vault.\n"
	if err := write(obsidianHome, []byte(home)); err != nil {
		return nil, err
	}
	coverage, err := archiveRead(root, "coverage.md", 64<<20)
	if err != nil {
		return nil, err
	}
	// Coverage is reference text, not a second network of clickable inventories.
	archiveNote := "---\npieces_export: 1\npieces_kind: navigation\ntags: [pieces/navigation]\n---\n\n# Archive and coverage\n\nThe complete archive is in the parent folder. `record-map.json` maps retained identities to browsing notes. Missing source bodies and privacy omissions remain missing; this view does not certify complete source history.\n\n```text\n" + strings.ReplaceAll(string(coverage), "```", "~~~") + "\n```\n"
	if err := write("Archive.md", []byte(archiveNote)); err != nil {
		return nil, err
	}
	p.info.Notes = 0
	p.info.VaultBytes = 0
	err = filepath.WalkDir(vault, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && path.Ext(file) == ".md" {
			i, e := d.Info()
			if e != nil {
				return e
			}
			p.info.Notes++
			p.info.VaultBytes += i.Size()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	p.info.SourceNotes = 0
	err = filepath.WalkDir(archive, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if file == vault {
			return fs.SkipDir
		}
		if !d.IsDir() && path.Ext(file) == ".md" {
			p.info.SourceNotes++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := (&run{ctx: ctx, stage: vault}).validateMarkdownLinks(); err != nil {
		return nil, err
	}
	guide := []byte("# Open your Pieces memories\n\nOpen the `vault` folder in Obsidian, then `Start Here.md`. This parent directory is the complete archival export; do not open the parent as a vault. Keep both together. Original records and evidence remain here, and the browsing notes are under `vault/`.\n")
	if err := os.WriteFile(filepath.Join(archive, "OBSIDIAN_START_HERE.md"), guide, 0600); err != nil {
		return nil, err
	}
	return &p.info, nil
}

func compactDocument(root *os.Root, p *obsidianPlan, r *compactRecord, metas map[string]*Meta, byPath map[string]*compactRecord) ([]byte, error) {
	v, _, err := compactCanonical(root, r.entry)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	// Report titles were often derived from their first Markdown line. Do not
	// show escaped heading/bold markers or duplicate that heading above it.
	reportBody := fieldString(v, "text")
	report := r.kind == "profile" || r.kind == "signal-description"
	if !report || !compactHasHeading(reportBody) {
		fmt.Fprintf(&b, "# %s\n\n", md(r.entry.Title))
	}
	if r.entry.Created != "" {
		fmt.Fprintf(&b, "Created: %s\n\n", md(displayTimestamp(r.entry.Created, p.zone)))
	}
	if r.kind == "connection" {
		_, kind := obsidianEntity(r.entry.Type)
		fmt.Fprintf(&b, "%s from Pieces. Connected memories below link to notes that reference this record.\n\n", md(kind))
		if kind == "person" {
			facts := personFacts(v)
			if facts.Email != "" {
				fmt.Fprintf(&b, "Email: %s\n\n", md(facts.Email))
			}
		}
	} else if r.kind == "profile" || r.kind == "signal-description" {
		b.WriteString(rewriteMarkdown(fieldString(v, "text"), r.destination, metas))
		b.WriteString("\n\n")
	} else {
		for _, block := range content(v) {
			if strings.HasSuffix(block.Path, ".readable") {
				continue
			}
			fmt.Fprintf(&b, "## %s\n\n%s\n\n", md(block.Path), rewriteMarkdown(block.Text, r.destination, metas))
		}
	}
	edges := append([]compactEdge(nil), r.edges...)
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i].target.entry, edges[j].target.entry
		at, _ := time.Parse(time.RFC3339Nano, a.Created)
		bt, _ := time.Parse(time.RFC3339Nano, b.Created)
		if !at.Equal(bt) {
			return at.After(bt)
		}
		return a.ID < b.ID
	})
	links := map[string][]navigationEntry{}
	seen := map[string]bool{}
	for _, edge := range edges {
		t := edge.target
		if r.kind == "summary" && edge.relation == "annotations" && t.kind == "annotation" {
			if seen["body:"+t.entry.ID] {
				continue
			}
			seen["body:"+t.entry.ID] = true
			av, _, err := compactCanonical(root, t.entry)
			if err != nil {
				return nil, err
			}
			body := fieldString(av, "text")
			if strings.TrimSpace(body) == "" {
				continue
			}
			fmt.Fprintf(&b, "## %s\n\n", md(t.annotation))
			if strings.Contains(edge.provenance, "historical_client_cache") {
				b.WriteString("This attachment comes from retained historical client-cache evidence and may be stale.\n\n")
			}
			b.WriteString(rewriteMarkdown(body, r.destination, metas))
			b.WriteString("\n\n")
		}
		if t.destination == "" || t == r || seen[t.destination] {
			continue
		}
		group := ""
		switch {
		case t.kind == "connection" && r.kind != "connection":
			group, _ = obsidianEntity(t.entry.Type)
			p.info.ConnectionLinks++
		case t.kind == "profile" && (r.kind == "connection" || r.kind == "summary"):
			group = "Profile history"
		case t.kind == "signal-description" && r.kind == "signal":
			group = "Descriptions"
		case t.kind == "summary" && r.kind == "summary" && edge.relation != "annotations":
			group = "Summary hierarchy"
		case t.kind == "summary" && r.kind == "signal":
			group = "Related summaries"
		}
		if group != "" {
			links[group] = append(links[group], navigationEntry{label: t.entry.Title, path: t.destination, detail: displayTimestamp(t.entry.Created, p.zone)})
			seen[t.destination] = true
		}
	}
	groups := []string{}
	for group := range links {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	for _, group := range groups {
		list := links[group]
		fmt.Fprintf(&b, "## %s\n\n", md(strings.ToUpper(group[:1])+group[1:]))
		// Large profile histories get their own bounded index. Other genuine
		// direct relationships are retained. Connection lists are bounded below.
		if group == "Profile history" && r.kind == "connection" && len(list) > 8 {
			for _, item := range list[:3] {
				b.WriteString(item.markdown(r.destination))
			}
			index := strings.TrimSuffix(r.destination, ".md") + ".history/index.md"
			fmt.Fprintf(&b, "\n[All %d profile versions](%s)\n", len(list), relative(r.destination, index))
		} else {
			for _, item := range list {
				b.WriteString(item.markdown(r.destination))
			}
		}
		b.WriteString("\n")
	}
	if r.kind == "summary" {
		original, err := archiveRead(root, r.entry.Path, 64<<20)
		if err != nil {
			return nil, err
		}
		peers, err := p.related(root, r.entry.Path, original)
		if err != nil {
			return nil, err
		}
		if len(peers) > 0 {
			b.WriteString("## Nearby summaries\n\nSuggestions based on the retained shared-relationship lists.\n\n")
		}
		for _, peer := range peers[:min(len(peers), compactRelatedLimit)] {
			if target := byPath[peer.entry.Path]; target != nil && target.destination != "" {
				fmt.Fprintf(&b, "- [%s](%s) — %d shared dimensions\n", md(navigationLabel(peer.entry.Title)), relative(r.destination, target.destination), peer.dimensions)
				p.info.DirectRelatedLinks++
			}
		}
	}
	if r.kind == "connection" {
		b.WriteString(compactConnectionNavigation(r, p.zone))
	}
	fmt.Fprintf(&b, "\n---\nArchive record: `%s`\n", opaque(r.entry.Type, r.entry.ID))
	return []byte(b.String()), nil
}

func compactDisplayTitle(title string) string {
	b := []byte(title)
	tree := goldmark.DefaultParser().Parse(text.NewReader(b))
	var label strings.Builder
	_ = ast.Walk(tree, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if !enter {
			if n.Type() == ast.TypeBlock {
				label.WriteByte(' ')
			}
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Text:
			label.Write(v.Value(b))
			if v.SoftLineBreak() || v.HardLineBreak() {
				label.WriteByte(' ')
			}
		case *ast.String:
			label.Write(v.Value)
		case *ast.AutoLink:
			label.Write(v.Label(b))
		}
		return ast.WalkContinue, nil
	})
	plain := strings.Join(strings.Fields(html.UnescapeString(label.String())), " ")
	if plain == "" {
		return strings.Join(strings.Fields(title), " ")
	}
	return plain
}

func compactHasHeading(body string) bool {
	tree := goldmark.DefaultParser().Parse(text.NewReader([]byte(body)))
	_, ok := tree.FirstChild().(*ast.Heading)
	return ok
}

func compactIndexes(ctx context.Context, vault string, records map[string]*compactRecord, zone *time.Location) error {
	folders := map[string][]navigationEntry{}
	children := map[string]bool{}
	byPath := map[string]*compactRecord{}
	for _, r := range records {
		if r.destination == "" {
			continue
		}
		folder := path.Dir(r.destination)
		byPath[r.destination] = r
		detail := ""
		if r.kind == "summary" || r.kind == "profile" || r.kind == "signal" || r.kind == "signal-description" {
			detail = displayTimestamp(r.entry.Created, zone)
		}
		entry := navigationEntry{label: r.entry.Title, path: r.destination, detail: detail}
		if r.kind == "summary" {
			entry = compactSummaryEntry(r, zone)
		}
		folders[folder] = append(folders[folder], entry)
		for folder != "." {
			parent := path.Dir(folder)
			if !children[folder] {
				folders[parent] = append(folders[parent], navigationEntry{label: strings.ReplaceAll(path.Base(folder), "_", " "), path: folder + "/index.md"})
				children[folder] = true
			}
			folder = parent
		}
	}
	run := &run{ctx: ctx, stage: vault}
	navHeader := "---\npieces_export: 1\npieces_kind: navigation\ntags: [pieces/navigation]\n---\n\n"
	for folder, list := range folders {
		sort.Slice(list, func(i, j int) bool {
			a, b := byPath[list[i].path], byPath[list[j].path]
			if a != nil && b != nil && a.kind != "connection" && b.kind != "connection" {
				at, _ := time.Parse(time.RFC3339Nano, a.entry.Created)
				bt, _ := time.Parse(time.RFC3339Nano, b.entry.Created)
				if !at.Equal(bt) {
					return at.After(bt)
				}
			}
			return list[i].path < list[j].path
		})
		name := path.Join(folder, "index.md")
		heading := strings.ReplaceAll(path.Base(folder), "_", " ")
		if folder == "." {
			heading = "Browse your memories"
		}
		preamble := navHeader + "# " + md(heading) + "\n\n"
		for _, entry := range list {
			if entry.preview != "" {
				preamble += "Newest-created first. Page dates use summary creation dates; each Activity line shows its recorded coverage. Timezone: " + md(zone.String()) + ".\n\n"
				break
			}
		}
		if err := run.writeNavigationIndex(name, preamble, list); err != nil {
			return err
		}
	}
	for _, r := range records {
		if r.kind != "connection" {
			continue
		}
		if err := compactConnectionIndex(run, r, zone); err != nil {
			return err
		}
		list := []navigationEntry{}
		seen := map[string]bool{}
		for _, e := range r.edges {
			t := e.target
			if t.kind == "profile" && t.destination != "" && !seen[t.destination] {
				list = append(list, navigationEntry{label: t.entry.Title, path: t.destination, detail: t.entry.Created})
				seen[t.destination] = true
			}
		}
		if len(list) > 8 {
			sort.Slice(list, func(i, j int) bool {
				a, _ := time.Parse(time.RFC3339Nano, list[i].detail)
				b, _ := time.Parse(time.RFC3339Nano, list[j].detail)
				if !a.Equal(b) {
					return a.After(b)
				}
				return list[i].path < list[j].path
			})
			for i := range list {
				list[i].detail = displayTimestamp(list[i].detail, zone)
			}
			name := strings.TrimSuffix(r.destination, ".md") + ".history/index.md"
			if err := run.writeNavigationIndex(name, navHeader+"# Profile history\n\n[Person]("+relative(name, r.destination)+")\n\n", list); err != nil {
				return err
			}
		}
	}
	// Paginated helper pages are indexes too, not graph entities.
	return filepath.WalkDir(vault, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.Contains(filepath.ToSlash(file), ".pages/") && path.Ext(file) == ".md" {
			b, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(string(b), "---\n") {
				return os.WriteFile(file, append([]byte(navHeader), b...), 0600)
			}
		}
		return nil
	})
}

// Obsidian labels graph nodes and backlinks with filenames, not aliases.
// Keep archival chronological names in the parent, and readable titles here.
func compactNotePaths(records map[string]*compactRecord, naming string, zone *time.Location, display displayFunc) error {
	if naming == "opaque" {
		return nil
	}
	if display == nil {
		display = showAsIs
	}
	candidates := map[string]string{}
	counts := map[string]int{}
	keys := []string{}
	for ref, r := range records {
		if r.destination == "" {
			continue
		}
		// Readable names show underscores as spaces, which the payment card
		// pattern accepts between digit groups. Scan the shown form again.
		base := strings.ReplaceAll(safeTitle(r.entry.Title, 80), "_", " ")
		shown, err := display(r.entry.Title, base)
		if err != nil {
			return err
		}
		if shown != base {
			base = strings.ReplaceAll(safeTitle(shown, 80), "_", " ")
		}
		// Preserve the Windows device-name escape made by safeTitle.
		if strings.HasPrefix(base, " ") {
			base = "Note" + base
		}
		candidate := path.Join(path.Dir(r.destination), base+".md")
		candidates[ref] = candidate
		counts[strings.ToLower(candidate)]++
		keys = append(keys, ref)
	}
	sort.Strings(keys)
	used := map[string]bool{}
	for _, ref := range keys {
		r := records[ref]
		candidate := candidates[ref]
		lower := strings.ToLower(candidate)
		if counts[lower] > 1 || strings.EqualFold(path.Base(candidate), "index.md") {
			date := ""
			if at, err := time.Parse(time.RFC3339Nano, r.entry.Created); err == nil {
				date = at.In(zone).Format("2006-01-02") + " "
			}
			candidate = strings.TrimSuffix(candidate, ".md") + " - " + date + ref[:12] + ".md"
			if counts[strings.ToLower(candidate)] > 0 || used[strings.ToLower(candidate)] {
				candidate = path.Join(path.Dir(candidate), ref+".md")
			}
		}
		if used[strings.ToLower(candidate)] {
			return errConfig("compact note path collision")
		}
		used[strings.ToLower(candidate)] = true
		r.destination = candidate
	}
	return nil
}
