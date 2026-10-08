package exporter

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// simulateUploadHardLinks behaves like iCloud Drive uploading a synced folder
// such as Desktop & Documents: it hard-links every new file under dir into
// dir/.tmp.driveupload, giving each file a second link while the export runs.
// The returned stop function reports how many files it linked.
func simulateUploadHardLinks(t *testing.T, dir string) func() int {
	t.Helper()
	upload := filepath.Join(dir, ".tmp.driveupload")
	if err := os.Mkdir(upload, 0700); err != nil {
		t.Fatal(err)
	}
	done, finished := make(chan struct{}), make(chan int)
	go func() {
		seen, linked := map[string]bool{}, 0
		for {
			_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() && path == upload {
					return filepath.SkipDir
				}
				if d.Type().IsRegular() && !seen[path] {
					seen[path] = true
					if os.Link(path, filepath.Join(upload, strconv.Itoa(linked))) == nil {
						linked++
					}
				}
				return nil
			})
			select {
			case <-done:
				finished <- linked
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	return func() int { close(done); return <-finished }
}

// The temporary association store holds private files that must keep a single
// link. It must not live beside the output, where a sync service links them.
func TestTransientAssociationStorageIsOutsideTheOutputFolder(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	r := &run{ctx: context.Background(), stage: filepath.Join(parent, "archive.partial")}
	s := r.canonicalRecords().(*associationCanonicalRecords)
	if err := s.ensure(r.ctx); err != nil {
		t.Fatal(err)
	}
	defer r.cleanupCanonicalStage()
	if rel, err := filepath.Rel(parent, s.parent); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("temporary association storage %s is inside the output's folder %s", s.parent, parent)
	}
}

// Reproduces "recovery storage permissions or ownership are unsafe" (rc4) for an
// export saved under an iCloud-synced Documents folder.
func TestExportSurvivesSyncServiceHardLinksBesideTheOutput(t *testing.T) {
	f := groupedJunctionFixture(123)
	srv := junctionServer(t, f, nil)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	o := junctionExportOptions(t)
	transient := t.TempDir()
	t.Setenv("TMPDIR", transient)
	stop := simulateUploadHardLinks(t, filepath.Dir(o.Output))
	_, err := Export(context.Background(), client, o)
	linked := stop()
	if err != nil {
		t.Fatalf("export failed while a sync service hard-linked files beside it: %v", err)
	}
	if linked == 0 {
		t.Fatal("the simulated sync service linked no files")
	}
	requireNoTransientAssociationStorage(t, transient)
}
