package recovery

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// This test cannot exhaust an ordinary development filesystem. It requires
// an empty, explicitly enabled disposable mount in the isolated Linux runner.
func TestStoreActualFilesystemFull(t *testing.T) {
	root := os.Getenv("PIECES_RECOVERY_ENOSPC_ROOT")
	if root == "" {
		t.Skip("requires an explicitly configured disposable tmpfs")
	}
	var fs unix.Statfs_t
	if root != "/enospc" || unix.Statfs(root, &fs) != nil || fs.Type != unix.TMPFS_MAGIC || fs.Blocks*uint64(fs.Bsize) > 32<<20 {
		t.Fatal("requires /enospc on a dedicated tmpfs of at most 32 MiB")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("requires an empty disposable mount")
	}
	for _, phase := range []string{"before_transaction", "before_commit"} {
		t.Run(phase, func(t *testing.T) {
			base, err := os.MkdirTemp(root, "recovery-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(base)
			o := fixtureOptions(base)
			o.MaxPayloadBytes = 256 << 10
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s, err := CreateStore(ctx, o, stateFixture(0, false))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.Commit(ctx, 0, []Record{recordFixture(1, storeSecretOne)}, stateFixture(1, false)); err != nil {
				t.Fatal(err)
			}
			next := []Record{recordFixture(2, storeSecretTwo+strings.Repeat("x", 128<<10))}
			filler := filepath.Join(base, "disposable-filler")
			fill := func() {
				f, err := os.OpenFile(filler, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				block := make([]byte, 64<<10)
				for written := 0; written <= 32<<20; {
					n, err := f.Write(block)
					written += n
					if errors.Is(err, syscall.ENOSPC) {
						return
					}
					if err != nil || n == 0 {
						t.Fatal("failed to exhaust the disposable mount", err)
					}
				}
				t.Fatal("disposable filesystem did not report actual ENOSPC")
			}
			var writeErr error
			if phase == "before_transaction" {
				fill()
				writeErr = s.Commit(ctx, 1, next, stateFixture(2, true))
				if err := s.Commit(ctx, 1, nil, nil); !errors.Is(err, ErrReopen) {
					t.Fatal("continued writing after filesystem exhaustion", err)
				}
			} else {
				tx, _, err := s.stageBatch(ctx, next, stateFixture(2, true))
				if err != nil {
					t.Fatal(err)
				}
				fill()
				writeErr = tx.Commit()
				tx.Rollback()
			}
			var sqliteErr *sqlite.Error
			if !errors.As(writeErr, &sqliteErr) || (sqliteErr.Code()&255 != sqlite3.SQLITE_FULL && sqliteErr.Code()&255 != sqlite3.SQLITE_IOERR) {
				t.Fatal("full filesystem did not cause an SQLite storage failure", writeErr)
			}
			// Reclaim only this test's filler, then recover from persisted state.
			if err := os.Remove(filler); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = OpenStore(ctx, o)
			if err != nil {
				t.Fatal("failed to reopen after space was restored", err)
			}
			defer s.Close()
			checkpoint, err := s.Snapshot(ctx)
			if err != nil || checkpoint.Generation != 1 || checkpoint.Records != 1 || !bytes.Equal(checkpoint.State, stateFixture(1, false)) || len(readAllRecords(t, s)) != 1 {
				t.Fatal("disk-full failure changed committed record/scanner/progress state", err)
			}
		})
	}
}
