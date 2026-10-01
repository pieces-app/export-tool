package exporter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"sort"
	"time"

	"github.com/pieces-app/export-tool/internal/recovery"
)

const canonicalBatchRecords = 50
const canonicalBatchBytes = 7 << 20
const canonicalPayloadLimit = (128 << 20) - (64 << 10)
const canonicalTransactionState = "canonical-stage/v1;not-a-source-recovery-checkpoint"

// This is a private capture backend, not a public archive format or a source
// resume adapter. Its caller owns the store/key lifecycle and must materialize
// the accepted public record set before final auditing. It is not yet selected
// by the CLI: grouped public records/navigation must be integrated first.
// Operations are sequential, just like source/graph/privacy processing.
type transactionalCanonicalRecords struct {
	store        *recovery.Store
	metrics      *localMeasurements
	generation   uint64
	known        map[[32]byte]bool
	pending      map[[32]byte][]byte
	pendingBytes int
	failed       bool
}

func newTransactionalCanonicalRecords(ctx context.Context, store *recovery.Store, metrics *localMeasurements) (*transactionalCanonicalRecords, error) {
	if store == nil {
		return nil, errConfig("canonical transactions require an owned private store")
	}
	cp, err := store.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	clear(cp.State)
	if cp.Generation != 0 || cp.Records != 0 || cp.Changes != 0 {
		return nil, errConfig("canonical transactions require a fresh private store")
	}
	return &transactionalCanonicalRecords{store: store, metrics: metrics, known: map[[32]byte]bool{}, pending: map[[32]byte][]byte{}}, nil
}

func canonicalStageRef(m *Meta) ([32]byte, error) {
	if m == nil || m.Type == "" || m.ID == "" || m.Key != m.Type+"\x00"+m.ID {
		return [32]byte{}, errConfig("canonical record identity is invalid")
	}
	return sha256.Sum256([]byte("canonical-stage/v1\x00" + m.Key)), nil
}

func (s *transactionalCanonicalRecords) usable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.failed {
		return errConfig("canonical transaction failed; this export cannot continue")
	}
	return nil
}

type privateCanonicalReader struct {
	*bytes.Reader
	payload []byte
}

func (r *privateCanonicalReader) Close() error {
	clear(r.payload)
	r.payload = nil
	r.Reader.Reset(nil)
	return nil
}

func (s *transactionalCanonicalRecords) Open(ctx context.Context, m *Meta) (io.ReadCloser, error) {
	if err := s.usable(ctx); err != nil {
		return nil, err
	}
	ref, err := canonicalStageRef(m)
	if err != nil {
		return nil, err
	}
	if !s.known[ref] {
		return nil, os.ErrNotExist
	}
	var payload []byte
	if pending, ok := s.pending[ref]; ok {
		// An open reader must survive later replacement/flush clearing buffers.
		payload = bytes.Clone(pending)
	} else {
		payload, err = s.store.Get(ctx, ref)
		if err != nil {
			return nil, err
		}
	}
	if len(payload) < 5 || !bytes.Equal(payload[:5], []byte{'P', 'C', 'S', '1', 0}) {
		clear(payload)
		return nil, errConfig("canonical transaction payload is invalid")
	}
	return &privateCanonicalReader{Reader: bytes.NewReader(payload[5:]), payload: payload}, nil
}

func (s *transactionalCanonicalRecords) Write(ctx context.Context, m *Meta, v map[string]any, replace bool) error {
	if err := s.usable(ctx); err != nil {
		return err
	}
	ref, err := canonicalStageRef(m)
	if err != nil {
		return err
	}
	if fieldString(v, "id") != m.ID {
		return errConfig("canonical value does not match its identity")
	}
	if s.known[ref] && !replace {
		return os.ErrExist
	}
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	defer clear(body)
	if len(body) > canonicalPayloadLimit-6 {
		return errConfig("canonical record exceeds transactional storage bounds")
	}
	payload := make([]byte, 5, len(body)+6)
	copy(payload, []byte{'P', 'C', 'S', '1', 0})
	payload = append(payload, body...)
	payload = append(payload, '\n')
	if err := s.enqueue(ctx, ref, payload); err != nil {
		clear(payload)
		return err
	}
	s.known[ref] = true
	return nil
}

func (s *transactionalCanonicalRecords) Remove(ctx context.Context, m *Meta) error {
	if err := s.usable(ctx); err != nil {
		return err
	}
	ref, err := canonicalStageRef(m)
	if err != nil {
		return err
	}
	if !s.known[ref] {
		return nil
	}
	// Persist an explicit tombstone; absence never means an approved exclusion.
	if err := s.enqueue(ctx, ref, []byte{'P', 'C', 'S', '1', 1}); err != nil {
		return err
	}
	s.known[ref] = false
	return nil
}

func (s *transactionalCanonicalRecords) enqueue(ctx context.Context, ref [32]byte, payload []byte) error {
	prior, exists := s.pending[ref]
	count := len(s.pending)
	if !exists {
		count++
	}
	if len(s.pending) > 0 && (count > canonicalBatchRecords || s.pendingBytes-len(prior)+len(payload) > canonicalBatchBytes) {
		if err := s.Flush(ctx); err != nil {
			return err
		}
		prior = nil
	}
	clear(prior)
	s.pendingBytes += len(payload) - len(prior)
	s.pending[ref] = payload
	// Large records run alone; count/byte-full batches commit before returning.
	if len(s.pending) >= canonicalBatchRecords || s.pendingBytes >= canonicalBatchBytes {
		return s.Flush(ctx)
	}
	return nil
}

func (s *transactionalCanonicalRecords) Flush(ctx context.Context) (result error) {
	if err := s.usable(ctx); err != nil {
		return err
	}
	if len(s.pending) == 0 {
		return nil
	}
	rows := make([]recovery.Record, 0, len(s.pending))
	for ref, payload := range s.pending {
		rows = append(rows, recovery.Record{Ref: ref, Payload: payload})
	}
	sort.Slice(rows, func(i, j int) bool { return bytes.Compare(rows[i].Ref[:], rows[j].Ref[:]) < 0 })
	started := time.Now()
	result = s.store.Commit(ctx, s.generation, rows, []byte(canonicalTransactionState))
	s.metrics.record("canonical_transaction", time.Since(started), int64(s.pendingBytes), result)
	if result != nil {
		s.failed = true
		return result
	}
	s.generation++
	s.discardPending()
	return nil
}

func (s *transactionalCanonicalRecords) discardPending() {
	for ref, payload := range s.pending {
		clear(payload)
		delete(s.pending, ref)
	}
	s.pendingBytes = 0
}

var _ canonicalRecordStore = (*transactionalCanonicalRecords)(nil)
