package exporter

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/pieces-app/export-tool/internal/recovery"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Stop on the second hydration request: the first batch must already be durable.
func interruptedFetchFixture(t *testing.T) (*fakeOS, *httptest.Server, RecoveryOptions, Options) {
	t.Helper()
	f := summaryScopeFixture()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	batches := 0
	f.beforeBatch = func() {
		batches++
		if batches == 2 {
			cancel()
		}
	}
	srv := f.server(t)
	t.Cleanup(srv.Close)
	o := captureTestOptions(t, DefaultPolicy())
	o.BatchSize = 5
	cfg := recoveryOptionsFixture(t)
	o.Recovery = &cfg
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	if _, err := Export(ctx, c, o); !errors.Is(err, context.Canceled) {
		t.Fatal("fixture did not interrupt fetching", err)
	}
	f.beforeBatch = nil
	return f, srv, cfg, o
}

func TestFetchRecoveryContinuesAndThenReplaysOffline(t *testing.T) {
	_, srv, cfg, o := interruptedFetchFixture(t)
	before := archiveHashes(t, o.Output+".partial")
	s, err := OpenRecovery(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if info := s.Info(); !info.CanResume || !info.NeedsSource || info.StoredItems == 0 {
		t.Fatal("missing partial source checkpoint", info)
	}
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	out := filepath.Join(t.TempDir(), "resumed")
	m, err := s.Continue(context.Background(), c, out, nil, "resumed-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if m.SourceRecovery == nil || !m.SourceRecovery.Resumed || m.SourceRecovery.ReusedRecords == 0 || m.Status != "complete_for_implemented_scope" {
		t.Fatal("source resume did not reuse complete snapshots", m.SourceRecovery, m.Status)
	}
	if _, err = inspectFinalArchive(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, archiveHashes(t, o.Output+".partial")) {
		t.Fatal("original partial changed")
	}
	baseline := o
	baseline.Recovery = nil
	baseline.Output = filepath.Join(t.TempDir(), "uninterrupted")
	baseline.Scanner = scanner(t, DefaultPolicy())
	original, e := Export(context.Background(), c, baseline)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(original.Coverage, m.Coverage) || !reflect.DeepEqual(original.People, m.People) || original.PolicyHash != m.PolicyHash {
		t.Fatal("resume changed scope, people, or privacy")
	}
	a, b := archiveHashes(t, baseline.Output), archiveHashes(t, out)
	for path, hash := range a {
		if strings.HasPrefix(path, "data/") || strings.HasPrefix(path, "markdown/") || strings.HasPrefix(path, "workstream_summaries/") || path == "relationships.jsonl" || path == "link-map.json" {
			if b[path] != hash {
				t.Fatal("resume changed retained documents or graph evidence", path)
			}
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	s, err = OpenRecovery(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if info := s.Info(); !info.CanResume || info.NeedsSource {
		t.Fatal("complete source did not become offline replay", info)
	}
	replay := filepath.Join(t.TempDir(), "replayed")
	replayed, err := s.Replay(context.Background(), replay, nil, "replay-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Performance.Requests != 0 || replayed.Status != m.Status {
		t.Fatal("offline replay status or source requests changed")
	}
	if _, err = inspectFinalArchive(context.Background(), replay); err != nil {
		t.Fatal(err)
	}
}

func TestFetchRecoveryRechecksChangedDeletedAndUnsupported(t *testing.T) {
	for _, kind := range []string{"changed", "deleted", "unsupported", "identity", "user"} {
		t.Run(kind, func(t *testing.T) {
			f, srv, cfg, _ := interruptedFetchFixture(t)
			s, err := OpenRecovery(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var cached []fetchedRecord
			err = s.fetch.records.Visit(context.Background(), func(item recovery.Record) error {
				row, e := validateFetched(item)
				cached = append(cached, row)
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(cached) == 0 {
				t.Fatal("empty fixture")
			}
			switch kind {
			case "changed":
				for _, row := range cached {
					for _, v := range f.data[row.Type] {
						if fieldString(v, "id") == row.ID {
							v["updated"] = map[string]any{"value": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}
							v["description"] = "new source description"
						}
					}
				}
			case "deleted":
				for _, row := range cached {
					values := f.data[row.Type]
					for i, v := range values {
						if fieldString(v, "id") == row.ID {
							f.data[row.Type] = append(values[:i], values[i+1:]...)
							break
						}
					}
				}
			case "unsupported":
				f.updatedUnavailable = true
			case "identity":
				f.healthIdentity = "another-installation"
			case "user":
				f.currentUserID = "another-user"
			}
			c, _ := NewClient(srv.URL, time.Second, 8<<20)
			dest := filepath.Join(t.TempDir(), "recovered")
			m, err := s.Continue(context.Background(), c, dest, nil, "fixture")
			if kind == "identity" || kind == "user" {
				if err == nil || !strings.Contains(err.Error(), "different OS") {
					t.Fatal("wrong source accepted", err)
				}
				if _, e := os.Stat(dest + ".partial"); !os.IsNotExist(e) {
					t.Fatal("wrong source created output")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if m.SourceRecovery.ReusedRecords != 0 {
				t.Fatal("stale or unverifiable snapshot reused", m.SourceRecovery)
			}
			if kind == "changed" {
				for _, row := range cached {
					v, _, e := readArchiveFixtureRecord(dest, row.Type, row.ID)
					if e != nil || fieldString(v, "description") != "new source description" {
						t.Fatal("changed record not rehydrated", e)
					}
				}
			}
		})
	}
}

func TestFetchRecoveryRetainsCredentialsAndPolicy(t *testing.T) {
	f := summaryScopeFixture()
	secret := "private-value-learned-from-original-record"
	// All records in the first batch teach the matcher; the next attempt removes
	// the credential field, but ordinary text containing its value remains unsafe.
	for _, records := range f.data {
		for _, v := range records {
			v["api_key"] = secret
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	f.beforeBatch = func() {
		count++
		if count == 2 {
			cancel()
		}
	}
	srv := f.server(t)
	defer srv.Close()
	o := captureTestOptions(t, DefaultPolicy())
	o.BatchSize = 5
	cfg := recoveryOptionsFixture(t)
	o.Recovery = &cfg
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	if _, err := Export(ctx, c, o); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	requireNoCapturePlaintext(t, cfg.Directory, secret)
	f.beforeBatch = nil
	for _, records := range f.data {
		for _, v := range records {
			delete(v, "api_key")
			v["description"] = "some " + secret + " text"
			v["updated"] = map[string]any{"value": time.Now().UTC().Format(time.RFC3339Nano)}
		}
	}
	s, err := OpenRecovery(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dest := filepath.Join(t.TempDir(), "resumed")
	m, err := s.Continue(context.Background(), c, dest, nil, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if m.PolicyHash != o.Scanner.Hash {
		t.Fatal("privacy policy changed")
	}
	requireNoCapturePlaintext(t, dest, secret)
	requireNoCapturePlaintext(t, cfg.Directory, secret)
}

func TestFetchRecoveryRejectsModifiedEncryptedState(t *testing.T) {
	_, _, cfg, _ := interruptedFetchFixture(t)
	s, err := OpenRecovery(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	state := s.fetch.state
	state.Complete = "../../outside"
	b, _ := json.Marshal(captureFrame{Version: captureVersion, Phase: "source-fetching", Parts: map[string]int{}, Fetch: &state})
	if err = s.store.Commit(context.Background(), s.fetch.generation, nil, b); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if other, e := OpenRecovery(context.Background(), cfg); e == nil {
		other.Close()
		t.Fatal("unsafe linked capture accepted")
	}
}

func TestFetchRecoverySurvivesSecondInterruptionAndCaptureFailure(t *testing.T) {
	for _, phase := range []string{"Fetch ANNOTATIONS", "Checkpoint source capture"} {
		t.Run(phase, func(t *testing.T) {
			_, srv, cfg, o := interruptedFetchFixture(t)
			original := archiveHashes(t, o.Output+".partial")
			s, err := OpenRecovery(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			progress := &phaseHookWriter{phase: phase, hook: cancel}
			c, _ := NewClient(srv.URL, time.Second, 8<<20)
			second := filepath.Join(t.TempDir(), "second")
			_, err = s.Continue(ctx, c, second, progress, "fixture")
			cancel()
			if !errors.Is(err, context.Canceled) || !progress.fired {
				t.Fatal("second interruption missed", err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			secondBefore := archiveHashes(t, second+".partial")
			s, err = OpenRecovery(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if !s.Info().NeedsSource {
				t.Fatal("unfinished source claimed complete")
			}
			if err = s.ValidateOutput(filepath.Join(second+".partial", "nested")); err == nil {
				t.Fatal("previous attempt folder could be modified")
			}
			third := filepath.Join(t.TempDir(), "third")
			if _, err = s.Continue(context.Background(), c, third, nil, "fixture"); err != nil {
				t.Fatal(err)
			}
			if _, err = inspectFinalArchive(context.Background(), third); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, archiveHashes(t, o.Output+".partial")) || !reflect.DeepEqual(secondBefore, archiveHashes(t, second+".partial")) {
				t.Fatal("earlier partial folder changed")
			}
		})
	}
}

func TestPackagedInterruptedFetchRecoveryCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the actual CLI")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	f := summaryScopeFixture()
	process := make(chan *os.Process, 1)
	killed := make(chan error, 1)
	var armed atomic.Bool
	var count atomic.Int32
	f.beforeBatch = func() {
		if !armed.Load() {
			return
		}
		if count.Add(1) == 2 {
			child := <-process
			killed <- child.Kill()
		}
	}
	srv := f.server(t)
	defer srv.Close()
	cfg := recoveryOptionsFixture(t)
	output := filepath.Join(t.TempDir(), "interrupted")
	cmd := exec.CommandContext(ctx, binary, "export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--metadata", "off", "--output", output, "--work", cfg.Directory, "--recovery-keys", cfg.KeyDirectory)
	progress := &phaseHookWriter{phase: "Stage: Fetch WORKSTREAM_SUMMARIES", hook: func() { armed.Store(true) }}
	cmd.Stdout, cmd.Stderr = progress, progress
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process <- cmd.Process
	if err = cmd.Wait(); err == nil {
		t.Fatal("child did not terminate")
	}
	srv.Close()
	select {
	case err = <-killed:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("did not interrupt hydration", progress.buf.String())
	}
	f.beforeBatch = nil
	before := archiveHashes(t, output+".partial")
	base := []string{"resume", "--work", cfg.Directory, "--recovery-keys", cfg.KeyDirectory}
	inspected, err := exec.CommandContext(ctx, binary, append(base, "--inspect")...).CombinedOutput()
	if err != nil || !strings.Contains(string(inspected), "Ready to resume source fetching") {
		t.Fatal("cannot inspect terminated fetch", err, string(inspected))
	}
	// Declining or inspecting must not require a running OS.
	dest := filepath.Join(t.TempDir(), "recovered")
	decline := exec.CommandContext(ctx, binary, append(base, "--output", dest, "--base-url", srv.URL, "--launch-os=false")...)
	decline.Stdin = strings.NewReader("n\n")
	if b, e := decline.CombinedOutput(); e != nil || !strings.Contains(string(b), "Canceled;") {
		t.Fatal("decline contacted OS", e, string(b))
	}
	srv = f.server(t)
	defer srv.Close()
	resumed, err := exec.CommandContext(ctx, binary, append(base, "--output", dest, "--base-url", srv.URL, "--launch-os=false", "--yes")...).CombinedOutput()
	if err != nil {
		t.Fatal("compiled source resume failed", err, string(resumed))
	}
	manifest, err := InspectArchive(dest)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SourceRecovery == nil || manifest.SourceRecovery.ReusedRecords == 0 {
		t.Fatal("compiled resume did not reuse saved records")
	}
	if _, err = inspectFinalArchive(ctx, dest); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, archiveHashes(t, output+".partial")) {
		t.Fatal("terminated output changed")
	}
}
