package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestObsidianConnectedMemoriesUseActualLinks(t *testing.T) {
	connection := &compactRecord{kind: "connection", destination: "connections/sources/Café Player.md"}
	other := &compactRecord{kind: "connection", destination: "connections/people/Example.md"}
	destinations := map[string]*compactRecord{connection.destination: connection, other.destination: other}
	body := []byte("---\npieces_export: 1\naliases: ['[not a link](../../connections/people/Example.md)']\n---\n\n[A](../../connections/sources/Caf%C3%A9%20Player.md) and [twice](../../connections/sources/Caf%C3%A9%20Player.md#heading).\n\n`[code](../../connections/people/Example.md)`\n\n[external](https://example.org/connections/people/Example.md)\n\n[absolute](/connections/people/Example.md)\n\n[missing](../../connections/people/Missing.md)\n")
	for _, kind := range []string{"summary", "profile", "signal", "signal-description", "connection", "annotation", "navigation"} {
		r := &compactRecord{kind: kind, destination: "workstream_summaries/timeline/" + kind + ".md"}
		compactCollectMentions(r, body, destinations)
	}
	if len(connection.mentions) != 4 || len(other.mentions) != 0 {
		t.Fatal("counted duplicates, excluded records or non-links", len(connection.mentions), len(other.mentions))
	}
	if !strings.Contains(compactConnectionNavigation(other, time.UTC), "No retained summary, profile or signal") {
		t.Fatal("missing explicit empty state")
	}
}

func TestObsidianConnectedMemoriesBoundedAndRelocatable(t *testing.T) {
	dir := t.TempDir()
	connection := &compactRecord{kind: "connection", entry: TimelineEntry{Title: "Café Player"}, destination: "connections/sources/Café Player.md"}
	for i := 0; i < 601; i++ {
		m := &compactRecord{kind: "summary", destination: fmt.Sprintf("memories/Summary %04d.md", i), entry: TimelineEntry{Title: fmt.Sprintf("Summary %04d", i), Created: time.Date(2026, 10, 1, 13, i, 0, 0, time.UTC).Format(time.RFC3339)}}
		connection.mentions = append(connection.mentions, m)
		file := filepath.Join(dir, m.destination)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("# A memory"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := &run{ctx: context.Background(), stage: dir}
	zone, _ := time.LoadLocation("America/New_York")
	if err := compactConnectionIndex(r, connection, zone); err != nil {
		t.Fatal(err)
	}
	nav := compactConnectionNavigation(connection, zone)
	if !strings.Contains(nav, "601 connected memories") || !strings.Contains(nav, "Summary 0600") || strings.Contains(nav, "Summary 0595") || !strings.Contains(nav, "EDT (UTC-04:00)") || len(acceptanceMarkdownLinks([]byte(nav))) != 6 {
		t.Fatal("missing latest links, date or limit", nav)
	}
	file := filepath.Join(dir, connection.destination)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(nav), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("# Index"), 0600); err != nil {
		t.Fatal(err)
	}
	pages, err := filepath.Glob(filepath.Join(dir, "connections/sources/Café Player.memories/index.pages/*.md"))
	if err != nil || len(pages) != 3 {
		t.Fatal(pages, err)
	}
	seen := map[string]bool{}
	for _, file := range pages {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) > navigationPageBytes {
			t.Fatal("oversized page")
		}
		if obsidianProperties(t, b)["pieces_kind"] != "navigation" {
			t.Fatal("missing navigation properties")
		}
		for link := range acceptanceMarkdownLinks(b) {
			if strings.Contains(link, "/memories/") {
				if seen[link] {
					t.Fatal("repeated memory")
				}
				seen[link] = true
			}
		}
	}
	if len(seen) != 601 {
		t.Fatal("lost memories", len(seen))
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	r.stage = moved
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
}

func TestObsidianConnectedMemoriesExportIntegration(t *testing.T) {
	dir, _ := obsidianFixture(t, "obsidian")
	b, err := os.ReadFile(filepath.Join(dir, "vault/record-map.json"))
	if err != nil {
		t.Fatal(err)
	}
	mapping := map[string]string{}
	if err := json.Unmarshal(b, &mapping); err != nil {
		t.Fatal(err)
	}
	person := mapping[opaque("PERSONS", "person")]
	summary := mapping[opaque("WORKSTREAM_SUMMARIES", "summary")]
	b, err = os.ReadFile(filepath.Join(dir, "vault", person))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "## Connected memories") || !acceptanceMarkdownLinks(obsidianBody(b))[relative(person, summary)] {
		t.Fatal("connection note omitted summary", string(b))
	}
	if _, err := inspectCompactBodies(dir); err != nil {
		t.Fatal(err)
	}
}
