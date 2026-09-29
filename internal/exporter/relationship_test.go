package exporter

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func TestRelatedRankingUsesAllDimensionsBeforeLimit(t *testing.T) {
	self := &Meta{Key: "self", ID: "self"}
	old := &Meta{Key: "old", ID: "old", Created: "2026-01-01T00:00:00Z"}
	recent := &Meta{Key: "recent", ID: "recent", Created: "2026-09-29T00:00:00Z"}
	undated := &Meta{Key: "undated", ID: "undated"}
	g := &summaryGraph{Keys: map[string]map[string]map[string]string{"self": {}}, Index: map[string]map[string][]*Meta{}}
	for _, d := range dimensions {
		g.Keys["self"][d] = map[string]string{"common": d}
		g.Index[d] = map[string][]*Meta{"common": {self, old}}
	}
	// More than the visible limit of recent candidates must not hide the old,
	// stronger candidate. Repeated matching tags must not inflate its score.
	g.Index["Tags"]["common"] = append(g.Index["Tags"]["common"], recent, undated)
	for i := 0; i < 75; i++ {
		g.Index["Tags"]["common"] = append(g.Index["Tags"]["common"], &Meta{Key: strings.Repeat("x", i+1), ID: strings.Repeat("x", i+1), Created: recent.Created})
	}
	g.Keys["self"]["Tags"]["second"] = "second tag"
	g.Index["Tags"]["second"] = []*Meta{self, recent}
	got, masks, _ := g.candidates(self, time.Time{}, "relevance")
	if got[0] != old || masks[old.Key] != 15 || masks[recent.Key] != 1 {
		t.Fatal("relevance must count distinct dimensions across full candidate set")
	}
	got, _, _ = g.candidates(self, time.Time{}, "recent")
	if got[0] != recent {
		t.Fatal("recent order did not prioritize timestamp")
	}
	cutoff, _ := time.Parse("2006-01-02", "2026-09-29")
	got, _, omitted := g.candidates(self, cutoff, "relevance")
	if len(got) != 76 || got[0] != recent || !omitted["Tags"] || !omitted["Person"] {
		t.Fatal("cutoff must be inclusive and exclude undated records")
	}
}

func TestUnresolvableEmbeddedLinksBecomeText(t *testing.T) {
	records := map[string]*Meta{
		"PERSONS\x00included": {State: "included", Path: "markdown/persons/valid.md"},
		"PERSONS\x00private":  {State: "excluded", Path: "markdown/persons/private.md"},
	}
	body := `[**Kept**](pieces://persons/included#unverified)

[**Missing**](pieces://persons/missing) [Private][p] [Empty]() [Local](../../absent.md) [Unknown](pieces://unknown/id)

![Image](https://example.com/image.png) <pieces://persons/missing> [Web](https://example.com/path)

<a href="file:///private/missing.md">HTML</a>

[p]: pieces://persons/private

` + "`[Code](pieces://persons/missing)`\n"
	out := rewriteMarkdown(body, "markdown/summaries/summary.md", records)
	var destinations []string
	_ = ast.Walk(goldmark.DefaultParser().Parse(text.NewReader([]byte(out))), func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if enter {
			if link, ok := n.(*ast.Link); ok {
				destinations = append(destinations, string(link.Destination))
			}
			if _, ok := n.(*ast.Image); ok {
				t.Fatal("image fetched by exported markdown")
			}
			if _, ok := n.(*ast.RawHTML); ok {
				t.Fatal("active raw HTML retained")
			}
		}
		return ast.WalkContinue, nil
	})
	if len(destinations) != 2 || destinations[0] != "../persons/valid.md" || destinations[1] != "https://example.com/path" {
		t.Fatalf("invalid retained links: %v", destinations)
	}
	for _, label := range []string{"**Kept**", "**Missing**", "Private", "Empty", "Local", "Unknown", "Image", "HTML", "`[Code](pieces://persons/missing)`"} {
		if !strings.Contains(out, label) {
			t.Errorf("lost label or code: %s", label)
		}
	}
	if strings.Contains(out, "[Private]") || strings.Contains(out, "]()") {
		t.Fatal("dead navigation remained")
	}
}

func TestPDFUsesDisplayedMarkdownText(t *testing.T) {
	blocks := markdownBlocks([]byte("# A heading\n\n[Display **label**](../a%20b.md) and `snake_case`, escaped\\_text &amp; entities.\n"))
	if len(blocks) != 2 || blocks[1].Text != "Display label and snake_case, escaped_text & entities." || len(blocks[1].Links) != 1 || blocks[1].Links[0].Label != "Display label" {
		t.Fatalf("PDF displayed Markdown syntax: %+v", blocks)
	}
}

func BenchmarkRelatedCandidatesLargeGroup(b *testing.B) {
	g := &summaryGraph{Keys: map[string]map[string]map[string]string{"self": {}}, Index: map[string]map[string][]*Meta{}}
	members := make([]*Meta, 10000)
	for i := range members {
		members[i] = &Meta{Key: fmt.Sprint(i), ID: fmt.Sprint(i), Created: time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC).Format(time.RFC3339)}
	}
	for _, d := range dimensions {
		g.Keys["self"][d] = map[string]string{"common": d}
		g.Index[d] = map[string][]*Meta{"common": members}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.candidates(&Meta{Key: "self"}, time.Time{}, "relevance")
	}
}
