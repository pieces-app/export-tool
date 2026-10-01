package exporter

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Canonical identity and navigation belong to Meta, not to the physical record
// representation. All current-record consumers use this boundary so a grouped
// backend can preserve privacy/recovery behavior without pretending every
// record is an independent file. Public archive import is a separate boundary.
// Implementations must make pending writes visible to Open, bound buffering,
// and report storage errors at Write or Flush. Flush is required before capture
// and before output validation; it is not a resumable source checkpoint.
type canonicalRecordStore interface {
	Open(context.Context, *Meta) (io.ReadCloser, error)
	Write(context.Context, *Meta, map[string]any, bool) error
	Remove(context.Context, *Meta) error
	Flush(context.Context) error
}

type fileCanonicalRecords struct{ run *run }

type canonicalFileReader struct {
	*os.File
	root *os.Root
}

func (r *canonicalFileReader) Close() error {
	err := r.File.Close()
	rootErr := r.root.Close()
	if err != nil {
		return err
	}
	return rootErr
}

func (r *run) canonicalRecords() canonicalRecordStore {
	if r.records == nil {
		r.records = &fileCanonicalRecords{run: r}
	}
	return r.records
}

func (s *fileCanonicalRecords) recordPath(ctx context.Context, m *Meta) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if m == nil || !safeArchivePath(m.DataPath) {
		return "", errConfig("canonical record has an invalid storage path")
	}
	return filepath.Join(s.run.stage, m.DataPath), nil
}

func (s *fileCanonicalRecords) Open(ctx context.Context, m *Meta) (io.ReadCloser, error) {
	_, err := s.recordPath(ctx, m)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.run.stage)
	if err != nil {
		return nil, err
	}
	f, err := archiveOpen(root, m.DataPath)
	if err != nil {
		root.Close()
		return nil, err
	}
	return &canonicalFileReader{File: f, root: root}, nil
}

func (s *fileCanonicalRecords) Write(ctx context.Context, m *Meta, v map[string]any, replace bool) error {
	path, err := s.recordPath(ctx, m)
	if err != nil {
		return err
	}
	if replace {
		return s.run.rewriteJSON(path, v)
	}
	return s.run.writeJSON(path, v)
}

func (s *fileCanonicalRecords) Remove(ctx context.Context, m *Meta) error {
	path, err := s.recordPath(ctx, m)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// The existing backend syncs every write before returning. This boundary does
// not remove or defer any of those sync calls.
func (s *fileCanonicalRecords) Flush(ctx context.Context) error { return ctx.Err() }

func (r *run) readCanonical(m *Meta) (v map[string]any, result error) {
	started := time.Now()
	counter := &countedReader{}
	defer func() { r.local.record("canonical_json_read", time.Since(started), counter.bytes, result) }()
	f, err := r.canonicalRecords().Open(r.ctx, m)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	counter.Reader = f
	d := json.NewDecoder(counter)
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return v, r.ctx.Err()
}

func (r *run) readCanonicalBytes(m *Meta, limit int64) ([]byte, error) {
	if limit <= 0 || limit > 128<<20 {
		return nil, errConfig("invalid canonical record read limit")
	}
	f, err := r.canonicalRecords().Open(r.ctx, m)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errConfig("canonical record exceeds its read bound or could not be read")
	}
	return b, r.ctx.Err()
}

func (r *run) canonicalDigest(m *Meta) (string, error) {
	f, err := r.canonicalRecords().Open(r.ctx, m)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return readerDigest(r.ctx, f)
}

func (r *run) writeCanonical(m *Meta, v map[string]any, replace bool) error {
	return r.canonicalRecords().Write(r.ctx, m, v, replace)
}

func (r *run) removeCanonical(m *Meta) error {
	return r.canonicalRecords().Remove(r.ctx, m)
}
