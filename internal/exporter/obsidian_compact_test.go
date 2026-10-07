package exporter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Check the browsing view against canonical evidence independently of the
// planner and its timeline selection. The ordinary archive check verifies the
// preserved parent; this check catches missing bodies in the smaller vault.
func inspectCompactBodies(source string) (map[string]int, error) {
	counts := map[string]int{}
	m, err := InspectArchive(source)
	if err != nil || m.Obsidian == nil || m.Obsidian.Layout != "compact" {
		return counts, errConfig("compact archive required")
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return counts, err
	}
	defer root.Close()
	b, err := archiveRead(root, "vault/record-map.json", 64<<20)
	if err != nil {
		return counts, err
	}
	mapping := map[string]string{}
	if err := json.Unmarshal(b, &mapping); err != nil {
		return counts, err
	}
	values := map[string]map[string]any{}
	metas := map[string]*Meta{}
	docs := map[string][]byte{}
	selected := map[string]bool{}
	for _, typ := range []string{"WORKSTREAM_SUMMARIES", "ANNOTATIONS", "PERSONS", "TAGS", "WORKSTREAM_PATTERN_ENGINE_SOURCES", "APPLICATIONS", "WEBSITES", "PIPELINES", "SIGNALS"} {
		material, _ := materialByType(typ)
		prefix := "data/"
		if m.Mode == "preserve" {
			prefix = "raw/"
		}
		if _, err := root.Stat(prefix + material.Folder); os.IsNotExist(err) {
			continue
		}
		err := archiveCollection(context.Background(), root, material, m.Mode, m.FormatVersion, func(ref string, b []byte, _ string, _ int64) error {
			v := map[string]any{}
			if json.Unmarshal(b, &v) != nil {
				return errConfig("invalid canonical record")
			}
			values[ref] = v
			annotation := fieldString(v, "type")
			keep := typ != "ANNOTATIONS" || annotation == "PROFILE_DESCRIPTION" || annotation == "HIERARCHICAL_PROFILE_SUMMARY" || annotation == "SIGNAL_DESCRIPTION"
			if !keep {
				if mapping[ref] != "" {
					return errConfig("technical annotation became a browsing note")
				}
				return nil
			}
			name := mapping[ref]
			if name == "" || !safeArchivePath(name) {
				return errConfig("retained browsing record absent from map")
			}
			doc, err := archiveRead(root, "vault/"+name, 64<<20)
			if err != nil {
				return errConfig("retained browsing note missing")
			}
			selected[ref] = true
			docs[ref] = doc
			id := fieldString(v, "id")
			metas[typ+"\x00"+id] = &Meta{Type: typ, ID: id, State: "included", Path: name}
			counts[typ]++
			return nil
		})
		if err != nil {
			return counts, err
		}
	}
	if len(selected) != len(mapping) {
		return counts, errConfig("unexpected compact identity")
	}
	archivePaths := map[string]string{}
	b, err = archiveRead(root, "link-map.json", 64<<20)
	if err != nil {
		return counts, err
	}
	archiveMap := map[string]string{}
	if err := json.Unmarshal(b, &archiveMap); err != nil {
		return counts, err
	}
	for ref, name := range archiveMap {
		if values[ref] != nil {
			archivePaths[name] = ref
		}
	}
	for ref := range selected {
		v := values[ref]
		typ := fieldString(v, "type")
		if typ != "PROFILE_DESCRIPTION" && typ != "HIERARCHICAL_PROFILE_SUMMARY" && typ != "SIGNAL_DESCRIPTION" {
			continue
		}
		body := fieldString(v, "text")
		if strings.TrimSpace(body) == "" {
			continue
		}
		want := escapeObsidianWikis([]byte(rewriteMarkdown(body, mapping[ref], metas)))
		if !bytes.Contains(docs[ref], want) {
			return counts, errConfig("profile or signal description body differs")
		}
		counts["verified_profile_or_signal_bodies"]++
	}
	f, err := archiveOpen(root, "relationships.jsonl")
	if err != nil {
		return counts, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64<<10), 4<<20)
	for s.Scan() {
		var edge PublicEdge
		if json.Unmarshal(s.Bytes(), &edge) != nil {
			return counts, errConfig("invalid graph record")
		}
		if edge.SourceRef == "" {
			edge.SourceRef = archivePaths[edge.Source]
		}
		if edge.TargetRef == "" {
			edge.TargetRef = archivePaths[edge.Target]
		}
		if edge.Relation != "annotations" || mapping[edge.SourceRef] == "" {
			continue
		}
		annotation := values[edge.TargetRef]
		if annotation == nil {
			continue
		}
		kind := fieldString(annotation, "type")
		if kind == "PROFILE_DESCRIPTION" || kind == "HIERARCHICAL_PROFILE_SUMMARY" || kind == "SIGNAL_DESCRIPTION" {
			// Long person histories can link through a paginated history index;
			// summary and signal attachment links must be direct.
			v := values[edge.SourceRef]
			id := fieldString(v, "id")
			if metas["WORKSTREAM_SUMMARIES\x00"+id] == nil && metas["SIGNALS\x00"+id] == nil {
				continue
			}
			link := relative(mapping[edge.SourceRef], mapping[edge.TargetRef])
			if !acceptanceMarkdownLinks(obsidianBody(docs[edge.SourceRef]))[link] {
				return counts, errConfig("retained report attachment not linked")
			}
			counts["verified_report_links"]++
			continue
		}
		id := fieldString(values[edge.SourceRef], "id")
		if metas["WORKSTREAM_SUMMARIES\x00"+id] == nil {
			continue
		}
		body := fieldString(annotation, "text")
		if strings.TrimSpace(body) == "" {
			continue
		}
		want := escapeObsidianWikis([]byte(rewriteMarkdown(body, mapping[edge.SourceRef], metas)))
		if !bytes.Contains(docs[edge.SourceRef], want) {
			return counts, errConfig("retained summary attachment body differs")
		}
		counts["verified_summary_attachment_bodies"]++
	}
	return counts, s.Err()
}

func TestObsidianCompactAcceptanceDetectsBodyLoss(t *testing.T) {
	dir, _ := obsidianFixture(t, "obsidian")
	counts, err := inspectCompactBodies(dir)
	if err != nil || counts["WORKSTREAM_SUMMARIES"] != 1 || counts["verified_profile_or_signal_bodies"] != 1 || counts["verified_summary_attachment_bodies"] == 0 {
		t.Fatal(counts, err)
	}
	var mapping map[string]string
	b, _ := os.ReadFile(filepath.Join(dir, "vault/record-map.json"))
	_ = json.Unmarshal(b, &mapping)
	file := filepath.Join(dir, "vault", mapping[opaque("WORKSTREAM_SUMMARIES", "summary")])
	b, _ = os.ReadFile(file)
	b = bytes.ReplaceAll(b, []byte("Actual summary narrative"), []byte("Lost body"))
	if err := os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectCompactBodies(dir); err == nil {
		t.Fatal("accepted missing browsing body")
	}
}

func TestLiveCompactObsidianAcceptance(t *testing.T) {
	dir := os.Getenv("PIECES_EXPORT_LIVE_COMPACT_ARCHIVE")
	if dir == "" {
		t.Skip("set PIECES_EXPORT_LIVE_COMPACT_ARCHIVE for read-only compact body acceptance")
	}
	counts, err := inspectCompactBodies(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(counts)
	t.Log(string(b))
}

func TestObsidianCompactReadableNamesAvoidCollisions(t *testing.T) {
	records := map[string]*compactRecord{}
	for id, title := range map[string]string{"one": "Daily Review", "two": "daily review", "index": "index", "con": "CON", "plain": "Useful topic"} {
		records[opaque("TAGS", id)] = &compactRecord{entry: TimelineEntry{Type: "TAGS", ID: id, Title: title, Created: "2026-10-01T01:00:00Z"}, kind: "connection", destination: "connections/topics/" + id + ".md"}
	}
	zone, _ := time.LoadLocation("America/New_York")
	if err := compactNotePaths(records, "readable", zone, nil); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range records {
		name := strings.ToLower(r.destination)
		if seen[name] || strings.HasSuffix(name, "/index.md") || strings.HasSuffix(name, "/con.md") {
			t.Fatal("unsafe/duplicate name", name)
		}
		seen[name] = true
		if (r.entry.ID == "one" || r.entry.ID == "two") && !strings.Contains(name, "2026-09-30") {
			t.Fatal("disambiguation date ignored timezone")
		}
	}
	if records[opaque("TAGS", "plain")].destination != "connections/topics/Useful topic.md" {
		t.Fatal("unique readable title unnecessarily decorated")
	}
}

func TestObsidianCompactReportHeadingFormatting(t *testing.T) {
	for raw, want := range map[string]string{"### Observational Profile: Alex": "Observational Profile: Alex", "**Alex** is an engineer": "Alex is an engineer", "C# and C++": "C# and C++", "# Topic `code`": "Topic code"} {
		if got := compactDisplayTitle(raw); got != want {
			t.Fatalf("title: %q, want %q", got, want)
		}
	}
	if !compactHasHeading("\n### Profile\n\nBody") || compactHasHeading("**Alex** is an engineer.") {
		t.Fatal("report heading classification")
	}
}
