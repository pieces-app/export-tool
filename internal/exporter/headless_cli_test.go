package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Exercise the documented agent invocation with closed stdin, no terminal and
// separate output streams. Never connect to or launch the user's Pieces OS.
func TestPackagedHeadlessMarkdownCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "local_complete", true: "utc_partial"}[partial], func(t *testing.T) {
			f := summaryScopeFixture()
			if partial {
				delete(f.data["PERSONS"][0], "annotations")
				delete(f.data["PERSONS"][0], "summaries")
			}
			var delay sync.Once
			f.beforeBatch = func() { delay.Do(func() { time.Sleep(2500 * time.Millisecond) }) }
			srv := f.server(t)
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			base := t.TempDir()
			out, work, keys := filepath.Join(base, "archive with spaces"), filepath.Join(base, "private work"), filepath.Join(base, "private keys")
			args := []string{"export", "--yes", "--format", "markdown", "--launch-os=false", "--close-desktop=false", "--base-url", srv.URL, "--output", out, "--work", work, "--recovery-keys", keys}
			zone := "Local"
			if partial {
				args = append(args, "--timezone", "UTC")
				zone = "UTC"
			}
			var stdout, stderr bytes.Buffer
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run() // nil Stdin supplies EOF; neither stream is a TTY.
			if partial {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
					t.Fatalf("partial headless export must exit 2: %v\n%s", err, stderr.String())
				}
			} else if err != nil {
				t.Fatalf("headless export failed: %v\n%s", err, stderr.String())
			}
			if !strings.Contains(stderr.String(), "Progress: ") || !strings.Contains(stderr.String(), "Stage: ") || strings.Contains(stdout.String(), "Progress: ") || strings.Contains(stdout.String(), "Export now?") {
				t.Fatal("headless progress streams or prompting contract changed")
			}
			var manifest Manifest
			data, err := os.ReadFile(filepath.Join(out, "manifest.json"))
			if err != nil || json.Unmarshal(data, &manifest) != nil || manifest.Timezone != zone || manifest.Format != "markdown" || manifest.People.Mode != "profiles" || manifest.Scope.Name != "summaries" {
				t.Fatal("headless archive or default selection missing")
			}
			if _, err := inspectFinalArchive(ctx, out); err != nil {
				t.Fatal(err)
			}
			assertSummaryScopeRequests(t, f)
			srv.Close()
			inspect := exec.CommandContext(ctx, binary, "resume", "--work", work, "--recovery-keys", keys, "--inspect")
			if text, err := inspect.CombinedOutput(); err != nil || !strings.Contains(string(text), "Local replay only") {
				t.Fatalf("headless recovery inspection needs no OS or input: %v\n%s", err, text)
			}
		})
	}
}
