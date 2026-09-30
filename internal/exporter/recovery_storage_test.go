package exporter

// This is a bounded storage experiment, not the exporter's resume feature.
// It uses only synthetic payloads and its own child process. Never point it at
// Pieces OS, a real archive, or another process's database.

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const recoveryProbeFirstSecret = "synthetic-checkpoint-credential-one"
const recoveryProbeNextSecret = "synthetic-checkpoint-credential-two"

type recoveryProbeState struct {
	Known  []string
	NextID int
}

func recoveryProbeBody(id int, secret string) []byte {
	return []byte(fmt.Sprintf("record %d: %s\n%s", id, secret, strings.Repeat("Synthetic data. ", 256)))
}

func recoveryProbeCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func recoveryProbeSeal(a cipher.AEAD, binding string, value []byte) []byte {
	return a.Seal(nil, nil, value, []byte("storage-probe-v1/"+binding))
}

func recoveryProbeOpen(a cipher.AEAD, binding string, value []byte) ([]byte, error) {
	return a.Open(nil, nil, value, []byte("storage-probe-v1/"+binding))
}

func recoveryProbeDB(path string, create bool) (*sql.DB, error) {
	if create {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := u.Query()
	q.Set("mode", "rw")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	// EXTRA also requests rollback-journal directory sync on commit. Read
	// back settings: SQLite silently ignores unknown PRAGMA names.
	for _, query := range []string{
		"PRAGMA journal_mode=DELETE", "PRAGMA synchronous=EXTRA",
		"PRAGMA fullfsync=ON", "PRAGMA temp_store=MEMORY", "PRAGMA cache_size=-64",
	} {
		if _, err := db.Exec(query); err != nil {
			db.Close()
			return nil, err
		}
	}
	for query, want := range map[string]string{
		"PRAGMA journal_mode": "delete", "PRAGMA synchronous": "3",
		"PRAGMA fullfsync": "1", "PRAGMA temp_store": "2", "PRAGMA cache_size": "-64",
	} {
		var got string
		if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
			db.Close()
			return nil, fmt.Errorf("storage probe setting %s not applied", query)
		}
	}
	if create {
		for _, query := range []string{
			"CREATE TABLE records (id INTEGER PRIMARY KEY, payload BLOB NOT NULL)",
			"CREATE TABLE checkpoint (id INTEGER PRIMARY KEY CHECK(id=1), sequence INTEGER NOT NULL, scanner BLOB NOT NULL)",
		} {
			if _, err := db.Exec(query); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	return db, nil
}

func recoveryProbeBatch(db *sql.DB, a cipher.AEAD, start, count, sequence int, secret string) (*sql.Tx, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = tx.Rollback()
		}
	}()
	stmt, err := tx.Prepare("INSERT INTO records(id,payload) VALUES(?,?)")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	for id := start; id < start+count; id++ {
		body := recoveryProbeBody(id, secret)
		if _, err := stmt.Exec(id, recoveryProbeSeal(a, fmt.Sprintf("record/%d", id), body)); err != nil {
			return nil, err
		}
	}
	checkpoint := recoveryProbeState{Known: []string{recoveryProbeFirstSecret}, NextID: start + count}
	if secret != recoveryProbeFirstSecret {
		checkpoint.Known = append(checkpoint.Known, secret)
	}
	plain, err := json.Marshal(checkpoint)
	if err != nil {
		return nil, err
	}
	state := recoveryProbeSeal(a, fmt.Sprintf("scanner/%d", sequence), plain)
	if _, err := tx.Exec("INSERT OR REPLACE INTO checkpoint(id,sequence,scanner) VALUES(1,?,?)", sequence, state); err != nil {
		return nil, err
	}
	failed = false
	return tx, nil
}

func TestRecoveryStorageAbruptExit(t *testing.T) {
	if path := os.Getenv("PIECES_RECOVERY_PROBE_CHILD_DB"); path != "" {
		// The parent supplies an in-memory key through stdin. No key, real
		// credential, record identity, or source data enters the environment.
		key := make([]byte, 32)
		if _, err := io.ReadFull(os.Stdin, key); err != nil {
			t.Fatal(err)
		}
		a, err := recoveryProbeCipher(key)
		if err != nil {
			t.Fatal(err)
		}
		db, err := recoveryProbeDB(path, true)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := recoveryProbeBatch(db, a, 0, 16, 1, recoveryProbeFirstSecret)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		// Larger than the 64 KiB page cache: exercises spilled pages and a
		// hot rollback journal, not just an abandoned in-memory transaction.
		tx, err = recoveryProbeBatch(db, a, 16, 512, 2, recoveryProbeNextSecret)
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("PIECES_RECOVERY_PROBE_COMMIT") == "yes" {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			os.Exit(24)
		}
		os.Exit(23) // Deliberately skip Rollback, Close, and every deferred call.
	}
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("second_batch_committed_%t", commit), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "checkpoint.sqlite")
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestRecoveryStorageAbruptExit$")
			cmd.Env = append(os.Environ(), "PIECES_RECOVERY_PROBE_CHILD_DB="+path, "PIECES_RECOVERY_PROBE_COMMIT=no")
			if commit {
				cmd.Env[len(cmd.Env)-1] = "PIECES_RECOVERY_PROBE_COMMIT=yes"
			}
			cmd.Stdin = bytes.NewReader(key)
			output, err := cmd.CombinedOutput()
			var exited *exec.ExitError
			wantExit, wantCount, wantSequence, wantSecret := 23, 16, 1, recoveryProbeFirstSecret
			if commit {
				wantExit, wantCount, wantSequence, wantSecret = 24, 528, 2, recoveryProbeNextSecret
			}
			if !errors.As(err, &exited) || exited.ExitCode() != wantExit {
				t.Fatalf("synthetic child did not reach its intended abrupt exit: %v; %s", err, output)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			journal := false
			for _, entry := range entries {
				data, err := os.ReadFile(filepath.Join(root, entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				journal = journal || entry.Name() == "checkpoint.sqlite-journal" && len(data) > 512
				if bytes.Contains(data, key) || bytes.Contains(data, []byte(recoveryProbeFirstSecret)) || bytes.Contains(data, []byte(recoveryProbeNextSecret)) {
					t.Fatal("key or plaintext credential entered the database/journal")
				}
			}
			if !commit && !journal {
				t.Fatal("experiment did not leave a nonempty hot rollback journal")
			}
			db, err := recoveryProbeDB(path, false)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var integrity string
			if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("reopened database integrity: %s, %v", integrity, err)
			}
			var count, sequence int
			var state []byte
			if err := db.QueryRow("SELECT COUNT(*) FROM records").Scan(&count); err != nil || count != wantCount {
				t.Fatalf("recovered count %d, expected %d: %v", count, wantCount, err)
			}
			if err := db.QueryRow("SELECT sequence,scanner FROM checkpoint WHERE id=1").Scan(&sequence, &state); err != nil || sequence != wantSequence {
				t.Fatalf("checkpoint advanced independently of records: %d, %v", sequence, err)
			}
			a, err := recoveryProbeCipher(key)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := recoveryProbeOpen(a, fmt.Sprintf("scanner/%d", sequence), state)
			var restored recoveryProbeState
			if err != nil || json.Unmarshal(decoded, &restored) != nil || restored.NextID != wantCount || len(restored.Known) != wantSequence || restored.Known[0] != recoveryProbeFirstSecret || restored.Known[len(restored.Known)-1] != wantSecret {
				t.Fatal("scanner state did not recover with its batch")
			}
			wrongKey := bytes.Clone(key)
			wrongKey[0] ^= 1
			wrongCipher, err := recoveryProbeCipher(wrongKey)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := recoveryProbeOpen(wrongCipher, fmt.Sprintf("scanner/%d", sequence), state); err == nil {
				t.Fatal("incorrect key could open stored scanner state")
			}
			for _, id := range []int{0, wantCount - 1} {
				var encrypted []byte
				if err := db.QueryRow("SELECT payload FROM records WHERE id=?", id).Scan(&encrypted); err != nil {
					t.Fatal(err)
				}
				decoded, err := recoveryProbeOpen(a, fmt.Sprintf("record/%d", id), encrypted)
				secret := recoveryProbeFirstSecret
				if id >= 16 {
					secret = recoveryProbeNextSecret
				}
				if err != nil || !bytes.Contains(decoded, []byte(secret)) {
					t.Fatal("recovered record body differs from its committed batch")
				}
				if _, err := recoveryProbeOpen(a, fmt.Sprintf("record/%d", id+1), encrypted); err == nil {
					t.Fatal("ciphertext could be assigned to another record")
				}
				encrypted[len(encrypted)-1] ^= 1
				if _, err := recoveryProbeOpen(a, fmt.Sprintf("record/%d", id), encrypted); err == nil {
					t.Fatal("modified record passed authentication")
				}
			}
			t.Logf("recovered %d committed rows and scanner generation %d after abrupt exit; synthetic payload absent from database/journal bytes", count, sequence)
		})
	}
}

// Measures only storage of 128 synthetic roughly 4 KiB payloads. This includes
// body formatting and, for SQLite, encryption and a scanner/cursor snapshot in
// each transaction. Schema/key setup and cleanup are excluded. It neither
// materializes a user archive nor measures source reads, privacy or graph work.
func BenchmarkRecoveryStorage(b *testing.B) {
	const records = 128
	for _, batch := range []int{0, 1, 16, 50} {
		name := fmt.Sprintf("sqlite_batch_%d", batch)
		if batch == 0 {
			name = "synced_files"
		}
		b.Run(name, func(b *testing.B) {
			root := b.TempDir()
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				b.Fatal(err)
			}
			a, err := recoveryProbeCipher(key)
			if err != nil {
				b.Fatal(err)
			}
			var db *sql.DB
			if batch != 0 {
				db, err = recoveryProbeDB(filepath.Join(root, "checkpoint.sqlite"), true)
				if err != nil {
					b.Fatal(err)
				}
				defer db.Close()
			}
			commits := 0
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				start, limit := iteration*records, (iteration+1)*records
				if batch == 0 {
					for id := start; id < limit; id++ {
						path := filepath.Join(root, fmt.Sprintf("record-%08d.json", id))
						if err := writeFile(path, recoveryProbeBody(id, recoveryProbeFirstSecret)); err != nil {
							b.Fatal(err)
						}
					}
					continue
				}
				for id := start; id < limit; id += batch {
					tx, err := recoveryProbeBatch(db, a, id, min(batch, limit-id), commits+1, recoveryProbeFirstSecret)
					if err != nil {
						b.Fatal(err)
					}
					if err := tx.Commit(); err != nil {
						b.Fatal(err)
					}
					commits++
				}
			}
			b.StopTimer()
			if db != nil {
				var count int
				if err := db.QueryRow("SELECT COUNT(*) FROM records").Scan(&count); err != nil || count != records*b.N {
					b.Fatalf("storage count %d does not match committed payloads: %v", count, err)
				}
				b.ReportMetric(float64(commits)/float64(b.N), "transactions/op")
			}
			b.ReportMetric(float64(records*b.N)/b.Elapsed().Seconds(), "records/s")
		})
	}
}
