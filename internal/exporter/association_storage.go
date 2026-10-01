package exporter

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/pieces-app/export-tool/internal/recovery"
)

// User-facing documents retain their individual files. Selected canonical
// records remain private until selection finishes, then become bounded JSONL.
// This temporary database is never a source recovery checkpoint.
type associationCanonicalRecords struct {
	run          *run
	files        fileCanonicalRecords
	transactions *transactionalCanonicalRecords
	store        *recovery.Store
	parent       string
	parentInfo   os.FileInfo
	parentRoot   *os.Root
	closed       bool
}

func (s *associationCanonicalRecords) ensure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed {
		return errConfig("association staging is closed")
	}
	if s.transactions != nil {
		return nil
	}
	// Detect an unusable public destination before accepting source records
	// into private staging. Publishing still happens only after privacy checks.
	root, err := os.OpenRoot(s.run.stage)
	if err != nil {
		return err
	}
	prefix := "data"
	if s.run.opts.Mode == "preserve" {
		prefix = "raw"
	}
	err = root.Mkdir(prefix, 0700)
	if err == nil || os.IsExist(err) {
		info, inspectErr := root.Lstat(prefix)
		err = inspectErr
		if err == nil && !info.IsDir() {
			err = errConfig("canonical destination is not a directory")
		}
	}
	closeErr := root.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	parent, err := os.MkdirTemp(filepath.Dir(s.run.stage), ".pieces-export-stage-")
	if err != nil {
		return err
	}
	s.parent = parent
	s.parentInfo, err = os.Lstat(parent)
	if err != nil {
		return err
	}
	s.parentRoot, err = os.OpenRoot(parent)
	if err != nil {
		return err
	}
	held, err := s.parentRoot.Stat(".")
	if err != nil || !os.SameFile(held, s.parentInfo) {
		s.parentRoot.Close()
		s.parentRoot = nil
		return errConfig("temporary association directory identity changed")
	}
	// Recovery applies/verifies owner-only permissions to both child roots,
	// including Windows ACLs, before storing keys or encrypted data.
	s.store, err = recovery.CreateStore(ctx, recovery.Options{
		Directory: filepath.Join(parent, "records"), KeyDirectory: filepath.Join(parent, "keys"),
		Binding: sha256.Sum256([]byte("pieces-export transient canonical storage v2")),
	}, []byte(canonicalTransactionState))
	if err != nil {
		return err
	}
	s.transactions, err = newTransactionalCanonicalRecords(ctx, s.store, s.run.local)
	return err
}

func (s *associationCanonicalRecords) Open(ctx context.Context, m *Meta) (io.ReadCloser, error) {
	if m != nil && m.DataLength > 0 {
		return openCanonicalSection(ctx, s.run.stage, m)
	}
	if m != nil {
		if groupedCanonicalMaterial(m.Type) {
			if err := s.ensure(ctx); err != nil {
				return nil, err
			}
			return s.transactions.Open(ctx, m)
		}
	}
	return s.files.Open(ctx, m)
}

func (s *associationCanonicalRecords) Write(ctx context.Context, m *Meta, v map[string]any, replace bool) error {
	if m != nil && m.DataLength > 0 {
		return errConfig("cannot rewrite a published canonical row")
	}
	if m != nil {
		if groupedCanonicalMaterial(m.Type) {
			if err := s.ensure(ctx); err != nil {
				return err
			}
			return s.transactions.Write(ctx, m, v, replace)
		}
	}
	return s.files.Write(ctx, m, v, replace)
}

func (s *associationCanonicalRecords) Remove(ctx context.Context, m *Meta) error {
	if m != nil && m.DataLength > 0 {
		return errConfig("cannot remove a published canonical row")
	}
	if m != nil {
		if groupedCanonicalMaterial(m.Type) {
			if err := s.ensure(ctx); err != nil {
				return err
			}
			return s.transactions.Remove(ctx, m)
		}
	}
	return s.files.Remove(ctx, m)
}

func (s *associationCanonicalRecords) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.transactions != nil && !s.closed {
		return s.transactions.Flush(ctx)
	}
	return nil
}

type canonicalSectionReader struct {
	*io.SectionReader
	file *os.File
	root *os.Root
}

func (s *canonicalSectionReader) Close() error { return errors.Join(s.file.Close(), s.root.Close()) }

func openCanonicalSection(ctx context.Context, directory string, m *Meta) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.DataOffset < 0 || m.DataLength < 1 || m.DataLength > canonicalPayloadLimit || !safeArchivePath(m.DataPath) {
		return nil, errConfig("invalid grouped canonical row")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	f, err := archiveOpen(root, m.DataPath)
	if err != nil {
		root.Close()
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || m.DataOffset > info.Size()-m.DataLength {
		f.Close()
		root.Close()
		return nil, errConfig("grouped canonical row is truncated")
	}
	return &canonicalSectionReader{io.NewSectionReader(f, m.DataOffset, m.DataLength), f, root}, nil
}

func (s *associationCanonicalRecords) close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.transactions != nil {
		s.transactions.discardPending()
	}
	var result error
	if s.store != nil {
		result = s.store.Close()
	}
	if s.parent == "" {
		return result
	}
	// Remove only our held directory's children. A replacement at the old
	// pathname must never authorize deletion of somebody else's directory.
	if s.parentRoot != nil {
		result = errors.Join(result, s.parentRoot.RemoveAll("records"), s.parentRoot.RemoveAll("keys"), s.parentRoot.Close())
	}
	current, err := os.Lstat(s.parent)
	if err == nil && s.parentInfo != nil && os.SameFile(current, s.parentInfo) {
		result = errors.Join(result, os.Remove(s.parent))
	} else if !os.IsNotExist(err) {
		result = errors.Join(result, errConfig("temporary association directory identity changed"))
	}
	if result != nil {
		return errConfig("temporary association storage could not be closed or removed")
	}
	return nil
}

func (r *run) closeCanonicalStage() error {
	if r.canonicalStage != nil {
		return r.canonicalStage.close()
	}
	return nil
}

func (r *run) cleanupCanonicalStage(resultErr ...*error) {
	if err := r.closeCanonicalStage(); err != nil && len(resultErr) == 1 {
		*resultErr[0] = errors.Join(*resultErr[0], err)
	}
}

// Format 8 groups every known canonical material. User-facing documents remain
// individual; association pages alone share document paths.
func groupedCanonicalMaterial(typ string) bool {
	_, ok := materialByType(typ)
	return ok
}

// Keep physical contracts of older archives independent of current write policy.
func groupedCanonicalInFormat(typ string, format int) bool {
	_, association := associationFamilyByType(typ)
	return format >= 6 && association || format >= 7 && typ == "RANGES" || format >= 8 && groupedCanonicalMaterial(typ)
}
