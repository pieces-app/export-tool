package exporter

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

type vaultGraphNode struct {
	path, kind, typ string
	bytes           int
	links           []int
}
type vaultGraphStats struct {
	Nodes             int `json:"nodes"`
	DirectedLinks     int `json:"directed_links"`
	Components        int `json:"components"`
	LargestComponent  int `json:"largest_component"`
	Isolated          int `json:"isolated_notes"`
	IsolatedSummaries int `json:"isolated_summaries"`
	DegreeMedian      int `json:"undirected_degree_median"`
	DegreeP95         int `json:"undirected_degree_p95"`
	DegreeMax         int `json:"undirected_degree_max"`
}

func vaultGraphMeasure(nodes []vaultGraphNode, include func(vaultGraphNode) bool) vaultGraphStats {
	parents := make([]int, len(nodes))
	adj := make([]map[int]bool, len(nodes))
	s := vaultGraphStats{}
	for i, n := range nodes {
		parents[i] = i
		if include(n) {
			s.Nodes++
			adj[i] = map[int]bool{}
		}
	}
	var find func(int) int
	find = func(i int) int {
		if parents[i] != i {
			parents[i] = find(parents[i])
		}
		return parents[i]
	}
	for i, n := range nodes {
		if adj[i] == nil {
			continue
		}
		for _, j := range n.links {
			if i == j || adj[j] == nil {
				continue
			}
			s.DirectedLinks++
			parents[find(i)] = find(j)
			adj[i][j] = true
			adj[j][i] = true
		}
	}
	components := map[int]int{}
	degrees := []int{}
	for i, n := range nodes {
		if adj[i] == nil {
			continue
		}
		components[find(i)]++
		degrees = append(degrees, len(adj[i]))
		if len(adj[i]) == 0 {
			s.Isolated++
			if n.kind == "summary" {
				s.IsolatedSummaries++
			}
		}
	}
	s.Components = len(components)
	for _, size := range components {
		if size > s.LargestComponent {
			s.LargestComponent = size
		}
	}
	sort.Ints(degrees)
	if len(degrees) > 0 {
		s.DegreeMedian = degrees[len(degrees)/2]
		s.DegreeP95 = degrees[(len(degrees)-1)*95/100]
		s.DegreeMax = degrees[len(degrees)-1]
	}
	return s
}

// Read-only acceptance aid: compare actual note links, not just canonical graph
// rows. No note names, source text or identities are logged.
func TestObsidianLiveGraphAudit(t *testing.T) {
	vault := os.Getenv("PIECES_EXPORT_OBSIDIAN_AUDIT")
	if vault == "" {
		t.Skip("set PIECES_EXPORT_OBSIDIAN_AUDIT to an Obsidian vault")
	}
	nodes := []vaultGraphNode{}
	ids := map[string]int{}
	err := filepath.WalkDir(vault, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if path.Ext(file) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(vault, file)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		ids[rel] = len(nodes)
		nodes = append(nodes, vaultGraphNode{path: rel})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	bytesByKind := map[string]int64{}
	over256KiB := 0
	over1MiB := 0
	pairs := map[string]int{}
	missing := 0
	summaryWithDirectPeer := 0
	summaryWithPerson := 0
	summaryWithTag := 0
	summaryWithSource := 0
	summaryWithConnection := 0
	for i := range nodes {
		n := &nodes[i]
		b, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(n.path)))
		if err != nil {
			t.Fatal(err)
		}
		n.bytes = len(b)
		header := b
		if at := bytes.Index(b[4:], []byte("\n---\n")); at >= 0 {
			header = b[4 : 4+at]
		}
		for _, line := range bytes.Split(header, []byte("\n")) {
			k, v, ok := bytes.Cut(line, []byte(": "))
			if !ok {
				continue
			}
			switch string(k) {
			case "pieces_kind":
				_ = json.Unmarshal(v, &n.kind)
			case "pieces_type":
				_ = json.Unmarshal(v, &n.typ)
			}
		}
		if n.kind == "support" {
			switch n.typ {
			case "WORKSTREAM_PATTERN_ENGINE_SOURCES", "APPLICATIONS":
				n.kind = "source"
			case "WEBSITES":
				n.kind = "website"
			case "PIPELINES":
				n.kind = "pipeline"
			}
		}
		if n.kind == "" {
			n.kind = "navigation"
		}
		kinds[n.kind]++
		bytesByKind[n.kind] += int64(len(b))
		if len(b) > 256<<10 {
			over256KiB++
		}
		if len(b) > 1<<20 {
			over1MiB++
		}
		unique := map[int]bool{}
		tree := goldmark.DefaultParser().Parse(text.NewReader(obsidianBody(b)))
		_ = ast.Walk(tree, func(node ast.Node, enter bool) (ast.WalkStatus, error) {
			if !enter {
				return ast.WalkContinue, nil
			}
			link, ok := node.(*ast.Link)
			if !ok {
				return ast.WalkContinue, nil
			}
			u, err := url.Parse(string(link.Destination))
			if err != nil || u.IsAbs() || u.Host != "" || path.Ext(u.Path) != ".md" {
				return ast.WalkContinue, nil
			}
			dest := path.Clean(path.Join(path.Dir(n.path), u.Path))
			j, ok := ids[dest]
			if !ok {
				missing++
			} else if j != i {
				unique[j] = true
			}
			return ast.WalkContinue, nil
		})
		for j := range unique {
			n.links = append(n.links, j)
		}
		sort.Ints(n.links)
	}
	for i, n := range nodes {
		seen := map[string]bool{}
		for _, j := range n.links {
			kind := nodes[j].kind
			pairs[n.kind+" -> "+kind]++
			if j != i {
				seen[kind] = true
			}
		}
		if n.kind == "summary" {
			if seen["connection"] {
				summaryWithConnection++
			}
			if seen["summary"] {
				summaryWithDirectPeer++
			}
			if seen["person"] {
				summaryWithPerson++
			}
			if seen["tag"] {
				summaryWithTag++
			}
			if seen["source"] {
				summaryWithSource++
			}
		}
	}
	old := func(n vaultGraphNode) bool {
		return strings.HasPrefix(n.path, "workstream_summaries/") && n.kind != "navigation" && n.kind != "relationships"
	}
	semantic := func(n vaultGraphNode) bool {
		switch n.kind {
		case "summary", "profile", "person", "tag", "source", "website", "pipeline", "signal":
			return true
		}
		return false
	}
	curated := func(n vaultGraphNode) bool {
		return n.kind == "summary" || n.kind == "profile" || n.kind == "signal" || n.kind == "connection"
	}
	report := map[string]any{"notes": len(nodes), "kinds": kinds, "markdown_bytes_by_kind": bytesByKind, "notes_over_256kib": over256KiB, "notes_over_1mib": over1MiB, "missing_markdown_targets": missing, "direct_links_by_kind": pairs, "summaries_with_direct_summary_links": summaryWithDirectPeer, "summaries_with_direct_person_links": summaryWithPerson, "summaries_with_direct_tag_links": summaryWithTag, "summaries_with_direct_source_links": summaryWithSource, "original_default_graph": vaultGraphMeasure(nodes, old), "semantic_graph": vaultGraphMeasure(nodes, semantic), "all_notes_graph": vaultGraphMeasure(nodes, func(vaultGraphNode) bool { return true })}
	report["curated_graph"] = vaultGraphMeasure(nodes, curated)
	report["summaries_with_direct_connection_links"] = summaryWithConnection
	encoded, _ := json.MarshalIndent(report, "", "  ")
	if dest := os.Getenv("PIECES_EXPORT_OBSIDIAN_AUDIT_REPORT"); dest != "" {
		if err := os.WriteFile(dest, append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Log(string(encoded))
	if missing > 0 {
		t.Fatal("missing local Markdown destinations")
	}
}
