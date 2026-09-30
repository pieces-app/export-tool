package exporter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pieces-app/export-tool/internal/recovery"
)

func recoveryOptionsFixture(t *testing.T) RecoveryOptions {
	t.Helper()
	root := t.TempDir()
	return RecoveryOptions{Directory: filepath.Join(root, "work"), KeyDirectory: filepath.Join(root, "keys")}
}

func TestRecoveryExportInterruptedLocalReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := captureTestOptions(t, DefaultPolicy())
	cfg := recoveryOptionsFixture(t)
	o.Recovery = &cfg
	o.Progress = &phaseHookWriter{phase: "Privacy reconciliation", hook: cancel}
	f := summaryScopeFixture()
	srv := f.server(t)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	_, err := Export(ctx, c, o)
	srv.Close()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("did not interrupt after source capture", err)
	}
	session, err := OpenRecovery(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	info := session.Info()
	if !info.CanResume || info.Records < 50 || info.Records != info.Included+info.Excluded+info.Withheld+info.Missing {
		t.Fatal("capture inspection misreported records", info)
	}
	if other, err := OpenRecovery(context.Background(), cfg); !errors.Is(err, recovery.ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatal("active workspace did not remain owned", err)
	}
	for _, bad := range []string{o.Output, o.Output + ".partial/inside", filepath.Join(cfg.Directory, "output"), filepath.Join(cfg.KeyDirectory, "output"), filepath.Dir(cfg.Directory)} {
		if err := session.ValidateOutput(bad); err == nil {
			t.Fatal("accepted overlapping output")
		}
	}
	before := archiveHashes(t, o.Output+".partial")
	dest := filepath.Join(t.TempDir(), "replay")
	m, err := session.Replay(context.Background(), dest, nil, "replay-fixture")
	if err != nil || m.Status != "complete_for_implemented_scope" || m.CaptureReplay == nil || m.CaptureReplay.SourceToolVersion != o.Version || m.ToolVersion != "replay-fixture" || m.People.Selected != 1 {
		t.Fatal("public replay failed", err, m.Status)
	}
	if !reflect.DeepEqual(before, archiveHashes(t, o.Output+".partial")) {
		t.Fatal("original partial changed")
	}
	if _, err := session.Replay(context.Background(), dest+"-again", nil, "replay-fixture"); !errors.Is(err, recovery.ErrClosed) {
		t.Fatal("single-use session replayed twice", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	// Retention permits another explicit replay from the same source capture.
	next, err := OpenRecovery(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}

func TestRecoveryIncompleteSourceInspectable(t *testing.T) {
	cfg := recoveryOptionsFixture(t)
	o := captureTestOptions(t, DefaultPolicy())
	o.Recovery = &cfg
	f := summaryScopeFixture()
	srv := f.server(t)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	srv.Close()
	if _, err := Export(context.Background(), c, o); err == nil {
		t.Fatal("unavailable source export succeeded")
	}
	s, err := OpenRecovery(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if info := s.Info(); info.CanResume || info.Phase != "fetching" || info.StoredItems != 0 {
		t.Fatal("incomplete source became resumable", info)
	}
	dest := filepath.Join(t.TempDir(), "replay")
	if _, err := s.Replay(context.Background(), dest, nil, "replay-fixture"); err == nil {
		t.Fatal("incomplete source replayed")
	}
	if _, err := os.Stat(dest + ".partial"); !os.IsNotExist(err) {
		t.Fatal("incomplete source created output")
	}
}

func TestRecoveryPathSeparation(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		a, b    string
		overlap bool
	}{
		{"work", "keys", false}, {"work", "work", true}, {"work", "work/child", true}, {"work/child", "work", true},
		{"work", "WORK/child", true}, {"\u212aey", "key/child", true}, {"caf\u00e9", "cafe\u0301/child", true},
		{"work", "work-copy", false}, {"work/output", "work/keys", false},
	} {
		a, b := filepath.Join(root, test.a), filepath.Join(root, test.b)
		got, err := recoveryPathsOverlap(a, b)
		if err != nil || got != test.overlap {
			t.Fatalf("overlap %q/%q=%t: %v", test.a, test.b, got, err)
		}
	}
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	if overlap, err := recoveryPathsOverlap(filepath.Join(real, "work"), filepath.Join(alias, "WORK/output")); err != nil || !overlap {
		t.Fatal("symlinked-parent overlap missed", err)
	}
	if err := ValidateRecoveryOptions(RecoveryOptions{Directory: alias, KeyDirectory: filepath.Join(root, "keys")}, filepath.Join(root, "out"), true); err == nil {
		t.Fatal("symlink workspace accepted")
	}
}

func TestRecoveryInvalidPathsBeforeSource(t *testing.T) {
	root := t.TempDir()
	for _, cfg := range []RecoveryOptions{
		{Directory: filepath.Join(root, "work")},
		{KeyDirectory: filepath.Join(root, "keys")},
		{Directory: filepath.Join(root, "work"), KeyDirectory: filepath.Join(root, "WORK")},
		{Directory: filepath.Join(root, "absent", "work"), KeyDirectory: filepath.Join(root, "keys")},
	} {
		o := captureTestOptions(t, DefaultPolicy())
		o.Recovery = &cfg
		if _, err := Export(context.Background(), nil, o); err == nil {
			t.Fatal("invalid recovery configuration accepted")
		}
		if _, err := os.Stat(o.Output + ".partial"); !os.IsNotExist(err) {
			t.Fatal("invalid recovery configuration created staging")
		}
	}
}

func TestPackagedRecoveryCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the native release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("complete", func(t *testing.T) { testPackagedRecoveryCLI(t, binary, false) })
	t.Run("partial", func(t *testing.T) { testPackagedRecoveryCLI(t, binary, true) })
}

func testPackagedRecoveryCLI(t *testing.T, binary string, partial bool) {
	root := t.TempDir()
	cfg := RecoveryOptions{Directory: filepath.Join(root, "work"), KeyDirectory: filepath.Join(root, "keys")}
	output := filepath.Join(root, "original")
	f := summaryScopeFixture()
	if partial {
		f.data["WORKSTREAM_SUMMARIES"][0]["annotations"] = refs("unavailable-body")
	}
	srv := f.server(t)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--format", "markdown", "--metadata", "off", "--output", output, "--work", cfg.Directory, "--recovery-keys", cfg.KeyDirectory)
	var killErr error
	progress := &phaseHookWriter{phase: "Privacy reconciliation", hook: func() { killErr = cmd.Process.Kill() }}
	cmd.Stdout, cmd.Stderr = progress, progress
	err := cmd.Run()
	srv.Close()
	if err == nil || !progress.fired || killErr != nil {
		t.Fatalf("CLI did not stop at the committed capture boundary: %v %v %s", err, killErr, progress.buf.String())
	}
	before := archiveHashes(t, output+".partial")
	base := []string{"resume", "--work", cfg.Directory, "--recovery-keys", cfg.KeyDirectory}
	inspect, err := exec.CommandContext(ctx, binary, append(base, "--inspect")...).CombinedOutput()
	if err != nil || !strings.Contains(string(inspect), "Ready to replay local processing") {
		t.Fatalf("CLI cannot inspect completed capture: %v %s", err, inspect)
	}
	if owner, err := OpenRecovery(context.Background(), cfg); err != nil {
		t.Fatal(err)
	} else {
		busy, err := exec.CommandContext(ctx, binary, append(base, "--inspect")...).CombinedOutput()
		owner.Close()
		if err == nil || !strings.Contains(string(busy), "owned by another operation") {
			t.Fatal("CLI bypassed active ownership", err, string(busy))
		}
	}
	bad := exec.CommandContext(ctx, binary, append(base, "--output", filepath.Join(cfg.Directory, "archive"))...)
	bad.Stdin = strings.NewReader("yes\n")
	badOutput, err := bad.CombinedOutput()
	if err == nil || strings.Contains(string(badOutput), "Export now?") {
		t.Fatal("unsafe destination reached confirmation")
	}
	dest := filepath.Join(root, "replayed")
	decline := exec.CommandContext(ctx, binary, append(base, "--output", dest)...)
	decline.Stdin = strings.NewReader("n\n")
	declined, err := decline.CombinedOutput()
	if err != nil || !strings.Contains(string(declined), "Canceled;") {
		t.Fatal("resume confirmation could not decline", err)
	}
	if _, err := os.Stat(dest + ".partial"); !os.IsNotExist(err) {
		t.Fatal("declined resume created output")
	}
	result, err := exec.CommandContext(ctx, binary, append(base, "--output", dest, "--yes")...).CombinedOutput()
	wantStatus := "complete_for_implemented_scope"
	if partial {
		wantStatus = "partial"
	}
	var exitError *exec.ExitError
	if (!partial && err != nil) || (partial && (!errors.As(err, &exitError) || exitError.ExitCode() != 2)) {
		t.Fatalf("offline CLI replay failed: %v %s", err, result)
	}
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dest, "manifest.json"))
	if err != nil || json.Unmarshal(b, &m) != nil {
		t.Fatal("missing replay manifest", err)
	}
	if m.Status != wantStatus || m.CaptureReplay == nil || m.CaptureReplay.SourcePerformance.Requests == 0 || m.Performance.Requests != 0 || m.People.Selected != 1 {
		t.Fatal("replayed CLI archive has incorrect source/people evidence")
	}
	if !reflect.DeepEqual(before, archiveHashes(t, output+".partial")) {
		t.Fatal("CLI modified interrupted output")
	}
	for _, path := range []string{cfg.Directory, cfg.KeyDirectory} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("CLI removed retained recovery inputs", err)
		}
	}
	r := &run{ctx: context.Background(), stage: dest, opts: Options{Scanner: scanner(t, DefaultPolicy())}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
}
