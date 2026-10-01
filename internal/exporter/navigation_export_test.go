package exporter

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPackagedPagedNavigationCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to a compiled candidate")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	f := junctionFixture()
	family, _ := junctionFamilyByName("workstream_summary_to_annotation_associations")
	const summaries = navigationPageEntries + 3
	for i := 1; i < summaries; i++ {
		id := fmt.Sprintf("summary-%04d", i)
		s := record(id, "2026-09-30T12:00:00Z")
		s["name"], s["parentHierarchicalType"] = fmt.Sprintf("Summary %04d", i), "TEMPORAL_DAY_HIERARCHICAL_SUMMARY"
		f.data["WORKSTREAM_SUMMARIES"] = append(f.data["WORKSTREAM_SUMMARIES"], s)
		a := record("binding-"+id, "2026-09-30T12:00:00Z")
		a[family.leftField], a[family.rightField] = id, "body"
		f.data[family.material().Type] = append(f.data[family.material().Type], a)
	}
	srv := junctionServer(t, f, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	parent := t.TempDir()
	out := filepath.Join(parent, "archive")
	b, err := exec.CommandContext(ctx, binary, "export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--format", "markdown", "--metadata", "off", "--output", out).CombinedOutput()
	if err != nil {
		t.Fatalf("compiled paged export failed: %v\n%s", err, b)
	}
	srv.Close()
	for _, pattern := range []string{"markdown/records/index.pages/*.md", "workstream_summaries/index.pages/*.md", "markdown/days/2026-09-30.pages/*.md"} {
		pages, _ := filepath.Glob(filepath.Join(out, pattern))
		if len(pages) < 2 {
			t.Fatal("large generated navigation was not paginated", pattern)
		}
	}
	moved := filepath.Join(parent, "moved archive")
	if err := os.Rename(out, moved); err != nil {
		t.Fatal(err)
	}
	rebuilt := filepath.Join(parent, "rebuilt")
	b, err = exec.CommandContext(ctx, binary, "rebuild", "--source", moved, "--output", rebuilt, "--format", "markdown", "--metadata", "off", "--yes").CombinedOutput()
	if err != nil {
		t.Fatalf("compiled paged rebuild failed: %v\n%s", err, b)
	}
	for _, root := range []string{moved, rebuilt} {
		counts, err := inspectFinalArchive(ctx, root)
		if err != nil || counts.Summaries != summaries || counts.SummariesWithBody != summaries {
			t.Fatal("paged export/rebuild lost source bodies or local links", err)
		}
		b, err := os.ReadFile(filepath.Join(root, "index.md"))
		if err != nil || len(b) > 4096 {
			t.Fatal("landing page is not compact", err)
		}
	}
}
