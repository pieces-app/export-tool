package exporter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMarkdownLinkValidationCancellation(t *testing.T) {
	for _, populated := range []bool{false, true} {
		root := t.TempDir()
		if populated {
			if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("[missing](missing.md)"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := &run{ctx: ctx, stage: root}
		if err := r.validateMarkdownLinks(); !errors.Is(err, context.Canceled) {
			t.Fatal("link traversal ignored cancellation", err)
		}
	}
}
