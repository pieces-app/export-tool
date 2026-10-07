package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func obsidianProperties(t *testing.T, b []byte) map[string]any {
	t.Helper()
	end := bytes.Index(b[4:], []byte("\n---\n"))
	if !bytes.HasPrefix(b, []byte("---\n")) || end < 0 {
		t.Fatal("missing frontmatter")
	}
	var p map[string]any
	if err := yaml.Unmarshal(b[4:4+end], &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestObsidianPropertiesAndWikiLiterals(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	zone, _ := time.LoadLocation("America/New_York")
	p := &obsidianPlan{entries: map[string]TimelineEntry{"note.md": {Type: "TAGS", ID: "tag", Title: "Topic: \"Café\" [[literal]]\nnext", Created: "2025-04-23T15:42:26.392376Z"}}, zone: zone}
	body := []byte("# A title\n\nLiteral [[missing]] and ![[embed]] and \\[[escaped]]. [Valid](index.md)\n\n`[[inline code]]`\n\n```md\n[[fenced code]]\n```\n\n    [[indented code]]\n")
	b, err := p.note(root, "note.md", body)
	if err != nil {
		t.Fatal(err)
	}
	props := obsidianProperties(t, b)
	if props["created"] != "2025-04-23T11:42:26.392376-04:00" || props["pieces_kind"] != "tag" {
		t.Fatal(props)
	}
	if strings.Contains(string(b[:bytes.Index(b[4:], []byte("\n---\n"))+4]), "[[") {
		t.Fatal("phantom link in property")
	}
	want := strings.ReplaceAll(strings.ReplaceAll(string(body), "Literal [[missing]]", `Literal \[\[missing]]`), "![[embed]]", `!\[\[embed]]`)
	if string(bytes.TrimPrefix(obsidianBody(b), []byte("\n"))) != want {
		t.Fatalf("changed code/link or left phantom wiki link: %s", b)
	}
	twice, err := p.note(root, "note.md", b)
	if err != nil || !bytes.Equal(b, twice) {
		t.Fatal("not idempotent", err)
	}
	if _, err := p.note(root, "note.md", []byte("---\nuser: true\n---\ntext")); err == nil {
		t.Fatal("discarded user properties")
	}
	for in, want := range map[string]string{"Product Planning": "topic/product-planning", "Café / 東京": "topic/café-東京", "123": "topic/123", "!!!": ""} {
		if got := obsidianTag("topic", in); got != want {
			t.Fatalf("%q -> %q", in, got)
		}
	}
	if obsidianTag("topic", strings.Repeat("x", 200)+"a") == obsidianTag("topic", strings.Repeat("x", 200)+"b") {
		t.Fatal("long labels collided")
	}
}

func obsidianFixture(t *testing.T, format string) (string, Manifest) {
	t.Helper()
	f := summaryScopeFixture()
	f.data["ANNOTATIONS"][0]["text"] = "Actual summary narrative with literal [[unresolved]] and [a person](pieces://persons/person)."
	f.data["ANNOTATIONS"][1]["text"] = "### Profile: **Example Person**\n\nRetained persona history."
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	sel, _ := SelectScope("summaries", "")
	out := filepath.Join(t.TempDir(), "source")
	m, err := Export(context.Background(), c, Options{Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, Output: out, Mode: "filtered", Timezone: "America/New_York", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Format: format, Metadata: "off", PeopleMode: "profiles"})
	if err != nil {
		t.Fatal(err)
	}
	assertSummaryScopeRequests(t, f)
	return out, m
}

func verifyObsidianFixture(t *testing.T, dir string) {
	t.Helper()
	var links map[string]string
	raw, err := os.ReadFile(filepath.Join(dir, obsidianVault, "record-map.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &links); err != nil {
		t.Fatal(err)
	}
	summary := links[opaque("WORKSTREAM_SUMMARIES", "summary")]
	doc, err := os.ReadFile(filepath.Join(dir, obsidianVault, summary))
	if err != nil {
		t.Fatal(err)
	}
	props := obsidianProperties(t, doc)
	if props["pieces_type"] != "WORKSTREAM_SUMMARIES" || props["created"] != "2026-09-29T08:00:00-04:00" {
		t.Fatal(props)
	}
	tags := props["tags"].([]any)
	for _, wanted := range []string{"topic/topic-0", "topic/topic-52", "source/example-editor", "person/example-person", "pieces/summary"} {
		found := false
		for _, tag := range tags {
			if tag == wanted {
				found = true
			}
		}
		if !found {
			t.Fatal("missing source-derived tag", wanted)
		}
	}
	if len(props["pieces_tags"].([]any)) != 53 {
		t.Fatal("original tag labels not retained")
	}
	if !bytes.Contains(doc, []byte("Actual summary narrative")) || bytes.Contains(doc, []byte("Retained persona history.")) {
		t.Fatal("summary body missing or profile inlined")
	}
	profile, err := os.ReadFile(filepath.Join(dir, obsidianVault, links[opaque("ANNOTATIONS", "profile")]))
	if err != nil || bytes.Count(obsidianBody(profile), []byte("Profile:")) != 1 || bytes.Contains(profile, []byte(`# \#`)) {
		t.Fatal("profile heading duplicated or escaped as literal markup", err)
	}
	if _, err := os.Stat(filepath.Join(dir, obsidianVault, ".obsidian/core-plugins.json")); err != nil {
		t.Fatal(err)
	}
	graphBytes, err := os.ReadFile(filepath.Join(dir, obsidianVault, ".obsidian/graph.json"))
	var graph map[string]any
	if err != nil || json.Unmarshal(graphBytes, &graph) != nil || graph["search"] != obsidianGraphSearch || !strings.Contains(obsidianGraphSearch, "tag:pieces/signal") {
		t.Fatal("graph filter must include named connections and selected signals")
	}
	count, err := inspectFinalArchive(context.Background(), dir)
	if err != nil || count.SummariesWithBody != 1 || count.PersonsWithProfile != 1 {
		t.Fatal("archive/body/graph acceptance", count, err)
	}
}

func TestObsidianExportConvertRebuildAndRelocate(t *testing.T) {
	source, original := obsidianFixture(t, "markdown")
	before := archiveHashes(t, source)
	vault := filepath.Join(t.TempDir(), "vault")
	m, err := ConvertObsidian(context.Background(), source, vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != "obsidian" || m.Obsidian == nil || m.Obsidian.Notes == 0 || m.Status != original.Status || !reflect.DeepEqual(m.Coverage, original.Coverage) {
		t.Fatal("lost coverage", m.Obsidian)
	}
	if !reflect.DeepEqual(before, archiveHashes(t, source)) {
		t.Fatal("source mutated")
	}
	after := archiveHashes(t, vault)
	for path, hash := range before {
		if strings.HasPrefix(path, "data/") || strings.HasPrefix(path, "timeline/") && !strings.HasSuffix(path, ".md") || path == "relationships.jsonl" || path == "link-map.json" || path == "archive-state.jsonl" {
			if after[path] != hash {
				t.Fatal("canonical evidence changed", path)
			}
		}
	}
	moved := vault + "-moved"
	if err := os.Rename(vault, moved); err != nil {
		t.Fatal(err)
	}
	verifyObsidianFixture(t, moved)
	again := filepath.Join(t.TempDir(), "second")
	if _, err := ConvertObsidian(context.Background(), moved, again, nil); err != nil {
		t.Fatal(err)
	}
	verifyObsidianFixture(t, again)
	rebuilt := filepath.Join(t.TempDir(), "rebuilt")
	if _, err := Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: rebuilt, Format: "obsidian", Scanner: scanner(t, DefaultPolicy())}}); err != nil {
		t.Fatal(err)
	}
	verifyObsidianFixture(t, rebuilt)
	direct, _ := obsidianFixture(t, "obsidian")
	verifyObsidianFixture(t, direct)
	if _, err := os.Stat(filepath.Join(direct, "pdf")); !os.IsNotExist(err) {
		t.Fatal("Obsidian invoked PDF rendering")
	}
}

func TestObsidianConversionFailureBoundaries(t *testing.T) {
	source, _ := obsidianFixture(t, "markdown")
	for _, name := range []string{"inside-source", "existing", "symlink", "missing-link", "cancel"} {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "vault")
			ctx := context.Background()
			switch name {
			case "inside-source":
				out = filepath.Join(source, "vault")
			case "existing":
				if err := os.Mkdir(out, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				link := filepath.Join(source, "outside.md")
				if err := os.Symlink(filepath.Join(t.TempDir(), "private"), link); err != nil {
					t.Skip(err)
				}
				defer os.Remove(link)
			case "missing-link":
				note := filepath.Join(source, "broken.md")
				if err := os.WriteFile(note, []byte("[missing](not-here.md)\n"), 0600); err != nil {
					t.Fatal(err)
				}
				defer os.Remove(note)
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := ConvertObsidian(ctx, source, out, nil)
			if err == nil {
				t.Fatal("invalid conversion finalized")
			}
			if name == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if name != "existing" {
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatal("published failed vault", err)
				}
			}
		})
	}
}

func TestObsidianSignalsRetainCoverageAndPrivacy(t *testing.T) {
	f, policy := signalDigestFixture()
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("SIGNALS,ANNOTATIONS,PERSONS,PIPELINES,WORKSTREAM_EVENTS,WEBSITES,WORKSTREAM_SUMMARIES,RANGES")
	source := filepath.Join(t.TempDir(), "source")
	original, err := Export(context.Background(), c, Options{Output: source, Mode: "filtered", Format: "obsidian", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy)})
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(t.TempDir(), "converted")
	m, err := ConvertObsidian(context.Background(), source, vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "partial" || !reflect.DeepEqual(m.SignalDigest, original.SignalDigest) || m.SignalDigest.Entries != 5 {
		t.Fatal("changed signal coverage", m.SignalDigest)
	}
	if counts, err := inspectCompactBodies(vault); err != nil || counts["SIGNALS"] != 5 || counts["verified_profile_or_signal_bodies"] < 2 || counts["verified_report_links"] < 2 {
		t.Fatal("compact signal descriptions or links lost", counts, err)
	}
	start, _ := os.ReadFile(filepath.Join(vault, obsidianVault, obsidianHome))
	if !bytes.Contains(start, []byte("signals/index.md")) {
		t.Fatal("signals undiscoverable")
	}
	err = filepath.WalkDir(vault, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(path, filepath.Join(vault, obsidianVault)+string(filepath.Separator)) {
			obsidianProperties(t, b)
		}
		if bytes.Contains(b, []byte(fakeSecret())) || bytes.Contains(b, []byte("Private signal narrative sentinel")) {
			t.Fatal("privacy regression")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range m.SignalDigest.Parts {
		info, err := os.Stat(filepath.Join(vault, part.Path))
		if err != nil || info.Size() != int64(part.Bytes) {
			t.Fatal("wrong signal document size", err)
		}
	}
}

func TestPackagedObsidianOfflineCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY")
	}
	source, _ := obsidianFixture(t, "markdown")
	output := filepath.Join(t.TempDir(), "vault")
	args := []string{"obsidian", "--source", source, "--output", output}
	cmd := exec.Command(binary, args...)
	b, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(b, []byte("Canceled")) {
		t.Fatal("EOF confirmation", err, string(b))
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("EOF created vault")
	}
	cmd = exec.Command(binary, append(args, "--yes")...)
	b, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(b))
	}
	verifyObsidianFixture(t, output)
}

func TestObsidianConnectionsShortenRoutesWithoutChangingEvidence(t *testing.T) {
	f := summaryScopeFixture()
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("peer-%02d", i)
		date := fmt.Sprintf("2026-09-%02dT12:00:00Z", i+1)
		peer := record(id, date)
		peer["name"] = "Nearby memory " + id
		peer["tags"], peer["sources"], peer["annotations"], peer["persons"], peer["pipelines"] = refs("tag-0"), refs("source"), refs(), refs(), refs()
		if i == 0 {
			peer["persons"] = refs("person")
		}
		f.data["WORKSTREAM_SUMMARIES"] = append(f.data["WORKSTREAM_SUMMARIES"], peer)
	}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	sel, _ := SelectScope("summaries", "")
	original := filepath.Join(t.TempDir(), "original")
	_, err := Export(context.Background(), c, Options{Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, Output: original, Mode: "filtered", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Format: "markdown", Metadata: "off", PeopleMode: "profiles", Relationships: "sidecar"})
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(t.TempDir(), "vault")
	m, err := ConvertObsidian(context.Background(), original, vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Obsidian.Version != 3 || m.Obsidian.ConnectionNotes != 55 || m.Obsidian.DirectRelatedLinks == 0 {
		t.Fatal("missing graph projection", m.Obsidian)
	}
	var links map[string]string
	b, _ := os.ReadFile(filepath.Join(vault, obsidianVault, "record-map.json"))
	if err := json.Unmarshal(b, &links); err != nil {
		t.Fatal(err)
	}
	from := links[opaque("WORKSTREAM_SUMMARIES", "summary")]
	doc, _ := os.ReadFile(filepath.Join(vault, obsidianVault, from))
	actual := acceptanceMarkdownLinks(obsidianBody(doc))
	want := relative(from, links[opaque("WORKSTREAM_SUMMARIES", "peer-00")])
	if !actual[want] {
		t.Fatal("strong older match lost to recent weak matches")
	}
	peers := 0
	for target := range actual {
		if strings.Contains(target, "Nearby%20memory") {
			peers++
		}
	}
	if peers != compactRelatedLimit {
		t.Fatal("shortlist not bounded", peers)
	}
	for _, typ := range []string{"PERSONS", "TAGS", "WORKSTREAM_PATTERN_ENGINE_SOURCES"} {
		id := map[string]string{"PERSONS": "person", "TAGS": "tag-0", "WORKSTREAM_PATTERN_ENGINE_SOURCES": "source"}[typ]
		connection := links[opaque(typ, id)]
		if connection == "" || !actual[relative(from, connection)] {
			t.Fatal("missing direct connection", typ)
		}
		view, err := os.ReadFile(filepath.Join(vault, obsidianVault, connection))
		if err != nil {
			t.Fatal(err)
		}
		props := obsidianProperties(t, view)
		if props["pieces_id"] != id || props["pieces_kind"] != "connection" {
			t.Fatal("connection identity wrong")
		}
	}
	again := filepath.Join(t.TempDir(), "again")
	second, err := ConvertObsidian(context.Background(), vault, again, nil)
	if err != nil {
		t.Fatal(err)
	}
	reDoc, _ := os.ReadFile(filepath.Join(again, obsidianVault, from))
	if !bytes.Equal(reDoc, doc) || second.Obsidian.ConnectionNotes != m.Obsidian.ConnectionNotes || second.Obsidian.Notes != m.Obsidian.Notes {
		t.Fatal("repeat conversion duplicates shortcuts/connections")
	}
	for _, file := range []string{"link-map.json", "relationships.jsonl", "rebuild-state.jsonl", "timeline/records.jsonl"} {
		a, _ := os.ReadFile(filepath.Join(original, file))
		b, _ := os.ReadFile(filepath.Join(vault, file))
		if !bytes.Equal(a, b) {
			t.Fatal("changed archive evidence", file)
		}
	}
	if _, err := inspectFinalArchive(context.Background(), vault); err != nil {
		t.Fatal(err)
	}
}

func TestObsidianConnectionsOpaqueNamingAndIdentity(t *testing.T) {
	p := &obsidianPlan{entries: map[string]TimelineEntry{
		"a.md": {Type: "PERSONS", ID: "a", Title: "Same Name"},
		"b.md": {Type: "PERSONS", ID: "b", Title: "Same Name"},
		"c.md": {Type: "TAGS", ID: "c", Title: "CON"},
	}}
	if err := p.planConnections("opaque"); err != nil {
		t.Fatal(err)
	}
	if p.connections["a.md"].Path == p.connections["b.md"].Path || strings.Contains(p.connections["a.md"].Path, "Same") {
		t.Fatal("identity collision or opaque name disclosure")
	}
	if got := obsidianKind("workstream_summaries/personas/profile_summaries/index.pages/page-000001.md", ""); got != "navigation" {
		t.Fatal("pagination polluted profile graph", got)
	}
}

func TestObsidianRelatedRespectsOrderInlineSectionsAndIdentity(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	p := &obsidianPlan{entries: map[string]TimelineEntry{
		"old.md":      {Type: "WORKSTREAM_SUMMARIES", ID: "old", Path: "old.md", Created: "2026-09-01T00:00:00Z"},
		"new.md":      {Type: "WORKSTREAM_SUMMARIES", ID: "new", Path: "new.md", Created: "2026-10-01T00:00:00Z"},
		"source.md":   {Type: "WORKSTREAM_SUMMARIES", ID: "self", Path: "source.md"},
		"unranked.md": {Type: "WORKSTREAM_SUMMARIES", ID: "unranked", Path: "unranked.md"},
	}}
	body := []byte("# A summary\n\n[unranked](unranked.md) — 4/4 dimensions\n\n#### Related Summaries by Tags\n\n- [old](old.md) — 3/4 dimensions\n- [new](new.md) — 1/4 dimensions\n- [self](source.md) — 4/4 dimensions\n- [unavailable](missing.md) — 4/4 dimensions\n\n#### Related Summaries by Source\n\n- [old again](old.md) — 2/4 dimensions\n\n## Unrelated section\n\n[unranked](unranked.md) — 4/4 dimensions\n")
	peers, err := p.related(root, "source.md", body)
	if err != nil || len(peers) != 2 || peers[0].entry.ID != "old" || peers[0].dimensions != 3 {
		t.Fatal("inline ranking/deduplication", peers, err)
	}
	p.relatedOrder = "recent"
	peers, err = p.related(root, "source.md", body)
	if err != nil || len(peers) != 2 || peers[0].entry.ID != "new" {
		t.Fatal("recent ordering", peers, err)
	}
	// Converted sidecars have frontmatter; parser offsets must refer to the body.
	if err := os.WriteFile(filepath.Join(dir, "source.relationships_graph.md"), append([]byte("---\npieces_export: 1\ntags: []\n---\n\n"), body...), 0600); err != nil {
		t.Fatal(err)
	}
	peers, err = p.related(root, "source.md", nil)
	if err != nil || len(peers) != 2 || peers[0].entry.ID != "new" {
		t.Fatal("converted sidecar", peers, err)
	}
}

func TestObsidianUndatedPreserveConnections(t *testing.T) {
	f := summaryScopeFixture()
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	sel, _ := SelectScope("summaries", "")
	out := filepath.Join(t.TempDir(), "source")
	_, err := Export(context.Background(), c, Options{Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, Output: out, Mode: "preserve", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Format: "markdown", Metadata: "off", PeopleMode: "profiles", Scanner: scanner(t, DefaultPolicy())})
	if err != nil {
		t.Fatal(err)
	}
	m, err := ConvertObsidian(context.Background(), out, filepath.Join(t.TempDir(), "vault"), nil)
	if err != nil || m.Mode != "preserve" || m.Obsidian.ConnectionNotes != 55 {
		t.Fatal("undated preserve identities", m.Obsidian, err)
	}
}
