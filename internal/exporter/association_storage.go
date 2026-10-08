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

// Ordinary user documents retain their individual files. Association evidence
// is private until selection finishes, then becomes bounded public JSONL chunks.
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
	// The store's private files must keep a single link. Sync services such as
	// iCloud Drive's Desktop & Documents hard-link the files in a synced folder
	// while uploading them, so keep the store out of the output's folder. The
	// system temporary folder isn't synced, and MkdirTemp makes this one private.
	parent, err := os.MkdirTemp(os.TempDir(), ".pieces-export-stage-")
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
		Binding: sha256.Sum256([]byte("pieces-export transient association storage v1")),
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
		if _, ok := associationFamilyByType(m.Type); ok {
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
		return errConfig("cannot rewrite a published association row")
	}
	if m != nil {
		if _, ok := associationFamilyByType(m.Type); ok {
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
		return errConfig("cannot remove a published association row")
	}
	if m != nil {
		if _, ok := associationFamilyByType(m.Type); ok {
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
