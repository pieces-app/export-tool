package exporter

import (
	"context"
	"fmt"
	"math/bits"
	"sort"
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
	got, err := g.selectRelated(context.Background(), self, time.Time{}, "relevance", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.lists[0]) != 50 || g.chronology[got.lists[0][0].rank].meta != old || got.lists[0][0].mask != 15 || got.lists[0][1].mask != 1 {
		t.Fatal("relevance must count distinct dimensions across full candidate set")
	}
	got, err = g.selectRelated(context.Background(), self, time.Time{}, "recent", 50)
	if err != nil || g.chronology[got.lists[0][0].rank].meta != recent {
		t.Fatal("recent order did not prioritize timestamp")
	}
	cutoff, _ := time.Parse("2006-01-02", "2026-09-29")
	got, err = g.selectRelated(context.Background(), self, cutoff, "relevance", 50)
	if err != nil || got.totals[0] != 76 || g.chronology[got.lists[0][0].rank].meta != recent || !got.omitted[0] || !got.omitted[2] {
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
	blocks, err := markdownBlocks(context.Background(), []byte("# A heading\n\n[Display **label**](../a%20b.md) and `snake_case`, escaped\\_text &amp; entities.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 || blocks[1].Text != "Display label and snake_case, escaped_text & entities." || len(blocks[1].Links) != 1 || blocks[1].Links[0].Label != "Display label" {
		t.Fatalf("PDF displayed Markdown syntax: %+v", blocks)
	}
}

func BenchmarkRelatedCandidatesLargeGroup(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			g := benchmarkRelatedGraph(n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				g.fullSortRelatedReference(&Meta{Key: "self"}, time.Time{}, "relevance")
			}
		})
	}
}

func benchmarkRelatedGraph(n int) *summaryGraph {
	g := &summaryGraph{Keys: map[string]map[string]map[string]string{"self": {}}, Index: map[string]map[string][]*Meta{}}
	members := make([]*Meta, n)
	for i := range members {
		members[i] = &Meta{Key: fmt.Sprint(i), ID: fmt.Sprint(i), Created: time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC).Format(time.RFC3339)}
	}
	for _, d := range dimensions {
		g.Keys["self"][d] = map[string]string{"common": d}
		g.Index[d] = map[string][]*Meta{"common": members}
	}
	return g
}

// Independent, unbounded reference retained for ranking regression checks.
// Score counts distinct shared dimensions, never the number of matching tags.
// Evaluate the complete candidate union before limiting the visible list.
func (g *summaryGraph) fullSortRelatedReference(m *Meta, since time.Time, order string) ([]*Meta, map[string]uint8, map[string]bool) {
	masks := map[string]uint8{}
	candidates := map[string]*Meta{}
	omitted := map[string]bool{}
	for dimension, d := range dimensions {
		for key := range g.Keys[m.Key][d] {
			for _, other := range g.Index[d][key] {
				if other.Key == m.Key {
					continue
				}
				if !since.IsZero() {
					created, err := time.Parse(time.RFC3339Nano, other.Created)
					if err != nil || created.Before(since) {
						omitted[d] = true
						continue
					}
				}
				masks[other.Key] |= 1 << dimension
				candidates[other.Key] = other
			}
		}
	}
	ordered := make([]*Meta, 0, len(candidates))
	for _, candidate := range candidates {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if order != "recent" {
			a, b := bits.OnesCount8(masks[ordered[i].Key]), bits.OnesCount8(masks[ordered[j].Key])
			if a != b {
				return a > b
			}
		}
		return newer(ordered[i], ordered[j])
	})
	return ordered, masks, omitted
}
