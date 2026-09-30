package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryFlagsValidatedBeforeSourceAccess(t *testing.T) {
	root := t.TempDir()
	work, keys := filepath.Join(root, "work"), filepath.Join(root, "keys")
	for _, test := range []struct {
		name  string
		flags []string
	}{
		{"missing_keys", []string{"export", "--work", work}},
		{"missing_workspace", []string{"export", "--recovery-keys", keys}},
		{"shared_roots", []string{"export", "--work", work, "--recovery-keys", work}},
		{"output_overlap", []string{"export", "--work", work, "--recovery-keys", keys, "--output", filepath.Join(work, "archive")}},
		{"scan", []string{"scan", "--work", work, "--recovery-keys", keys}},
		{"dry_run", []string{"export", "--dry-run", "--work", work, "--recovery-keys", keys}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append(test.flags, "--base-url", "http://127.0.0.1:1", "--launch-os=false", "--close-desktop=false")
			code := RunWithInput(context.Background(), args, strings.NewReader(""), &stdout, &stderr, "fixture")
			if code != 1 || strings.Contains(stderr.String(), "cannot reach") || strings.Contains(stdout.String(), "Scanning retained") {
				t.Fatalf("invalid recovery flags reached source: %d %s", code, stderr.String())
			}
		})
	}
}

func TestResumeFlagsAndHelp(t *testing.T) {
	for _, args := range [][]string{{"resume", "--help"}, {"resume"}, {"resume", "--scope", "all"}, {"resume", "--base-url", "http://127.0.0.1:1"}, {"resume", "unexpected"}} {
		var stdout, stderr bytes.Buffer
		code := RunWithInput(context.Background(), args, strings.NewReader(""), &stdout, &stderr, "fixture")
		want := 1
		if len(args) == 2 && args[1] == "--help" {
			want = 0
		}
		if code != want || stdout.Len()+stderr.Len() == 0 {
			t.Fatalf("resume validation %v: %d", args, code)
		}
	}
}
