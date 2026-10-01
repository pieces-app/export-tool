package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Legacy/public per-file fixtures inspect their expected representation here.
// Production current-record consumers must use canonicalRecordStore instead.
func readRecord(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.UseNumber()
	var v map[string]any
	err = d.Decode(&v)
	return v, err
}

// This test backend has no per-record files until the validation boundary.
// Rendering/privacy must use record identity, not reach around the interface
// into the old filenames. It models read-your-writes and deferred persistence.
type deferredRecordFixture struct {
	r             *run
	values        map[string][]byte
	reads, writes int
	removes       int
	flushes       int
	failFlush     error
}

func (s *deferredRecordFixture) Open(ctx context.Context, m *Meta) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, ok := s.values[m.Key]
	if !ok {
		return nil, os.ErrNotExist
	}
	s.reads++
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *deferredRecordFixture) Write(ctx context.Context, m *Meta, v map[string]any, replace bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, exists := s.values[m.Key]
	if exists && !replace {
		return os.ErrExist
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	s.values[m.Key] = append(b, '\n')
	s.writes++
	return nil
}

func (s *deferredRecordFixture) Remove(ctx context.Context, m *Meta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(s.values, m.Key)
	s.removes++
	return nil
}

func (s *deferredRecordFixture) Flush(ctx context.Context) error {
	s.flushes++
	if s.failFlush != nil {
		return s.failFlush
	}
	files := fileCanonicalRecords{run: s.r}
	for key, b := range s.values {
		m := s.r.meta[key]
		if m == nil || m.State != "included" {
			return errors.New("non-included canonical payload survived removal")
		}
		var v map[string]any
		if err := decodeArchiveJSON(b, &v); err != nil {
			return err
		}
		if err := files.Write(ctx, m, v, false); err != nil {
			return err
		}
	}
	return nil
}

func deferFixtureRecords(t *testing.T, r *run) *deferredRecordFixture {
	t.Helper()
	s := &deferredRecordFixture{r: r, values: map[string][]byte{}}
	for _, m := range r.meta {
		if m.State != "included" {
			continue
		}
		b, err := r.readCanonicalBytes(m, 128<<20)
		if err != nil {
			t.Fatal(err)
		}
		s.values[m.Key] = b
		if err := r.removeCanonical(m); err != nil {
			t.Fatal(err)
		}
	}
	r.records = s
	return s
}

func TestCanonicalAccessDeferredPrivacyGraphAndRebuild(t *testing.T) {
	f := junctionFixture()
	f.data["PERSONS"] = append(f.data["PERSONS"], record("unprofiled", "2026-09-30T00:00:00Z"))
	srv := junctionServer(t, f, nil)
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	sel, _ := SelectScope("summaries", "")
	out := filepath.Join(t.TempDir(), "archive")
	var deferred *deferredRecordFixture
	late := "Current linked narrative."
	m, err := Export(context.Background(), client, Options{Output: out, Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, PeopleMode: "profiles", Mode: "filtered", Format: "both", Timezone: "UTC", Scanner: scanner(t, DefaultPolicy()), BatchSize: 50, WindowIDs: 5000, captureCheckpoint: func(r *run) error {
		deferred = deferFixtureRecords(t, r)
		r.opts.Scanner.remember(late)
		return nil
	}})
	if err != nil || deferred == nil || deferred.flushes != 1 || deferred.reads == 0 || deferred.writes == 0 || deferred.removes == 0 {
		t.Fatal("deferred canonical access did not cover privacy/render/persistence", err)
	}
	if m.People.Selected != 1 || m.People.Omitted != 1 {
		t.Fatal("person selection changed with deferred canonical records")
	}
	for _, root := range []string{out, out + "-rebuilt"} {
		if root != out {
			if _, err := Rebuild(context.Background(), RebuildOptions{Source: out, Options: Options{Output: root, Scanner: scanner(t, DefaultPolicy()), Format: "both"}}); err != nil {
				t.Fatal(err)
			}
		}
		report, err := inspectFinalArchive(context.Background(), root)
		if err != nil || report.Summaries != 1 || report.SummariesWithBody != 1 || report.PersonsWithProfile != 1 {
			t.Fatal("canonical adapter lost verified bodies/graph/rebuild", err, report)
		}
		if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(path)
			if bytes.Contains(b, []byte(late)) {
				t.Fatal("late credential escaped deferred canonical storage")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCanonicalAccessDeferredSignalsAndFlushFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "signals", true: "flush_failure"}[fail], func(t *testing.T) {
			f, p := signalDigestFixture()
			srv := f.server(t)
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			materials, _ := SelectMaterials("SIGNALS,ANNOTATIONS,PERSONS,PIPELINES,WORKSTREAM_EVENTS,WEBSITES,WORKSTREAM_SUMMARIES,RANGES")
			out := filepath.Join(t.TempDir(), "archive")
			failure := errors.New("synthetic deferred storage failure")
			m, err := Export(context.Background(), client, Options{Output: out, Materials: materials, Mode: "filtered", Format: "markdown", Timezone: "UTC", Scanner: scanner(t, p), BatchSize: 50, WindowIDs: 5000, SignalDigest: SignalDigestOptions{Mode: "single"}, captureCheckpoint: func(r *run) error {
				s := deferFixtureRecords(t, r)
				if fail {
					s.failFlush = failure
				}
				return nil
			}})
			if fail {
				if !errors.Is(err, failure) || m.Status != "failed" || !m.Finished.IsZero() {
					t.Fatal("flush failure was finalized or hidden", err)
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatal("failed flush produced a completed archive")
				}
				return
			}
			if err != nil || m.SignalDigest == nil || m.SignalDigest.Entries != 5 || m.SignalDigest.DescriptionAttachments != 3 {
				t.Fatal("signals bypassed canonical storage", err)
			}
		})
	}
}

func TestCanonicalAccessBoundsPathsAndCancellation(t *testing.T) {
	r := &run{ctx: context.Background(), stage: t.TempDir()}
	m := &Meta{DataPath: "record.json"}
	if err := r.writeCanonical(m, map[string]any{"value": strings.Repeat("x", 100)}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.readCanonicalBytes(m, 10); err == nil {
		t.Fatal("bounded capture read accepted excess bytes")
	}
	for _, path := range []string{"../outside.json", "/outside.json", `data\outside.json`} {
		if _, err := r.readCanonical(&Meta{DataPath: path}); err == nil {
			t.Fatal("canonical path escaped its output root")
		}
	}
	if err := os.Symlink(filepath.Join(r.stage, m.DataPath), filepath.Join(r.stage, "alias.json")); err == nil {
		if _, err := r.readCanonicalBytes(&Meta{DataPath: "alias.json"}, 1024); err == nil {
			t.Fatal("capture read followed a symlink")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.ctx = ctx
	if err := r.removeCanonical(m); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled storage operation proceeded", err)
	}
}
