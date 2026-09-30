//go:build darwin || linux

package recovery

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestStoreRejectsLinkedDatabaseFiles(t *testing.T) {
	for _, name := range []string{"database_symlink", "database_hardlink", "journal_symlink", "journal_hardlink", "journal_permissions"} {
		t.Run(name, func(t *testing.T) {
			s, o := createStoreFixture(t, 1024)
			s.Close()
			outside := filepath.Join(filepath.Dir(o.Directory), "unrelated")
			body := []byte("unrelated file that must remain untouched")
			if err := os.WriteFile(outside, body, 0600); err != nil {
				t.Fatal(err)
			}
			database := filepath.Join(o.Directory, storeFile)
			journal := database + "-journal"
			var err error
			switch name {
			case "database_symlink":
				err = os.Remove(database)
				if err == nil {
					err = os.Symlink(outside, database)
				}
			case "database_hardlink":
				err = os.Link(database, outside+"-db")
			case "journal_symlink":
				err = os.Symlink(outside, journal)
			case "journal_hardlink":
				err = os.Link(outside, journal)
			case "journal_permissions":
				err = os.WriteFile(journal, body, 0600)
				if err == nil {
					err = os.Chmod(journal, 0644)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if opened, err := OpenStore(context.Background(), o); err == nil {
				opened.Close()
				t.Fatal("unsafe database/journal accepted")
			}
			if got, err := os.ReadFile(outside); err != nil || string(got) != string(body) {
				t.Fatal("unrelated file was touched")
			}
		})
	}
}

func TestStoreMovedWhileOpenStopsBeforeJournal(t *testing.T) {
	s, o := createStoreFixture(t, 1024)
	moved := o.Directory + "-moved"
	if err := os.Rename(o.Directory, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(o.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(context.Background(), 0, []Record{recordFixture(1, storeSecretOne)}, nil); !errors.Is(err, ErrPermissions) {
		t.Fatal("replaced directory redirected an active transaction", err)
	}
	entries, err := os.ReadDir(o.Directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("new directory received database files", err)
	}
	s.Close()
	o.Directory = moved
	s, err = OpenStore(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkpoint, err := s.Snapshot(context.Background())
	if err != nil || checkpoint.Generation != 0 || checkpoint.Records != 0 {
		t.Fatal("move advanced the transaction", err)
	}
}

func TestStoreChecksPreserveSQLiteLocks(t *testing.T) {
	if path := os.Getenv("PIECES_RECOVERY_SQL_LOCK_CHILD"); path != "" {
		// Bypass the workspace lock in this synthetic child to verify the
		// database's own locking. A workspace-lock test cannot catch a stray
		// close dropping SQLite's POSIX lock in the parent process.
		u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=rw"}
		db, err := sql.Open("sqlite", u.String())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		_, err = db.Exec("BEGIN IMMEDIATE")
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.SQLITE_BUSY {
			if err == nil {
				db.Exec("ROLLBACK")
			}
			t.Fatal("another process acquired SQLite's active writer lock", err)
		}
		return
	}
	s, o := createStoreFixture(t, 1024)
	tx, _, err := s.stageBatch(context.Background(), []Record{recordFixture(1, storeSecretOne)}, stateFixture(1, false))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := s.checkFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestStoreChecksPreserveSQLiteLocks$")
	cmd.Env = append(os.Environ(), "PIECES_RECOVERY_SQL_LOCK_CHILD="+filepath.Join(o.Directory, storeFile))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("database ownership did not survive permission checks: %v\n%s", err, output)
	}
}
