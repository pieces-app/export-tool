package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/pieces-app/export-tool/internal/recovery"
)

func canonicalFixture(id int, text string) (*Meta, map[string]any) {
	name := fmt.Sprintf("record-%06d", id)
	m := &Meta{ID: name, Type: "ANNOTATIONS", Key: "ANNOTATIONS\x00" + name, DataPath: "data/annotations/" + opaque("ANNOTATIONS", name) + ".json", State: "included"}
	return m, map[string]any{"id": name, "text": text, "precise": json.Number("9007199254740993")}
}

func stagedFixture(t *testing.T) (*transactionalCanonicalRecords, *recovery.Store) {
	t.Helper()
	store, _ := captureTestStore(t)
	s, err := newTransactionalCanonicalRecords(context.Background(), store, newLocalMeasurements())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.discardPending)
	return s, store
}

func readStageFixture(t *testing.T, s *transactionalCanonicalRecords, m *Meta) []byte {
	t.Helper()
	f, err := s.Open(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	if err := errors.Join(err, f.Close()); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCanonicalTransactionsReadReplaceAndTombstones(t *testing.T) {
	ctx := context.Background()
	s, store := stagedFixture(t)
	m, v := canonicalFixture(1, "Original text")
	if err := s.Write(ctx, m, v, false); err != nil {
		t.Fatal(err)
	}
	ref, _ := canonicalStageRef(m)
	if _, err := store.Get(ctx, ref); !errors.Is(err, recovery.ErrNotFound) {
		t.Fatal("partial batch was committed without its boundary", err)
	}
	opened, err := s.Open(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := s.Write(ctx, m, v, false); !errors.Is(err, os.ErrExist) {
		t.Fatal("duplicate canonical create accepted", err)
	}
	v["text"] = "Replacement text"
	if err := s.Write(ctx, m, v, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	prior, err := io.ReadAll(opened)
	if err != nil || !bytes.Contains(prior, []byte("Original text")) {
		t.Fatal("replacement or flush cleared a caller-owned reader", err)
	}
	body := readStageFixture(t, s, m)
	if !bytes.Contains(body, []byte("Replacement text")) || !bytes.Contains(body, []byte("9007199254740993")) {
		t.Fatal("canonical bytes lost precision or latest value")
	}
	if err := s.Remove(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, m); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("removed pending record remained readable", err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	tombstone, err := store.Get(ctx, ref)
	if err != nil || !bytes.Equal(tombstone, []byte{'P', 'C', 'S', '1', 1}) {
		t.Fatal("record removal lost its explicit transaction decision", err)
	}
	clear(tombstone)
	if err := s.Write(ctx, m, v, false); err != nil {
		t.Fatal("recreate after removal failed", err)
	}
	if _, err := newTransactionalCanonicalRecords(ctx, store, nil); err == nil {
		t.Fatal("temporary adapter reopened nonempty state as a fresh run")
	}
}

func TestCanonicalTransactionsBoundedBatches(t *testing.T) {
	ctx := context.Background()
	s, store := stagedFixture(t)
	for i := 0; i < 123; i++ {
		m, v := canonicalFixture(i, "Ordinary synthetic content")
		if err := s.Write(ctx, m, v, false); err != nil {
			t.Fatal(err)
		}
		if len(s.pending) >= canonicalBatchRecords || s.pendingBytes >= canonicalBatchBytes {
			t.Fatal("full capture batch was left buffered")
		}
	}
	cp, err := store.Snapshot(ctx)
	if err != nil || cp.Generation != 2 || cp.Records != 100 || len(s.pending) != 23 {
		t.Fatal("record batches did not reconcile", err, cp.Generation, cp.Records)
	}
	clear(cp.State)
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	cp, err = store.Snapshot(ctx)
	if err != nil || cp.Generation != 3 || cp.Records != 123 || string(cp.State) != canonicalTransactionState || len(s.pending) != 0 || s.pendingBytes != 0 {
		t.Fatal("terminal capture batch did not commit", err)
	}
	clear(cp.State)
	for i := 0; i < 123; i++ {
		m, _ := canonicalFixture(i, "")
		if !bytes.Contains(readStageFixture(t, s, m), []byte(m.ID)) {
			t.Fatal("selected lookup returned the wrong identity")
		}
	}
	if s.metrics.snapshot()["canonical_transaction"].Calls != 3 {
		t.Fatal("commit accounting differs from actual generations")
	}
}

func TestCanonicalTransactionsByteBoundsAndLargeRecord(t *testing.T) {
	ctx := context.Background()
	s, store := stagedFixture(t)
	for i := 0; i < 3; i++ {
		m, v := canonicalFixture(i, strings.Repeat("x", 3<<20))
		if err := s.Write(ctx, m, v, false); err != nil {
			t.Fatal(err)
		}
	}
	if s.generation != 1 || len(s.pending) != 1 || s.pendingBytes > canonicalBatchBytes {
		t.Fatal("byte-bounded batches did not flush before overflow")
	}
	m, v := canonicalFixture(99, strings.Repeat("x", 8<<20))
	if err := s.Write(ctx, m, v, false); err != nil {
		t.Fatal(err)
	}
	cp, err := store.Snapshot(ctx)
	if err != nil || cp.Generation != 3 || cp.Records != 4 || len(s.pending) != 0 {
		t.Fatal("large record did not run alone after draining prior work", err)
	}
	clear(cp.State)
	if got := readStageFixture(t, s, m); len(got) < 8<<20 {
		t.Fatal("large canonical record was truncated")
	}
}

func TestCanonicalTransactionsFailuresStopAccess(t *testing.T) {
	ctx := context.Background()
	s, store := stagedFixture(t)
	m, v := canonicalFixture(1, "Approved")
	wrong := *m
	wrong.Key = "wrong"
	if err := s.Write(ctx, &wrong, v, false); err == nil {
		t.Fatal("mismatched canonical identity accepted")
	}
	if err := s.Write(ctx, m, map[string]any{"id": "wrong"}, false); err == nil {
		t.Fatal("mismatched payload identity accepted")
	}
	if err := s.Write(ctx, m, v, false); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Flush(canceled); !errors.Is(err, context.Canceled) || len(s.pending) != 1 {
		t.Fatal("cancellation advanced or discarded pending capture", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); !errors.Is(err, recovery.ErrClosed) {
		t.Fatal("storage failure was hidden", err)
	}
	if err := s.Write(ctx, m, v, true); err == nil {
		t.Fatal("capture continued after a failed transaction")
	}
	if _, err := s.Open(ctx, m); err == nil {
		t.Fatal("uncertain capture state remained readable")
	}
}
