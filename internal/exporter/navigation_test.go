package exporter

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNavigationPaginationPreservesEveryEntryAndLink(t *testing.T) {
	for _, count := range []int{0, 250, 251, 62501} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "original")
			r := &run{ctx: context.Background(), stage: root}
			if err := r.writeFile(filepath.Join(root, "target.md"), []byte("# Target\n")); err != nil {
				t.Fatal(err)
			}
			entries := make([]navigationEntry, count)
			for i := range entries {
				entries[i] = navigationEntry{label: fmt.Sprintf("Record %07d", i), path: "target.md"}
			}
			if err := r.writeNavigationIndex("index.md", "# Navigation\n\n", entries); err != nil {
				t.Fatal(err)
			}
			seen := map[string]int{}
			if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if len(b) > navigationPageBytes || strings.Count(string(b), "- [Record") > navigationPageEntries {
					t.Fatal("generated list exceeded its byte/entry bound")
				}
				for _, line := range strings.Split(string(b), "\n") {
					if strings.HasPrefix(line, "- [Record") {
						label, _, _ := strings.Cut(strings.TrimPrefix(line, "- ["), "]")
						seen[label]++
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(seen) != count {
				t.Fatal("navigation lost entries", len(seen), count)
			}
			for _, entry := range entries {
				if seen[entry.label] != 1 {
					t.Fatal("navigation duplicated or lost an entry")
				}
			}
			moved := filepath.Join(filepath.Dir(root), "moved")
			if err := os.Rename(root, moved); err != nil {
				t.Fatal(err)
			}
			r.stage = moved
			if err := r.validateMarkdownLinks(); err != nil {
				t.Fatal("navigation failed after moving archive", err)
			}
		})
	}
}

func TestNavigationByteLimitPrivacyAndPDFs(t *testing.T) {
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
	target := "docs/café (review)#%.md"
	if err := r.writeFile(filepath.Join(r.stage, target), []byte("# Target\n")); err != nil {
		t.Fatal(err)
	}
	entries := make([]navigationEntry, 100)
	for i := range entries {
		entries[i] = navigationEntry{label: strings.Repeat("界", 200), path: target, detail: strings.Repeat("approved ", 40)}
	}
	if err := r.writeNavigationIndex("index.md", "# Index\n\n", entries); err != nil {
		t.Fatal(err)
	}
	pages, _ := filepath.Glob(filepath.Join(r.stage, "index.pages/*.md"))
	if len(pages) < 2 {
		t.Fatal("byte bound did not split list below entry-count bound")
	}
	for _, page := range pages {
		b, _ := os.ReadFile(page)
		if len(b) > navigationPageBytes || !strings.Contains(string(b), strings.Repeat("界", 160)+"…") {
			t.Fatal("bounded Unicode label or page size changed")
		}
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal("paged navigation did not pass content scan", err)
	}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.renderPDFs(); err != nil {
		t.Fatal("paged navigation PDF conversion failed", err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal("PDF or navigation links failed final scan", err)
	}
	// New/altered pages still receive full privacy checks after the earlier pass.
	if err := os.WriteFile(pages[0], []byte(fakeSecret()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err == nil {
		t.Fatal("changed paged index bypassed secret detection")
	}
}

func TestNavigationCancellationAndOversizedEntry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &run{ctx: ctx, stage: t.TempDir()}
	if err := r.writeNavigationIndex("index.md", "# Index\n", nil); err == nil {
		t.Fatal("canceled navigation wrote output")
	}
	r.ctx = context.Background()
	if err := r.writeNavigationIndex("index.md", "# Index\n", []navigationEntry{{label: "Entry", path: "target.md", detail: strings.Repeat("x", navigationPageBytes)}}); err == nil {
		t.Fatal("oversized entry bypassed navigation bound")
	}
}
