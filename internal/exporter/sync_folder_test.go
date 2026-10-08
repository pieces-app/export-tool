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

// useTempDir points os.TempDir at dir for one test: TMPDIR on Unix, and TMP
// and TEMP, which Windows reads instead.
func useTempDir(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
	if os.TempDir() != dir {
		t.Fatalf("temporary folder is %s, want %s", os.TempDir(), dir)
	}
}

// simulateUploadHardLinks behaves like iCloud Drive uploading a synced folder
// such as Desktop & Documents: it hard-links every new file under dir into
// dir/.tmp.driveupload, giving each file a second link while the export runs.
// It also watches temp, so a test can tell that it ran while the temporary
// association store existed. The returned stop function reports how many
// files it linked and whether it saw the store in dir and in temp.
func simulateUploadHardLinks(t *testing.T, dir, temp string) func() (int, bool, bool) {
	t.Helper()
	upload := filepath.Join(dir, ".tmp.driveupload")
	if err := os.Mkdir(upload, 0700); err != nil {
		t.Fatal(err)
	}
	type result struct {
		linked       int
		sawStore     bool
		sawStoreTemp bool
	}
	done, finished := make(chan struct{}), make(chan result)
	go func() {
		seen, linked, sawStore, sawStoreTemp := map[string]bool{}, 0, false, false
		for {
			_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() && path == upload {
					return filepath.SkipDir
				}
				sawStore = sawStore || strings.Contains(path, ".pieces-export-stage-")
				if d.Type().IsRegular() && !seen[path] {
					seen[path] = true
					if os.Link(path, filepath.Join(upload, strconv.Itoa(linked))) == nil {
						linked++
					}
				}
				return nil
			})
			if entries, err := os.ReadDir(temp); err == nil {
				for _, entry := range entries {
					sawStoreTemp = sawStoreTemp || strings.HasPrefix(entry.Name(), ".pieces-export-stage-")
				}
			}
			select {
			case <-done:
				finished <- result{linked, sawStore, sawStoreTemp}
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	return func() (int, bool, bool) {
		close(done)
		r := <-finished
		return r.linked, r.sawStore, r.sawStoreTemp
	}
}

// The temporary association store holds private files that must keep a single
// link. It must not live beside the output, where a sync service links them.
func TestTransientAssociationStorageIsOutsideTheOutputFolder(t *testing.T) {
	parent, transient := t.TempDir(), t.TempDir()
	useTempDir(t, transient)
	r := &run{ctx: context.Background(), stage: filepath.Join(parent, "archive.partial")}
	s := r.canonicalRecords().(*associationCanonicalRecords)
	if err := s.ensure(r.ctx); err != nil {
		t.Fatal(err)
	}
	defer r.cleanupCanonicalStage()
	if rel, err := filepath.Rel(parent, s.parent); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("temporary association storage %s is inside the output's folder %s", s.parent, parent)
	}
	if rel, err := filepath.Rel(transient, s.parent); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("temporary association storage %s is not in the temporary folder %s", s.parent, transient)
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
	useTempDir(t, transient)
	stop := simulateUploadHardLinks(t, filepath.Dir(o.Output), transient)
	_, err := Export(context.Background(), client, o)
	linked, sawStore, sawStoreTemp := stop()
	if err != nil {
		t.Fatalf("export failed while a sync service hard-linked files beside it: %v", err)
	}
	if linked == 0 {
		t.Fatal("the simulated sync service linked no files")
	}
	// The watcher saw the store while it existed, so it would have seen it in the
	// synced folder too had it been created there.
	if !sawStoreTemp {
		t.Fatal("the watcher never saw the temporary association store; the check proved nothing")
	}
	if sawStore {
		t.Fatal("the temporary association store appeared in the synced folder")
	}
	requireNoTransientAssociationStorage(t, transient)
}
