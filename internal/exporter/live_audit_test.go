package exporter

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Read one explicitly selected file, including a failed export's diagnostic
// artifact. Never changes it or treats a successful scan as archive acceptance.
func TestDiagnosticAuditFile(t *testing.T) {
	path := os.Getenv("PIECES_EXPORT_DIAGNOSTIC_AUDIT_FILE")
	if path == "" {
		t.Skip("set PIECES_EXPORT_DIAGNOSTIC_AUDIT_FILE for a read-only single-file scan")
	}
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		t.Fatal("diagnostic input is not a regular file")
	}
	s, err := NewScanner(DefaultPolicy(), "")
	if err != nil {
		t.Fatal("cannot initialize default scanner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r := &run{ctx: ctx, stage: filepath.Dir(path), opts: Options{Mode: "filtered", Scanner: s}}
	started := time.Now()
	err = r.auditOutputFile(path)
	t.Logf("bytes=%d elapsed_ms=%d passed=%t; diagnostic only, original learned credentials unavailable", st.Size(), time.Since(started).Milliseconds(), err == nil)
	if err != nil {
		t.Fatal("diagnostic audit failed:", err)
	}
}

// Re-render only list navigation from an explicitly selected index into a
// disposable private directory, leaving the source (including partial exports)
// untouched. Only content scans are measured: target documents are not copied.
func TestDiagnosticPagedIndexAudit(t *testing.T) {
	path := os.Getenv("PIECES_EXPORT_DIAGNOSTIC_INDEX")
	if path == "" {
		t.Skip("set PIECES_EXPORT_DIAGNOSTIC_INDEX for a read-only source/index pagination comparison")
	}
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 64<<20 {
		t.Fatal("diagnostic index is not a bounded regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read diagnostic index")
	}
	entries := []navigationEntry{}
	doc := goldmark.DefaultParser().Parse(text.NewReader(data))
	err = ast.Walk(doc, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		link, ok := n.(*ast.Link)
		if !enter || !ok {
			return ast.WalkContinue, nil
		}
		inList := false
		for parent := n.Parent(); parent != nil; parent = parent.Parent() {
			_, item := parent.(*ast.ListItem)
			inList = inList || item
		}
		if !inList {
			return ast.WalkContinue, nil
		}
		u, err := url.Parse(string(link.Destination))
		if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" {
			return ast.WalkStop, errConfig("diagnostic index has an unsupported destination")
		}
		entries = append(entries, navigationEntry{label: string(link.Text(data)), path: u.Path})
		return ast.WalkContinue, nil
	})
	if err != nil || len(entries) == 0 {
		t.Fatal("cannot parse diagnostic navigation")
	}
	s, err := NewScanner(DefaultPolicy(), "")
	if err != nil {
		t.Fatal("cannot initialize diagnostic scanner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	r := &run{ctx: ctx, stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: s}}
	if err := r.writeNavigationIndex("index.md", "# Diagnostic navigation\n\n", entries); err != nil {
		t.Fatal("diagnostic pagination failed")
	}
	started := time.Now()
	files, maximum := 0, int64(0)
	err = filepath.WalkDir(r.stage, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		files++
		maximum = max(maximum, st.Size())
		return r.auditOutputFile(path)
	})
	t.Logf("entries=%d files=%d maximum_bytes=%d audit_ms=%d passed=%t; diagnostic only, original learned credentials and target documents not reproduced", len(entries), files, maximum, time.Since(started).Milliseconds(), err == nil)
	if err != nil {
		t.Fatal("diagnostic paged index audit failed:", err)
	}
}

// Explicitly opt-in, read-only performance comparison. No OS client, source
// cache, renderer, metadata writer or finalization is constructed here. This
// scanner cannot reconstruct credentials learned by the original exporter;
// therefore passing is not a fresh privacy/completeness certification.
func TestLiveAuditUnchangedArchive(t *testing.T) {
	source := os.Getenv("PIECES_EXPORT_LIVE_AUDIT_ARCHIVE")
	if source == "" {
		t.Skip("set PIECES_EXPORT_LIVE_AUDIT_ARCHIVE to a finalized default-filter archive for a read-only comparison")
	}
	manifest, err := InspectArchive(source)
	if err != nil || manifest.Mode != "filtered" {
		t.Fatal("audit comparison requires a finalized filtered archive")
	}
	s, err := NewScanner(DefaultPolicy(), "")
	if err != nil || manifest.PolicyHash != s.Hash || len(manifest.CategoryHashes) != 0 {
		t.Fatal("audit comparison requires the default privacy policy")
	}
	// Resolve the explicitly selected finalized directory once; the traversal
	// itself rejects symlinks and does not follow a .partial directory.
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal("finalized archive cannot be resolved")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	r := &run{ctx: ctx, stage: source, opts: Options{Mode: "filtered", Scanner: s}, local: newLocalMeasurements()}
	for _, phase := range []string{"full_scan", "checksum_reuse"} {
		before := r.local.snapshot()
		started := time.Now()
		if err := r.auditOutput(); err != nil {
			// Do not expose filesystem paths or record-derived error text.
			t.Fatal("read-only audit comparison failed during", phase)
		}
		delta := map[string]OperationMeasurement{}
		addMeasurements(delta, r.local.snapshot(), before)
		report := struct {
			Phase        string                          `json:"phase"`
			Milliseconds int64                           `json:"milliseconds"`
			Operations   map[string]OperationMeasurement `json:"operations"`
			CachedFiles  int                             `json:"cached_files"`
			PathBytes    int                             `json:"cached_path_bytes"`
		}{phase, time.Since(started).Milliseconds(), delta, len(r.auditCache.entries), r.auditCache.keyBytes}
		b, _ := json.Marshal(report)
		t.Log(string(b))
	}
	t.Log("Read-only phase comparison; original learned credentials are unavailable, so this is not privacy recertification or complete-source acceptance.")
}
