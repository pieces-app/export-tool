package exporter

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

const compactMentionsInline = 8
const compactMentionsRecent = 5

func compactMemoryLabel(kind string) string {
	switch kind {
	case "summary":
		return "Summary"
	case "profile":
		return "Profile"
	case "signal":
		return "Signal"
	case "signal-description":
		return "Signal description"
	}
	return ""
}

// Use rendered Markdown so embedded references count too. Navigation pages,
// properties, technical records, code examples and external links do not create
// connected-memory entries. Count a memory once even if it links repeatedly.
func compactCollectMentions(source *compactRecord, document []byte, destinations map[string]*compactRecord) {
	if compactMemoryLabel(source.kind) == "" || source.destination == "" {
		return
	}
	seen := map[*compactRecord]bool{}
	_ = ast.Walk(goldmark.DefaultParser().Parse(text.NewReader(obsidianBody(document))), func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		link, ok := n.(*ast.Link)
		if !enter || !ok {
			return ast.WalkContinue, nil
		}
		u, err := url.Parse(string(link.Destination))
		if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" || path.IsAbs(u.Path) {
			return ast.WalkContinue, nil
		}
		name := path.Join(path.Dir(source.destination), u.Path)
		target := destinations[name]
		if target != nil && target != source && target.kind == "connection" && !seen[target] {
			seen[target] = true
			target.mentions = append(target.mentions, source)
		}
		return ast.WalkContinue, nil
	})
}

func compactMentionEntries(r *compactRecord, zone *time.Location) []navigationEntry {
	if zone == nil {
		zone = time.UTC
	}
	memories := append([]*compactRecord(nil), r.mentions...)
	sort.Slice(memories, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, memories[i].entry.Created)
		b, _ := time.Parse(time.RFC3339Nano, memories[j].entry.Created)
		if !a.Equal(b) {
			return a.After(b)
		}
		return memories[i].destination < memories[j].destination
	})
	list := make([]navigationEntry, 0, len(memories))
	for _, m := range memories {
		detail := compactMemoryLabel(m.kind)
		if _, err := time.Parse(time.RFC3339Nano, m.entry.Created); err == nil {
			detail += " · Created: " + displayTimestamp(m.entry.Created, zone)
		} else {
			detail += " · Creation date unavailable"
		}
		list = append(list, navigationEntry{label: m.entry.Title, path: m.destination, detail: detail})
	}
	return list
}

func compactMentionIndexPath(r *compactRecord) string {
	return strings.TrimSuffix(r.destination, ".md") + ".memories/index.md"
}

func compactConnectionNavigation(r *compactRecord, zone *time.Location) string {
	var b strings.Builder
	b.WriteString("\n## Connected memories\n\n")
	list := compactMentionEntries(r, zone)
	if len(list) == 0 {
		b.WriteString("No retained summary, profile or signal notes link to this record.\n\n")
		return b.String()
	}
	if len(list) == 1 {
		b.WriteString("1 connected memory.\n\n")
	} else {
		fmt.Fprintf(&b, "%d connected memories. Newest-created first.\n\n", len(list))
	}
	visible := list
	if len(list) > compactMentionsInline {
		visible = list[:compactMentionsRecent]
	}
	for _, item := range visible {
		b.WriteString(item.markdown(r.destination))
	}
	if len(visible) < len(list) {
		fmt.Fprintf(&b, "\n[Browse all %d connected memories](%s)\n", len(list), relative(r.destination, compactMentionIndexPath(r)))
	}
	b.WriteString("\nThe Backlinks panel also includes navigation pages and any links added after export.\n\n")
	return b.String()
}

func compactConnectionIndex(run *run, r *compactRecord, zone *time.Location) error {
	if len(r.mentions) <= compactMentionsInline {
		return nil
	}
	name := compactMentionIndexPath(r)
	header := "---\npieces_export: 1\npieces_kind: navigation\ntags: [pieces/navigation]\n---\n\n# Connected memories: " + md(r.entry.Title) + "\n\n[Back to " + md(navigationLabel(r.entry.Title)) + "](" + relative(name, r.destination) + ")\n\nRetained summary, profile and signal notes that link to this record. Newest-created first.\n\n"
	return run.writeNavigationIndex(name, header, compactMentionEntries(r, zone))
}
