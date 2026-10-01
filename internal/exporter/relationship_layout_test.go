package exporter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRelationshipLayoutDefaultAndRecordedChoices(t *testing.T) {
	for _, choice := range []string{"", "both", "inline"} {
		name := choice
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			f := junctionFixture()
			srv := junctionServer(t, f, nil)
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			o := junctionExportOptions(t)
			o.Relationships = choice
			store, _ := captureTestStore(t)
			o.captureCheckpoint = func(r *run) error { return r.saveCapture(store) }
			if _, err := Export(context.Background(), client, o); err != nil {
				t.Fatal(err)
			}
			srv.Close()
			paths := []string{o.Output, filepath.Join(t.TempDir(), "replayed"), filepath.Join(t.TempDir(), "rebuilt")}
			if _, err := replayCapture(context.Background(), store, paths[1], nil); err != nil {
				t.Fatal(err)
			}
			if _, err := Rebuild(context.Background(), RebuildOptions{Source: o.Output, Options: Options{Output: paths[2], Scanner: scanner(t, DefaultPolicy())}}); err != nil {
				t.Fatal(err)
			}
			want := choice
			if want == "" {
				want = "sidecar"
			}
			var original finalArchiveCounts
			for index, path := range paths {
				manifest, err := InspectArchive(path)
				if err != nil || manifest.Relationships != want {
					t.Fatal("relationship layout default or captured preference changed", err)
				}
				counts, err := inspectFinalArchive(context.Background(), path)
				if err != nil {
					t.Fatal(err)
				}
				if index == 0 {
					original = counts
				} else if counts.Included != original.Included || counts.SummariesWithBody != original.SummariesWithBody || counts.Edges != original.Edges {
					t.Fatal("layout choice changed record/body/graph coverage")
				}
				var links map[string]string
				b, err := os.ReadFile(filepath.Join(path, "link-map.json"))
				if err != nil || json.Unmarshal(b, &links) != nil {
					t.Fatal("cannot read navigation")
				}
				main := filepath.Join(path, links[opaque("WORKSTREAM_SUMMARIES", "summary")])
				body, err := os.ReadFile(main)
				if err != nil || strings.Contains(string(body), "## Related summaries") != (want != "sidecar") {
					t.Fatal("summary relationship-list placement differs", err)
				}
				sibling, err := os.ReadFile(relationshipPath(main))
				if want == "inline" {
					if !os.IsNotExist(err) || strings.Contains(string(body), "[Relationship graph](") {
						t.Fatal("inline layout retained a sibling or dangling link")
					}
					continue
				}
				if err != nil || !strings.Contains(string(body), "[Relationship graph](") {
					t.Fatal("summary footer lost its relationship sibling", err)
				}
				for _, dimension := range dimensions {
					if !strings.Contains(string(sibling), "#### Related Summaries by "+dimension) {
						t.Fatal("relationship sibling lost a dimension")
					}
				}
			}
		})
	}
}
