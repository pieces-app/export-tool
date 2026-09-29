package exporter

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func TestBoundedRelatedRankingMatchesCompleteReference(t *testing.T) {
	g := &summaryGraph{Keys: map[string]map[string]map[string]string{}, Index: map[string]map[string][]*Meta{}}
	for _, d := range dimensions {
		g.Index[d] = map[string][]*Meta{}
	}
	rng := rand.New(rand.NewSource(90210))
	members := []*Meta{}
	for i := 0; i < 600; i++ {
		m := &Meta{ID: fmt.Sprintf("%04d", i), Key: fmt.Sprint(i)}
		date := time.Date(2026, 9, 1+rng.Intn(30), 0, 0, 0, 0, time.UTC)
		if i%3 == 0 {
			date = date.In(time.FixedZone("offset", -7*60*60))
		}
		m.Created = date.Format(time.RFC3339Nano)
		if i%11 == 0 {
			m.Created = ""
		} else if i%19 == 0 {
			m.Created = "invalid date"
		}
		members = append(members, m)
		g.Keys[m.Key] = map[string]map[string]string{}
		for _, d := range dimensions {
			g.Keys[m.Key][d] = map[string]string{}
			for n := 0; n < rng.Intn(6); n++ {
				key := fmt.Sprint(rng.Intn(20))
				g.Keys[m.Key][d][key] = key
				// Repeated memberships must never inflate score or list totals.
				g.Index[d][key] = append(g.Index[d][key], m)
			}
		}
	}
	cutoff := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	for _, self := range append(members[:8], &Meta{Key: "unconnected"}) {
		for _, since := range []time.Time{{}, cutoff, cutoff.AddDate(1, 0, 0)} {
			for _, order := range []string{"relevance", "recent"} {
				want, masks, omitted := g.fullSortRelatedReference(self, since, order)
				for _, limit := range []int{1, 7, 50, 500} {
					got, err := g.selectRelated(context.Background(), self, since, order, limit)
					if err != nil {
						t.Fatal(err)
					}
					for dim, name := range dimensions {
						var eligible []*Meta
						for _, item := range want {
							if masks[item.Key]&(1<<dim) != 0 {
								eligible = append(eligible, item)
							}
						}
						if got.totals[dim] != len(eligible) || got.omitted[dim] != omitted[name] || len(got.lists[dim]) != min(limit, len(eligible)) {
							t.Fatalf("incorrect totals/cutoffs/limit: self=%s order=%s dim=%s limit=%d", self.Key, order, name, limit)
						}
						for i, match := range got.lists[dim] {
							actual := g.chronology[match.rank].meta
							if actual.Key != eligible[i].Key || match.mask != masks[actual.Key] || actual.Key == self.Key {
								t.Fatalf("bounded ranking differs: self=%s order=%s dim=%s limit=%d position=%d", self.Key, order, name, limit, i)
							}
						}
					}
				}
			}
		}
	}
}

func TestRelatedRankingCancellation(t *testing.T) {
	g := benchmarkRelatedGraph(1000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.selectRelated(ctx, &Meta{Key: "self"}, time.Time{}, "relevance", 50); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled ranking did not stop")
	}
}

func BenchmarkRelatedTop50LargeGroup(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			g := benchmarkRelatedGraph(n)
			if err := g.prepareRanking(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := g.selectRelated(context.Background(), &Meta{Key: "self"}, time.Time{}, "relevance", 50); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRelatedRankingPreparation(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			g := benchmarkRelatedGraph(n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				g.chronology, g.ranks = nil, nil
				if err := g.prepareRanking(context.Background()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
