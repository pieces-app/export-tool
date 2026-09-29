package exporter

import (
	"container/heap"
	"context"
	"math/bits"
	"sort"
	"time"
)

type summaryChronology struct {
	meta    *Meta
	created time.Time
	dated   bool
}

// The graph is immutable after construction. Parse dates and assign stable
// recency ranks once, rather than parsing timestamps in every list comparison.
func (g *summaryGraph) prepareRanking(ctx context.Context) error {
	if g.ranks != nil {
		return ctx.Err()
	}
	seen := map[string]bool{}
	var chronology []summaryChronology
	visited := 0
	for _, groups := range g.Index {
		for _, members := range groups {
			for _, m := range members {
				visited++
				if visited%1024 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				if seen[m.Key] {
					continue
				}
				seen[m.Key] = true
				created, err := time.Parse(time.RFC3339Nano, m.Created)
				chronology = append(chronology, summaryChronology{m, created, err == nil})
			}
		}
	}
	sort.Slice(chronology, func(i, j int) bool {
		a, b := chronology[i], chronology[j]
		if a.dated != b.dated {
			return a.dated
		}
		if a.dated && !a.created.Equal(b.created) {
			return a.created.After(b.created)
		}
		return a.meta.ID < b.meta.ID
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	g.chronology, g.ranks = chronology, make(map[string]int, len(chronology))
	for i, item := range chronology {
		g.ranks[item.meta.Key] = i
	}
	return nil
}

type rankedMatch struct {
	rank int
	mask uint8
}

func betterMatch(a, b rankedMatch, recent bool) bool {
	if !recent {
		x, y := bits.OnesCount8(a.mask), bits.OnesCount8(b.mask)
		if x != y {
			return x > y
		}
	}
	return a.rank < b.rank
}

// The least desirable retained match is at the root. All candidates are scored
// across all dimensions before any are discarded from a visible section.
type relatedHeap struct {
	items  []rankedMatch
	recent bool
}

func (h relatedHeap) Len() int           { return len(h.items) }
func (h relatedHeap) Less(i, j int) bool { return betterMatch(h.items[j], h.items[i], h.recent) }
func (h relatedHeap) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *relatedHeap) Push(v any)        { h.items = append(h.items, v.(rankedMatch)) }
func (h *relatedHeap) Pop() any {
	i := len(h.items) - 1
	v := h.items[i]
	h.items = h.items[:i]
	return v
}

type relatedSelection struct {
	lists   [4][]rankedMatch
	totals  [4]int
	omitted [4]bool
}

func (g *summaryGraph) selectRelated(ctx context.Context, m *Meta, since time.Time, order string, limit int) (relatedSelection, error) {
	var result relatedSelection
	if err := g.prepareRanking(ctx); err != nil {
		return result, err
	}
	masks := map[string]uint8{}
	visited := 0
	for dimension, d := range dimensions {
		for key := range g.Keys[m.Key][d] {
			for _, other := range g.Index[d][key] {
				visited++
				if visited%1024 == 0 {
					if err := ctx.Err(); err != nil {
						return result, err
					}
				}
				if other.Key == m.Key {
					continue
				}
				if !since.IsZero() {
					item := g.chronology[g.ranks[other.Key]]
					if !item.dated || item.created.Before(since) {
						result.omitted[dimension] = true
						continue
					}
				}
				masks[other.Key] |= 1 << dimension
			}
		}
	}
	if len(masks) == 0 {
		return result, ctx.Err()
	}
	heaps := [4]relatedHeap{}
	for i := range heaps {
		heaps[i].recent = order == "recent"
		heaps[i].items = make([]rankedMatch, 0, limit)
	}
	for key, mask := range masks {
		visited++
		if visited%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return result, err
			}
		}
		match := rankedMatch{g.ranks[key], mask}
		for dimension := range dimensions {
			if mask&(1<<dimension) == 0 {
				continue
			}
			result.totals[dimension]++
			h := &heaps[dimension]
			if h.Len() < limit {
				h.items = append(h.items, match)
				if h.Len() == limit {
					heap.Init(h)
				}
			} else if limit > 0 && betterMatch(match, h.items[0], h.recent) {
				h.items[0] = match
				heap.Fix(h, 0)
			}
		}
	}
	for dimension := range dimensions {
		h := &heaps[dimension]
		sort.Slice(h.items, func(i, j int) bool { return betterMatch(h.items[i], h.items[j], h.recent) })
		result.lists[dimension] = h.items
	}
	return result, ctx.Err()
}
