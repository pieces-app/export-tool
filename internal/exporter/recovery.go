package exporter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pieces-app/export-tool/internal/recovery"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// This explicit compatibility contract must change with incompatible capture,
// privacy or rendering semantics. A development version label is insufficient.
// Compatible navigation fixes may correct how complete captured inputs render;
// source exclusion/withholding decisions and configured privacy rules stay fixed.
var captureBinding = sha256.Sum256([]byte("pieces-export/completed-source-replay/2026-09-30/v2-current-junctions"))

// RecoveryOptions is opt-in. Parents must exist; the workspace must be new.
// Keys stay outside archives and workspaces. Neither is deleted automatically.
type RecoveryOptions struct {
	Directory    string
	KeyDirectory string
}

type RecoveryInfo struct {
	Phase, Scope, Mode, Format, PeopleMode                 string
	CanResume                                              bool
	NeedsSource                                            bool
	CapturedAt                                             time.Time
	SourceStarted                                          time.Time
	Generation, StoredItems                                uint64
	Records, Included, Excluded, Withheld, Missing, Issues int
}

// RecoverySession holds exclusive workspace and key ownership across review
// and replay. It is single-use and must be closed; do not use it concurrently.
type RecoverySession struct {
	fetch      *fetchCheckpoint
	child      *RecoverySession
	store      *recovery.Store
	options    RecoveryOptions
	r          *run
	generation uint64
	info       RecoveryInfo
	used       bool
}

func recoveryStorage(o RecoveryOptions) recovery.Options {
	return recovery.Options{Directory: o.Directory, KeyDirectory: o.KeyDirectory, Binding: captureBinding}
}

type recoveryPathAncestor struct {
	info os.FileInfo
	tail string
}

// Compare existing directory identities as well as unresolved suffixes. This
// handles /var aliases, symlinked parents, case aliases and bind-mount parents.
// Unknown suffix case is compared conservatively even on case-sensitive disks.
func recoveryPathAncestry(path string) ([]recoveryPathAncestor, error) {
	if path == "" {
		return nil, errConfig("recovery paths must not be empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, errConfig("recovery path is invalid")
	}
	parent, tail := abs, ""
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			var result []recoveryPathAncestor
			for {
				info, err := os.Stat(resolved)
				if err != nil {
					return nil, errConfig("recovery path ancestry is unavailable")
				}
				result = append(result, recoveryPathAncestor{info, tail})
				if filepath.Dir(resolved) == resolved {
					return result, nil
				}
				tail = filepath.Join(filepath.Base(resolved), tail)
				resolved = filepath.Dir(resolved)
			}
		}
		if !os.IsNotExist(err) || filepath.Dir(parent) == parent {
			return nil, errConfig("recovery path ancestry is unavailable")
		}
		tail = filepath.Join(filepath.Base(parent), tail)
		parent = filepath.Dir(parent)
	}
}

func recoverySuffixContains(a, b string) bool {
	fold := cases.Fold()
	a, b = fold.String(norm.NFC.String(filepath.ToSlash(a))), fold.String(norm.NFC.String(filepath.ToSlash(b)))
	if a == "" || a == "." || strings.EqualFold(a, b) {
		return true
	}
	return len(b) > len(a) && b[len(a)] == '/' && strings.EqualFold(a, b[:len(a)])
}

func recoveryPathsOverlap(a, b string) (bool, error) {
	aa, err := recoveryPathAncestry(a)
	if err != nil {
		return false, err
	}
	bb, err := recoveryPathAncestry(b)
	if err != nil {
		return false, err
	}
	for _, left := range aa {
		for _, right := range bb {
			if os.SameFile(left.info, right.info) {
				return recoverySuffixContains(left.tail, right.tail) || recoverySuffixContains(right.tail, left.tail), nil
			}
		}
	}
	return false, nil
}

func separateRecoveryPaths(paths ...string) error {
	for i, a := range paths {
		for _, b := range paths[i+1:] {
			overlap, err := recoveryPathsOverlap(a, b)
			if err != nil {
				return err
			}
			if overlap {
				return errConfig("recovery workspace, key directory, output and partial output must be separate, non-nested paths")
			}
		}
	}
	return nil
}

// ValidateRecoveryOptions performs read-only layout checks before CLI discovery.
// Ownership/permissions and OS-backed locks are additionally checked on open.
func ValidateRecoveryOptions(o RecoveryOptions, output string, fresh bool) error {
	for _, path := range []string{o.Directory, o.KeyDirectory} {
		if path == "" {
			return errConfig("recovery requires both --work and --recovery-keys")
		}
		parent, err := os.Stat(filepath.Dir(path))
		if err != nil || !parent.IsDir() {
			return errConfig("recovery workspace and key-directory parents must already exist")
		}
		if info, err := os.Lstat(path); err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errConfig("recovery roots must be directories, not symlinks")
			}
		} else if !os.IsNotExist(err) {
			return errConfig("recovery root cannot be inspected")
		}
	}
	paths := []string{o.Directory, o.KeyDirectory}
	if output != "" {
		paths = append(paths, output, output+".partial")
	}
	if err := separateRecoveryPaths(paths...); err != nil {
		return err
	}
	if fresh {
		if _, err := os.Lstat(o.Directory); !os.IsNotExist(err) {
			return errConfig("export recovery workspace must be new; inspect an existing workspace with resume --inspect")
		}
	}
	return nil
}

func OpenRecovery(ctx context.Context, o RecoveryOptions) (_ *RecoverySession, resultErr error) {
	if err := ValidateRecoveryOptions(o, "", false); err != nil {
		return nil, err
	}
	store, err := recovery.OpenStore(ctx, recoveryStorage(o))
	if err != nil {
		return nil, err
	}
	s := &RecoverySession{store: store, options: o}
	defer func() {
		if resultErr != nil {
			_ = s.Close()
		}
	}()
	cp, err := store.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	var frame captureFrame
	if err := decodeCapture(cp.State, &frame); err != nil || frame.Version != captureVersion {
		return nil, errConfig("incompatible exporter recovery checkpoint")
	}
	s.info = RecoveryInfo{Phase: frame.Phase, Generation: cp.Generation, StoredItems: cp.Records}
	switch frame.Phase {
	case "source-fetching":
		if cp.Records != 0 {
			return nil, errConfig("unexpected records in recovery controller")
		}
		s.fetch, err = openFetchRecovery(ctx, store, o, frame, cp.Generation)
		if err != nil {
			return nil, err
		}
		if s.fetch.state.Complete != "" {
			childOptions, e := s.fetch.completeOptions()
			if e != nil {
				return nil, e
			}
			s.child, e = OpenRecovery(ctx, childOptions)
			if e != nil {
				return nil, e
			}
			if !s.child.info.CanResume || s.child.info.NeedsSource {
				return nil, errConfig("incomplete linked source capture")
			}
			s.r = s.child.r
			s.info = s.child.info
		} else {
			core := s.fetch.state.Core
			s.info.CanResume = true
			s.info.NeedsSource = true
			s.info.Scope = core.Options.Scope
			s.info.Mode = core.Options.Mode
			s.info.Format = core.Options.Format
			s.info.PeopleMode = core.Options.PeopleMode
			s.info.SourceStarted = core.Manifest.Started
			checkpoint, e := s.fetch.records.Snapshot(ctx)
			if e != nil {
				return nil, e
			}
			s.info.StoredItems = checkpoint.Records
		}
	case "fetching":
		if cp.Generation != 0 || cp.Records != 0 || len(frame.Parts) != 0 {
			return nil, errConfig("invalid initial recovery checkpoint")
		}
	case "writing":
		// Authenticated partial capture is inspectable but never replayable.
	case "capture-complete":
		s.r, s.generation, err = loadCapture(ctx, store)
		if err != nil {
			return nil, err
		}
		r := s.r
		if err := ValidateRecoveryOptions(o, r.opts.Output, false); err != nil {
			return nil, err
		}
		s.info.CanResume = true
		s.info.Scope, s.info.Mode, s.info.Format, s.info.PeopleMode = r.opts.Scope, r.opts.Mode, r.opts.Format, r.opts.PeopleMode
		s.info.CapturedAt, s.info.SourceStarted = r.manifest.CaptureReplay.CapturedAt, r.manifest.Started
		s.info.Records, s.info.Issues = len(r.meta), len(r.manifest.Issues)
		for _, m := range r.meta {
			switch m.State {
			case "included":
				s.info.Included++
			case "excluded":
				s.info.Excluded++
			case "withheld":
				s.info.Withheld++
			case "missing":
				s.info.Missing++
			}
		}
	default:
		return nil, errConfig("unsupported recovery phase")
	}
	return s, nil
}

func (s *RecoverySession) Info() RecoveryInfo { return s.info }

func (s *RecoverySession) Close() error {
	if s.store == nil {
		return nil
	}
	var first error
	if s.child != nil {
		first = s.child.Close()
		s.child = nil
	}
	if s.fetch != nil {
		if e := s.fetch.close(); first == nil {
			first = e
		}
	}
	err := s.store.Close()
	s.store, s.r = nil, nil
	if first != nil {
		return first
	}
	return err
}

func (s *RecoverySession) Replay(ctx context.Context, output string, progress io.Writer, version string) (Manifest, error) {
	if err := s.ValidateOutput(output); err != nil {
		return Manifest{}, err
	}
	if s.info.NeedsSource {
		return Manifest{}, errConfig("source fetching is incomplete; resume requires a verified OS connection")
	}
	s.used = true
	if s.child != nil {
		return s.child.Replay(ctx, output, progress, version)
	}
	s.r.ctx = ctx
	s.r.opts.Version, s.r.manifest.ToolVersion = version, version
	return replayLoadedCapture(s.r, s.store, s.generation, output, progress)
}

func (s *RecoverySession) ValidateOutput(output string) error {
	if s.store == nil || s.used {
		return recovery.ErrClosed
	}
	if !s.info.CanResume {
		return errConfig("source capture is incomplete; interrupted source fetching cannot be resumed yet")
	}
	if output == "" {
		return errConfig("resume requires a new output directory")
	}
	if err := ValidateRecoveryOptions(s.options, output, false); err != nil {
		return err
	}
	var previous []string
	if s.fetch != nil {
		previous = s.fetch.state.Outputs
	} else {
		previous = []string{s.r.opts.Output}
	}
	for _, prior := range previous {
		if err := separateRecoveryPaths(prior, prior+".partial", output, output+".partial"); err != nil {
			return errConfig("resume output must be separate from every previous output and partial directory")
		}
	}
	for _, path := range []string{output, output + ".partial"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return errConfig("resume output or partial directory already exists or cannot be inspected")
		}
	}
	return nil
}

func createExportRecovery(ctx context.Context, o RecoveryOptions, output string) (*recovery.Store, error) {
	if err := ValidateRecoveryOptions(o, output, true); err != nil {
		return nil, err
	}
	state, err := json.Marshal(captureFrame{Version: captureVersion, Phase: "fetching", Parts: map[string]int{}})
	if err != nil {
		return nil, err
	}
	return recovery.CreateStore(ctx, recoveryStorage(o), state)
}
