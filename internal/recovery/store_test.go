package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const storeSecretOne = "synthetic-store-private-credential-one"
const storeSecretTwo = "synthetic-store-private-credential-two"

type syntheticState struct {
	Known []string
	Next  int
	Phase string
}

func stateFixture(next int, two bool) []byte {
	s := syntheticState{Known: []string{storeSecretOne}, Next: next, Phase: "capture"}
	if two {
		s.Known = append(s.Known, storeSecretTwo)
	}
	b, _ := json.Marshal(s)
	return b
}

func recordFixture(id int, payload string) Record {
	return Record{sha256.Sum256([]byte(fmt.Sprintf("synthetic record %d", id))), []byte(payload)}
}

func createStoreFixture(t *testing.T, limit int) (*Store, Options) {
	t.Helper()
	o := fixtureOptions(t.TempDir())
	o.MaxPayloadBytes = limit
	s, err := CreateStore(context.Background(), o, stateFixture(0, false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, o
}

func readAllRecords(t *testing.T, s *Store) map[[32]byte]string {
	t.Helper()
	result := map[[32]byte]string{}
	if err := s.Visit(context.Background(), func(r Record) error {
		if _, duplicate := result[r.Ref]; duplicate {
			t.Fatal("duplicate current identity")
		}
		result[r.Ref] = string(r.Payload)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func checkNoStorePlaintext(t *testing.T, o Options) {
	t.Helper()
	entries, err := os.ReadDir(o.Directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(o.Directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range []string{storeSecretOne, storeSecretTwo, "synthetic withheld private title"} {
			if bytes.Contains(b, []byte(marker)) {
				t.Fatal("private payload entered workspace storage as plaintext")
			}
		}
	}
}

func TestStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, o := createStoreFixture(t, 64<<10)
	initial, err := s.Snapshot(ctx)
	if err != nil || initial.Generation != 0 || initial.Records != 0 || initial.Changes != 0 || !bytes.Equal(initial.State, stateFixture(0, false)) {
		t.Fatal("invalid initial checkpoint", err)
	}
	a := recordFixture(1, `{"state":"included","body":"`+storeSecretOne+`","projection":"absent"}`)
	b := recordFixture(2, `{"state":"withheld","original_title":"synthetic withheld private title","edges":["original-private-evidence"]}`)
	if err := s.Commit(ctx, 0, []Record{a, b}, stateFixture(2, false)); err != nil {
		t.Fatal(err)
	}
	a.Payload = []byte(`{"state":"excluded","credential":"` + storeSecretTwo + `","projection":"absent"}`)
	c := recordFixture(3, "")
	if err := s.Commit(ctx, 1, []Record{a, c}, stateFixture(3, true)); err != nil {
		t.Fatal(err)
	}
	// Phase/cursor checkpoints may advance without writing another record.
	if err := s.Commit(ctx, 2, nil, stateFixture(3, true)); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := s.Snapshot(ctx)
	if err != nil || checkpoint.Generation != 3 || checkpoint.Records != 3 || checkpoint.Changes != 4 {
		t.Fatal("record replacement changed the wrong counters", err)
	}
	checkpoint.State[0] ^= 1
	want := map[[32]byte]string{a.Ref: string(a.Payload), b.Ref: string(b.Payload), c.Ref: ""}
	if got := readAllRecords(t, s); !reflect.DeepEqual(got, want) {
		t.Fatal("record versions or non-inclusion evidence changed")
	}
	checkNoStorePlaintext(t, o)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(ctx); !errors.Is(err, ErrClosed) {
		t.Fatal("closed checkpoint remained accessible")
	}
	s, err = OpenStore(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkpoint, err = s.Snapshot(ctx)
	if err != nil || checkpoint.Generation != 3 || !bytes.Equal(checkpoint.State, stateFixture(3, true)) || !reflect.DeepEqual(readAllRecords(t, s), want) {
		t.Fatal("reopen did not restore exact records and complete scanner/progress state", err)
	}
	stop := errors.New("caller stopped iteration")
	if err := s.Visit(ctx, func(Record) error { return stop }); !errors.Is(err, stop) {
		t.Fatal("callback failure was not preserved")
	}
	if err := s.Commit(ctx, 3, nil, stateFixture(3, true)); err != nil {
		t.Fatal("callback failure poisoned the store", err)
	}
}

func mutateStore(t *testing.T, o Options, fn func(*sql.DB)) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(o.Directory, storeFile)), RawQuery: "mode=rw"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	fn(db)
}

func sqlFixture(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRejectsDamagedState(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"deleted_item", "deleted_change", "old_item", "old_checkpoint", "ciphertext", "changed_digest", "extra_item", "extra_change", "checkpoint_auth", "extra_schema", "missing_schema", "version", "oversized_payload", "truncated_database"} {
		t.Run(name, func(t *testing.T) {
			s, o := createStoreFixture(t, 1024)
			a := recordFixture(1, storeSecretOne)
			if err := s.Commit(ctx, 0, []Record{a}, stateFixture(1, false)); err != nil {
				t.Fatal(err)
			}
			var oldRecord, oldCheckpoint []byte
			if err := s.conn.QueryRowContext(ctx, "SELECT payload FROM items").Scan(&oldRecord); err != nil {
				t.Fatal(err)
			}
			if err := s.conn.QueryRowContext(ctx, "SELECT payload FROM checkpoint").Scan(&oldCheckpoint); err != nil {
				t.Fatal(err)
			}
			a.Payload = []byte(storeSecretTwo)
			if err := s.Commit(ctx, 1, []Record{a, recordFixture(2, storeSecretOne)}, stateFixture(2, true)); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if name == "truncated_database" {
				if err := os.Truncate(filepath.Join(o.Directory, storeFile), 600); err != nil {
					t.Fatal(err)
				}
			} else {
				mutateStore(t, o, func(db *sql.DB) {
					switch name {
					case "deleted_item":
						sqlFixture(t, db, "DELETE FROM items WHERE ref=?", a.Ref[:])
					case "deleted_change":
						sqlFixture(t, db, "DELETE FROM changes WHERE sequence=1")
					case "old_item":
						sqlFixture(t, db, "UPDATE items SET generation=1,payload=? WHERE ref=?", oldRecord, a.Ref[:])
					case "old_checkpoint":
						sqlFixture(t, db, "UPDATE checkpoint SET generation=1,payload=?", oldCheckpoint)
					case "ciphertext":
						sqlFixture(t, db, "UPDATE items SET payload=zeroblob(length(payload)) WHERE ref=?", a.Ref[:])
					case "changed_digest":
						sqlFixture(t, db, "UPDATE changes SET digest=zeroblob(32) WHERE sequence=1")
					case "extra_item":
						r := recordFixture(999, "")
						sqlFixture(t, db, "INSERT INTO items VALUES(?,1,?)", r.Ref[:], oldRecord)
					case "extra_change":
						sqlFixture(t, db, "INSERT INTO changes VALUES(4,?,2,zeroblob(32))", a.Ref[:])
					case "checkpoint_auth":
						sqlFixture(t, db, "UPDATE checkpoint SET payload=zeroblob(length(payload))")
					case "extra_schema":
						sqlFixture(t, db, "CREATE TRIGGER unexpected AFTER INSERT ON items BEGIN DELETE FROM changes; END")
					case "missing_schema":
						sqlFixture(t, db, "DROP INDEX changes_ref")
					case "version":
						sqlFixture(t, db, "PRAGMA user_version=2")
					case "oversized_payload":
						sqlFixture(t, db, "UPDATE items SET payload=zeroblob(8000)")
					}
				})
			}
			before := snapshot(t, filepath.Dir(o.Directory))
			opened, err := OpenStore(ctx, o)
			if err == nil {
				opened.Close()
				t.Fatal("damaged checkpoint accepted")
			}
			if strings.Contains(err.Error(), filepath.Dir(o.Directory)) || strings.Contains(err.Error(), storeSecretOne) {
				t.Fatal("failure printed private storage information")
			}
			assertSnapshot(t, filepath.Dir(o.Directory), before)
		})
	}
}

func TestStoreBoundsAndConflicts(t *testing.T) {
	ctx := context.Background()
	s, o := createStoreFixture(t, 1024)
	a := recordFixture(1, "body")
	for _, tc := range []struct {
		name    string
		records []Record
		state   []byte
	}{
		{"duplicate", []Record{a, a}, nil},
		{"zero_reference", []Record{{Payload: []byte("body")}}, nil},
		{"large_record", []Record{recordFixture(1, strings.Repeat("x", 1025))}, nil},
		{"large_state", nil, make([]byte, 1024-checkpointSize+1)},
		{"many_records", make([]Record, maxBatchItems+1), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.Commit(ctx, 0, tc.records, tc.state); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid batch accepted", err)
			}
		})
	}
	if err := s.Commit(ctx, 1, []Record{a}, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("stale generation accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Commit(cancelled, 0, []Record{a}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled batch accepted")
	}
	if err := s.Visit(cancelled, func(Record) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled empty traversal accepted")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { results <- s.Commit(ctx, 0, []Record{a}, stateFixture(1, false)) })
	}
	wg.Wait()
	close(results)
	accepted, conflict := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrConflict):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if accepted != 1 || conflict != 1 {
		t.Fatal("concurrent stale progress overwrote a committed generation")
	}
	if opened, err := OpenStore(ctx, o); !errors.Is(err, ErrBusy) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("active store ownership was not exclusive", err)
	}
	s.Close()
	o.MaxPayloadBytes = checkpointSize - 1
	o.Directory = filepath.Join(filepath.Dir(o.Directory), "invalid")
	if opened, err := CreateStore(ctx, o, nil); !errors.Is(err, ErrInvalid) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("impossible checkpoint bound accepted")
	}
	if _, err := os.Stat(o.Directory); !os.IsNotExist(err) {
		t.Fatal("invalid options created a workspace")
	}
}

func TestStoreLargeSingleBatch(t *testing.T) {
	s, _ := createStoreFixture(t, 10<<20)
	ctx := context.Background()
	a := recordFixture(1, strings.Repeat("large synthetic record ", (maxBatchBytes/23)+1))
	if len(a.Payload) <= maxBatchBytes {
		t.Fatal("fixture did not cross the ordinary batch bound")
	}
	b := recordFixture(2, "another record")
	if err := s.Commit(ctx, 0, []Record{a, b}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("large multi-record batch accepted", err)
	}
	if err := s.Commit(ctx, 0, []Record{a}, nil); err != nil {
		t.Fatal("bounded large record could not commit alone", err)
	}
	if got := readAllRecords(t, s); got[a.Ref] != string(a.Payload) {
		t.Fatal("large record was truncated")
	}
}

func TestStoreFullDatabaseStopsWrites(t *testing.T) {
	s, o := createStoreFixture(t, 256<<10)
	ctx := context.Background()
	if err := s.Commit(ctx, 0, []Record{recordFixture(1, storeSecretOne)}, stateFixture(1, false)); err != nil {
		t.Fatal(err)
	}
	var pages, limit int
	if err := s.conn.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := s.conn.QueryRowContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", pages)).Scan(&limit); err != nil || limit != pages {
		t.Fatal("fixture could not constrain the database", err)
	}
	err := s.Commit(ctx, 1, []Record{recordFixture(2, strings.Repeat("x", 128<<10))}, stateFixture(2, true))
	if !errors.Is(err, ErrStorage) {
		t.Fatal("SQLITE_FULL did not stop the transaction", err)
	}
	if err := s.Commit(ctx, 1, nil, nil); !errors.Is(err, ErrReopen) {
		t.Fatal("storage failure allowed more progress")
	}
	s.Close()
	s, err = OpenStore(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkpoint, err := s.Snapshot(ctx)
	if err != nil || checkpoint.Generation != 1 || checkpoint.Records != 1 || !bytes.Equal(checkpoint.State, stateFixture(1, false)) || len(readAllRecords(t, s)) != 1 {
		t.Fatal("failed transaction advanced records/scanner/cursor", err)
	}
}

func TestStoreCancelledTransaction(t *testing.T) {
	s, o := createStoreFixture(t, 64<<10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel after real record/checkpoint writes, before the commit boundary.
	// The same staging routine is used by Commit; no fake database is used.
	tx, _, err := s.stageBatch(ctx, []Record{recordFixture(1, storeSecretTwo)}, stateFixture(1, true))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := tx.Commit(); err == nil {
		t.Fatal("cancelled transaction committed")
	}
	tx.Rollback()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkpoint, err := s.Snapshot(context.Background())
	if err != nil || checkpoint.Generation != 0 || checkpoint.Records != 0 || !bytes.Equal(checkpoint.State, stateFixture(0, false)) || len(readAllRecords(t, s)) != 0 {
		t.Fatal("cancelled transaction left partial progress", err)
	}
}

func TestStoreUnexpectedJournalModes(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm"} {
		t.Run(suffix, func(t *testing.T) {
			s, o := createStoreFixture(t, 1024)
			s.Close()
			if err := os.WriteFile(filepath.Join(o.Directory, storeFile+suffix), []byte("unexpected alternate journal"), 0600); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, filepath.Dir(o.Directory))
			if opened, err := OpenStore(context.Background(), o); !errors.Is(err, ErrInvalid) {
				if opened != nil {
					opened.Close()
				}
				t.Fatal("alternate journal was interpreted or removed", err)
			}
			assertSnapshot(t, filepath.Dir(o.Directory), before)
		})
	}
}

func TestStoreAbruptExit(t *testing.T) {
	ctx := context.Background()
	if base := os.Getenv("PIECES_RECOVERY_STORE_CHILD"); base != "" {
		o := fixtureOptions(base)
		o.MaxPayloadBytes = 64 << 10
		s, err := CreateStore(ctx, o, stateFixture(0, false))
		if err != nil {
			t.Fatal(err)
		}
		first := make([]Record, 16)
		for i := range first {
			first[i] = recordFixture(i, storeSecretOne+strings.Repeat(" first ", 128))
		}
		if err := s.Commit(ctx, 0, first, stateFixture(16, false)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.conn.ExecContext(ctx, "PRAGMA cache_size=-64"); err != nil {
			t.Fatal(err)
		}
		next := make([]Record, 50)
		for i := range next {
			next[i] = recordFixture(i+16, storeSecretTwo+strings.Repeat(" second ", 2048))
		}
		if err := s.validateBatch(1, next, stateFixture(66, true)); err != nil {
			t.Fatal(err)
		}
		tx, _, err := s.stageBatch(ctx, next, stateFixture(66, true))
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("PIECES_RECOVERY_STORE_COMMIT") == "yes" {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			os.Exit(24)
		}
		os.Exit(23) // Only this synthetic process; deliberately skip cleanup.
	}
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("committed_%t", commit), func(t *testing.T) {
			base := t.TempDir()
			o := fixtureOptions(base)
			o.MaxPayloadBytes = 64 << 10
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			childCtx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			cmd := exec.CommandContext(childCtx, exe, "-test.run=^TestStoreAbruptExit$")
			value := "no"
			if commit {
				value = "yes"
			}
			cmd.Env = append(os.Environ(), "PIECES_RECOVERY_STORE_CHILD="+base, "PIECES_RECOVERY_STORE_COMMIT="+value)
			output, err := cmd.CombinedOutput()
			var exited *exec.ExitError
			wantExit := 23
			if commit {
				wantExit = 24
			}
			if !errors.As(err, &exited) || exited.ExitCode() != wantExit {
				t.Fatalf("synthetic child did not reach intended exit: %v\n%s", err, output)
			}
			checkNoStorePlaintext(t, o)
			if !commit {
				journal, err := os.ReadFile(filepath.Join(o.Directory, storeFile+"-journal"))
				if err != nil || len(journal) <= 512 || bytes.Equal(journal[:8], make([]byte, 8)) {
					t.Fatal("fixture did not leave a spilled hot journal", err)
				}
			}
			// Authentication/configuration must succeed before SQLite gets an
			// opportunity to recover and modify an interrupted transaction.
			wrong := o
			wrong.Binding = sha256.Sum256([]byte("different config"))
			before := snapshot(t, base)
			if opened, err := OpenStore(ctx, wrong); !errors.Is(err, ErrCompatibility) {
				if opened != nil {
					opened.Close()
				}
				t.Fatal("configuration mismatch reached storage recovery", err)
			}
			assertSnapshot(t, base, before)
			var keyPath string
			keys, err := os.ReadDir(o.KeyDirectory)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range keys {
				if strings.HasSuffix(key.Name(), ".key") {
					keyPath = filepath.Join(o.KeyDirectory, key.Name())
				}
			}
			key, err := os.ReadFile(keyPath)
			if err != nil {
				t.Fatal(err)
			}
			wrongKey := bytes.Clone(key)
			wrongKey[0] ^= 1
			if err := os.WriteFile(keyPath, wrongKey, 0600); err != nil {
				t.Fatal(err)
			}
			before = snapshot(t, base)
			if opened, err := OpenStore(ctx, o); !errors.Is(err, ErrKey) {
				if opened != nil {
					opened.Close()
				}
				t.Fatal("wrong key reached storage recovery", err)
			}
			assertSnapshot(t, base, before)
			if err := os.WriteFile(keyPath, key, 0600); err != nil {
				t.Fatal(err)
			}
			s, err := OpenStore(ctx, o)
			if err != nil {
				t.Fatal("could not recover committed state", err)
			}
			defer s.Close()
			wantCount, wantGeneration := 16, uint64(1)
			if commit {
				wantCount, wantGeneration = 66, 2
			}
			checkpoint, err := s.Snapshot(ctx)
			if err != nil || checkpoint.Generation != wantGeneration || checkpoint.Records != uint64(wantCount) || checkpoint.Changes != uint64(wantCount) || !bytes.Equal(checkpoint.State, stateFixture(wantCount, commit)) || len(readAllRecords(t, s)) != wantCount {
				t.Fatal("records, accumulated credentials and progress recovered different generations", err)
			}
			checkNoStorePlaintext(t, o)
			if _, err := os.Stat(filepath.Join(o.Directory, storeFile+"-journal")); !os.IsNotExist(err) {
				t.Fatal("journal remained after recovery")
			}
		})
	}
}

func BenchmarkTransactionalStore(b *testing.B) {
	for _, size := range []int{1, 16, 50} {
		b.Run(fmt.Sprintf("batch_%d", size), func(b *testing.B) {
			ctx := context.Background()
			records := make([]Record, 128)
			for i := range records {
				records[i] = recordFixture(i, storeSecretOne+strings.Repeat("Synthetic data. ", 256))
			}
			b.SetBytes(int64(len(records) * len(records[0].Payload)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				root, err := os.MkdirTemp("", "recovery-store-bench-")
				if err != nil {
					b.Fatal(err)
				}
				o := fixtureOptions(root)
				o.MaxPayloadBytes = 64 << 10
				s, err := CreateStore(ctx, o, stateFixture(0, false))
				if err != nil {
					os.RemoveAll(root)
					b.Fatal(err)
				}
				b.StartTimer()
				generation := uint64(0)
				for start := 0; start < len(records); start += size {
					end := min(start+size, len(records))
					if err := s.Commit(ctx, generation, records[start:end], stateFixture(end, false)); err != nil {
						s.Close()
						os.RemoveAll(root)
						b.Fatal(err)
					}
					generation++
				}
				b.StopTimer()
				if err := s.Close(); err != nil {
					os.RemoveAll(root)
					b.Fatal(err)
				}
				os.RemoveAll(root)
			}
			b.ReportMetric(float64(b.N*len(records))/b.Elapsed().Seconds(), "records/s")
		})
	}
}
