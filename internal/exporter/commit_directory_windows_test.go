package exporter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsFinalizationLongPaths(t *testing.T) {
	for input, want := range map[string]string{
		`C:\exports\archive`:     `\\?\C:\exports\archive`,
		`\\server\share\archive`: `\\?\UNC\server\share\archive`,
		`\\?\C:\exports\archive`: `\\?\C:\exports\archive`,
		`\\?\UNC\server\share\a`: `\\?\UNC\server\share\a`,
	} {
		got, err := extendedWindowsPath(input)
		if err != nil || got != want {
			t.Fatalf("extended path conversion: %q %v", got, err)
		}
	}
	root := filepath.Join(t.TempDir(), strings.Repeat("long-root-", 12), strings.Repeat("nested-", 20))
	stage, target := filepath.Join(root, "archive.partial"), filepath.Join(root, "archive")
	if len(stage) <= 260 {
		t.Fatal("fixture did not exceed MAX_PATH")
	}
	if err := os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := commitDirectory(stage, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}
}
