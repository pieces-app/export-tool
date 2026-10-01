package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestStoreGetCurrentPayloadAndOwnership(t *testing.T) {
	ctx := context.Background()
	s, o := createStoreFixture(t, 64<<10)
	a, b := recordFixture(1, storeSecretOne), recordFixture(2, "")
	if _, err := s.Get(ctx, a.Ref); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing record was not distinguished", err)
	}
	if _, err := s.Get(ctx, [32]byte{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("reserved zero reference accepted", err)
	}
	if err := s.Commit(ctx, 0, []Record{a, b}, stateFixture(2, false)); err != nil {
		t.Fatal(err)
	}
	for _, row := range []Record{a, b} {
		body, err := s.Get(ctx, row.Ref)
		if err != nil || !bytes.Equal(body, row.Payload) {
			t.Fatal("current bytes changed", err)
		}
		clear(body)
	}
	body, err := s.Get(ctx, a.Ref)
	if err != nil || !bytes.Equal(body, a.Payload) {
		t.Fatal("returned bytes alias stored data", err)
	}
	clear(body)
	a.Payload = []byte(storeSecretTwo)
	if err := s.Commit(ctx, 1, []Record{a}, stateFixture(2, true)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, a.Ref); !errors.Is(err, ErrClosed) {
		t.Fatal("closed store remained readable", err)
	}
	s, err = OpenStore(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body, err = s.Get(ctx, a.Ref)
	if err != nil || !bytes.Equal(body, a.Payload) {
		t.Fatal("reopen returned a stale generation", err)
	}
	clear(body)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Get(canceled, a.Ref); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled lookup performed work", err)
	}
	s.failed = true
	if _, err := s.Get(ctx, a.Ref); !errors.Is(err, ErrReopen) {
		t.Fatal("uncertain commit state remained readable", err)
	}
}

func TestStoreGetRejectsCorruptSelectedRecord(t *testing.T) {
	for _, damage := range []string{"old_record", "future_generation", "missing_change", "digest", "ciphertext", "oversized"} {
		t.Run(damage, func(t *testing.T) {
			ctx := context.Background()
			s, _ := createStoreFixture(t, 1024)
			a := recordFixture(1, storeSecretOne)
			if err := s.Commit(ctx, 0, []Record{a}, stateFixture(1, false)); err != nil {
				t.Fatal(err)
			}
			var old []byte
			if err := s.conn.QueryRowContext(ctx, "SELECT payload FROM items WHERE ref=?", a.Ref[:]).Scan(&old); err != nil {
				t.Fatal(err)
			}
			a.Payload = []byte(storeSecretTwo)
			if err := s.Commit(ctx, 1, []Record{a}, stateFixture(1, true)); err != nil {
				t.Fatal(err)
			}
			var err error
			switch damage {
			case "old_record":
				_, err = s.conn.ExecContext(ctx, "UPDATE items SET generation=1,payload=? WHERE ref=?", old, a.Ref[:])
			case "future_generation":
				_, err = s.conn.ExecContext(ctx, "UPDATE items SET generation=3 WHERE ref=?", a.Ref[:])
			case "missing_change":
				_, err = s.conn.ExecContext(ctx, "DELETE FROM changes WHERE ref=?", a.Ref[:])
			case "digest":
				_, err = s.conn.ExecContext(ctx, "UPDATE changes SET digest=zeroblob(32) WHERE sequence=2")
			case "ciphertext":
				blob := bytes.Repeat([]byte{1}, envelopeOverhead+32)
				digest := sha256.Sum256(blob)
				if _, err = s.conn.ExecContext(ctx, "UPDATE items SET payload=? WHERE ref=?", blob, a.Ref[:]); err == nil {
					_, err = s.conn.ExecContext(ctx, "UPDATE changes SET digest=? WHERE sequence=2", digest[:])
				}
			case "oversized":
				_, err = s.conn.ExecContext(ctx, "UPDATE items SET payload=zeroblob(8000) WHERE ref=?", a.Ref[:])
			}
			if err != nil {
				t.Fatal(err)
			}
			if body, err := s.Get(ctx, a.Ref); err == nil || len(body) != 0 {
				t.Fatal("invalid selected bytes were returned", err)
			}
		})
	}
}

func TestStoreGetReadsOnlySelectedRecord(t *testing.T) {
	ctx := context.Background()
	s, _ := createStoreFixture(t, 1024)
	a, b := recordFixture(1, "selected"), recordFixture(2, "unrelated")
	if err := s.Commit(ctx, 0, []Record{a, b}, stateFixture(2, false)); err != nil {
		t.Fatal(err)
	}
	// A selected-record read is not global integrity verification. Corrupting
	// another row proves this operation does not visit/decrypt that payload;
	// Visit/OpenStore still perform their respective whole-store checks.
	if _, err := s.conn.ExecContext(ctx, "UPDATE items SET payload=zeroblob(32) WHERE ref=?", b.Ref[:]); err != nil {
		t.Fatal(err)
	}
	body, err := s.Get(ctx, a.Ref)
	if err != nil || !bytes.Equal(body, a.Payload) {
		t.Fatal("lookup read unrelated payloads", err)
	}
	clear(body)
	if err := s.Visit(ctx, func(Record) error { return nil }); err == nil {
		t.Fatal("whole-store reader accepted corruption")
	}
}
