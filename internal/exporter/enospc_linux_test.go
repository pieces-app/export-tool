package exporter

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Explicitly opt in only inside a disposable container with a separate small,
// empty tmpfs. Never fill the host filesystem or an ordinary /tmp mount.
func TestActualENOSPCLeavesUnfinalizedArchive(t *testing.T) {
	root := disposableTmpfs(t)
	for _, tc := range []struct {
		name, phase string
		reserve     int64
	}{
		{"hydration", "", 0},
		{"pdf", "Stage: Render PDFs", 8192},
		{"metadata_sidecar", "Stage: Native and portable metadata", 0},
		{"archive_state", "Stage: Record archive reconstruction evidence", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runActualENOSPC(t, root, tc.phase, tc.reserve)
		})
	}
}

func disposableTmpfs(t *testing.T) string {
	t.Helper()
	root := os.Getenv("PIECES_EXPORT_ENOSPC_ROOT")
	if root == "" {
		t.Skip("requires an explicitly configured disposable tmpfs")
	}
	var fs unix.Statfs_t
	if root != "/enospc" || unix.Statfs(root, &fs) != nil || fs.Type != unix.TMPFS_MAGIC || fs.Blocks*uint64(fs.Bsize) > 32<<20 {
		t.Fatal("disk-full test requires /enospc on a dedicated tmpfs of at most 32 MiB")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("disk-full test requires an empty dedicated mount")
	}
	return root
}

func runActualENOSPC(t *testing.T, root, phase string, reserve int64) {
	t.Helper()
	work, err := os.MkdirTemp(root, "fixture-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(work)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var batches atomic.Int32
	var fillErr error
	var filled atomic.Bool
	fill := func() {
		fillErr = exhaustTmpfs(work, reserve)
		filled.Store(true)
		if fillErr != nil {
			cancel()
		}
	}
	summary := record("summary", "2026-09-29T12:00:00Z")
	summary["name"], summary["parentHierarchicalType"] = "Synthetic disk-full summary", "TEMPORAL_DAY_HIERARCHICAL_SUMMARY"
	summary["annotations"], summary["persons"], summary["pipelines"] = refs("body"), refs(), refs()
	body := record("body", "2026-09-29T12:00:00Z")
	body["type"], body["text"] = "SUMMARY", strings.Repeat("Only synthetic exported narrative.\n\n", 100)
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {summary}, "ANNOTATIONS": {body}}}
	f.beforeBatch = func() {
		if batches.Add(1) == 1 && phase == "" {
			fill()
		}
	}
	srv := f.server(t)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("WORKSTREAM_SUMMARIES,ANNOTATIONS")
	out := filepath.Join(work, "archive")
	progress := &phaseHookWriter{phase: phase, hook: fill}
	manifest, exportErr := Export(ctx, client, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 1, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Format: "both", Metadata: "auto", Progress: progress})
	if !filled.Load() || fillErr != nil {
		t.Fatalf("controlled exhaustion did not occur: %v; export result: %v", fillErr, exportErr)
	}
	if exportErr == nil || errors.Is(exportErr, context.DeadlineExceeded) {
		t.Fatalf("disk exhaustion was hidden or hung: %v", exportErr)
	}
	if phase != "" && !errors.Is(exportErr, syscall.ENOSPC) {
		t.Fatalf("expected real ENOSPC: %v", exportErr)
	}
	if phase == "Stage: Render PDFs" {
		var pathErr *os.PathError
		if !errors.As(exportErr, &pathErr) || pathErr.Op != "write" || filepath.Ext(pathErr.Path) != ".pdf" {
			t.Fatal("PDF exhaustion did not reach an actual PDF byte write")
		}
		if _, err := os.Stat(pathErr.Path); !os.IsNotExist(err) {
			t.Fatal("the incomplete PDF was not removed after its failed write")
		}
	}
	if phase == "" && batches.Load() != 1 {
		t.Fatal("hydration continued after the destination filled")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("failed export was finalized")
	}
	if _, err := os.Stat(out + ".partial"); err != nil {
		t.Fatal("partial evidence was lost")
	}
	if _, err := os.Stat(filepath.Join(out+".partial", "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("disk-full export wrote a successful manifest")
	}
	if manifest.Status == "complete_for_implemented_scope" {
		t.Fatal("returned manifest claimed success after output failure")
	}
	t.Log("actual ENOSPC stopped output; final directory and success manifest absent")
}

func TestPackagedDiskFullCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("requires the packaged Linux executable")
	}
	root := disposableTmpfs(t)
	work, err := os.MkdirTemp(root, "packaged-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(work)
	summary := record("summary", "2026-09-29T12:00:00Z")
	summary["name"], summary["parentHierarchicalType"] = "Synthetic disk-full summary", "TEMPORAL_DAY_HIERARCHICAL_SUMMARY"
	summary["annotations"], summary["persons"], summary["pipelines"] = refs("body"), refs(), refs()
	body := record("body", "2026-09-29T12:00:00Z")
	body["type"], body["text"] = "SUMMARY", strings.Repeat("Only synthetic exported narrative.\n\n", 4000)
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {summary}, "ANNOTATIONS": {body}}}
	srv := f.server(t)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out := filepath.Join(work, "archive")
	cmd := exec.CommandContext(ctx, binary, "export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--materials", "WORKSTREAM_SUMMARIES,ANNOTATIONS", "--output", out, "--yes", "--format", "both", "--metadata", "off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	pipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Signal(syscall.SIGCONT) }()
	fired := false
	var injectionErr error
	lines := bufio.NewScanner(pipe)
	for lines.Scan() {
		line := lines.Text()
		fmt.Fprintln(&stderr, line)
		if !fired && strings.HasPrefix(line, "Stage: Render PDFs") {
			fired = true
			// Pause only our synthetic child in this container. Confirm the
			// stop before filling the dedicated mount, then immediately resume.
			injectionErr = cmd.Process.Signal(syscall.SIGSTOP)
			if injectionErr == nil {
				injectionErr = waitFixtureStopped(cmd.Process.Pid)
			}
			if injectionErr == nil {
				injectionErr = exhaustTmpfs(work, 0)
			}
			_ = cmd.Process.Signal(syscall.SIGCONT)
			if injectionErr != nil {
				cancel()
			}
		}
	}
	err = cmd.Wait()
	var exit *exec.ExitError
	if !fired || injectionErr != nil || lines.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 1 || ctx.Err() != nil {
		t.Fatalf("packaged disk-full failure not observed: injection=%v process=%v scanner=%v", injectionErr, err, lines.Err())
	}
	if strings.Contains(stdout.String(), "Export written:") || strings.Contains(stderr.String(), "Stage: Native and portable metadata") || !strings.Contains(stderr.String(), "output was not finalized") {
		t.Fatalf("packaged PDF failure had unexpected output: %s\n%s", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("packaged disk-full export finalized its output")
	}
	if st, err := os.Stat(out + ".partial"); err != nil || !st.IsDir() {
		t.Fatal("packaged disk-full export lost its staging directory")
	}
	if _, err := os.Stat(filepath.Join(out+".partial", "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("packaged disk-full export wrote a success manifest")
	}
	t.Log("actual packaged PDF conversion on full tmpfs exited 1 without finalization or success output")
}

func waitFixtureStopped(pid int) error {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "State:") && strings.Contains(line, "T (stopped)") {
				return nil
			}
		}
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("fixture child did not enter stopped state")
}

func exhaustTmpfs(dir string, reserve int64) error {
	f, err := os.OpenFile(filepath.Join(dir, "fixture-filler"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	block := make([]byte, 64<<10)
	var written int64
	for written <= 32<<20 {
		n, err := f.Write(block)
		written += int64(n)
		if errors.Is(err, syscall.ENOSPC) {
			if reserve > written {
				return fmt.Errorf("tmpfs did not leave room for a controlled reserve")
			}
			if reserve != 0 {
				return f.Truncate(written - reserve)
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
	return fmt.Errorf("refused to write beyond the disposable tmpfs budget")
}
