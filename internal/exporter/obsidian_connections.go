package exporter

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

const obsidianRelatedLimit = 8
const obsidianNavStart = "<!-- pieces-obsidian-navigation:start -->\n"
const obsidianNavEnd = "<!-- pieces-obsidian-navigation:end -->\n\n"

type obsidianConnection struct {
	Entry             TimelineEntry
	Path, Group, Kind string
	Mentions          int
}

func obsidianEntity(typ string) (group, kind string) {
	switch typ {
	case "PERSONS":
		return "people", "person"
	case "TAGS":
		return "topics", "tag"
	case "WORKSTREAM_PATTERN_ENGINE_SOURCES", "APPLICATIONS":
		return "sources", "source"
	case "WEBSITES":
		return "websites", "website"
	case "PIPELINES":
		return "pipelines", "pipeline"
	case "SIGNALS":
		return "signals", "signal"
	}
	return "", ""
}

// The chronological stream deliberately omits undated records. Recover their
// identities from the existing link map and canonical JSON, without inventing
// dates or relying on display names. Grouped records are not individual notes.
func (p *obsidianPlan) undatedEntries(root *os.Root) error {
	f, err := archiveOpen(root, "link-map.json")
	if err != nil {
		return err
	}
	defer f.Close()
	var links map[string]string
	if err := json.NewDecoder(f).Decode(&links); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, name := range links {
		counts[name]++
	}
	for ref, name := range links {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		if _, ok := p.entries[name]; ok || p.shared[name] || counts[name] != 1 {
			continue
		}
		if !safeArchivePath(name) {
			return errConfig("invalid Obsidian link-map path")
		}
		data, err := archiveRead(root, name, 64<<20)
		if err != nil {
			return err
		}
		data = obsidianBody(data)
		match := obsidianType.FindSubmatch(data)
		if len(match) != 2 {
			continue
		}
		typ := string(match[1])
		group, _ := obsidianEntity(typ)
		if group == "" && typ != "WORKSTREAM_SUMMARIES" && typ != "ANNOTATIONS" && typ != "RANGES" {
			continue
		}
		canonical := ""
		tree := goldmark.DefaultParser().Parse(text.NewReader(data))
		_ = ast.Walk(tree, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
			if link, ok := n.(*ast.Link); ok && enter && string(n.Text(data)) == "Record JSON" {
				canonical = obsidianLocalTarget(name, link)
				return ast.WalkStop, nil
			}
			return ast.WalkContinue, nil
		})
		if (!strings.HasPrefix(canonical, "data/") && !strings.HasPrefix(canonical, "raw/")) || path.Ext(canonical) != ".json" {
			return errConfig("undated Obsidian record lacks canonical identity")
		}
		data, err = archiveRead(root, canonical, 64<<20)
		if err != nil {
			return err
		}
		var record map[string]any
		if json.Unmarshal(data, &record) != nil {
			return errConfig("invalid undated canonical record")
		}
		id := fieldString(record, "id")
		if id == "" || opaque(typ, id) != ref {
			return errConfig("undated Obsidian identity mismatch")
		}
		p.entries[name] = TimelineEntry{Type: typ, ID: id, Path: name, Title: title(record, Material{Type: typ}), Updated: timestamp(record, "updated"), TimeBasis: "undated"}
	}
	return nil
}

func (p *obsidianPlan) planConnections(naming string) error {
	p.connections = map[string]*obsidianConnection{}
	destinations := map[string]bool{}
	for source, e := range p.entries {
		group, kind := obsidianEntity(e.Type)
		if group == "" || e.ID == "" {
			continue
		}
		label := safeTitle(e.Title, 72)
		if naming == "opaque" {
			label = kind
		}
		dest := "connections/" + group + "/" + label + "." + opaque(e.Type, e.ID)[:12] + ".md"
		if destinations[dest] {
			return errConfig("connection note path collision")
		}
		destinations[dest] = true
		p.connections[source] = &obsidianConnection{Entry: e, Path: dest, Group: group, Kind: kind}
	}
	// A connection note is a view of one existing record, never a new record.
	for _, c := range p.connections {
		e := c.Entry
		e.Path = c.Path
		p.entries[c.Path] = e
	}
	return nil
}

func obsidianLocalTarget(from string, link *ast.Link) string {
	u, err := url.Parse(string(link.Destination))
	if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" {
		return ""
	}
	target := path.Clean(path.Join(path.Dir(from), u.Path))
	if !safeArchivePath(target) {
		return ""
	}
	return target
}

type obsidianPeer struct {
	entry      TimelineEntry
	dimensions int
	date       time.Time
}

var obsidianDimensionScore = regexp.MustCompile(`\b([1-4])/4 dimensions\b`)

// Read only the already-filtered, already-ranked related-summary sections.
// An absent sidecar is valid when ordinary export uses inline relationships.
func (p *obsidianPlan) related(root *os.Root, from string, body []byte) ([]obsidianPeer, error) {
	sidecar := strings.TrimSuffix(from, ".md") + ".relationships_graph.md"
	data, err := archiveRead(root, sidecar, 64<<20)
	if err != nil {
		if _, statErr := root.Lstat(sidecar); !os.IsNotExist(statErr) {
			return nil, err
		}
		data = body
		sidecar = from
	}
	data = obsidianBody(data)
	tree := goldmark.DefaultParser().Parse(text.NewReader(data))
	candidates := map[string]obsidianPeer{}
	inSection := false
	_ = ast.Walk(tree, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if !enter {
			return ast.WalkContinue, nil
		}
		if h, ok := n.(*ast.Heading); ok {
			inSection = false
			if h.Level == 4 {
				for _, d := range dimensions {
					if string(h.Text(data)) == "Related Summaries by "+d {
						inSection = true
					}
				}
			}
		}
		link, ok := n.(*ast.Link)
		if !ok || !inSection {
			return ast.WalkContinue, nil
		}
		target := obsidianLocalTarget(sidecar, link)
		e, ok := p.entries[target]
		if !ok || e.Type != "WORKSTREAM_SUMMARIES" || target == from {
			return ast.WalkContinue, nil
		}
		score := obsidianDimensionScore.FindSubmatch(n.Parent().Text(data))
		if len(score) != 2 {
			return ast.WalkContinue, nil
		}
		strength, _ := strconv.Atoi(string(score[1]))
		stamp, _ := time.Parse(time.RFC3339Nano, e.Created)
		if prior, ok := candidates[target]; !ok || strength > prior.dimensions {
			candidates[target] = obsidianPeer{e, strength, stamp}
		}
		return ast.WalkContinue, nil
	})
	peers := make([]obsidianPeer, 0, len(candidates))
	for _, peer := range candidates {
		peers = append(peers, peer)
	}
	sort.Slice(peers, func(i, j int) bool {
		a, b := peers[i], peers[j]
		if p.relatedOrder != "recent" && a.dimensions != b.dimensions {
			return a.dimensions > b.dimensions
		}
		if !a.date.Equal(b.date) {
			return a.date.After(b.date)
		}
		return a.entry.ID < b.entry.ID
	})
	return peers[:min(len(peers), obsidianRelatedLimit)], nil
}

func removeObsidianNavigation(body []byte) ([]byte, error) {
	if !bytes.HasPrefix(body, []byte(obsidianNavStart)) {
		return body, nil
	}
	end := bytes.Index(body, []byte(obsidianNavEnd))
	if end < 0 {
		return nil, errConfig("incomplete Obsidian navigation block")
	}
	return body[end+len(obsidianNavEnd):], nil
}
