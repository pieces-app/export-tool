package exporter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pieces-app/export-tool/internal/recovery"
)

func captureTestStore(t *testing.T, limit ...int) (*recovery.Store, recovery.Options) {
	t.Helper()
	dir := t.TempDir()
	o := recovery.Options{Directory: filepath.Join(dir, "workspace"), KeyDirectory: filepath.Join(dir, "keys"), Binding: sha256.Sum256([]byte("capture adapter fixture v1"))}
	if len(limit) > 0 {
		o.MaxPayloadBytes = limit[0]
	}
	s, err := recovery.CreateStore(context.Background(), o, []byte(`{"phase":"initial"}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, o
}

func captureTestOptions(t *testing.T, p Policy) Options {
	t.Helper()
	sel, err := SelectScope("summaries", "")
	if err != nil {
		t.Fatal(err)
	}
	return Options{Output: filepath.Join(t.TempDir(), "original"), Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, Mode: "filtered", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, PeopleMode: "profiles", Scanner: scanner(t, p), Version: "capture-fixture"}
}

func requireNoCapturePlaintext(t *testing.T, dir string, markers ...string) {
	t.Helper()
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, marker := range markers {
			if bytes.Contains(b, []byte(marker)) {
				t.Fatalf("private fixture marker found in %s", filepath.Base(path))
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureReplayPreservesPrivacyProfilesAndMovedLinks(t *testing.T) {
	ctx := context.Background()
	store, storage := captureTestStore(t)
	f := summaryScopeFixture()
	const late = "plain-late-capture-credential"
	const excluded = "private-excluded-capture-title"
	f.data["ANNOTATIONS"][0]["text"] = "Actual summary narrative contains " + late + ". [Profile](pieces://persons/person)."
	f.data["TAGS"][0]["api_key"] = late
	private := record("private-capture-annotation", "")
	private["text"] = excluded + " https://bank.example/private"
	f.data["ANNOTATIONS"] = append(f.data["ANNOTATIONS"], private)
	// Force the bounded person-history path and retain its unknown counts.
	delete(f.data["PERSONS"][0], "annotations")
	delete(f.data["PERSONS"][0], "summaries")
	list := filepath.Join(t.TempDir(), "domains.txt")
	if err := os.WriteFile(list, []byte("bank.example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy()
	policy.Lists = []DomainList{{Path: list, Category: "banking"}}
	o := captureTestOptions(t, policy)
	o.Format = "both"
	var capturedMeta map[string]*Meta
	o.captureCheckpoint = func(r *run) error {
		body, err := readRecord(filepath.Join(r.stage, r.meta["ANNOTATIONS\x00body"].DataPath))
		if err != nil || !strings.Contains(fieldString(body, "text"), late) || !r.opts.Scanner.known[late] {
			t.Fatal("fixture did not discover a credential after writing the earlier body", err)
		}
		// Preserve arbitrary credential bytes without JSON UTF-8 substitution.
		r.opts.Scanner.remember("byte-secret-\xff\xfe")
		if err := r.saveCapture(store); err != nil {
			return err
		}
		restored, _, err := loadCapture(ctx, store)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(r.meta, restored.meta) || !reflect.DeepEqual(r.inventory, restored.inventory) || !reflect.DeepEqual(r.opts.Scanner.known, restored.opts.Scanner.known) || !reflect.DeepEqual(r.userPersonIDs, restored.userPersonIDs) {
			t.Fatal("source evidence or credential state changed during capture")
		}
		capturedMeta = restored.meta
		return nil
	}
	srv := f.server(t)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	if err := c.ConfigurePerformance("adaptive", 50, 250*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	original, err := Export(ctx, c, o)
	srv.Close()
	if err != nil {
		t.Fatal(err)
	}
	if capturedMeta == nil || original.People.Selected != 1 || original.People.UnknownEventConnections != 1 {
		t.Fatal("fixture did not preserve profile evidence")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	requireNoCapturePlaintext(t, storage.Directory, late, excluded, "Actual summary narrative", "byte-secret-")
	store, err = recovery.OpenStore(ctx, storage)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// Replay must use the captured list; source services and files are gone.
	if err := os.Remove(list); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "replayed")
	replayed, err := replayCapture(ctx, store, dest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.CaptureReplay == nil || replayed.CaptureReplay.ResumedAt.Before(replayed.CaptureReplay.CapturedAt) || original.Performance.Requests == 0 || replayed.CaptureReplay.SourcePerformance.Requests != original.Performance.Requests || replayed.Performance.Requests != 0 || !replayed.Started.Equal(original.Started) {
		t.Fatal("replay source/current timing and HTTP provenance is invalid")
	}
	if replayed.PolicyHash != original.PolicyHash || replayed.Status != original.Status || !reflect.DeepEqual(replayed.Coverage, original.Coverage) || !reflect.DeepEqual(replayed.People, original.People) || !reflect.DeepEqual(replayed.RelationshipCoverage, original.RelationshipCoverage) {
		t.Fatal("replay changed privacy, coverage or people selection")
	}
	a, b := archiveHashes(t, o.Output), archiveHashes(t, dest)
	if len(a) != len(b) {
		t.Fatalf("archive file counts differ: %d vs %d", len(a), len(b))
	}
	for path, hash := range a {
		if _, ok := b[path]; !ok {
			t.Fatalf("replay lost file %s", path)
		}
		// Execution diagnostics and PDF creation timestamps describe this run.
		if path == "manifest.json" || path == localPerformanceFile || strings.HasSuffix(path, ".pdf") {
			continue
		}
		if b[path] != hash {
			t.Fatalf("replay changed document/evidence %s", path)
		}
	}
	requireNoCapturePlaintext(t, dest, late, excluded, "private-capture-annotation")
	moved := dest + "-moved"
	if err := os.Rename(dest, moved); err != nil {
		t.Fatal(err)
	}
	r, _, err := loadCapture(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	r.stage = moved
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureInterruptedExportPreservesMissingRecords(t *testing.T) {
	ctx := context.Background()
	store, _ := captureTestStore(t)
	f := summaryScopeFixture()
	f.data["WORKSTREAM_SUMMARIES"][0]["annotations"] = refs("unavailable-body")
	o := captureTestOptions(t, DefaultPolicy())
	interrupted := errors.New("synthetic interruption after committed capture")
	o.captureCheckpoint = func(r *run) error {
		if err := r.saveCapture(store); err != nil {
			return err
		}
		return interrupted
	}
	srv := f.server(t)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	_, err := Export(ctx, c, o)
	srv.Close()
	if !errors.Is(err, interrupted) {
		t.Fatal("source capture did not stop at the tested boundary", err)
	}
	before := archiveHashes(t, o.Output+".partial")
	dest := filepath.Join(t.TempDir(), "recovered")
	m, err := replayCapture(ctx, store, dest, nil)
	if err != nil || m.Status != "partial" {
		t.Fatal("missing source body was silently repaired or replay failed", err)
	}
	found := false
	for _, issue := range m.Issues {
		found = found || issue.Code == "record_fetch_failed"
	}
	if !found || !reflect.DeepEqual(before, archiveHashes(t, o.Output+".partial")) {
		t.Fatal("missing-record evidence or interrupted output changed")
	}
	if _, err := os.Stat(o.Output); !os.IsNotExist(err) {
		t.Fatal("interrupted output was finalized")
	}
	for _, path := range []string{dest, o.Output} {
		if _, err := replayCapture(ctx, store, path, nil); err == nil {
			t.Fatal("replay reused an existing output or partial directory")
		}
	}
	for _, phase := range []string{"Restore captured records", "Privacy reconciliation", "Render Markdown"} {
		t.Run("cancel_"+phase, func(t *testing.T) {
			interruptedCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			progress := &phaseHookWriter{phase: phase, hook: cancel}
			output := filepath.Join(t.TempDir(), "cancelled")
			m, err := replayCapture(interruptedCtx, store, output, progress)
			if !progress.fired || !errors.Is(err, context.Canceled) || m.Status == "complete_for_implemented_scope" || !m.Finished.IsZero() {
				t.Fatalf("cancellation did not stop replay at %s: %v", phase, err)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("cancelled replay published an archive")
			}
		})
	}
}

func TestCaptureChunkedEvidenceRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, _ := captureTestStore(t)
	f := summaryScopeFixture()
	o := captureTestOptions(t, DefaultPolicy())
	stop := errors.New("synthetic evidence-only capture")
	o.captureCheckpoint = func(r *run) error {
		r.inventory["ANNOTATIONS"] = nil
		r.derivedEdges, r.associationEdges, r.cachedEdges = map[Edge]bool{}, map[Edge]string{}, map[Edge]CacheEvidence{}
		for i := range 260 {
			id := fmt.Sprintf("fragment-%03d", i)
			r.inventory["ANNOTATIONS"] = append(r.inventory["ANNOTATIONS"], id)
			r.opts.Scanner.remember("synthetic-long-known-" + id)
			r.manifest.Issues = append(r.manifest.Issues, Issue{Code: id})
			e := Edge{Source: "WORKSTREAM_SUMMARIES\x00summary", Target: "ANNOTATIONS\x00" + id, Relation: "annotations"}
			r.derivedEdges[e] = i%2 == 0
			r.associationEdges[e] = "synthetic-relationship-proof"
			r.cachedEdges[e] = CacheEvidence{Cache: 1, Material: "WORKSTREAM_SUMMARIES", RecordRef: "fixture", CachedRecordUpdated: "2026-09-29T12:00:00Z"}
		}
		r.meta["WORKSTREAM_SUMMARIES\x00summary"].ProjectionStates = map[string]string{"annotations": "absent", "persons": "null", "pipelines": "empty"}
		r.meta["WORKSTREAM_SUMMARIES\x00summary"].SupplementableFields = map[string]bool{"annotations": true, "pipelines": false}
		if err := r.saveCapture(store); err != nil {
			return err
		}
		got, _, err := loadCapture(ctx, store)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got.meta, r.meta) || !reflect.DeepEqual(got.inventory, r.inventory) || !reflect.DeepEqual(got.derivedEdges, r.derivedEdges) || !reflect.DeepEqual(got.associationEdges, r.associationEdges) || !reflect.DeepEqual(got.cachedEdges, r.cachedEdges) || !reflect.DeepEqual(got.manifest.Issues, r.manifest.Issues) || !reflect.DeepEqual(got.opts.Scanner.known, r.opts.Scanner.known) {
			t.Fatalf("chunked evidence changed: meta=%t inventory=%t derived=%t associations=%t caches=%t issues=%t secrets=%t", reflect.DeepEqual(got.meta, r.meta), reflect.DeepEqual(got.inventory, r.inventory), reflect.DeepEqual(got.derivedEdges, r.derivedEdges), reflect.DeepEqual(got.associationEdges, r.associationEdges), reflect.DeepEqual(got.cachedEdges, r.cachedEdges), reflect.DeepEqual(got.manifest.Issues, r.manifest.Issues), reflect.DeepEqual(got.opts.Scanner.known, r.opts.Scanner.known))
		}
		cp, err := store.Snapshot(ctx)
		if err != nil || cp.Generation < 3 || cp.Records < 50 {
			t.Fatal("fixture did not cross transaction and chunk boundaries")
		}
		return stop
	}
	srv := f.server(t)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	_, err := Export(ctx, c, o)
	srv.Close()
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
}

func TestKnownCredentialOverlapOrderIsStable(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	s.remember("abcdefgh")
	s.remember("efghijkl")
	for range 100 {
		got, _, err := s.Sanitize(context.Background(), map[string]any{"text": "abcdefghijkl"})
		if err != nil || fieldString(got, "text") != "[REDACTED:SECRET]ijkl" {
			t.Fatalf("credential tie order changed: %v %v", got, err)
		}
	}
}

func TestCaptureRejectsInvalidStateBeforeCreatingOutput(t *testing.T) {
	ctx := context.Background()
	store, _ := captureTestStore(t)
	f := summaryScopeFixture()
	o := captureTestOptions(t, DefaultPolicy())
	stop := errors.New("synthetic capture stop")
	o.captureCheckpoint = func(r *run) error {
		if err := r.saveCapture(store); err != nil {
			return err
		}
		return stop
	}
	srv := f.server(t)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	_, err := Export(ctx, c, o)
	srv.Close()
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	var coreItem, bodyItem recovery.Record
	if err := store.Visit(ctx, func(item recovery.Record) error {
		var p capturePart
		if err := decodeCapture(item.Payload, &p); err != nil {
			return err
		}
		if p.Kind == "core" {
			coreItem = recovery.Record{Ref: item.Ref, Payload: bytes.Clone(item.Payload)}
		} else if p.Name == "ANNOTATIONS\x00body" {
			bodyItem = recovery.Record{Ref: item.Ref, Payload: bytes.Clone(item.Payload)}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cp, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state := bytes.Clone(cp.State)
	for _, test := range []struct {
		name   string
		core   bool
		mutate func(*capturePart, *captureFrame)
	}{
		{name: "incomplete_capture", mutate: func(_ *capturePart, f *captureFrame) { f.Phase = "writing" }},
		{name: "wrong_version", mutate: func(_ *capturePart, f *captureFrame) { f.Version++ }},
		{name: "missing_part", mutate: func(_ *capturePart, f *captureFrame) { f.Parts["record"]++ }},
		{name: "wrong_ref", mutate: func(p *capturePart, _ *captureFrame) { p.Name += "changed" }},
		{name: "escaping_path", mutate: func(p *capturePart, _ *captureFrame) { p.Record.Meta.DataPath = "../outside.json" }},
		{name: "wrong_body", mutate: func(p *capturePart, _ *captureFrame) { p.Record.Data = json.RawMessage(`{"id":"wrong"}`) }},
		{name: "unexpected_field", mutate: func(p *capturePart, _ *captureFrame) { p.Strings = []string{"unexpected"} }},
		{name: "changed_policy", core: true, mutate: func(p *capturePart, _ *captureFrame) { p.Core.PolicyHash = "changed" }},
		{name: "bad_scope", core: true, mutate: func(p *capturePart, _ *captureFrame) { p.Core.Options.Scope = "invalid" }},
		{name: "bad_worker_limit", core: true, mutate: func(p *capturePart, _ *captureFrame) { p.Core.Options.FileWorkers = 1000 }},
		{name: "bad_pdf_limit", core: true, mutate: func(p *capturePart, _ *captureFrame) { p.Core.Options.PDFLimits.InputMiB = 0 }},
		{name: "bad_person_selection", core: true, mutate: func(p *capturePart, _ *captureFrame) { p.Core.Options.PeopleMode = "unknown" }},
		{name: "bad_format", core: true, mutate: func(p *capturePart, _ *captureFrame) { p.Core.Options.Format = "unknown" }},
		{name: "bad_material_path", core: true, mutate: func(p *capturePart, _ *captureFrame) { p.Core.Options.Materials[0].Folder = "../outside" }},
		{name: "finished_capture", core: true, mutate: func(p *capturePart, _ *captureFrame) { p.Core.Manifest.Finished = time.Now() }},
		{name: "changed_font", core: true, mutate: func(p *capturePart, _ *captureFrame) { p.Core.FontDigest = "changed" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := bodyItem
			if test.core {
				item = coreItem
			}
			var part capturePart
			var frame captureFrame
			if err := decodeCapture(item.Payload, &part); err != nil {
				t.Fatal(err)
			}
			if err := decodeCapture(state, &frame); err != nil {
				t.Fatal(err)
			}
			test.mutate(&part, &frame)
			payload, err := json.Marshal(part)
			if err != nil {
				t.Fatal(err)
			}
			changedState, err := json.Marshal(frame)
			if err != nil {
				t.Fatal(err)
			}
			cp, err := store.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Commit(ctx, cp.Generation, []recovery.Record{{Ref: item.Ref, Payload: payload}}, changedState); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(t.TempDir(), "rejected")
			if _, err := replayCapture(ctx, store, dest, nil); err == nil {
				t.Fatal("invalid capture was accepted")
			}
			for _, path := range []string{dest, dest + ".partial"} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("invalid capture created output")
				}
			}
			if err := store.Commit(ctx, cp.Generation+1, []recovery.Record{item}, state); err != nil {
				t.Fatal(err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	dest := filepath.Join(t.TempDir(), "cancelled")
	if _, err := replayCapture(cancelled, store, dest, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("replay ignored cancellation", err)
	}
	if _, err := os.Lstat(dest + ".partial"); !os.IsNotExist(err) {
		t.Fatal("cancelled validation created output")
	}
}

func TestCaptureReplayAssociationAndCacheEvidence(t *testing.T) {
	t.Run("association_metadata", func(t *testing.T) {
		store, _ := captureTestStore(t)
		srv, _, policy, secret := associationFixture(t)
		o := associationFixtureOptions(t, filepath.Join(t.TempDir(), "original"), policy)
		o.Associations = "linked"
		o.captureCheckpoint = func(r *run) error { return r.saveCapture(store) }
		c, _ := NewClient(srv.URL, time.Second, 8<<20)
		m, err := Export(context.Background(), c, o)
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		assertAssociationArchive(t, o.Output, m, policy, secret, true)
		dest := filepath.Join(t.TempDir(), "replayed")
		m, err = replayCapture(context.Background(), store, dest, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertAssociationArchive(t, dest, m, policy, secret, true)
	})
	t.Run("historical_cache", func(t *testing.T) {
		store, _ := captureTestStore(t)
		f, cache := wrappedExportFixture(t)
		policy := DefaultPolicy()
		policy.Deny = []DomainRule{{"bank.example", true}}
		o := captureTestOptions(t, policy)
		o.Scope, o.ReferenceOnly = "custom", nil
		o.Materials, _ = SelectMaterials("WORKSTREAM_SUMMARIES,ANNOTATIONS,PERSONS,SIGNALS,WORKSTREAM_EVENTS")
		o.SDKCaches = []string{cache}
		o.captureCheckpoint = func(r *run) error { return r.saveCapture(store) }
		srv := f.server(t)
		c, _ := NewClient(srv.URL, time.Second, 8<<20)
		m, err := Export(context.Background(), c, o)
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		assertWrappedRecovered(t, o.Output, m, policy)
		// A completed capture must never reload its historical source.
		if err := os.Rename(cache, cache+".unavailable"); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "replayed")
		m, err = replayCapture(context.Background(), store, dest, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertWrappedRecovered(t, dest, m, policy)
	})
}

func TestCaptureReplayPreservationAndWithheldDecisions(t *testing.T) {
	for _, mode := range []string{"filtered", "preserve"} {
		t.Run(mode, func(t *testing.T) {
			store, storage := captureTestStore(t)
			secret := fakeSecret()
			v := record(secret, "")
			v["name"] = "Synthetic private identity title"
			f := &fakeOS{data: map[string][]map[string]any{"TAGS": {v}}}
			o := captureTestOptions(t, DefaultPolicy())
			o.Mode, o.Scope, o.ReferenceOnly, o.PeopleMode = mode, "custom", nil, "all"
			o.Materials, _ = SelectMaterials("TAGS")
			stop := errors.New("synthetic interruption")
			o.captureCheckpoint = func(r *run) error {
				if err := r.saveCapture(store); err != nil {
					return err
				}
				return stop
			}
			srv := f.server(t)
			c, _ := NewClient(srv.URL, time.Second, 8<<20)
			_, err := Export(context.Background(), c, o)
			srv.Close()
			if !errors.Is(err, stop) {
				t.Fatal(err)
			}
			requireNoCapturePlaintext(t, storage.Directory, secret, "Synthetic private identity title")
			dest := filepath.Join(t.TempDir(), "replayed")
			m, err := replayCapture(context.Background(), store, dest, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(m.Coverage) != 1 {
				t.Fatal("unexpected material scope")
			}
			if mode == "filtered" {
				if m.Status != "partial" || m.Coverage[0].Withheld != 1 || m.Coverage[0].Included != 0 {
					t.Fatal("withheld identity was restored")
				}
				requireNoCapturePlaintext(t, dest, secret, "Synthetic private identity title")
			} else {
				if m.Coverage[0].Included != 1 {
					t.Fatal("preserve mode discarded original")
				}
				body, err := readRecord(filepath.Join(dest, "raw/tags", opaque("TAGS", secret)+".json"))
				if err != nil || fieldString(body, "id") != secret {
					t.Fatal("preserved identity changed", err)
				}
			}
		})
	}
}

func TestCaptureIncompleteCommitCannotBeReplayed(t *testing.T) {
	ctx := context.Background()
	store, _ := captureTestStore(t, 128<<10)
	f := summaryScopeFixture()
	f.data["WORKSTREAM_SUMMARIES"][0]["large_fixture_field"] = strings.Repeat("synthetic prose ", 12000)
	o := captureTestOptions(t, DefaultPolicy())
	o.captureCheckpoint = func(r *run) error { return r.saveCapture(store) }
	srv := f.server(t)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	_, err := Export(ctx, c, o)
	srv.Close()
	if err == nil {
		t.Fatal("oversized capture item was accepted")
	}
	cp, err := store.Snapshot(ctx)
	if err != nil || cp.Generation == 0 || cp.Records == 0 {
		t.Fatal("fixture did not commit an earlier batch", err)
	}
	var frame captureFrame
	if err := decodeCapture(cp.State, &frame); err != nil || frame.Phase != "writing" {
		t.Fatal("failed commit marked capture complete", err)
	}
	dest := filepath.Join(t.TempDir(), "replay")
	if _, err := replayCapture(ctx, store, dest, nil); err == nil {
		t.Fatal("incomplete capture was replayed")
	}
	for _, path := range []string{o.Output, dest, dest + ".partial"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("incomplete capture published output")
		}
	}
}

func TestCaptureAbruptExitReplay(t *testing.T) {
	const childVariable = "PIECES_EXPORT_CAPTURE_CRASH_FIXTURE"
	storageFor := func(root string) recovery.Options {
		return recovery.Options{Directory: filepath.Join(root, "work"), KeyDirectory: filepath.Join(root, "keys"), Binding: sha256.Sum256([]byte("capture crash fixture v1"))}
	}
	if root := os.Getenv(childVariable); root != "" {
		store, err := recovery.CreateStore(context.Background(), storageFor(root), []byte(`{"phase":"initial"}`))
		if err != nil {
			t.Fatal(err)
		}
		f := summaryScopeFixture()
		srv := f.server(t)
		c, _ := NewClient(srv.URL, time.Second, 8<<20)
		if err := c.ConfigurePerformance("adaptive", 50, 250*time.Millisecond, nil); err != nil {
			t.Fatal(err)
		}
		sel, _ := SelectScope("summaries", "")
		s, err := NewScanner(DefaultPolicy(), ".")
		if err != nil {
			t.Fatal(err)
		}
		o := Options{Output: filepath.Join(root, "original"), Scope: sel.Name, Materials: sel.Materials, ReferenceOnly: sel.ReferenceOnly, Mode: "filtered", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, PeopleMode: "profiles", Scanner: s}
		o.captureCheckpoint = func(r *run) error {
			if err := r.saveCapture(store); err != nil {
				return err
			}
			// No Close, deferred cleanup, final diagnostics or final rename.
			os.Exit(73)
			return nil
		}
		if _, err := Export(context.Background(), c, o); err != nil {
			t.Fatal(err)
		}
		t.Fatal("child did not exit at the committed capture boundary")
	}
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCaptureAbruptExitReplay$")
	cmd.Env = append(os.Environ(), childVariable+"="+root)
	output, err := cmd.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 73 {
		t.Fatalf("capture child did not reach crash boundary: %v %s", err, output)
	}
	store, err := recovery.OpenStore(context.Background(), storageFor(root))
	if err != nil {
		t.Fatal("could not reopen committed capture after process exit", err)
	}
	defer store.Close()
	partial := filepath.Join(root, "original.partial")
	before := archiveHashes(t, partial)
	dest := filepath.Join(root, "replayed")
	m, err := replayCapture(context.Background(), store, dest, nil)
	if err != nil || m.Status != "complete_for_implemented_scope" || m.People.Selected != 1 || m.Performance.Requests != 0 || m.CaptureReplay == nil || m.CaptureReplay.SourcePerformance.Requests == 0 {
		t.Fatalf("abrupt-exit replay: status=%s people=%d requests=%d capture=%+v issues=%+v err=%v", m.Status, m.People.Selected, m.Performance.Requests, m.CaptureReplay, m.Issues, err)
	}
	if !reflect.DeepEqual(before, archiveHashes(t, partial)) {
		t.Fatal("recovery modified interrupted output")
	}
	if _, err := os.Stat(filepath.Join(root, "original")); !os.IsNotExist(err) {
		t.Fatal("recovery finalized the old partial folder")
	}
}
